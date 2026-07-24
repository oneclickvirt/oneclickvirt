package agent

import (
	"encoding/base64"
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
