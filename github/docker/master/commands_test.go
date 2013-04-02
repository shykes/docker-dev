package docker

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"testing"
	"time"
)

func closeWrap(args ...io.Closer) error {
	e := false
	ret := fmt.Errorf("Error closing elements")
	for _, c := range args {
		if err := c.Close(); err != nil {
			e = true
			ret = fmt.Errorf("%s\n%s", ret, err)
		}
	}
	if e {
		return ret
	}
	return nil
}

func setTimeout(t *testing.T, msg string, d time.Duration, f func()) {
	c := make(chan bool)

	// Make sure we are not too long
	go func() {
		time.Sleep(d)
		c <- true
	}()
	go func() {
		f()
		c <- false
	}()
	if <-c {
		t.Fatal(msg)
	}
}

func assertPipe(input, output string, r io.Reader, w io.Writer, count int) error {
	for i := 0; i < count; i++ {
		if _, err := w.Write([]byte(input)); err != nil {
			return err
		}
		o, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			return err
		}
		if strings.Trim(o, " \r\n") != output {
			return fmt.Errorf("Unexpected output. Expected [%s], received [%s]", output, o)
		}
	}
	return nil
}

// TestRunHostname checks that 'docker run -h' correctly sets a custom hostname
func TestRunHostname(t *testing.T) {
	runtime, err := newTestRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer nuke(runtime)

	srv := &Server{runtime: runtime}

	var stdin, stdout bytes.Buffer
	setTimeout(t, "CmdRun timed out", 2*time.Second, func() {
		if err := srv.CmdRun(ioutil.NopCloser(&stdin), &nopWriteCloser{&stdout}, "-h", "foobar", GetTestImage(runtime).Id, "hostname"); err != nil {
			t.Fatal(err)
		}
	})
	if output := string(stdout.Bytes()); output != "foobar\n" {
		t.Fatalf("'hostname' should display '%s', not '%s'", "foobar\n", output)
	}
}

func TestRunExit(t *testing.T) {
	runtime, err := newTestRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer nuke(runtime)

	srv := &Server{runtime: runtime}

	stdin, stdinPipe := io.Pipe()
	stdout, stdoutPipe := io.Pipe()
	c1 := make(chan struct{})
	go func() {
		srv.CmdRun(stdin, stdoutPipe, "-i", GetTestImage(runtime).Id, "/bin/cat")
		close(c1)
	}()

	setTimeout(t, "Read/Write assertion timed out", 2*time.Second, func() {
		if err := assertPipe("hello\n", "hello", stdout, stdinPipe, 15); err != nil {
			t.Fatal(err)
		}
	})

	container := runtime.List()[0]

	// Closing /bin/cat stdin, expect it to exit
	p, err := container.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	// as the process exited, CmdRun must finish and unblock. Wait for it
	setTimeout(t, "Waiting for CmdRun timed out", 2*time.Second, func() {
		<-c1
	})

	// Make sure that the client has been disconnected
	setTimeout(t, "The client should have been disconnected once the remote process exited.", 2*time.Second, func() {
		// Expecting pipe i/o error, just check that read does not block
		stdin.Read([]byte{})
	})

	// Cleanup pipes
	if err := closeWrap(stdin, stdinPipe, stdout, stdoutPipe); err != nil {
		t.Fatal(err)
	}
}

// Expected behaviour: the process dies when the client disconnects
func TestRunDisconnect(t *testing.T) {
	runtime, err := newTestRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer nuke(runtime)

	srv := &Server{runtime: runtime}

	stdin, stdinPipe := io.Pipe()
	stdout, stdoutPipe := io.Pipe()
	c1 := make(chan struct{})
	go func() {
		// We're simulating a disconnect so the return value doesn't matter. What matters is the
		// fact that CmdRun returns.
		srv.CmdRun(stdin, stdoutPipe, "-i", GetTestImage(runtime).Id, "/bin/cat")
		close(c1)
	}()

	setTimeout(t, "Read/Write assertion timed out", 2*time.Second, func() {
		if err := assertPipe("hello\n", "hello", stdout, stdinPipe, 15); err != nil {
			t.Fatal(err)
		}
	})

	// Close pipes (simulate disconnect)
	if err := closeWrap(stdin, stdinPipe, stdout, stdoutPipe); err != nil {
		t.Fatal(err)
	}

	// as the pipes are close, we expect the process to die,
	// therefore CmdRun to unblock. Wait for CmdRun
	setTimeout(t, "Waiting for CmdRun timed out", 2*time.Second, func() {
		<-c1
	})

	// Client disconnect after run -i should cause stdin to be closed, which should
	// cause /bin/cat to exit.
	setTimeout(t, "Waiting for /bin/cat to exit timed out", 2*time.Second, func() {
		container := runtime.List()[0]
		container.Wait()
		if container.State.Running {
			t.Fatalf("/bin/cat is still running after closing stdin")
		}
	})
}

