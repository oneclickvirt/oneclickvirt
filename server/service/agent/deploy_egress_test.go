package agent

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestBuildEnvFileEnablesManagedEgress(t *testing.T) {
	env := buildEnvFile(&AgentConfig{Token: "test-token"})
	for _, expected := range []string{
		"ONECLICKVIRT_EGRESS_AUTO_INSTALL=true\n",
		"ONECLICKVIRT_EGRESS_APPLY=true\n",
	} {
		if count := strings.Count(env, expected); count != 1 {
			t.Fatalf("buildEnvFile contains %q %d times, want once\nenv:\n%s", expected, count, env)
		}
	}
}

func TestBuildEnvFileQuotesUntrustedValues(t *testing.T) {
	env := buildEnvFile(&AgentConfig{
		Token:                "token\"\nINJECTED=yes",
		TrafficCollectMethod: "nft\\safe",
		ExtraExcludeCIDRsV4:  "10.0.0.0/8\r\nINJECTED_V4=yes",
		ProxyTLSCertPath:     "/tmp/cert\"name",
		ProxyTLSKeyPath:      "/tmp/key",
		EnableReverseProxy:   true,
		ProxyEnableHTTPS:     true,
	})
	if strings.Contains(env, "\nINJECTED=") || strings.Contains(env, "\nINJECTED_V4=") {
		t.Fatalf("untrusted values created additional environment entries:\n%s", env)
	}
	for _, expected := range []string{
		`API_TOKEN="token\"\nINJECTED=yes"`,
		`TRAFFIC_COLLECT_METHOD="nft\\safe"`,
		`EXTRA_EXCLUDE_CIDRS_V4="10.0.0.0/8\r\nINJECTED_V4=yes"`,
		`PROXY_TLS_CERT="/tmp/cert\"name"`,
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("quoted environment value %q missing from:\n%s", expected, env)
		}
	}
}

func TestBuildEnvFileCloudflareTrustIsExplicit(t *testing.T) {
	defaultEnv := buildEnvFile(&AgentConfig{EnableReverseProxy: true})
	if strings.Contains(defaultEnv, "PROXY_TRUST_CLOUDFLARE_HEADERS=") {
		t.Fatal("Cloudflare header trust must be disabled by default")
	}
	trustedEnv := buildEnvFile(&AgentConfig{EnableReverseProxy: true, ProxyTrustCloudflareHeaders: true})
	if !strings.Contains(trustedEnv, "PROXY_TRUST_CLOUDFLARE_HEADERS=true\n") {
		t.Fatal("explicit Cloudflare trust was not deployed")
	}
}

func TestSyncAgentConfigAvoidsUnchangedRestartAndRetainsReverseCredentials(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	restartLog := filepath.Join(dir, "restarts")
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = is-active ]; then exit 0; fi\n" +
		"if [ \"$1\" = restart ]; then printf '%s\\n' \"$2\" >> \"$RESTART_LOG\"; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("WS_URL=\"wss://controller.example.test/api/v1/ws/agent\"\nAGENT_SECRET=\"secret\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(cfg *AgentConfig) string {
		t.Helper()
		command := exec.Command("sh", "-c", buildSyncAgentConfigCommandFor(cfg, dir, "oneclickvirt-agent", false, "/run/systemd/system"))
		command.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "RESTART_LOG="+restartLog)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("sync command failed: %v\n%s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	cfg := &AgentConfig{Token: "test-token", EnableReverseProxy: true}
	if got := run(cfg); got != "updated-and-restarted" {
		t.Fatalf("initial sync = %q", got)
	}
	if got := run(cfg); got != "unchanged" {
		t.Fatalf("unchanged sync = %q", got)
	}
	cfg.ProxyTrustCloudflareHeaders = true
	if got := run(cfg); got != "updated-and-restarted" {
		t.Fatalf("changed sync = %q", got)
	}
	restarts, err := os.ReadFile(restartLog)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(restarts), "oneclickvirt-agent\n"); count != 2 {
		t.Fatalf("restart count = %d, want 2", count)
	}
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"WS_URL=", "AGENT_SECRET=", "PROXY_TRUST_CLOUDFLARE_HEADERS=true"} {
		if !strings.Contains(string(content), value) {
			t.Fatalf("synced Agent config lost %q", value)
		}
	}
}

