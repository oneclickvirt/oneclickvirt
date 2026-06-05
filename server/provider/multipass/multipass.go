package multipass

import (
	rootProvider "oneclickvirt/provider"
	"oneclickvirt/provider/vmcli"
)

func NewMultipassProvider() rootProvider.Provider {
	return vmcli.New(vmcli.MultipassSpec())
}

func init() {
	rootProvider.RegisterProvider("multipass", NewMultipassProvider)
}
