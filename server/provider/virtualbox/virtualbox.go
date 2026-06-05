package virtualbox

import (
	rootProvider "oneclickvirt/provider"
	"oneclickvirt/provider/vmcli"
)

func NewVirtualBoxProvider() rootProvider.Provider {
	return vmcli.New(vmcli.VirtualBoxSpec())
}

func init() {
	rootProvider.RegisterProvider("virtualbox", NewVirtualBoxProvider)
}