func TestSyncReverseAgentConfigUsesInstalledEnvAndSchedulesOneRestart(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	systemdDir := filepath.Join(dir, "systemd")
	for _, path := range []string{binDir, systemdDir} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	restartLog := filepath.Join(dir, "restarts")
	for name, content := range map[string]string{
		"systemctl":   "#!/bin/sh\nif [ \"$1\" = is-active ]; then exit 0; fi\nexit 1\n",
		"systemd-run": "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$RESTART_LOG\"\n",
	} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	envPath := filepath.Join(dir, "env")
	oldEnv := "WS_URL=wss://controller.example.test/api/v1/ws/agent\nAGENT_SECRET=old-secret\nAGENT_SOURCE=controller\nCONTROLLER_BASE_URL=https://controller.example.test/releases\nCUSTOM_SETTING=keep-me\n"
	if err := os.WriteFile(envPath, []byte(oldEnv), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(cfg *AgentConfig) string {
		t.Helper()
		command := exec.Command("sh", "-c", buildSyncAgentConfigCommandFor(cfg, dir, "oneclickvirt-agent", true, systemdDir))
		command.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "RESTART_LOG="+restartLog)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("reverse sync command failed: %v\n%s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	cfg := &AgentConfig{Token: "new-secret", EnableReverseProxy: true, ProxyEnableHTTP: true}
	if got := run(cfg); got != "restart-scheduled" {
		t.Fatalf("first reverse sync = %q", got)
	}
	if got := run(cfg); got != "restart-pending" {
		t.Fatalf("idempotent reverse sync = %q", got)
	}
	cfg.ProxyTrustCloudflareHeaders = true
	if got := run(cfg); got != "restart-pending" {
		t.Fatalf("changed reverse sync = %q", got)
	}
	if err := os.Remove(filepath.Join(dir, ".agent-config-restart-pending")); err != nil {
		t.Fatal(err)
	}
	if got := run(cfg); got != "unchanged" {
		t.Fatalf("post-restart reverse sync = %q", got)
	}
	cfg.ProxyTrustCloudflareHeaders = false
	if got := run(cfg); got != "restart-scheduled" {
		t.Fatalf("second changed reverse sync = %q", got)
	}
	restarts, err := os.ReadFile(restartLog)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(restarts), "--on-active=2s"); count != 2 {
		t.Fatalf("scheduled restart count = %d, want 2: %s", count, restarts)
	}
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"WS_URL=", "AGENT_SECRET=", "AGENT_SOURCE=controller", "CONTROLLER_BASE_URL=", "CUSTOM_SETTING=keep-me", "API_TOKEN=\"new-secret\"", "PROXY_HTTP_ADDR=0.0.0.0:80"} {
		if !strings.Contains(string(content), value) {
			t.Fatalf("reverse Agent config lost %q", value)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatalf("sync unexpectedly wrote forward Agent .env: %v", err)
	}

	// A failed detached restart must have a finite retry budget. A later
	// configuration edit is an explicit new attempt sequence.
	marker := filepath.Join(dir, ".agent-config-restart-pending")
	if err := os.WriteFile(marker, []byte("1 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := run(cfg); got != "restart-scheduled" {
		t.Fatalf("third automatic restart attempt = %q", got)
	}
	if err := os.WriteFile(marker, []byte("1 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-c", buildSyncAgentConfigCommandFor(cfg, dir, "oneclickvirt-agent", true, systemdDir))
	command.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "RESTART_LOG="+restartLog)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "failed after 3 attempts") {
		t.Fatalf("exhausted restart did not stop with a diagnostic: err=%v output=%q", err, output)
	}
	cfg.ProxyTrustCloudflareHeaders = true
	if got := run(cfg); got != "restart-scheduled" {
		t.Fatalf("changed config did not reset restart budget: %q", got)
	}
	restarts, err = os.ReadFile(restartLog)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(restarts), "--on-active=2s"); count != 4 {
		t.Fatalf("bounded scheduled restart count = %d, want 4: %s", count, restarts)
	}
}

func TestBuildDeployScriptSanitizesVersionInTempPath(t *testing.T) {
	script := buildDeployScript(&AgentConfig{Token: "token"}, "../../bad; touch /tmp/pwn", "amd64", nil, "")
	if strings.Contains(script, "/tmp/ocv_agent_deploy_../../bad") || strings.Contains(script, "/tmp/pwn.sh") {
		t.Fatalf("unsafe version leaked into generated temp path")
	}
}

func TestBuildDeployScriptUsesConfiguredTrafficMethodAndStagedAsset(t *testing.T) {
	script := buildDeployScript(&AgentConfig{Token: "token", TrafficCollectMethod: "ipt"}, "v-test", "amd64", nil, "")
	for _, want := range []string{"COLLECT_METHOD='ipt'", `cp "$OCV_AGENT_ARCHIVE" "$ARCHIVE_NAME"`, "source: staged controller asset"} {
		if !strings.Contains(script, want) {
			t.Fatalf("deployment script lacks %q", want)
		}
	}
}

func TestBuildDeployScriptInstallsFailClosedBootGuard(t *testing.T) {
	script := buildDeployScript(
		&AgentConfig{Token: "controller-token"},
		"v-test",
		"amd64",
		[]string{"https://example.invalid/agent.tar.gz"},
		"",
	)
	var decodedPayloads strings.Builder
	for _, match := range regexp.MustCompile(`printf '%s' "([A-Za-z0-9+/=]+)" \| base64 -d`).FindAllStringSubmatch(script, -1) {
		decoded, err := base64.StdEncoding.DecodeString(match[1])
		if err != nil {
			t.Fatalf("decode generated service payload: %v", err)
		}
		decodedPayloads.Write(decoded)
		decodedPayloads.WriteByte('\n')
	}
	generated := script + decodedPayloads.String()
	for _, expected := range []string{
		"oneclickvirt-egress-boot-guard",
		"oneclickvirt_egress_boot",
		"oneclickvirt-egress-guard.service",
		"RequiredBy=network-pre.target",
		"ExecStartPre=/usr/local/bin/oneclickvirt-egress-boot-guard",
		"chmod 600 \"$INSTALL_DIR/.env\"",
	} {
		if !strings.Contains(generated, expected) {
			t.Fatalf("generated deploy script is missing %q", expected)
		}
	}
	for _, forbidden := range []string{
		"ExecStart=/opt/oneclickvirt/agent/oneclickvirt-agent --secret",
		"ExecStart=/opt/oneclickvirt/agent/oneclickvirt-agent --ws-url",
	} {
		if strings.Contains(generated, forbidden) {
			t.Fatalf("generated service still exposes credentials in argv: %q", forbidden)
		}
	}
}
