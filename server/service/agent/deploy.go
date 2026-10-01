package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"oneclickvirt/assets"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

const (
	AgentBinaryName  = "oneclickvirt-agent"
	AgentInstallDir  = "/opt/oneclickvirt/agent"
	AgentPort        = 23782
	AgentServiceName = "oneclickvirt-agent"
	agentEgressGuard = "/usr/local/bin/oneclickvirt-egress-boot-guard"
	egressGuardUnit  = "oneclickvirt-egress-guard"

	maxInlineAgentArchiveBytes = 32 * 1024
)

var agentConfigSyncLocks sync.Map // provider ID -> *sync.Mutex

// AgentConfig holds the configuration parameters for the agent deployment.
type AgentConfig struct {
	Token                   string
	TrafficCollectInterval  int // seconds, default 5
	ResourceCollectInterval int // seconds, default 30
	ExtraExcludeCIDRsV4     string
	ExtraExcludeCIDRsV6     string
	TrafficCollectMethod    string // "nft" (default) or "ipt"

	// Reverse proxy configuration
	EnableReverseProxy          bool   // Enable reverse proxy feature
	ProxyHTTPPort               int    // HTTP port (default 80)
	ProxyHTTPSPort              int    // HTTPS port (default 443)
	ProxyEnableHTTP             bool   // Enable HTTP proxy
	ProxyEnableHTTPS            bool   // Enable HTTPS proxy
	ProxyTrustCloudflareHeaders bool   // Trust CF-Visitor on the HTTP listener
	ProxyTLSCertPath            string // TLS cert file path on node
	ProxyTLSKeyPath             string // TLS key file path on node
}

// ConfigForProvider is the single source for monitoring and proxy settings
// applied by both the monitoring page and the provider reload task.
func ConfigForProvider(p *providerModel.Provider, monitoring *monitoringModel.MonitoringConfig) *AgentConfig {
	return &AgentConfig{
		Token:                       monitoring.AgentToken,
		TrafficCollectInterval:      monitoring.CollectInterval,
		ResourceCollectInterval:     monitoring.ResourceCollectInterval,
		ExtraExcludeCIDRsV4:         monitoring.ExtraExcludeCIDRsV4,
		ExtraExcludeCIDRsV6:         monitoring.ExtraExcludeCIDRsV6,
		TrafficCollectMethod:        monitoring.TrafficCollectMethod,
		EnableReverseProxy:          p.EnableDomainBinding,
		ProxyHTTPPort:               p.ProxyHTTPPort,
		ProxyHTTPSPort:              p.ProxyHTTPSPort,
		ProxyEnableHTTP:             p.ProxyEnableHTTP,
		ProxyEnableHTTPS:            p.ProxyEnableHTTPS,
		ProxyTrustCloudflareHeaders: p.ProxyTrustCloudflareHeaders,
		ProxyTLSCertPath:            p.ProxyTLSCertPath,
		ProxyTLSKeyPath:             p.ProxyTLSKeyPath,
	}
}

func (c *AgentConfig) trafficInterval() int {
	if c.TrafficCollectInterval <= 0 {
		return 5
	}
	return c.TrafficCollectInterval
}

func (c *AgentConfig) resourceInterval() int {
	if c.ResourceCollectInterval <= 0 {
		return 30
	}
	return c.ResourceCollectInterval
}

func buildEnvFile(cfg *AgentConfig) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("API_TOKEN=%s\n", systemdEnvironmentValue(cfg.Token)))
	sb.WriteString(fmt.Sprintf("TRAFFIC_COLLECT_INTERVAL=%d\n", cfg.trafficInterval()))
	sb.WriteString(fmt.Sprintf("RESOURCE_COLLECT_INTERVAL=%d\n", cfg.resourceInterval()))
	sb.WriteString("RUST_LOG=info\n")
	method := cfg.TrafficCollectMethod
	if method == "" {
		method = "nft"
	}
	sb.WriteString(fmt.Sprintf("TRAFFIC_COLLECT_METHOD=%s\n", systemdEnvironmentValue(method)))
	if cfg.ExtraExcludeCIDRsV4 != "" {
		sb.WriteString(fmt.Sprintf("EXTRA_EXCLUDE_CIDRS_V4=%s\n", systemdEnvironmentValue(cfg.ExtraExcludeCIDRsV4)))
	}
	if cfg.ExtraExcludeCIDRsV6 != "" {
		sb.WriteString(fmt.Sprintf("EXTRA_EXCLUDE_CIDRS_V6=%s\n", systemdEnvironmentValue(cfg.ExtraExcludeCIDRsV6)))
	}
	// Transparent egress uses only a fixed, audited dependency set.  The
	// agent still reports missing kernel capabilities separately; this flag
	// permits the guarded package-manager probe to install user-space tools.
	sb.WriteString("ONECLICKVIRT_EGRESS_AUTO_INSTALL=true\n")
	// Controller-managed agents are explicitly authorized to reconcile the
	// desired egress state selected by an administrator. The Agent still
	// validates capabilities and keeps affected sources fail-closed on errors.
	sb.WriteString("ONECLICKVIRT_EGRESS_APPLY=true\n")

	// Reverse proxy configuration
	sb.WriteString(fmt.Sprintf("ENABLE_REVERSE_PROXY=%t\n", cfg.EnableReverseProxy))
	if cfg.EnableReverseProxy {
		if cfg.ProxyTrustCloudflareHeaders {
			sb.WriteString("PROXY_TRUST_CLOUDFLARE_HEADERS=true\n")
		}
		if cfg.ProxyEnableHTTP {
			httpPort := cfg.ProxyHTTPPort
			if httpPort == 0 {
				httpPort = 80
			}
			sb.WriteString(fmt.Sprintf("PROXY_HTTP_ADDR=0.0.0.0:%d\n", httpPort))
		}
		if cfg.ProxyEnableHTTPS {
			httpsPort := cfg.ProxyHTTPSPort
			if httpsPort == 0 {
				httpsPort = 443
			}
			sb.WriteString(fmt.Sprintf("PROXY_HTTPS_ADDR=0.0.0.0:%d\n", httpsPort))
			if cfg.ProxyTLSCertPath != "" {
				sb.WriteString(fmt.Sprintf("PROXY_TLS_CERT=%s\n", systemdEnvironmentValue(cfg.ProxyTLSCertPath)))
			}
			if cfg.ProxyTLSKeyPath != "" {
				sb.WriteString(fmt.Sprintf("PROXY_TLS_KEY=%s\n", systemdEnvironmentValue(cfg.ProxyTLSKeyPath)))
			}
		}
	}

	return sb.String()
}

