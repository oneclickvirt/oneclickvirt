package vagrant

import (
	rootProvider "oneclickvirt/provider"
	"oneclickvirt/provider/vmcli"
)

func NewVagrantProvider() rootProvider.Provider {
	return vmcli.New(vmcli.VagrantSpec())
}

func init() {
	rootProvider.RegisterProvider("vagrant", NewVagrantProvider)
}
