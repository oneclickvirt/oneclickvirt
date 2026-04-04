package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

const (
	AgentBinaryName  = "oneclickvirt-agent"
	AgentInstallDir  = "/opt/oneclickvirt/agent"
	AgentPort        = 23782
	AgentServiceName = "oneclickvirt-agent"
)

// AgentConfig holds the configuration parameters for the agent deployment.
type AgentConfig struct {
	Token                   string
	TrafficCollectInterval  int // seconds, default 5
	ResourceCollectInterval int // seconds, default 30
	ExtraExcludeCIDRsV4     string
	ExtraExcludeCIDRsV6     string
	TrafficCollectMethod    string // "nft" (default) or "ipt"
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
	sb.WriteString(fmt.Sprintf("API_TOKEN=%s\n", cfg.Token))
	sb.WriteString(fmt.Sprintf("TRAFFIC_COLLECT_INTERVAL=%d\n", cfg.trafficInterval()))
	sb.WriteString(fmt.Sprintf("RESOURCE_COLLECT_INTERVAL=%d\n", cfg.resourceInterval()))
	sb.WriteString("RUST_LOG=info\n")
	method := cfg.TrafficCollectMethod
	if method == "" {
		method = "nft"
	}
	sb.WriteString(fmt.Sprintf("TRAFFIC_COLLECT_METHOD=%s\n", method))
	if cfg.ExtraExcludeCIDRsV4 != "" {
		sb.WriteString(fmt.Sprintf("EXTRA_EXCLUDE_CIDRS_V4=%s\n", cfg.ExtraExcludeCIDRsV4))
	}
	if cfg.ExtraExcludeCIDRsV6 != "" {
		sb.WriteString(fmt.Sprintf("EXTRA_EXCLUDE_CIDRS_V6=%s\n", cfg.ExtraExcludeCIDRsV6))
	}
	return sb.String()
}

// buildDeployScript generates a self-contained bash deploy script for the agent.
// The script handles download verification, extraction, .env writing and systemd setup.
func buildDeployScript(cfg *AgentConfig, version, arch string, downloadURLs []string) string {
	binaryName := fmt.Sprintf("%s-linux-%s", AgentBinaryName, arch)
	archiveName := fmt.Sprintf("%s.tar.gz", binaryName)
	envContent := buildEnvFile(cfg)

	serviceUnit := fmt.Sprintf(`[Unit]
Description=OneclickVirt Monitoring Agent
After=network.target

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s/%s
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`, AgentInstallDir, AgentInstallDir, AgentBinaryName)

	// We use printf to write files to avoid heredoc nesting issues within the script itself.
	// envContent and serviceUnit are base64-encoded inside the script so any special chars are safe.
	envB64 := base64.StdEncoding.EncodeToString([]byte(envContent))
	svcB64 := base64.StdEncoding.EncodeToString([]byte(serviceUnit))

	// Space-separated URL list for the shell script to iterate over (CDN mirrors first, GitHub last).
	urlList := strings.Join(downloadURLs, " ")

	script := fmt.Sprintf(`#!/bin/sh
set -e
INSTALL_DIR="%s"
BINARY_NAME="%s"
SRC_BINARY_NAME="%s"
ARCHIVE_NAME="%s"
DOWNLOAD_URLS="%s"
SERVICE_NAME="%s"
VERSION="%s"

echo "[1/6] check and install traffic monitoring dependency..."
# Read collect method from .env if already present, otherwise default to nft
COLLECT_METHOD=""
if [ -f "$INSTALL_DIR/.env" ]; then
    COLLECT_METHOD=$(grep -oP 'TRAFFIC_COLLECT_METHOD=\K.*' "$INSTALL_DIR/.env" 2>/dev/null || true)
fi
# The new .env will be written below; parse the base64-encoded one for the intended method
INTENDED_METHOD=$(printf '%%s' "%s" | base64 -d 2>/dev/null | grep -oP 'TRAFFIC_COLLECT_METHOD=\K.*' || echo "nft")
COLLECT_METHOD="${INTENDED_METHOD:-nft}"

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
for url in $DOWNLOAD_URLS; do
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

echo "[5/6] write .env and systemd service..."
printf '%%s' "%s" | base64 -d > "$INSTALL_DIR/.env"
printf '%%s' "%s" | base64 -d > /etc/systemd/system/"$SERVICE_NAME".service
echo "[OK] 5/6 configuration written"

echo "[6/6] enable and start service..."
systemctl daemon-reload
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
		AgentServiceName,
		version,
		envB64, // for dependency detection
		envB64,
		svcB64,
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

	providerName := providerInstance.GetName()

	script := buildDeployScript(cfg, version, arch, downloadURLs)
	scriptB64 := base64.StdEncoding.EncodeToString([]byte(script))

	// Upload via printf + base64 decode, then execute, then clean up regardless of outcome.
	// Using a unique tmp file to avoid collisions on concurrent deploys.
	tmpScript := fmt.Sprintf("/tmp/ocv_agent_deploy_%s.sh", version)
	uploadAndRun := fmt.Sprintf(
		`printf '%%s' '%s' | base64 -d > %s && chmod +x %s && %s; RC=$?; rm -f %s; exit $RC`,
		scriptB64, tmpScript, tmpScript, tmpScript, tmpScript,
	)

	deployCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()

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

// UninstallAgent removes the agent from a provider host.
func UninstallAgent(ctx context.Context, providerInstance provider.Provider) error {
	commands := []string{
		fmt.Sprintf("systemctl stop %s 2>/dev/null || true", AgentServiceName),
		fmt.Sprintf("systemctl disable %s 2>/dev/null || true", AgentServiceName),
		fmt.Sprintf("rm -f /etc/systemd/system/%s.service", AgentServiceName),
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

// SyncAgentConfig updates the agent .env file and restarts the service to apply new config.
func SyncAgentConfig(ctx context.Context, providerInstance provider.Provider, cfg *AgentConfig) error {
	envContent := buildEnvFile(cfg)
	envB64 := base64.StdEncoding.EncodeToString([]byte(envContent))

	// Write .env via base64 to avoid heredoc quoting issues, then restart.
	cmd := fmt.Sprintf(
		`printf '%%s' '%s' | base64 -d > %s/.env && systemctl restart %s`,
		envB64, AgentInstallDir, AgentServiceName,
	)

	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := providerInstance.ExecuteSSHCommand(syncCtx, cmd)
	if err != nil {
		return fmt.Errorf("sync agent config failed: %w", err)
	}

	if global.APP_LOG != nil {
		global.APP_LOG.Info("agent config synced and restarted",
			zap.String("provider", providerInstance.GetName()))
	}
	return nil
}
