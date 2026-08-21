package admin

// KubeVirt exposes VNC through its VMI subresource. virtctl's proxy-only mode
// provides a short-lived, node-local RFB listener for that stream. Starting it
// is deliberately deferred until the user chooses VNC; the capability request
// checks only the live VMI, CLI and RBAC prerequisites.

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/utils"

	"github.com/gorilla/websocket"
)

const (
	kubeVirtConsoleNamespace = "kubevirt-vms"
	kubeVirtVNCSetupTimeout  = 15 * time.Second
	kubeVirtVNCDialTimeout   = 5 * time.Second
)

var kubeVirtVNCSessionSequence uint64

type kubeVirtVNCSession struct {
	pid     int
	port    int
	lockDir string
}

func kubeVirtVNCProxyAvailable(executor utils.ShellExecutor) bool {
	return kubeVirtConsoleSubresourceAvailable(executor, "vnc")
}

// kubeVirtSerialConsoleAvailable deliberately checks the same live CLI/RBAC
// prerequisites as VNC. Opening virtctl console only to probe it would create
// an interactive stream, so this is the bounded, non-mutating capability test.
func kubeVirtSerialConsoleAvailable(executor utils.ShellExecutor) bool {
	return kubeVirtConsoleSubresourceAvailable(executor, "console")
}

func kubeVirtConsoleSubresourceAvailable(executor utils.ShellExecutor, subresource string) bool {
	available, _ := kubeVirtConsoleSubresourceReason(executor, subresource)
	return available
}

// kubeVirtConsoleSubresourceReason runs the same live CLI/RBAC probe as the
// availability helper but retains the command/output diagnostic for the
// capability response. This prevents a VMI with a missing virtctl binary or
// RBAC rule from silently losing its VNC option.
func kubeVirtConsoleSubresourceReason(executor utils.ShellExecutor, subresource string) (bool, string) {
	command, ok := kubeVirtConsoleAccessProbeCommand(subresource)
	if !ok {
		return false, "KubeVirt 控制台子资源不受支持: " + subresource
	}
	output, err := executor.ExecuteWithTimeout(command, consoleRuntimeProbeTimeout)
	if err != nil {
		reason := "KubeVirt " + subresource + " 控制台实际探测失败: " + err.Error()
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			reason += "；远端输出: " + utils.TruncateString(trimmed, 240)
		}
		return false, reason
	}
	if strings.TrimSpace(output) != "yes" {
		return false, "KubeVirt 节点未通过 " + subresource + " 控制台 CLI/RBAC 实际校验；远端返回: " + utils.TruncateString(strings.TrimSpace(output), 240)
	}
	return true, ""
}

func kubeVirtConsoleAccessProbeCommand(subresource string) (string, bool) {
	switch subresource {
	case "vnc", "console":
		return "command -v virtctl >/dev/null 2>&1 && " +
			"KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl auth can-i get virtualmachineinstances/" + subresource + " -n " + kubeVirtConsoleNamespace +
			" 2>/dev/null | grep -qx yes && printf yes", true
	default:
		return "", false
	}
}

func buildKubeVirtConsoleVNCTarget(inst providerModel.Instance, p providerModel.Provider, runtimeID string) consoleTarget {
	target := consoleTarget{
		protocol:    consoleProtocolVNC,
		kubeVirtVNC: true,
		runtimeID:   strings.TrimSpace(runtimeID),
		instanceID:  inst.ID,
		provider:    p,
	}
	if target.runtimeID == "" {
		target.reason = "KubeVirt VMI 缺少运行时标识，无法建立 VNC 控制台"
		return target
	}
	target.transport, target.reason = consoleTerminalTransport(p)
	target.available = target.reason == ""
	return target
}

func nextKubeVirtVNCSessionID() string {
	sequence := atomic.AddUint64(&kubeVirtVNCSessionSequence, 1)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), sequence)
}

