package utils

import (
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSSHConnectionPoolCloseAllDetachesAndClosesConcurrently(t *testing.T) {
	pool := NewSSHConnectionPool(time.Hour, nil)
	transports := make([]*lifecycleSSHTransport, 32)
	pool.mu.Lock()
	for providerID := range transports {
		transport := &lifecycleSSHTransport{done: make(chan struct{})}
		transports[providerID] = transport
		pool.conns[uint(providerID)] = &SSHClient{client: &ssh.Client{Conn: transport}}
		pool.configs[uint(providerID)] = SSHConfig{Host: "198.51.100.1", Port: 22}
		pool.lastUsed[uint(providerID)] = time.Now()
	}
	pool.mu.Unlock()

	pool.CloseAll()

	pool.mu.RLock()
	closed := pool.closed
	remaining := len(pool.conns) + len(pool.configs) + len(pool.lastUsed)
	pool.mu.RUnlock()
	if !closed {
		t.Fatal("CloseAll did not mark the pool closed")
	}
	if remaining != 0 {
		t.Fatalf("CloseAll left pooled state behind: %d entries", remaining)
	}
	for i, transport := range transports {
		if got := transport.closes.Load(); got != 1 {
			t.Fatalf("provider %d transport close count = %d, want 1", i, got)
		}
	}

	if _, err := pool.GetOrCreate(999, SSHConfig{Host: "198.51.100.2", Port: 22}); err == nil {
		t.Fatal("closed pool accepted a new connection")
	}
	// CloseAll is intentionally idempotent so lifecycle shutdown can race a
	// second cleanup hook without panicking or touching a new map.
	pool.CloseAll()
}

func TestSSHConnectionPoolDoesNotDialOrCloseActiveClientOnConfigChange(t *testing.T) {
	pool := NewSSHConnectionPool(time.Hour, nil)
	defer pool.CloseAll()
	active := &SSHClient{client: &ssh.Client{Conn: &lifecycleSSHTransport{done: make(chan struct{})}}, activeUses: 1}
	pool.mu.Lock()
	pool.conns[7] = active
	pool.configs[7] = SSHConfig{Host: "old.example", Port: 22, Username: "root"}
	pool.lastUsed[7] = time.Now()
	pool.mu.Unlock()

	started := time.Now()
	_, err := pool.GetOrCreate(7, SSHConfig{Host: "new.example", Port: 22, Username: "root"})
	if err == nil {
		t.Fatal("config change unexpectedly replaced an active SSH client")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("active-client guard dialed before returning: %v", time.Since(started))
	}
	if active.closed {
		t.Fatal("active SSH client was closed during config-change rejection")
	}
}

func TestSSHConnectionPoolDoesNotEvictProviderZeroWhenAllConnectionsAreActive(t *testing.T) {
	pool := NewSSHConnectionPool(time.Hour, nil)
	defer pool.CloseAll()
	transport := &lifecycleSSHTransport{done: make(chan struct{})}
	active := &SSHClient{
		client:     &ssh.Client{Conn: transport},
		activeUses: 1,
	}
	pool.mu.Lock()
	pool.conns[0] = active
	pool.configs[0] = SSHConfig{Host: "198.51.100.10", Port: 22}
	pool.lastUsed[0] = time.Now()
	// evictOldestConnection is called with the pool write lock held by
	// GetOrCreate. Calling it directly here exercises the no-candidate path
	// without requiring a real SSH dial.
	pool.evictOldestConnection()
	_, stillPresent := pool.conns[0]
	pool.mu.Unlock()
	if !stillPresent {
		t.Fatal("provider ID 0 was evicted despite having an active lease")
	}
	if got := transport.closes.Load(); got != 0 {
		t.Fatalf("active provider ID 0 transport close count = %d, want 0", got)
	}
}
