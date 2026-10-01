package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSFTPCancellationInterruptsSilentPeer(t *testing.T) {
	for _, negotiateSSH := range []bool{false, true} {
		t.Run(strconv.FormatBool(negotiateSSH), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			_, key, _ := ed25519.GenerateKey(rand.Reader)
			signer, _ := ssh.NewSignerFromKey(key)
			config := &ssh.ServerConfig{NoClientAuth: true}
			config.AddHostKey(signer)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				if !negotiateSSH {
					_, _ = conn.Read(make([]byte, 512))
					_, _ = conn.Read(make([]byte, 512))
					return
				}
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					stream, requests, err := channel.Accept()
					if err != nil {
						return
					}
					// Read, but never acknowledge the SFTP subsystem request.
					for range requests {
					}
					stream.Close()
				}
			}()
			port := listener.Addr().(*net.TCPAddr).Port
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			started := time.Now()
			client, cleanup, err := OpenSFTPClientContext(ctx, &SSHAccessTarget{Host: "127.0.0.1", Port: port, Username: "fixture", Password: "fixture"})
			if cleanup != nil {
				cleanup()
			}
			if err == nil || client != nil {
				t.Fatal("silent peer unexpectedly opened SFTP")
			}
			if time.Since(started) > 2*time.Second {
				t.Fatal("cancellation did not bound negotiation")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("SSH connection leaked")
			}
		})
	}
}
