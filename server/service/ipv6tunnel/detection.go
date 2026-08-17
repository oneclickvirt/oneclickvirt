package ipv6tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"oneclickvirt/utils"
)

const defaultIPv4RouteProbe = "1.1.1.1"

const appliedLocalIPv4Marker = "ONECLICKVIRT_LOCAL_IPV4"

var (
	routeSourcePattern      = regexp.MustCompile(`(?:^|\s)src\s+([^\s]+)`)
	routeDevicePattern      = regexp.MustCompile(`(?:^|\s)dev\s+([^\s]+)`)
	appliedLocalIPv4Pattern = regexp.MustCompile(`(?m)^` + appliedLocalIPv4Marker + `\|([^\s|]+)\s*$`)
	// SSH commands can inherit a node's forced color configuration. Strip
	// terminal escape sequences before both parsing and returning diagnostics so
	// control characters cannot split route fields or leak into the UI.
	ansiEscapePattern      = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[PX^_][^\x1b]*\x1b\\|[\x20-\x2f]*[\x30-\x7e])`)
	terminalControlPattern = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
)

// DetectLocalIPv4Request accepts the tunnel server IPv4. If it is omitted the
// node's ordinary IPv4 default route is inspected instead.
type DetectLocalIPv4Request struct {
	RemoteIPv4 string `json:"remoteIpv4"`
}

// LocalIPv4Detection is the route-selected address to use as the tunnel's
// local endpoint. It may intentionally be RFC1918 when the node is behind NAT.
type LocalIPv4Detection struct {
	LocalIPv4  string `json:"localIpv4"`
	RemoteIPv4 string `json:"remoteIpv4"`
	Interface  string `json:"interfaceName,omitempty"`
}

// RemoteCommandError preserves the node-side diagnostic output for the API
// and UI without exposing controller credentials or command internals.
type RemoteCommandError struct {
	Operation string
	Output    string
	Cause     error
}

func (e *RemoteCommandError) Error() string {
	if e == nil {
		return ""
	}
	if e.Output == "" {
		return fmt.Sprintf("%s: %v", e.Operation, e.Cause)
	}
	return fmt.Sprintf("%s: %v\n节点输出:\n%s", e.Operation, e.Cause, e.Output)
}

func (e *RemoteCommandError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newRemoteCommandError(operation, output string, cause error) error {
	if cause == nil {
		return nil
	}
	return &RemoteCommandError{
		Operation: operation,
		Output:    limitRemoteOutput(output),
		Cause:     cause,
	}
}

// IsRemoteCommandError lets transport handlers distinguish node execution
// failures from request validation failures.
func IsRemoteCommandError(err error) bool {
	var remoteErr *RemoteCommandError
	return errors.As(err, &remoteErr)
}

func limitRemoteOutput(output string) string {
	output = strings.TrimSpace(stripTerminalControlSequences(output))
	if len(output) > maxRemoteError {
		return output[:maxRemoteError]
	}
	return output
}

func stripTerminalControlSequences(output string) string {
	output = ansiEscapePattern.ReplaceAllString(output, "")
	return terminalControlPattern.ReplaceAllString(output, "")
}

// DetectLocalIPv4 performs exactly one read-only remote route query. It does
// not open a database transaction and deliberately accepts private IPv4 route
// sources, because NAT-backed PVE nodes must bind the tunnel to that source.
func (s *Service) DetectLocalIPv4(ctx context.Context, providerID uint, remoteIPv4 string) (*LocalIPv4Detection, error) {
	lock := providerMutex(providerID)
	lock.Lock()
	defer lock.Unlock()

	db, err := s.database()
	if err != nil {
		return nil, err
	}
	if err := ensureProviderExists(db, providerID); err != nil {
		return nil, err
	}
	return s.detectLocalIPv4(ctx, providerID, remoteIPv4)
}

// detectLocalIPv4 is called with the provider lock already held by lifecycle
// operations, preventing an address probe from interleaving with an apply.
func (s *Service) detectLocalIPv4(ctx context.Context, providerID uint, remoteIPv4 string) (*LocalIPv4Detection, error) {
	target := strings.TrimSpace(remoteIPv4)
	if target == "" {
		target = defaultIPv4RouteProbe
	}
	target, err := normalizeIPv4(target)
	if err != nil {
		return nil, fmt.Errorf("远端IPv4无效，无法识别客户端IPv4: %w", err)
	}

	output, err := s.run(ctx, providerID, buildDetectLocalIPv4Command(target))
	if err != nil {
		return nil, newRemoteCommandError("识别隧道客户端IPv4失败", output, err)
	}
	localIPv4, interfaceName, err := parseRouteSource(output)
	if err != nil {
		return nil, newRemoteCommandError("识别隧道客户端IPv4失败", output, err)
	}
	return &LocalIPv4Detection{
		LocalIPv4:  localIPv4,
		RemoteIPv4: target,
		Interface:  interfaceName,
	}, nil
}

func buildDetectLocalIPv4Command(remoteIPv4 string) string {
	return fmt.Sprintf(`set -eu
command -v ip >/dev/null 2>&1 || { echo 'iproute2 is unavailable' >&2; exit 1; }
ip -4 route get %s`, utils.ShellSingleQuote(remoteIPv4))
}

func parseRouteSource(output string) (string, string, error) {
	output = stripTerminalControlSequences(output)
	match := routeSourcePattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return "", "", fmt.Errorf("路由结果中没有 src 字段")
	}
	localIPv4, err := normalizeIPv4(match[1])
	if err != nil {
		return "", "", fmt.Errorf("路由返回的 src 地址无效: %w", err)
	}
	ip := net.ParseIP(localIPv4)
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Equal(net.IPv4bcast) {
		return "", "", fmt.Errorf("路由返回的 src 地址不可用于IPv6隧道: %s", localIPv4)
	}
	deviceMatch := routeDevicePattern.FindStringSubmatch(output)
	interfaceName := ""
	if len(deviceMatch) == 2 && interfacePattern.MatchString(deviceMatch[1]) {
		interfaceName = deviceMatch[1]
	}
	return localIPv4, interfaceName, nil
}

func parseAppliedLocalIPv4(output string) (string, error) {
	match := appliedLocalIPv4Pattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return "", fmt.Errorf("节点未返回自动识别的客户端IPv4")
	}
	localIPv4, err := normalizeIPv4(match[1])
	if err != nil {
		return "", fmt.Errorf("节点返回的客户端IPv4无效: %w", err)
	}
	ip := net.ParseIP(localIPv4)
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Equal(net.IPv4bcast) {
		return "", fmt.Errorf("节点返回的客户端IPv4不可用于IPv6隧道: %s", localIPv4)
	}
	return localIPv4, nil
}
