//go:build live_incus

package incus

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

// Missing live prerequisites fail, never skip. CLI creates owned fixtures;
// the actual provider handles security, mappings and deletion. This is not a
// panel/API E2E test, and must only run against an authorized disposable host.
func TestLiveIncusPortMappingAndNesting(t *testing.T) {
	host, password, image := os.Getenv("OCV_LIVE_HOST"), os.Getenv("OCV_LIVE_PASSWORD"), os.Getenv("OCV_LIVE_IMAGE")
	if host == "" || password == "" || image == "" || os.Getenv("OCV_LIVE_DISPOSABLE") != "yes" {
		t.Fatal("set OCV_LIVE_HOST, OCV_LIVE_PASSWORD, OCV_LIVE_IMAGE and OCV_LIVE_DISPOSABLE=yes for the authorized disposable workflow")
	}
	basePort := 29876
	if value := os.Getenv("OCV_LIVE_PORT"); value != "" {
		var err error
		basePort, err = strconv.Atoi(value)
		if err != nil || basePort < 1024 || basePort > 65532 {
			t.Fatal("OCV_LIVE_PORT must leave room for four ports in 1024..65535")
		}
	}
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLog })
	client, err := utils.NewSSHClient(utils.SSHConfig{Host: host, Port: 22, Username: "root", Password: password, ConnectTimeout: 15 * time.Second, ExecuteTimeout: 180 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	p := &IncusProvider{connected: true, config: provider.NodeConfig{Host: host, PortIP: host, ExecutionRule: "ssh_only"}, sshClient: utils.NewSafeShellExecutor(client)}
	run := func(command string) string {
		t.Helper()
		output, err := client.Execute(command)
		if err != nil {
			t.Fatalf("command failed: %s\n%s\n%v", command, output, err)
		}
		return output
	}
	assertFreePorts := func() {
		t.Helper()
		// NAT proxies create rules, not listeners. Inspect both, before create
		// and after delete, without removing any unrelated node configuration.
		for port := basePort; port < basePort+4; port++ {
			out := run(fmt.Sprintf("ss -H -lntup 'sport = :%d'; nft list ruleset 2>/dev/null | grep -w %d || true; iptables-save -t nat 2>/dev/null | grep -w %d || true", port, port, port))
			if strings.TrimSpace(out) != "" {
				t.Fatalf("port %d in use or retained after deletion: %s", port, out)
			}
		}
	}
	assertFreePorts()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	// The private key remains in memory. Only the disposable guest receives
	// the public key; no node credential is copied into it or written to disk.
	publicKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	httpClient := &http.Client{Timeout: 15 * time.Second, Transport: transport}
	assertHTTP := func(port int, name string) {
		t.Helper()
		resp, err := httpClient.Get("http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/identity.txt")
		if err != nil {
			t.Fatalf("external HTTP port %d: %v; devices: %s", port, err, run("incus config device show "+shellSingleQuote(name)))
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != name {
			t.Fatalf("wrong HTTP destination: port=%d status=%d body=%q error=%v want=%s", port, resp.StatusCode, body, readErr, name)
		}
	}
	// Reuse exactly the same ports. A generic HTTP 200 is insufficient: the
	// body must identify the new guest so a stale DNAT destination cannot pass.
	for generation := 0; generation < 2; generation++ {
		name := fmt.Sprintf("ocv-live-ports-%d-%d", time.Now().UnixNano(), generation)
		run("incus init " + shellSingleQuote(image) + " " + shellSingleQuote(name) + " -c user.ocv.test=" + shellSingleQuote(name))
		deleted := false
		t.Cleanup(func() {
			if deleted {
				return
			}
			command := "test \"$(incus config get " + shellSingleQuote(name) + " user.ocv.test)\" = " + shellSingleQuote(name) + " && incus delete -f " + shellSingleQuote(name)
			if output, err := client.Execute(command); err != nil {
				t.Errorf("owned disposable guest cleanup failed: %s: %v", output, err)
			}
		})
		allow := generation == 0
		if err := p.configureInstanceSecurity(context.Background(), provider.InstanceConfig{Name: name, InstanceType: "container", AllowNesting: &allow}); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(run("incus config get " + shellSingleQuote(name) + " security.nesting")); got != strconv.FormatBool(allow) {
			t.Fatalf("nesting=%q, want %t", got, allow)
		}
		run("incus start " + shellSingleQuote(name))
		address := ""
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			output, err := client.Execute("incus exec " + shellSingleQuote(name) + " -- ip -4 -o addr show dev eth0 scope global")
			if err == nil {
				address, _ = utils.ParseFirstIPv4AddressOutput(output)
			}
			if address != "" {
				break
			}
			time.Sleep(time.Second)
		}
		if address == "" {
			t.Fatal("guest IPv4 did not become ready")
		}
		t.Logf("created guest %s address=%s nesting=%t", name, address, allow)
		// An exec scope can reap background children. Let systemd own the
		// services, and confirm readiness before assessing port forwarding.
		serviceScript := "set -e\n" +
			"if ! command -v python3 >/dev/null || ! command -v sshd >/dev/null; then apt-get update -qq; DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3 openssh-server; fi\n" +
			"printf '%s\\n' 'SSH fixture initial authentication settings:'\nsshd -T | grep -E '^(permitrootlogin|pubkeyauthentication|authorizedkeysfile) '\n" +
			"install -d -m 700 /root/.ssh\nprintf '%s\\n' " + shellSingleQuote(publicKey) + " > /root/.ssh/authorized_keys\nchmod 600 /root/.ssh/authorized_keys\n" +
			"mkdir -p /etc/ssh/sshd_config.d\nprintf '%s\\n' 'PubkeyAuthentication yes' 'PermitRootLogin prohibit-password' > /etc/ssh/sshd_config.d/00-ocv-live-test.conf\n" +
			"systemctl restart ssh\nmkdir -p /tmp/ocv-live-http\nprintf '%s\\n' " + shellSingleQuote(name) + " > /tmp/ocv-live-http/identity.txt\n" +
			"systemd-run --unit=ocv-live-http --collect /usr/bin/python3 -m http.server 18080 --bind 0.0.0.0 --directory /tmp/ocv-live-http\n" +
			"systemd-run --unit=ocv-live-http-range --collect /usr/bin/python3 -m http.server 18081 --bind 0.0.0.0 --directory /tmp/ocv-live-http\n"
		t.Logf("guest service setup: %s", run("incus exec "+shellSingleQuote(name)+" -- sh -c "+shellSingleQuote(serviceScript)))
		ready := false
		for attempt := 0; attempt < 20; attempt++ {
			probe := "import urllib.request; [urllib.request.urlopen('http://127.0.0.1:%s/identity.txt' % p, timeout=2).read() for p in (18080,18081)]"
			if _, err := client.Execute("incus exec " + shellSingleQuote(name) + " -- python3 -c " + shellSingleQuote(probe)); err == nil {
				ready = true
				break
			}
			time.Sleep(time.Second)
		}
		if !ready {
			t.Fatalf("guest HTTP not ready: %s", run("incus exec "+shellSingleQuote(name)+" -- journalctl -u ocv-live-http -u ocv-live-http-range --no-pager -n 30"))
		}
		if err := p.SetupPortMappingWithIP(context.Background(), name, basePort, 18080, "tcp", "device_proxy", address); err != nil {
			t.Fatalf("provider HTTP mapping: %v", err)
		}
		if err := p.SetupPortMappingWithIP(context.Background(), name, basePort+1, 22, "tcp", "device_proxy", address); err != nil {
			t.Fatalf("provider SSH mapping: %v", err)
		}
		if err := p.setupPortRangeMapping(name, basePort+2, basePort+3, 18080, 18081, "both", address); err != nil {
			t.Fatalf("provider range mapping: %v", err)
		}
		for _, port := range []int{basePort, basePort + 2, basePort + 3} {
			assertHTTP(port, name)
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(basePort+1)), 15*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		// Bound the SSH handshake and command as well as the TCP connect.
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		sshConn, channels, requests, err := ssh.NewClientConn(conn, conn.RemoteAddr().String(), &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
		if err != nil {
			_ = conn.Close()
			t.Fatalf("external NAT IPv4 SSH login: %v; guest SSH diagnostics: %s", err, run("incus exec "+shellSingleQuote(name)+" -- sh -c 'sshd -T | grep -E \"^(permitrootlogin|pubkeyauthentication|authorizedkeysfile) \"; journalctl -u ssh --no-pager -n 15'"))
		}
		guestSSH := ssh.NewClient(sshConn, channels, requests)
		session, err := guestSSH.NewSession()
		if err != nil {
			_ = guestSSH.Close()
			t.Fatal(err)
		}
		sshResult, sshErr := session.CombinedOutput("cat /tmp/ocv-live-http/identity.txt; printf '%s\\n' \"$SSH_CONNECTION\"")
		_ = session.Close()
		_ = guestSSH.Close()
		if sshErr != nil || !strings.HasPrefix(string(sshResult), name+"\n") {
			t.Fatalf("wrong SSH guest or command failed: %s: %v", sshResult, sshErr)
		}
		t.Logf("external NAT IPv4 SSH/HTTP and offset range passed: %s", strings.TrimSpace(string(sshResult)))
		if err := p.RemovePortMappingForFamily(name, basePort, 18080, basePort, 18080, 1, "tcp", "device_proxy", address, false); err != nil {
			t.Fatal(err)
		}
		if out := run("incus config device show " + shellSingleQuote(name)); strings.Contains(out, fmt.Sprintf("proxy-tcp-%d:", basePort)) {
			t.Fatalf("proxy survived deletion: %s", out)
		}
		assertHTTP(basePort+2, name) // One mapping's removal must preserve others.
		if generation == 0 && os.Getenv("OCV_LIVE_NESTING") == "yes" {
			run("incus exec " + shellSingleQuote(name) + " -- sh -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker.io && systemctl start docker && docker run --rm hello-world'")
			t.Log("nested Docker hello-world passed")
		}
		run("test \"$(incus config get " + shellSingleQuote(name) + " user.ocv.test)\" = " + shellSingleQuote(name))
		if err := p.DeleteInstance(context.Background(), name); err != nil {
			t.Fatalf("provider deletion: %v", err)
		}
		deleted = true
		assertFreePorts()
		t.Logf("provider deletion removed all test mappings for generation %d", generation)
	}
}
