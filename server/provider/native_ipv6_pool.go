package provider

import "context"

// NativeIPv6PoolNetwork is a live, read-only snapshot of the guest network.
// It must describe the guest bridge, not a larger prefix on the host uplink.
type NativeIPv6PoolNetwork struct {
	CIDR     string   `json:"cidr"`
	Gateway  string   `json:"gateway"`
	Bridge   string   `json:"bridge"`
	Reserved []string `json:"reserved"`
}

type NativeIPv6PoolNetworkProvider interface {
	GetNativeIPv6PoolNetwork(context.Context) (*NativeIPv6PoolNetwork, error)
}
