package proxmox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

const listResourcesFixture = `[{"id":"lxc/100","node":"pve","name":"conteneur-déjà","status":"running","type":"lxc","vmid":100,"maxcpu":1,"maxmem":1073741824,"maxdisk":8589934592},{"id":"lxc/200","node":"other","name":"other-node","status":"stopped","type":"lxc","vmid":200}]`

func TestAPIListInstancesRejectsFailedAndMalformedResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"denied", http.StatusForbidden, `{"message":"forbidden"}`},
		{"missing data", http.StatusOK, `{"message":"empty"}`},
		{"invalid data", http.StatusOK, `{"data":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProxmoxProvider().(*ProxmoxProvider)
			p.config.Host = "pve.test"
			p.apiClient = &http.Client{Transport: discoveryRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			if instances, err := p.apiListInstances(context.Background()); err == nil {
				t.Fatalf("accepted failed API response as %v", instances)
			}
		})
	}
}

func TestSSHListInstancesUsesJSONDespiteColorAndLocalizedNames(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.setNodeName("pve")
	executor := &ipv6CommandExecutor{output: func(command string) string {
		if strings.Contains(command, "pvesh get /cluster/resources") {
			return fmt.Sprintf("\x1b[32m%s\x1b[0m", listResourcesFixture)
		}
		return ""
	}}
	p.sshClient.SetExecutor(executor)
	instances, err := p.sshListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ID != "100" || instances[0].Name != "conteneur-déjà" || instances[0].Status != "running" {
		t.Fatalf("unexpected PVE instance list: %+v", instances)
	}
	for _, command := range executor.commands {
		if strings.HasPrefix(command, "pct list") || strings.HasPrefix(command, "qm list") {
			t.Fatalf("used a locale-dependent list command: %q", command)
		}
	}
}

func TestAPIListInstancesAcceptsResourceSnapshot(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config.Host = "pve.test"
	p.setNodeName("pve")
	p.apiClient = &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api2/json/cluster/resources" || request.URL.Query().Get("type") != "vm" {
			t.Fatalf("unexpected PVE list URL: %s", request.URL.String())
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":` + listResourcesFixture + `}`)), Header: make(http.Header)}, nil
	})}
	instances, err := p.apiListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ID != "100" {
		t.Fatalf("unexpected PVE API instance list: %+v", instances)
	}
}
