package agent

import (
	"net"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
)

func TestTunnelConcurrentStopIsIdempotent(t *testing.T) {
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previous }()
	client, peer := net.Pipe()
	defer peer.Close()
	mgr := NewTunnelManager(newAgentConn(1, nil, ""))
	session := &TunnelSession{client: client, done: make(chan struct{})}
	mgr.sessions["a"] = session
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); mgr.CloseAllSessions(); session.stop() }()
	}
	wg.Wait()
	select {
	case <-session.done:
	default:
		t.Fatal("session was not stopped")
	}
	late, latePeer := net.Pipe()
	defer latePeer.Close()
	mgr.handleConn(late, "127.0.0.1", 22)
	if len(mgr.sessions) != 1 {
		t.Fatal("retired manager accepted a new session")
	}
}

func TestTunnelRemoteCloseFollowsQueuedData(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	mgr := NewTunnelManager(newAgentConn(1, nil, ""))
	session := &TunnelSession{client: client, done: make(chan struct{}), sendCh: make(chan []byte, 4)}
	mgr.sessions["a"] = session
	mgr.hashIndex[hashString("a")] = session
	mgr.DeliverData("a", []byte("tail"))
	mgr.CloseSession("a")
	if got := <-session.sendCh; string(got) != "tail" {
		t.Fatalf("tail lost: %q", got)
	}
	if got := <-session.sendCh; got != nil {
		t.Fatal("missing ordered EOF sentinel")
	}
	select {
	case <-session.done:
		t.Fatal("close bypassed the data queue")
	default:
	}
}

func TestTunnelOpenCancelledDuringAckWait(t *testing.T) {
	done := make(chan struct{})
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := sendTunnelOpenWithRetry("a", func() error { close(started); return nil },
			make(chan tunnelAckPayload), 2, time.Minute, done)
		result <- err
	}()
	<-started
	close(done)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled open succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled open kept waiting for ACK")
	}
}