func kubeVirtVNCStartCommand(runtimeID, sessionID string) string {
	return "sh -c " + utils.ShellSingleQuote(fmt.Sprintf(`set -eu
INSTANCE=%s
SESSION=%s
RUNTIME=/run/oneclickvirt-kubevirt-vnc
if ! mkdir -p "$RUNTIME" 2>/dev/null || [ ! -w "$RUNTIME" ]; then
  RUNTIME=/tmp/oneclickvirt-kubevirt-vnc
  mkdir -p "$RUNTIME"
fi
LOCK="$RUNTIME/session-$SESSION"
mkdir "$LOCK"
cleanup() { rm -rf -- "$LOCK"; }
trap cleanup EXIT
if ! command -v ss >/dev/null 2>&1 && ! command -v netstat >/dev/null 2>&1; then
  echo "节点未安装 ss 或 netstat，无法安全检查 KubeVirt VNC 代理端口" >&2
  exit 21
fi
listener_table() {
  if command -v ss >/dev/null 2>&1 && ss -ltn 2>/dev/null; then return 0; fi
  netstat -ltn
}
port_is_listening() {
  listener_table 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]$1$"
}
for candidate in $(seq 6200 6399); do
  PORTLOCK="$RUNTIME/port-$candidate.lock"
  if ! mkdir "$PORTLOCK" 2>/dev/null; then
    existing="$(cat "$PORTLOCK/pid" 2>/dev/null || true)"
    case "$existing" in ''|*[!0-9]*) rm -rf -- "$PORTLOCK" ;; *) kill -0 "$existing" 2>/dev/null || rm -rf -- "$PORTLOCK" ;; esac
    mkdir "$PORTLOCK" 2>/dev/null || continue
  fi
  if port_is_listening "$candidate"; then rm -rf -- "$PORTLOCK"; continue; fi
  nohup env KUBECONFIG=/etc/rancher/k3s/k3s.yaml virtctl vnc --proxy-only --address=127.0.0.1 --port="$candidate" -n %s "$INSTANCE" >"$LOCK/proxy.log" 2>&1 </dev/null &
  PID=$!
  printf '%%s\n' "$PID" > "$PORTLOCK/pid"
  printf '%%s\n' "$PID" > "$LOCK/pid"
  printf '%%s\n' "$PORTLOCK" > "$LOCK/port-lock"
  for _ in $(seq 1 30); do
    if port_is_listening "$candidate"; then
      printf 'ONECLICKVIRT_KUBEVIRT_VNC\t%%s\t%%s\t%%s\n' "$PID" "$candidate" "$LOCK"
      trap - EXIT
      exit 0
    fi
    kill -0 "$PID" 2>/dev/null || break
    sleep 0.2
  done
  kill "$PID" >/dev/null 2>&1 || true
  rm -rf -- "$PORTLOCK"
done
if [ -f "$LOCK/proxy.log" ]; then tail -n 30 "$LOCK/proxy.log" >&2 || true; fi
echo "KubeVirt VNC 代理未能启动可用的本机监听端口" >&2
exit 20`, utils.ShellSingleQuote(runtimeID), utils.ShellSingleQuote(sessionID), utils.ShellSingleQuote(kubeVirtConsoleNamespace)))
}

func parseKubeVirtVNCSession(raw string) (kubeVirtVNCSession, error) {
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 4)
		if len(parts) != 4 || parts[0] != "ONECLICKVIRT_KUBEVIRT_VNC" {
			continue
		}
		pid, pidErr := strconv.Atoi(parts[1])
		port, portErr := strconv.Atoi(parts[2])
		lockDir := strings.TrimSpace(parts[3])
		if pidErr != nil || pid <= 0 || portErr != nil || port < 1 || port > 65535 || !isKubeVirtVNCLockDir(lockDir) {
			return kubeVirtVNCSession{}, fmt.Errorf("KubeVirt VNC 代理返回无效会话信息")
		}
		return kubeVirtVNCSession{pid: pid, port: port, lockDir: lockDir}, nil
	}
	return kubeVirtVNCSession{}, fmt.Errorf("KubeVirt VNC 代理未返回启动结果")
}

func isKubeVirtVNCLockDir(value string) bool {
	value = strings.TrimSpace(value)
	for _, root := range []string{"/run/oneclickvirt-kubevirt-vnc", "/tmp/oneclickvirt-kubevirt-vnc"} {
		prefix := root + "/session-"
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(value, prefix)
		if isKubeVirtVNCSessionSegment(suffix) {
			return true
		}
	}
	return false
}

