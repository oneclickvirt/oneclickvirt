package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

const (
	AgentBinaryName  = "oneclickvirt-agent"
	AgentInstallDir  = "/opt/oneclickvirt/agent"
	AgentPort        = 23782
	AgentServiceName = "oneclickvirt-agent"
)

// DeployAgent deploys the agent binary to a provider host via SSH.
// It downloads the binary from GitHub releases (with CDN fallback), installs it,
// and creates a systemd service.
func DeployAgent(ctx context.Context, providerInstance provider.Provider, token string, version string) error {
	arch, err := detectArchitecture(ctx, providerInstance)
	if err != nil {
		arch = "amd64"
	}

	binaryName := fmt.Sprintf("%s-linux-%s", AgentBinaryName, arch)
	archiveName := fmt.Sprintf("%s.tar.gz", binaryName)
	downloadURL := buildDownloadURL(version, archiveName)
	cdnURL := buildCDNDownloadURL(version, archiveName)

	commands := []string{
		fmt.Sprintf("mkdir -p %s", AgentInstallDir),
		// Try CDN first, fallback to GitHub
		fmt.Sprintf(
			`cd %s && (curl -sL --connect-timeout 10 -o %s '%s' || curl -sL --connect-timeout 30 -o %s '%s')`,
			AgentInstallDir, archiveName, cdnURL, archiveName, downloadURL,
		),
		fmt.Sprintf("cd %s && tar -xzf %s && rm -f %s", AgentInstallDir, archiveName, archiveName),
		fmt.Sprintf("mv %s/%s %s/%s 2>/dev/null; chmod +x %s/%s",
			AgentInstallDir, binaryName, AgentInstallDir, AgentBinaryName,
			AgentInstallDir, AgentBinaryName),
		// Create env file
		fmt.Sprintf(`cat > %s/.env << 'ENVEOF'
API_TOKEN=%s
RUST_LOG=info
ENVEOF`, AgentInstallDir, token),
		// Create systemd service
		fmt.Sprintf(`cat > /etc/systemd/system/%s.service << 'SVCEOF'
[Unit]
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
SVCEOF`, AgentServiceName, AgentInstallDir, AgentInstallDir, AgentBinaryName),
		"systemctl daemon-reload",
		fmt.Sprintf("systemctl enable %s", AgentServiceName),
		fmt.Sprintf("systemctl restart %s", AgentServiceName),
	}

	combined := strings.Join(commands, " && ")
	deployCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	_, err = providerInstance.ExecuteSSHCommand(deployCtx, combined)
	if err != nil {
		return fmt.Errorf("deploy agent failed: %w", err)
	}

	if global.APP_LOG != nil {
		global.APP_LOG.Info("agent deployed successfully",
			zap.String("provider", providerInstance.GetName()),
			zap.String("arch", arch))
	}
	return nil
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
		"systemctl is-active %s 2>/dev/null && %s/%s --version 2>/dev/null || echo unknown",
		AgentServiceName, AgentInstallDir, AgentBinaryName))
	if err != nil {
		return false, ""
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "active" {
		version := ""
		if len(lines) > 1 {
			version = strings.TrimSpace(lines[1])
		}
		return true, version
	}
	return false, ""
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

func buildCDNDownloadURL(version, archiveName string) string {
	cfg := global.GetAppConfig()
	if len(cfg.CDN.Endpoints) > 0 {
		return fmt.Sprintf("%s/oneclickvirt/oneclickvirt/releases/download/%s/%s",
			cfg.CDN.Endpoints[0], version, archiveName)
	}
	if cfg.CDN.BaseEndpoint != "" {
		return fmt.Sprintf("%s/oneclickvirt/oneclickvirt/releases/download/%s/%s",
			cfg.CDN.BaseEndpoint, version, archiveName)
	}
	return buildDownloadURL(version, archiveName)
}
