package utils

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oneclickvirt/global"

	"github.com/pkg/sftp"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

type blockingSFTPWriterHandler struct {
	base    sftp.Handlers
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (h *blockingSFTPWriterHandler) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	if h.calls.Add(1) == 1 {
		return &blockingSFTPWriter{started: h.started, release: h.release}, nil
	}
	return h.base.FilePut.Filewrite(request)
}

type blockingSFTPWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingSFTPWriter) WriteAt(p []byte, _ int64) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return 0, io.ErrClosedPipe
}

func (*blockingSFTPWriter) Close() error { return nil }

func newLoopbackSSHClient(t *testing.T, handlers sftp.Handlers) *SSHClient {
	t.Helper()
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLog })

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	serverConfig.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		serverConn, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
		if err != nil {
			_ = conn.Close()
			return
		}
		go ssh.DiscardRequests(requests)
		for newChannel := range channels {
			if newChannel.ChannelType() != "session" {
				_ = newChannel.Reject(ssh.UnknownChannelType, "only sessions are supported")
				continue
			}
			channel, channelRequests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go serveLoopbackSFTP(channel, channelRequests, handlers)
		}
		_ = serverConn.Close()
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	client, err := NewSSHClient(SSHConfig{
		Host:           host,
		Port:           port,
		Username:       "test",
		Password:       "test",
		ConnectTimeout: 2 * time.Second,
	})
	if err != nil {
		_ = listener.Close()
		t.Fatalf("NewSSHClient() error: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = listener.Close()
		select {
		case <-serverDone:
		case <-time.After(time.Second):
		}
	})
	return client
}

func serveLoopbackSFTP(channel ssh.Channel, requests <-chan *ssh.Request, handlers sftp.Handlers) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "subsystem" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Name string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil || payload.Name != "sftp" {
			_ = request.Reply(false, nil)
			continue
		}
		_ = request.Reply(true, nil)
		server := sftp.NewRequestServer(channel, handlers)
		_ = server.Serve()
		_ = server.Close()
		return
	}
}

func TestSSHUploadContentContextCancelsBlockedSFTPWriteWithoutClosingTransport(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	base := sftp.InMemHandler()
	writer := &blockingSFTPWriterHandler{base: base, started: started, release: release}
	client := newLoopbackSSHClient(t, sftp.Handlers{
		FileGet:  base.FileGet,
		FilePut:  writer,
		FileCmd:  base.FileCmd,
		FileList: base.FileList,
	})

	ctx, cancel := context.WithCancel(context.Background())
	uploadDone := make(chan error, 1)
	go func() { uploadDone <- client.UploadContentContext(ctx, "blocked payload", "/cancelled-upload", 0600) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("SFTP writer did not receive an upload")
	}
	startedAt := time.Now()
	cancel()
	select {
	case err := <-uploadDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("UploadContentContext() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UploadContentContext() did not return promptly after cancellation")
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("cancelled upload took %s to return", elapsed)
	}
	if !client.IsHealthy() {
		t.Fatal("cancelling one SFTP session made the shared SSH transport unhealthy")
	}
	if err := client.UploadContentContext(context.Background(), "follow-up", "/follow-up-upload", 0600); err != nil {
		t.Fatalf("shared SSH transport could not open a follow-up SFTP session: %v", err)
	}
}

func TestReconnectContextHonorsCancellationBeforeDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &SSHClient{}
	if err := client.ReconnectContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReconnectContext() error = %v, want context.Canceled", err)
	}
}

func TestReconnectContextCancelsWhileWaitingForReconnectGate(t *testing.T) {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	client := &SSHClient{reconnectGate: gate}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := client.ReconnectContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReconnectContext() error = %v, want context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("ReconnectContext() waited %s for a reconnect gate after cancellation", elapsed)
	}
}

func TestReconnectContextCancelsSSHHandshake(t *testing.T) {
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLog })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	transportFailed := make(chan struct{})
	close(transportFailed)
	client := &SSHClient{
		client:          &ssh.Client{Conn: &lifecycleSSHTransport{done: make(chan struct{})}},
		transportFailed: transportFailed,
		config: SSHConfig{
			Host:           "127.0.0.1",
			Port:           port,
			Username:       "test",
			Password:       "test",
			ConnectTimeout: 5 * time.Second,
		},
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()

	started := time.Now()
	err = client.ReconnectContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReconnectContext() error = %v, want context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("ReconnectContext() took %s to stop a stalled SSH handshake", elapsed)
	}
	_ = listener.Close()
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("loopback handshake server did not exit after the client cancelled")
	}
}