func isKubeVirtVNCSessionSegment(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func kubeVirtVNCStopCommand(session kubeVirtVNCSession) string {
	return "sh -c " + utils.ShellSingleQuote(fmt.Sprintf(`LOCK=%s
PID=%d
case "$LOCK" in /run/oneclickvirt-kubevirt-vnc/session-*|/tmp/oneclickvirt-kubevirt-vnc/session-*) ;; *) exit 0;; esac
SESSION="${LOCK##*/}"
case "$SESSION" in session-|*[!A-Za-z0-9_-]*) exit 0;; esac
if [ -r "$LOCK/pid" ]; then PID="$(cat "$LOCK/pid" 2>/dev/null || true)"; fi
case "$PID" in ''|*[!0-9]*) ;; *)
  if [ -r "/proc/$PID/cmdline" ] && tr '\000' ' ' < "/proc/$PID/cmdline" | grep -q '[v]irtctl vnc'; then kill "$PID" >/dev/null 2>&1 || true; fi
;; esac
PORTLOCK="$(cat "$LOCK/port-lock" 2>/dev/null || true)"
case "$PORTLOCK" in /run/oneclickvirt-kubevirt-vnc/port-*.lock|/tmp/oneclickvirt-kubevirt-vnc/port-*.lock) rm -rf -- "$PORTLOCK";; esac
rm -rf -- "$LOCK"`, utils.ShellSingleQuote(session.lockDir), session.pid))
}

func openKubeVirtVNCConn(target consoleTarget) (net.Conn, func(), error) {
	if !target.kubeVirtVNC || strings.TrimSpace(target.runtimeID) == "" {
		return nil, nil, fmt.Errorf("KubeVirt VNC 运行时信息无效")
	}
	executor, executorCleanup, err := newConsoleExecutor(target.provider)
	if err != nil {
		return nil, nil, err
	}
	output, err := executor.ExecuteWithTimeout(kubeVirtVNCStartCommand(target.runtimeID, nextKubeVirtVNCSessionID()), kubeVirtVNCSetupTimeout)
	if err != nil {
		executorCleanup()
		return nil, nil, fmt.Errorf("KubeVirt VNC 代理创建失败: %w；远端输出: %s", err, utils.TruncateString(strings.TrimSpace(output), 600))
	}
	session, err := parseKubeVirtVNCSession(output)
	if err != nil {
		executorCleanup()
		return nil, nil, err
	}

	dial := func() (net.Conn, error) {
		switch target.transport {
		case "agent":
			return agentService.OpenTunnelConn(target.provider.ID, "127.0.0.1", session.port)
		case "ssh":
			client, ok := executor.(*utils.SSHClient)
			if !ok {
				return nil, fmt.Errorf("SSH KubeVirt VNC 控制台传输初始化失败")
			}
			return client.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(session.port)))
		case "local":
			return net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(session.port)), kubeVirtVNCDialTimeout)
		default:
			return nil, fmt.Errorf("KubeVirt VNC 不支持节点连接方式 %q", target.transport)
		}
	}
	conn, dialErr := dial()
	if dialErr != nil {
		_, _ = executor.ExecuteWithTimeout(kubeVirtVNCStopCommand(session), kubeVirtVNCDialTimeout)
		executorCleanup()
		return nil, nil, fmt.Errorf("连接 KubeVirt VNC 代理失败: %w", dialErr)
	}
	return conn, func() {
		_ = conn.Close()
		_, _ = executor.ExecuteWithTimeout(kubeVirtVNCStopCommand(session), kubeVirtVNCDialTimeout)
		executorCleanup()
	}, nil
}

// probeKubeVirtVNCConnection starts the node-local virtctl proxy, reads the
// RFB greeting, and tears the proxy down immediately. This is a real endpoint
// check rather than a provider-type guess; the browser still waits for an
// explicit VNC selection before opening its own session.
func probeKubeVirtVNCConnection(target consoleTarget) (bool, string) {
	conn, cleanup, err := openKubeVirtVNCConn(target)
	if err != nil {
		return false, err.Error()
	}
	defer cleanup()
	_ = conn.SetDeadline(time.Now().Add(kubeVirtVNCSetupTimeout))
	version := make([]byte, 12)
	if _, err := io.ReadFull(conn, version); err != nil {
		return false, "读取 KubeVirt VNC RFB 版本失败: " + err.Error()
	}
	if _, _, err := parseRFBVersion(version); err != nil {
		return false, "KubeVirt VNC 返回无效的 RFB 版本: " + err.Error()
	}
	return true, ""
}

func proxyKubeVirtVNCWebSocket(panel *websocket.Conn, target consoleTarget) {
	conn, cleanup, err := openKubeVirtVNCConn(target)
	if err != nil {
		writeVNCWebSocketFailure(panel, "KubeVirt VNC 连接失败: "+err.Error())
		return
	}
	defer cleanup()
	proxyConsoleByteStreams(panel, &consoleWebSocketReader{ws: panel}, conn)
}
