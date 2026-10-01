package task

import (
	"strings"

	providerModel "oneclickvirt/model/provider"
)

func providerInstanceIdentifier(instance providerModel.Instance) string {
	if id := strings.TrimSpace(instance.ProviderVMID); id != "" {
		return id
	}
	return instance.Name
}

// resetPortMappingOwnerName returns the identity used in node-side mapping
// comments. Proxmox uses a numeric ProviderVMID for API operations, while the
// existing firewall owner comments use the stable controller instance name.
// Keep those identities separate so a reset remains idempotent across VMID
// reuse and can clean rules created by older versions.
func resetPortMappingOwnerName(resetCtx *ResetTaskContext) string {
	if resetCtx == nil {
		return ""
	}
	providerType := strings.ToLower(strings.TrimSpace(resetCtx.Provider.Type))
	if (providerType == "proxmox" || providerType == "proxmoxve" || providerType == "pve") && strings.TrimSpace(resetCtx.OldInstanceName) != "" {
		return strings.TrimSpace(resetCtx.OldInstanceName)
	}
	if name := strings.TrimSpace(resetCtx.NewProviderInstanceID); name != "" {
		return name
	}
	return strings.TrimSpace(resetCtx.OldInstanceName)
}

// 辅助函数：创建指针类型
func boolPtr(b bool) *bool {
	return &b
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intPtr(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}

// 辅助函数：字符串包含检查（不区分大小写）
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(substr) == 0 ||
			findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	sLower := toLower(s)
	substrLower := toLower(substr)
	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		if sLower[i:i+len(substrLower)] == substrLower {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}
