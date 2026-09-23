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
