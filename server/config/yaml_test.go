package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCamelToKebab(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// OAuth2 特殊处理 - 最关键的测试
		{"enableOAuth2", "enable-oauth2"},
		{"OAuth2", "oauth2"},

		// QQ 特殊处理
		{"enableQQ", "enable-qq"},
		{"qqAppID", "qq-app-id"},

		// SMTP 处理
		{"emailSMTPHost", "email-smtp-host"},

		// 基本转换
		{"enableEmail", "enable-email"},
		{"defaultLevel", "default-level"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := camelToKebab(tt.input)
			if result != tt.expected {
				t.Errorf("camelToKebab(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestUpdateYAMLNode_MergeMapKeepsExistingKeys(t *testing.T) {
	originalYAML := `
quota:
  default-level: 1
  level-limits:
    "1":
      max-instances: 1
      max-resources:
        cpu: 1
        memory: 512
        disk: 1024
        bandwidth: 100
      max-traffic: 102400
  instance-type-permissions:
    min-level-for-container: 1
    min-level-for-vm: 2
`

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(originalYAML), &node); err != nil {
		t.Fatalf("failed to unmarshal original YAML: %v", err)
	}

	// 模拟局部更新 quota（类似 SaveInstanceTypePermissions 的更新方式）
	updateValue := map[string]interface{}{
		"instance-type-permissions": map[string]interface{}{
			"min-level-for-vm": 3,
		},
	}
	if err := updateYAMLNode(&node, "quota", updateValue); err != nil {
		t.Fatalf("updateYAMLNode failed: %v", err)
	}

	out, err := yaml.Marshal(&node)
	if err != nil {
		t.Fatalf("failed to marshal updated YAML: %v", err)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("failed to unmarshal updated YAML to map: %v", err)
	}

	quota, ok := parsed["quota"].(map[string]interface{})
	if !ok {
		t.Fatalf("quota not found or invalid type in updated YAML")
	}

	if _, exists := quota["level-limits"]; !exists {
		t.Fatalf("level-limits should be preserved after partial quota update")
	}

	permissions, ok := quota["instance-type-permissions"].(map[string]interface{})
	if !ok {
		t.Fatalf("instance-type-permissions not found or invalid type")
	}

	if got, exists := permissions["min-level-for-vm"]; !exists || got != 3 {
		t.Fatalf("expected min-level-for-vm=3, got=%v exists=%v", got, exists)
	}

	// 确认未更新字段仍保留
	if got, exists := permissions["min-level-for-container"]; !exists || got != 1 {
		t.Fatalf("expected min-level-for-container to be preserved as 1, got=%v exists=%v", got, exists)
	}
}
