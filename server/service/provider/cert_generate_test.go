package provider

import (
	"strings"
	"testing"
)

func TestGenerateProxmoxScriptDoesNotPrintTokenSecret(t *testing.T) {
	script := (&CertService{}).generateProxmoxScript("provider-uuid", "oneclickvirt", "test-token")

	for _, forbidden := range []string{
		`cat /tmp/oneclickvirt-proxmox-config`,
		`${token_secret:0:8}`,
		`for user in $(pveum user list`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("generated Proxmox script exposes token secret via %q", forbidden)
		}
	}

	for _, required := range []string{
		`token_secret=$(echo "$output" | jq -r '.value')`,
		`chmod 600 /tmp/oneclickvirt-proxmox-config`,
		`TOKEN_SECRET=$token_secret`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("generated Proxmox script missing %q", required)
		}
	}
}
