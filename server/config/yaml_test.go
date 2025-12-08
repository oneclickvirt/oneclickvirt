package config

import (
	"testing"
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
