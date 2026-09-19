package firewall

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestInitCacheIsManagerOwnedAndRepairsMissingChains(t *testing.T) {
	chains := map[string]bool{}
	mutations := 0
	failedChain := ""
	e := &cleanupExecutor{run: func(command string) (string, error) {
		if strings.HasPrefix(command, "nft list chain") {
			for _, chain := range []string{"prerouting", "postrouting", "forward"} {
				if !chains[chain] {
					return "", errors.New("chain absent")
				}
			}
			return "ok\n", nil
		}
		mutations++
		for _, chain := range []string{"prerouting", "postrouting", "forward"} {
			if strings.Contains(command, " "+chain+" {") && chain != failedChain {
				chains[chain] = true
			}
		}
		return "", nil
	}}
	m := NewManager(e, "ocvtest", "")
	m.backend, m.detected = BackendNft, true
	if err := m.InitTable(); err != nil {
		t.Fatal(err)
	}
	if err := m.InitTable(); err != nil || mutations != 4 {
		t.Fatalf("healthy cache mutated the table: %d, %v", mutations, err)
	}
	// Reusing exactly the same wrapper address is deterministic. A second
	// manager must not inherit another manager's completed initialization.
	second := &Manager{sshClient: m.sshClient, backend: BackendNft, tableName: "ocvtest", detected: true}
	chains = map[string]bool{}
	if err := second.InitTable(); err != nil || mutations != 8 {
		t.Fatalf("new manager reused stale completion: %d, %v", mutations, err)
	}
	for _, chain := range []string{"prerouting", "postrouting", "forward"} {
		delete(chains, chain)
		before := mutations
		if err := m.InitTable(); err != nil || mutations != before+4 || !chains[chain] {
			t.Fatalf("missing %s was not repaired: %d, %v", chain, mutations, err)
		}
	}
	failedChain = "postrouting"
	delete(chains, failedChain)
	if err := m.InitTable(); err == nil {
		t.Fatal("partial initialization was cached as success")
	}
	failedChain = ""
	if err := m.InitTable(); err != nil {
		t.Fatalf("failed initialization poisoned retry: %v", err)
	}
}

func TestConcurrentInitTableOnlyInitializesOnce(t *testing.T) {
	var callsMu sync.Mutex
	mutations := 0
	e := &cleanupExecutor{run: func(command string) (string, error) {
		callsMu.Lock()
		defer callsMu.Unlock()
		if strings.HasPrefix(command, "nft list chain") {
			return "ok\n", nil
		}
		mutations++
		return "", nil
	}}
	m := NewManager(e, "ocvtest", "")
	m.backend, m.detected = BackendNft, true
	var workers sync.WaitGroup
	for n := 0; n < 24; n++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := m.InitTable(); err != nil {
				t.Errorf("concurrent initialization: %v", err)
			}
		}()
	}
	workers.Wait()
	if mutations != 4 {
		t.Fatalf("duplicated initialization mutations: %d", mutations)
	}
}

func TestFirewallRejectsUnsafeDNATInputs(t *testing.T) {
	m := NewManager(&cleanupExecutor{run: func(string) (string, error) { return "", nil }}, "ocvtest", "")
	m.backend, m.detected = BackendIptables, true
	for _, tc := range []struct {
		name string
		ip   string
		port int
	}{
		{"guest'bad", "192.0.2.10", 22000},
		{"bad-ip", "192.0.2.10; touch /tmp/pwn", 22000},
		{"bad-port", "192.0.2.10", 0},
	} {
		if err := m.AddDNAT(tc.name, tc.ip, tc.port, 30000, 30001); err == nil {
			t.Fatalf("unsafe DNAT input accepted: %+v", tc)
		}
	}
	if err := m.AddDNAT("guest", "192.0.2.10", 22000, 30001, 30000); err == nil {
		t.Fatal("reversed DNAT range accepted")
	}
}

func TestInitTableRejectsUnsafeNamesBeforeRemoteCommands(t *testing.T) {
	e := &cleanupExecutor{run: func(command string) (string, error) {
		t.Fatalf("unexpected remote command: %s", command)
		return "", nil
	}}
	m := NewManager(e, "bad;table", "")
	m.backend, m.detected = BackendNft, true
	if err := m.InitTable(); err == nil {
		t.Fatal("unsafe table name accepted")
	}
}
