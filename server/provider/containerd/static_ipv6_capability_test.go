package containerd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedStaticIPv6CapabilityUpgrade(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "network.json")
	state := filepath.Join(dir, "subnet")
	original := `{"name":"containerd-ipv6","plugins":[{"type":"bridge","bridge":"ctn-br1","capabilities":{"other":true},"ipam":{"type":"host-local","ranges":[[{"subnet":"2001:db8::/80"}]]}}]}`
	if err := os.WriteFile(config, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("2001:db8::/80"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func() error {
		cmd := exec.Command(python, "-", config, state)
		cmd.Stdin = strings.NewReader(containerdStaticIPv6CapabilityProgram)
		return cmd.Run()
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var network struct {
		Plugins []struct{ Capabilities map[string]bool }
	}
	if err := json.Unmarshal(data, &network); err != nil {
		t.Fatal(err)
	}
	if !network.Plugins[0].Capabilities["ips"] || !network.Plugins[0].Capabilities["other"] {
		t.Fatal("capabilities lost")
	}
	if err := os.WriteFile(state, []byte("2001:db8:1::/80"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("unowned subnet accepted")
	}
	after, _ := os.ReadFile(config)
	if string(after) != string(data) {
		t.Fatal("unowned configuration changed")
	}
}