// TestAttachStdin checks attaching to stdin without stdout and stderr.
// 'docker run -i -a stdin' should sends the client's stdin to the command,
// then detach from it and print the container id.
func TestAttachStdin(t *testing.T) {
	runtime, err := newTestRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer nuke(runtime)
	srv := &Server{runtime: runtime}

	stdinR, stdinW := io.Pipe()
	var stdout bytes.Buffer

	ch := make(chan struct{})
	go func() {
		srv.CmdRun(stdinR, &stdout, "-i", "-a", "stdin", GetTestImage(runtime).Id, "sh", "-c", "echo hello; cat")
		close(ch)
	}()

	// Send input to the command, close stdin, wait for CmdRun to return
	setTimeout(t, "Read/Write timed out", 2*time.Second, func() {
		if _, err := stdinW.Write([]byte("hi there\n")); err != nil {
			t.Fatal(err)
		}
		stdinW.Close()
		<-ch
	})

	// Check output
	cmdOutput := string(stdout.Bytes())
	container := runtime.List()[0]
	if cmdOutput != container.ShortId()+"\n" {
		t.Fatalf("Wrong output: should be '%s', not '%s'\n", container.ShortId()+"\n", cmdOutput)
	}

	setTimeout(t, "Waiting for command to exit timed out", 2*time.Second, func() {
		container.Wait()
	})

	// Check logs
	if cmdLogs, err := container.ReadLog("stdout"); err != nil {
		t.Fatal(err)
	} else {
		if output, err := ioutil.ReadAll(cmdLogs); err != nil {
			t.Fatal(err)
		} else {
			expectedLog := "hello\nhi there\n"
			if string(output) != expectedLog {
				t.Fatalf("Unexpected logs: should be '%s', not '%s'\n", expectedLog, output)
			}
		}
	}
}

// Expected behaviour, the process stays alive when the client disconnects
func TestAttachDisconnect(t *testing.T) {
	runtime, err := newTestRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer nuke(runtime)

	srv := &Server{runtime: runtime}

	container, err := runtime.Create(
		&Config{
			Image:     GetTestImage(runtime).Id,
			Memory:    33554432,
			Cmd:       []string{"/bin/cat"},
			OpenStdin: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Destroy(container)

	// Start the process
	if err := container.Start(); err != nil {
		t.Fatal(err)
	}

	stdin, stdinPipe := io.Pipe()
	stdout, stdoutPipe := io.Pipe()

	// Attach to it
	c1 := make(chan struct{})
	go func() {
		// We're simulating a disconnect so the return value doesn't matter. What matters is the
		// fact that CmdAttach returns.
		srv.CmdAttach(stdin, stdoutPipe, container.Id)
		close(c1)
	}()

	setTimeout(t, "First read/write assertion timed out", 2*time.Second, func() {
		if err := assertPipe("hello\n", "hello", stdout, stdinPipe, 15); err != nil {
			t.Fatal(err)
		}
	})
	// Close pipes (client disconnects)
	if err := closeWrap(stdin, stdinPipe, stdout, stdoutPipe); err != nil {
		t.Fatal(err)
	}

	// Wait for attach to finish, the client disconnected, therefore, Attach finished his job
	setTimeout(t, "Waiting for CmdAttach timed out", 2*time.Second, func() {
		<-c1
	})

	// We closed stdin, expect /bin/cat to still be running
	// Wait a little bit to make sure container.monitor() did his thing
	err = container.WaitTimeout(500 * time.Millisecond)
	if err == nil || !container.State.Running {
		t.Fatalf("/bin/cat is not running after closing stdin")
	}

	// Try to avoid the timeoout in destroy. Best effort, don't check error
	cStdin, _ := container.StdinPipe()
	cStdin.Close()
}
