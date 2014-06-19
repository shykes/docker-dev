package libcontainer

import (
	"github.com/docker/libcontainer/mount"
	"github.com/docker/libcontainer/network"
	"github.com/docker/libcontainer/security/capabilities"
)

func GetInternalMountSpec(container *Container) *mount.MountSpec {
	out := &mount.MountSpec{
		NoPivotRoot: container.NoPivotRoot,
		ReadonlyFs:  container.ReadonlyFs,
		DeviceNodes: container.DeviceNodes,
		MountLabel:  container.Context["mount_label"],
	}
	for _, mountFromSpec := range container.Mounts {
		out.Mounts = append(out.Mounts, mount.Mount{
			Type:        mountFromSpec.Type,
			Source:      mountFromSpec.Source,
			Destination: mountFromSpec.Destination,
			Writable:    mountFromSpec.Writable,
			Private:     mountFromSpec.Private})
	}
	return out
}

func GetInternalNetworkSpec(net *Network) *network.Network {
	return &network.Network{
		Type:       net.Type,
		NsPath:     net.Context["nspath"],
		Bridge:     net.Context["bridge"],
		VethPrefix: net.Context["prefix"],
		Address:    net.Address,
		Gateway:    net.Gateway,
		Mtu:        net.Mtu,
	}
}

func GetAllCapabilities() []string {
	return capabilities.GetAllCapabilities()
}