// systemdEnvironmentValue writes a value in the quoted EnvironmentFile form.
// Values originate from provider/user configuration, so newlines and control
// characters must never be allowed to create additional environment entries.
func systemdEnvironmentValue(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// buildDeployScript generates a self-contained bash deploy script for the agent.
// The script handles download verification, extraction, .env writing and systemd setup.
func buildDeployScript(cfg *AgentConfig, version, arch string, downloadURLs []string, embeddedArchiveB64 string) string {
	binaryName := fmt.Sprintf("%s-linux-%s", AgentBinaryName, arch)
	archiveName := fmt.Sprintf("%s.tar.gz", binaryName)
	envContent := buildEnvFile(cfg)
	collectMethod := cfg.TrafficCollectMethod
	if collectMethod == "" {
		collectMethod = "nft"
	}

	serviceUnit := fmt.Sprintf(`[Unit]
Description=OneclickVirt Monitoring Agent
After=network.target %s.service
Wants=%s.service

[Service]
Type=simple
WorkingDirectory=%s
EnvironmentFile=-%s/.env
ExecStartPre=%s
ExecStart=%s/%s
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`, egressGuardUnit, egressGuardUnit, AgentInstallDir, AgentInstallDir, agentEgressGuard, AgentInstallDir, AgentBinaryName)

	guardScript := `#!/bin/sh
set -eu

SOURCE_DIR="${ONECLICKVIRT_EGRESS_STATE_DIR:-/var/lib/oneclickvirt/egress}"
SOURCE_FILE="${SOURCE_DIR}/managed-sources"
NFT_BIN="${ONECLICKVIRT_EGRESS_NFT_BIN:-nft}"
TABLE_FAMILY="inet"
TABLE_NAME="oneclickvirt_egress_boot"

[ -f "$SOURCE_FILE" ] || exit 0
[ -s "$SOURCE_FILE" ] || exit 0
command -v "$NFT_BIN" >/dev/null 2>&1 || {
    printf '%s\n' "oneclickvirt egress boot guard: nft is unavailable" >&2
    exit 1
}

TMP_SCRIPT=$(mktemp "${TMPDIR:-/tmp}/oneclickvirt-egress-guard.XXXXXX")
trap 'rm -f "$TMP_SCRIPT"' EXIT HUP INT TERM

TABLE_EXISTS=0
if "$NFT_BIN" list table "$TABLE_FAMILY" "$TABLE_NAME" >/dev/null 2>&1; then
    TABLE_EXISTS=1
fi
if [ "$TABLE_EXISTS" -eq 1 ]; then
    printf 'flush table %s %s\n' "$TABLE_FAMILY" "$TABLE_NAME" > "$TMP_SCRIPT"
else
    printf 'add table %s %s\n' "$TABLE_FAMILY" "$TABLE_NAME" > "$TMP_SCRIPT"
fi
cat >> "$TMP_SCRIPT" << 'NFTEOF'
add chain inet oneclickvirt_egress_boot boot_forward { type filter hook forward priority -200; policy accept; }
add chain inet oneclickvirt_egress_boot boot_output { type filter hook output priority -200; policy accept; }
add chain inet oneclickvirt_egress_boot boot_input { type filter hook input priority -200; policy accept; }
NFTEOF

VALID_SOURCES=0
while IFS= read -r source || [ -n "$source" ]; do
    [ -n "$source" ] || {
        printf '%s\n' "oneclickvirt egress boot guard: empty source line" >&2
        exit 1
    }
    case "$source" in
        *[!0-9A-Fa-f:./]*|*/*/*|*/)
            printf '%s\n' "oneclickvirt egress boot guard: invalid source entry" >&2
            exit 1
            ;;
    esac
    case "$source" in
        *:*/*) FAMILY="ip6"; MAX_PREFIX=128 ;;
        *.*/*) FAMILY="ip"; MAX_PREFIX=32 ;;
        *)
            printf '%s\n' "oneclickvirt egress boot guard: source is not an IP CIDR" >&2
            exit 1
            ;;
    esac
    PREFIX=${source##*/}
    case "$PREFIX" in
        ''|*[!0-9]*)
            printf '%s\n' "oneclickvirt egress boot guard: invalid CIDR prefix" >&2
            exit 1
            ;;
    esac
    if ! awk -v prefix="$PREFIX" -v maximum="$MAX_PREFIX" 'BEGIN { exit !(prefix <= maximum) }'; then
        printf '%s\n' "oneclickvirt egress boot guard: CIDR prefix out of range" >&2
        exit 1
    fi
    printf 'add rule inet oneclickvirt_egress_boot boot_forward %s saddr %s counter drop\n' "$FAMILY" "$source" >> "$TMP_SCRIPT"
    printf 'add rule inet oneclickvirt_egress_boot boot_output %s saddr %s counter drop\n' "$FAMILY" "$source" >> "$TMP_SCRIPT"
    printf 'add rule inet oneclickvirt_egress_boot boot_input %s saddr %s counter drop\n' "$FAMILY" "$source" >> "$TMP_SCRIPT"
    VALID_SOURCES=$((VALID_SOURCES + 1))
done < "$SOURCE_FILE"

[ "$VALID_SOURCES" -gt 0 ] || exit 0
"$NFT_BIN" -c -f "$TMP_SCRIPT" >/dev/null
"$NFT_BIN" -f "$TMP_SCRIPT" >/dev/null
`

	guardUnit := fmt.Sprintf(`[Unit]
Description=OneClickVirt early-boot egress fail-closed guard
Documentation=https://github.com/oneclickvirt/oneclickvirt
DefaultDependencies=no
After=local-fs.target nftables.service firewalld.service
Before=network-pre.target network.target network-online.target %s.service docker.service containerd.service crio.service podman.service libvirtd.service lxc.service lxd.service incus.service pve-guests.service kubelet.service

[Service]
Type=oneshot
EnvironmentFile=-%s/.env
ExecStart=%s
RemainAfterExit=yes

[Install]
RequiredBy=network-pre.target
`, AgentServiceName, AgentInstallDir, agentEgressGuard)

	// We use printf to write files to avoid heredoc nesting issues within the script itself.
	// envContent and serviceUnit are base64-encoded inside the script so any special chars are safe.
	envB64 := base64.StdEncoding.EncodeToString([]byte(envContent))
	svcB64 := base64.StdEncoding.EncodeToString([]byte(serviceUnit))
	guardB64 := base64.StdEncoding.EncodeToString([]byte(guardScript))
	guardUnitB64 := base64.StdEncoding.EncodeToString([]byte(guardUnit))

	// Space-separated URL list for the shell script to iterate over (CDN mirrors first, GitHub last).
	urlList := strings.Join(downloadURLs, " ")

	script := fmt.Sprintf(`#!/bin/sh
set -e
INSTALL_DIR="%s"
BINARY_NAME="%s"
SRC_BINARY_NAME="%s"
ARCHIVE_NAME="%s"
DOWNLOAD_URLS="%s"
EMBEDDED_ARCHIVE_B64="%s"
SERVICE_NAME="%s"
VERSION="%s"

echo "[0/6] check native egress dependencies..."
EGRESS_MISSING=""
command -v ip >/dev/null 2>&1 || EGRESS_MISSING="$EGRESS_MISSING iproute"
command -v nft >/dev/null 2>&1 || EGRESS_MISSING="$EGRESS_MISSING nftables"
command -v wg >/dev/null 2>&1 || EGRESS_MISSING="$EGRESS_MISSING wireguard-tools"
if [ -n "$EGRESS_MISSING" ]; then
    echo "  missing:$EGRESS_MISSING"
    EGRESS_INSTALL_OK=0
    if command -v apt-get >/dev/null 2>&1; then
        if apt-get update -qq >/dev/null 2>&1 && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq iproute2 nftables wireguard-tools >/dev/null 2>&1; then EGRESS_INSTALL_OK=1; fi
    elif command -v dnf >/dev/null 2>&1; then
        if dnf install -y -q iproute nftables wireguard-tools >/dev/null 2>&1; then EGRESS_INSTALL_OK=1; fi
    elif command -v yum >/dev/null 2>&1; then
        if yum install -y -q iproute nftables wireguard-tools >/dev/null 2>&1; then EGRESS_INSTALL_OK=1; fi
    elif command -v apk >/dev/null 2>&1; then
        if apk add --quiet iproute2 nftables wireguard-tools >/dev/null 2>&1; then EGRESS_INSTALL_OK=1; fi
    elif command -v pacman >/dev/null 2>&1; then
        if pacman -Sy --noconfirm iproute2 nftables wireguard-tools >/dev/null 2>&1; then EGRESS_INSTALL_OK=1; fi
    elif command -v zypper >/dev/null 2>&1; then
        if zypper --non-interactive install iproute2 nftables wireguard-tools >/dev/null 2>&1; then EGRESS_INSTALL_OK=1; fi
    fi
    if [ "$EGRESS_INSTALL_OK" -ne 1 ]; then
        echo "  [WARN] native egress dependencies could not be installed; capability endpoint will report the exact missing tools"
    fi
fi
echo "[OK] 0/6 native egress dependency probe complete"

echo "[1/6] check and install traffic monitoring dependency..."
# Use the configured method directly: systemd .env values are quoted, so
# grep/sed would otherwise compare the literal '"ipt"' against 'ipt'.
COLLECT_METHOD=%s

if [ "$COLLECT_METHOD" = "ipt" ]; then
    echo "  Traffic collect method: iptables"
    if ! command -v iptables >/dev/null 2>&1; then
        echo "  iptables command not found, installing..."
        if command -v apt-get >/dev/null 2>&1; then
            apt-get update -qq && apt-get install -y -qq iptables >/dev/null 2>&1
        elif command -v dnf >/dev/null 2>&1; then
            dnf install -y -q iptables >/dev/null 2>&1
        elif command -v yum >/dev/null 2>&1; then
            yum install -y -q iptables >/dev/null 2>&1
        elif command -v apk >/dev/null 2>&1; then
            apk add --quiet iptables >/dev/null 2>&1
        elif command -v pacman >/dev/null 2>&1; then
            pacman -Sy --noconfirm iptables >/dev/null 2>&1
        elif command -v zypper >/dev/null 2>&1; then
            zypper install -y -q iptables >/dev/null 2>&1
        fi
    fi
    echo "  iptables: $(iptables --version 2>/dev/null || echo 'not found')"
else
    echo "  Traffic collect method: nftables"
    if ! command -v nft >/dev/null 2>&1; then
        echo "  nft command not found, installing nftables..."
        if command -v apt-get >/dev/null 2>&1; then
            apt-get update -qq && apt-get install -y -qq nftables >/dev/null 2>&1
        elif command -v dnf >/dev/null 2>&1; then
            dnf install -y -q nftables >/dev/null 2>&1
        elif command -v yum >/dev/null 2>&1; then
            yum install -y -q nftables >/dev/null 2>&1
        elif command -v apk >/dev/null 2>&1; then
            apk add --quiet nftables >/dev/null 2>&1
        elif command -v pacman >/dev/null 2>&1; then
            pacman -Sy --noconfirm nftables >/dev/null 2>&1
        elif command -v zypper >/dev/null 2>&1; then
            zypper install -y -q nftables >/dev/null 2>&1
        else
            echo "  [WARN] unknown package manager, please install nftables manually"
        fi
        if command -v nft >/dev/null 2>&1; then
            echo "  nftables installed successfully"
        else
            echo "  [WARN] nftables installation may have failed, traffic monitoring may not work"
        fi
    else
        echo "  nftables already installed ($(nft -v 2>/dev/null || echo 'version unknown'))"
    fi
    # Ensure nftables service is enabled and started
    if command -v systemctl >/dev/null 2>&1; then
        systemctl enable nftables 2>/dev/null || true
        systemctl start nftables 2>/dev/null || true
    fi
fi
echo "[OK] 1/6 dependency checked"

echo "[2/6] create install directory..."
mkdir -p "$INSTALL_DIR"
echo "[OK] 2/6 install directory created"

echo "[3/6] download agent binary (version $VERSION)..."
cd "$INSTALL_DIR"
DOWNLOADED=0
if [ -n "${OCV_AGENT_ARCHIVE:-}" ]; then
    # Large controller-local assets are staged in bounded chunks over the
    # existing authenticated transport, not embedded in a huge shell argv.
    cp "$OCV_AGENT_ARCHIVE" "$ARCHIVE_NAME"
    DOWNLOADED=1
    echo "  source: staged controller asset"
elif [ -n "$EMBEDDED_ARCHIVE_B64" ]; then
    if printf '%%s' "$EMBEDDED_ARCHIVE_B64" | base64 -d > "$ARCHIVE_NAME" 2>/dev/null && [ -s "$ARCHIVE_NAME" ]; then
        DOWNLOADED=1
        echo "  source: embedded controller asset"
    else
        rm -f "$ARCHIVE_NAME"
    fi
fi
for url in $DOWNLOAD_URLS; do
    if [ "$DOWNLOADED" -eq 1 ]; then
        break
    fi
    if curl -fsSL --connect-timeout 20 --retry 1 -o "$ARCHIVE_NAME" "$url" 2>/dev/null && [ -s "$ARCHIVE_NAME" ]; then
        DOWNLOADED=1
        echo "  source: $url"
        break
    fi
    rm -f "$ARCHIVE_NAME"
done
if [ "$DOWNLOADED" -eq 0 ]; then
    echo "[FAIL] download failed - all mirrors unreachable or returned empty file"
    exit 1
fi
echo "[OK] 3/6 binary downloaded"

echo "[4/6] verify and extract binary..."
if ! tar -tzf "$ARCHIVE_NAME" > /dev/null 2>&1; then
    echo "[FAIL] downloaded file is not a valid tar.gz archive (possible 404 or network error)"
    rm -f "$ARCHIVE_NAME"
    exit 1
fi
tar -xzf "$ARCHIVE_NAME"
rm -f "$ARCHIVE_NAME"
if [ -f "$SRC_BINARY_NAME" ]; then
    mv "$SRC_BINARY_NAME" "$BINARY_NAME"
fi
chmod +x "$BINARY_NAME"
if [ ! -x "$BINARY_NAME" ]; then
    echo "[FAIL] binary not found after extraction"
    exit 1
fi
echo "[OK] 4/6 binary ready at $INSTALL_DIR/$BINARY_NAME"

echo "[5/6] write .env, boot guard, and systemd services..."
printf '%%s' "%s" | base64 -d > "$INSTALL_DIR/.env"
chmod 600 "$INSTALL_DIR/.env"
printf '%%s' "%s" | base64 -d > "%s"
chmod 700 "%s"
printf '%%s' "%s" | base64 -d > /etc/systemd/system/"%s".service
printf '%%s' "%s" | base64 -d > /etc/systemd/system/"$SERVICE_NAME".service
echo "[OK] 5/6 configuration written"

echo "[6/6] enable and start service..."
systemctl daemon-reload
systemctl enable "%s".service
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"
echo "[OK] 6/6 service started"
echo "DEPLOY_SUCCESS"
`,
		AgentInstallDir,
		AgentBinaryName,
		binaryName,
		archiveName,
		urlList,
		embeddedArchiveB64,
		AgentServiceName,
		version,
		utils.ShellSingleQuote(collectMethod),
		envB64,
		guardB64,
		agentEgressGuard,
		agentEgressGuard,
		guardUnitB64,
		egressGuardUnit,
		svcB64,
		egressGuardUnit,
	)
	return script
}

// DeployAgent deploys the agent binary to a provider host via SSH.
// Returns a deployment log string and any error.
func DeployAgent(ctx context.Context, providerInstance provider.Provider, token string, version string) (string, error) {
	return DeployAgentWithConfig(ctx, providerInstance, &AgentConfig{Token: token}, version)
}

// DeployAgentWithConfig deploys the agent binary with full configuration.
// It generates a complete shell script, uploads it via SSH (base64-encoded), executes it,
// and captures the per-step log output.
func DeployAgentWithConfig(ctx context.Context, providerInstance provider.Provider, cfg *AgentConfig, version string) (string, error) {
	arch, err := detectArchitecture(ctx, providerInstance)
	if err != nil {
		arch = "amd64"
	}

	binaryName := fmt.Sprintf("%s-linux-%s", AgentBinaryName, arch)
	archiveName := fmt.Sprintf("%s.tar.gz", binaryName)
	downloadURLs := buildDownloadURLList(version, archiveName)
	embeddedArchiveB64 := inlineAgentArchiveB64(archiveName)
	deployCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	stagedArchive := ""
	if content, assetErr := assets.ReadAgentAsset(archiveName); assetErr == nil && len(content) > maxInlineAgentArchiveBytes {
		stagedArchive, err = stageAgentArchive(deployCtx, providerInstance.ExecuteSSHCommand, content)
		if err != nil {
			return "", fmt.Errorf("stage controller Agent asset: %w", err)
		}
		defer removeStagedAgentArchive(providerInstance.ExecuteSSHCommand, stagedArchive)
	}

	providerName := providerInstance.GetName()

	script := buildDeployScript(cfg, version, arch, downloadURLs, embeddedArchiveB64)
	scriptB64 := base64.StdEncoding.EncodeToString([]byte(script))

	// Upload via printf + base64 decode, then execute, then clean up regardless of outcome.
	// Using a unique tmp file to avoid collisions on concurrent deploys.
	safeVersion := utils.SanitizeShellArg(version)
	if safeVersion == "" {
		safeVersion = "unknown"
	}
	tmpScript := fmt.Sprintf("/tmp/ocv_agent_deploy_%s_%s.sh", safeVersion, newAgentTransferID())
	uploadAndRun := fmt.Sprintf(
		`umask 077; printf '%%s' '%s' | base64 -d > %s && chmod +x %s && OCV_AGENT_ARCHIVE=%s %s; RC=$?; rm -f %s; exit $RC`,
		scriptB64, utils.ShellSingleQuote(tmpScript), utils.ShellSingleQuote(tmpScript), utils.ShellSingleQuote(stagedArchive), utils.ShellSingleQuote(tmpScript), utils.ShellSingleQuote(tmpScript),
	)

	out, execErr := providerInstance.ExecuteSSHCommand(deployCtx, uploadAndRun)
	out = strings.TrimSpace(out)

	if global.APP_LOG != nil {
		if execErr != nil {
			global.APP_LOG.Error("agent deploy failed",
				zap.String("provider", providerName),
				zap.String("version", version),
				zap.String("output", out),
				zap.Error(execErr))
		} else {
			global.APP_LOG.Info("agent deployed successfully",
				zap.String("provider", providerName),
				zap.String("version", version),
				zap.String("arch", arch))
		}
	}

	if execErr != nil {
		return out, fmt.Errorf("deploy failed: %w\noutput:\n%s", execErr, out)
	}
	if !strings.Contains(out, "DEPLOY_SUCCESS") {
		return out, fmt.Errorf("deploy script exited without success marker; output:\n%s", out)
	}
	return out, nil
}

func inlineAgentArchiveB64(archiveName string) string {
	content, err := assets.ReadAgentAsset(archiveName)
	if err != nil || len(content) == 0 || len(content) > maxInlineAgentArchiveBytes {
		return ""
	}
	return base64.StdEncoding.EncodeToString(content)
}

// UninstallAgent removes the agent from a provider host.
func UninstallAgent(ctx context.Context, providerInstance provider.Provider) error {
	commands := []string{
		fmt.Sprintf("systemctl stop %s 2>/dev/null || true", AgentServiceName),
		fmt.Sprintf("systemctl disable %s 2>/dev/null || true", AgentServiceName),
		fmt.Sprintf("systemctl disable --now %s.service 2>/dev/null || true", egressGuardUnit),
		fmt.Sprintf("rm -f /etc/systemd/system/%s.service", AgentServiceName),
		fmt.Sprintf("rm -f /etc/systemd/system/%s.service", egressGuardUnit),
		fmt.Sprintf("rm -f %s", agentEgressGuard),
		"nft delete table inet oneclickvirt_egress_boot 2>/dev/null || true",
		"systemctl daemon-reload",
		fmt.Sprintf("rm -rf %s", AgentInstallDir),
	}

	combined := strings.Join(commands, " && ")
	uninstallCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err := providerInstance.ExecuteSSHCommand(uninstallCtx, combined)
	return err
}

// CheckAgentStatus checks if the agent is running on the provider host.
func CheckAgentStatus(ctx context.Context, providerInstance provider.Provider) (bool, string) {
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(checkCtx, fmt.Sprintf(
		"systemctl is-active %s 2>/dev/null && %s/%s --version 2>&1 | head -1 || echo unknown",
		AgentServiceName, AgentInstallDir, AgentBinaryName))
	if err != nil {
		return false, ""
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "active" {
		version := ""
		if len(lines) > 1 {
			version = stripANSI(strings.TrimSpace(lines[1]))
		}
		return true, version
	}
	return false, ""
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	const ansiEscape = "\x1b"
	result := strings.Builder{}
	i := 0
	for i < len(s) {
		if s[i] == ansiEscape[0] && i+1 < len(s) && s[i+1] == '[' {
			// Skip until we find the terminal letter (@ through ~)
			j := i + 2
			for j < len(s) && (s[j] < '@' || s[j] > '~') {
				j++
			}
			if j < len(s) {
				j++ // skip the terminal character
			}
			i = j
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}

// DetectKernelVersionForNFT checks if the provider host kernel version >= 3.14 (minimum for nftables).
// This only checks kernel version, not whether nft binary is installed (the deploy script handles installation).
func DetectKernelVersionForNFT(ctx context.Context, providerInstance provider.Provider) (bool, error) {
	detectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(detectCtx, `uname -r`)
	if err != nil {
		return false, fmt.Errorf("check kernel version failed: %w", err)
	}

	kernelVersion := strings.TrimSpace(output)
	return checkKernelVersionForNFT(kernelVersion), nil
}

// DetectKernelSupportsNFT checks if the provider host kernel supports nftables.
// Returns true if nft is available and kernel >= 3.14.
func DetectKernelSupportsNFT(ctx context.Context, providerInstance provider.Provider) (bool, error) {
	detectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(detectCtx,
		`uname -r && (which nft >/dev/null 2>&1 && nft list tables >/dev/null 2>&1 && echo "NFT_OK" || echo "NFT_FAIL")`)
	if err != nil {
		return false, fmt.Errorf("check kernel/nft support failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return false, fmt.Errorf("empty kernel check output")
	}

	kernelVersion := strings.TrimSpace(lines[0])
	if !checkKernelVersionForNFT(kernelVersion) {
		return false, nil
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "NFT_OK" {
			return true, nil
		}
	}

	return false, nil
}

// checkKernelVersionForNFT returns true if kernel version >= 3.14.
func checkKernelVersionForNFT(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}

	var major, minor int
	if _, err := fmt.Sscanf(parts[0], "%d", &major); err != nil {
		return false
	}
	minorStr := parts[1]
	for i, c := range minorStr {
		if c < '0' || c > '9' {
			minorStr = minorStr[:i]
			break
		}
	}
	if _, err := fmt.Sscanf(minorStr, "%d", &minor); err != nil {
		return false
	}

	if major > 3 {
		return true
	}
	if major == 3 && minor >= 14 {
		return true
	}
	return false
}

func detectArchitecture(ctx context.Context, providerInstance provider.Provider) (string, error) {
	detectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(detectCtx, "uname -m")
	if err != nil {
		return "", err
	}

	arch := strings.TrimSpace(output)
	switch arch {
	case "x86_64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "amd64", nil
	}
}

func buildDownloadURL(version, archiveName string) string {
	return fmt.Sprintf("https://github.com/oneclickvirt/oneclickvirt/releases/download/%s/%s", version, archiveName)
}

// buildDownloadURLList returns CDN-accelerated URLs (from config) followed by the direct GitHub URL.
// Each CDN endpoint is prepended to the full GitHub URL, matching the project's standard CDN pattern.
func buildDownloadURLList(version, archiveName string) []string {
	githubURL := buildDownloadURL(version, archiveName)
	endpoints := utils.GetCDNEndpoints()
	urls := make([]string, 0, len(endpoints)+1)
	for _, ep := range endpoints {
		urls = append(urls, ep+githubURL)
	}
	urls = append(urls, githubURL)
	return urls
}

// buildSyncAgentConfigCommandFor preserves installer credentials and unknown
// operator settings. Unchanged managed settings must not trigger a restart.
func buildSyncAgentConfigCommandFor(cfg *AgentConfig, agentDir, serviceName string, reverseAgent bool, systemdRuntimeDir string) string {
	envB64 := base64.StdEncoding.EncodeToString([]byte(buildEnvFile(cfg)))
	envName, mode := ".env", "forward"
	if reverseAgent {
		envName, mode = "env", "reverse"
	}
	return fmt.Sprintf(`set -eu
agent_dir=%s
agent_env="$agent_dir/%s"
restart_mode=%s
restart_marker="$agent_dir/.agent-config-restart-pending"
agent_tmp=$(mktemp "$agent_dir/.agent-env.tmp.XXXXXX")
trap 'rm -f "$agent_tmp"' EXIT HUP INT TERM
schedule_reverse_restart() {
    now=$(date +%%s)
    pending_at=0
    attempts=0
    if [ -f "$restart_marker" ]; then
        read -r pending_at attempts < "$restart_marker" || true
        case "$pending_at" in ''|*[!0-9]*) pending_at=0 ;; esac
        case "$attempts" in ''|*[!0-9]*) attempts=0 ;; esac
    fi
    if [ "$pending_at" -gt 0 ] && [ "$((now - pending_at))" -lt 15 ]; then
        printf 'restart-pending\n'
        return 0
    fi
    if [ "$attempts" -ge 3 ]; then
        printf 'Agent config restart failed after 3 attempts; inspect systemctl status %s and journalctl -u %s, then change the configuration to retry\n' >&2
        return 1
    fi
    attempts=$((attempts + 1))
    printf '%%s %%s\n' "$now" "$attempts" > "$restart_marker"
    if command -v systemd-run >/dev/null 2>&1 && [ -d %s ]; then
        if ! systemd-run --quiet --on-active=2s "$(command -v sh)" -c 'systemctl restart "$1" && rm -f "$2"' sh %s "$restart_marker"; then
            printf 'unable to schedule Agent restart attempt %%s\n' "$attempts" >&2
            return 1
        fi
    elif [ -x /usr/local/bin/ocv ]; then
        nohup sh -c 'sleep 2; /usr/local/bin/ocv restart && rm -f "$1"' sh "$restart_marker" </dev/null >/dev/null 2>&1 &
    else
        printf 'Agent config written, but no detached restart mechanism is available; run ocv restart manually\n' >&2
        return 1
    fi
    printf 'restart-scheduled\n'
}
printf '%%s' '%s' | base64 -d > "$agent_tmp"
if [ "$restart_mode" = reverse ]; then
    if [ ! -f "$agent_env" ] || ! grep -Eq '^WS_URL=.+$' "$agent_env" || ! grep -Eq '^AGENT_SECRET=.+$' "$agent_env"; then
        printf 'reverse Agent credentials missing from %%s; reinstall with ocv installer before syncing\n' "$agent_env" >&2
        exit 1
    fi
fi
if [ -f "$agent_env" ]; then
    awk -F= '
        /^[A-Za-z_][A-Za-z0-9_]*=/ {
            key=$1
            if (key == "API_TOKEN" || key == "TRAFFIC_COLLECT_INTERVAL" ||
                key == "RESOURCE_COLLECT_INTERVAL" || key == "TRAFFIC_COLLECT_METHOD" ||
                key == "EXTRA_EXCLUDE_CIDRS_V4" || key == "EXTRA_EXCLUDE_CIDRS_V6" ||
                key == "RUST_LOG" || key == "ONECLICKVIRT_EGRESS_AUTO_INSTALL" ||
                key == "ONECLICKVIRT_EGRESS_APPLY" || key == "ENABLE_REVERSE_PROXY" ||
                key ~ /^PROXY_/) next
            if (!(key in seen)) order[++count]=key
            seen[key]=1
            line[key]=$0
        }
        END { for (i=1; i<=count; i++) print line[order[i]] }
    ' "$agent_env" >> "$agent_tmp"
fi
chmod 600 "$agent_tmp"
if [ -f "$agent_env" ] && cmp -s "$agent_tmp" "$agent_env"; then
    if [ "$restart_mode" = reverse ]; then
        if [ -f "$restart_marker" ]; then
            schedule_reverse_restart
        else
            printf 'unchanged\n'
        fi
    elif ! systemctl is-active --quiet %s; then
        systemctl restart %s
        printf 'restarted-inactive\n'
    else
        printf 'unchanged\n'
    fi
else
    mv -f "$agent_tmp" "$agent_env"
    if [ "$restart_mode" = reverse ]; then
        if [ -f "$restart_marker" ]; then
            read -r pending_at _ < "$restart_marker" || true
            case "$pending_at" in ''|*[!0-9]*) pending_at=0 ;; esac
            now=$(date +%%s)
            if [ "$pending_at" -eq 0 ] || [ "$((now - pending_at))" -ge 15 ]; then
                # A changed desired configuration starts a fresh bounded
                # attempt sequence; a pending restart already reads this file.
                rm -f "$restart_marker"
            fi
        fi
        schedule_reverse_restart
    else
        systemctl restart %s
        printf 'updated-and-restarted\n'
    fi
fi`, utils.ShellSingleQuote(agentDir), envName, mode, serviceName, serviceName, utils.ShellSingleQuote(systemdRuntimeDir), serviceName, envB64, serviceName, serviceName, serviceName)
}

// SyncAgentConfigForProvider rereads the committed desired state under one
// provider-scoped lock. Concurrent provider edits, monitoring edits and Agent
// reconnects therefore cannot overwrite each other with stale snapshots.
func SyncAgentConfigForProvider(ctx context.Context, providerInstance provider.Provider, providerID uint) error {
	lockValue, _ := agentConfigSyncLocks.LoadOrStore(providerID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	var dbProvider providerModel.Provider
	if err := global.APP_DB.WithContext(ctx).First(&dbProvider, providerID).Error; err != nil {
		return fmt.Errorf("读取节点配置失败: %w", err)
	}
	if dbProvider.IsReverseAgent() && dbProvider.AgentSecret == "" {
		return fmt.Errorf("反向 Agent 节点未生成连接密钥，请先在节点页面生成安装命令")
	}
	monitoring, err := GetMonitoringConfig(global.APP_DB.WithContext(ctx), providerID)
	if err != nil {
		return fmt.Errorf("读取 Agent 监控配置失败: %w", err)
	}
	return syncAgentConfig(ctx, providerInstance, ConfigForProvider(&dbProvider, monitoring), &dbProvider)
}

// syncAgentConfig updates the environment file actually read by this mode.
func syncAgentConfig(ctx context.Context, providerInstance provider.Provider, cfg *AgentConfig, dbProvider *providerModel.Provider) error {
	reverseAgent := dbProvider != nil && dbProvider.IsReverseAgent()
	cmd := buildSyncAgentConfigCommandFor(cfg, AgentInstallDir, AgentServiceName, reverseAgent, "/run/systemd/system")
	var previousConn *AgentConn
	if reverseAgent {
		previousConn, _ = GetHub().GetConn(dbProvider.ID)
	}

	syncCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var output string
	var err error
	if reverseAgent {
		if previousConn == nil {
			return fmt.Errorf("反向 Agent 未连接，无法下发配置")
		}
		// Provider.ExecuteSSHCommand does not apply its context to the Agent
		// executor. Use the bound connection's deadline so an overloaded node
		// cannot hold this provider lock for the executor's 300 second default.
		output, err = previousConn.ExecuteWithTimeout(cmd, 20*time.Second)
	} else {
		output, err = providerInstance.ExecuteSSHCommand(syncCtx, cmd)
	}
	if err != nil {
		return fmt.Errorf("sync agent config failed: %w", err)
	}
	if reverseAgent && (strings.Contains(output, "restart-scheduled") || strings.Contains(output, "restart-pending")) {
		if previousConn == nil {
			return fmt.Errorf("Agent 配置已写入并安排重启，但无法确认原连接；请检查节点 Agent 状态")
		}
		poll := time.NewTicker(250 * time.Millisecond)
		defer poll.Stop()
		for {
			if current, ok := GetHub().GetConn(dbProvider.ID); ok && current != previousConn {
				break
			}
			select {
			case <-syncCtx.Done():
				return fmt.Errorf("Agent 配置已写入并安排重启，但未在规定时限内确认重连；检查 systemctl status %s 和 journalctl -u %s: %w", AgentServiceName, AgentServiceName, syncCtx.Err())
			case <-poll.C:
			}
		}
	}

	if global.APP_LOG != nil {
		global.APP_LOG.Info("agent config synchronized",
			zap.String("provider", providerInstance.GetName()),
			zap.String("result", strings.TrimSpace(output)))
	}
	return nil
}
