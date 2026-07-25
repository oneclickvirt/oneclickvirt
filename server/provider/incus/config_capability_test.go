package incus

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"testing"

	"oneclickvirt/utils"
)

func TestIncusSetInstanceConfigIncludesRemoteOutputForCapabilityDetection(t *testing.T) {
	executor := &recordingIncusIPv6Executor{
		outputs: []string{
			"Error: unknown key limits.memory.swap.priority",
			"Error: unknown key limits.memory.swap.priority",
		},
		errors: []error{errors.New("Process exited with status 1"), errors.New("Process exited with status 1")},
	}
	provider := NewIncusProvider().(*IncusProvider)
	provider.connected = true
	provider.config.ExecutionRule = "ssh_only"
	provider.sshClient = utils.NewSafeShellExecutor(executor)

	err := provider.setInstanceConfig(context.Background(), "guest", "limits.memory.swap.priority", "1")
	if err == nil {
		t.Fatal("setInstanceConfig() succeeded")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("setInstanceConfig() dropped remote output: %v", err)
	}
	if !isIncusConfigUnsupportedError(err) {
		t.Fatalf("unsupported capability was not classified: %v", err)
	}
}

func TestIncusAPIAccessRequiresLoadedTLSConfig(t *testing.T) {
	provider := NewIncusProvider().(*IncusProvider)
	provider.config.CertPath = "configured.crt"
	provider.config.KeyPath = "configured.key"
	if provider.hasAPIAccess() {
		t.Fatal("configured paths without loaded TLS enabled API access")
	}
	provider.transport.TLSClientConfig = &tls.Config{}
	if !provider.hasAPIAccess() {
		t.Fatal("loaded TLS config did not enable API access")
	}
}
