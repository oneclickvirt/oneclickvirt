package utils

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type lifecycleSSHTransport struct {
	ssh.Conn
	done   chan struct{}
	once   sync.Once
	opens  atomic.Int32
	closes atomic.Int32
}

func (c *lifecycleSSHTransport) Wait() error { <-c.done; return nil }
func (c *lifecycleSSHTransport) Close() error {
	c.once.Do(func() { c.closes.Add(1); close(c.done) })
	return nil
}
func (c *lifecycleSSHTransport) OpenChannel(string, []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	c.opens.Add(1)
	return nil, nil, &ssh.OpenChannelError{Reason: ssh.ResourceShortage, Message: "MaxSessions"}
}

func TestSSHSessionRejectionDoesNotRetireSharedTransport(t *testing.T) {
	transport := &lifecycleSSHTransport{done: make(chan struct{})}
	client := &SSHClient{client: &ssh.Client{Conn: transport}}
	defer client.Close()
	for i := 0; i < 5; i++ {
		if !client.IsHealthy() {
			t.Fatal("live transport considered unhealthy")
		}
	}
	if transport.opens.Load() != 0 {
		t.Fatal("health check consumed a session slot")
	}
	if _, err := client.ExecuteRaw("true", time.Second); err == nil {
		t.Fatal("expected MaxSessions rejection")
	}
	if transport.opens.Load() != 1 {
		t.Fatal("session rejection was retried")
	}
	if transport.closes.Load() != 0 {
		t.Fatal("session rejection closed shared SSH connection")
	}
	if err := client.Reconnect(); err != nil {
		t.Fatal(err)
	}
	if transport.closes.Load() != 0 {
		t.Fatal("recovery retired healthy transport")
	}
}

func TestSSHCloseIsTerminalAndRaceSafe(t *testing.T) {
	transport := &lifecycleSSHTransport{done: make(chan struct{})}
	client := &SSHClient{client: &ssh.Client{Conn: transport}}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.IsHealthy()
			client.GetUnderlyingClient()
			client.Close()
		}()
	}
	wg.Wait()
	if client.IsHealthy() || client.GetUnderlyingClient() != nil {
		t.Fatal("closed transport remained usable")
	}
	if err := client.Reconnect(); err == nil {
		t.Fatal("closed wrapper was resurrected")
	}
	if transport.closes.Load() != 1 {
		t.Fatal("transport closed more than once")
	}
}
