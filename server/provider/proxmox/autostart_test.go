package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	coreprovider "oneclickvirt/provider"

	"go.uber.org/zap"
)

func newAutostartTestProvider(t *testing.T, bandwidth int) (*ProxmoxProvider, coreprovider.InstanceConfig) {
	t.Helper()
	db, oldDB, oldLog := newProxmoxAPITestDB(t)
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = oldDB, oldLog })
	if err := db.Create(&providerModel.Provider{ID: 1, StoragePool: "local", NetworkType: "nat_ipv4", DefaultOutboundBandwidth: bandwidth}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&systemModel.SystemImage{
		Name: "debian-12", URL: "https://example.test/debian-12.qcow2",
		ProviderType: "proxmox", InstanceType: "vm", Architecture: "amd64", Status: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config = coreprovider.NodeConfig{ID: 1, Host: "pve.test", Architecture: "amd64"}
	p.node = "pve-node"
	return p, coreprovider.InstanceConfig{
		Name: "autostart-test", Image: "debian-12", InstanceType: "vm",
		CPU: "1", Memory: "1024", Disk: "8", Metadata: map[string]string{"network_type": "nat_ipv4"},
	}
}

func autostartSSHOutput(command string) string {
	switch {
	case command == "uname -m":
		return "x86_64"
	case strings.Contains(command, "kvm_available"):
		return "kvm_available"
	case strings.Contains(command, "echo 'exists'"):
		return "exists"
	case strings.Contains(command, "echo 'valid'"):
		return "valid"
	default:
		return ""
	}
}

// Stop at the actual create boundary, so the test inspects the command sent to
// PVE without needing a guest image, networking, or a running hypervisor.
func TestProxmoxSSHVMCreatePersistsAutostart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bandwidth int
		installer bool
		attempts  int
	}{
		{name: "cloud-default-rate-fallback", attempts: 2},
		{name: "cloud-rate-fallback", bandwidth: 64, attempts: 2},
		{name: "installer", installer: true, attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, config := newAutostartTestProvider(t, tc.bandwidth)
			var commands []string
			stop := errors.New("test stops at create")
			p.sshClient.SetExecutor(&ipv6CommandExecutor{output: autostartSSHOutput, fail: func(command string) error {
				if strings.HasPrefix(command, "qm create ") {
					commands = append(commands, command)
					return stop
				}
				return nil
			}})
			var err error
			if tc.installer {
				err = p.createInstallerVM(context.Background(), 101, config, "https://example.test/installer.iso", false, func(int, string) {})
			} else {
				err = p.createVM(context.Background(), 101, config, func(int, string) {})
			}
			if err == nil || !strings.Contains(err.Error(), stop.Error()) {
				t.Fatalf("create did not reach its intercepted boundary: %v", err)
			}
			if len(commands) != tc.attempts {
				t.Fatalf("create attempts = %d, want %d", len(commands), tc.attempts)
			}
			for _, command := range commands {
				if !strings.Contains(command, " --onboot 1 ") {
					t.Fatalf("VM will remain stopped after host reboot: %s", command)
				}
			}
		})
	}
}

type autostartCaptureTransport struct{ payload map[string]interface{} }

func (t *autostartCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := json.NewDecoder(request.Body).Decode(&t.payload); err != nil {
		return nil, err
	}
	return nil, errors.New("test stops at create request")
}

func TestProxmoxAPIVMCreatePersistsAutostart(t *testing.T) {
	p, config := newAutostartTestProvider(t, 0)
	p.sshClient.SetExecutor(&ipv6CommandExecutor{output: autostartSSHOutput})
	capture := &autostartCaptureTransport{}
	p.apiClient = &http.Client{Transport: capture}
	err := p.apiCreateVM(context.Background(), 101, config, func(int, string) {})
	if err == nil || !strings.Contains(err.Error(), "test stops at create request") {
		t.Fatalf("create did not reach its intercepted API boundary: %v", err)
	}
	if capture.payload["onboot"] != "1" {
		t.Fatalf("VM will remain stopped after host reboot: onboot = %v", capture.payload["onboot"])
	}
}
