package config

import (
	"testing"
)

// TestOAuth2ConfigFlow 测试 OAuth2 配置的完整流程
func TestOAuth2ConfigFlow(t *testing.T) {
	tests := []struct {
		name          string
		inputKey      string
		inputValue    interface{}
		expectedKey   string
		expectedValue interface{}
	}{
		{
			name:          "OAuth2 enabled from frontend",
			inputKey:      "enableOAuth2",
			inputValue:    true,
			expectedKey:   "enable-oauth2",
			expectedValue: true,
		},
		{
			name:          "QQ enabled from frontend",
			inputKey:      "enableQQ",
			inputValue:    true,
			expectedKey:   "enable-qq",
			expectedValue: true,
		},
		{
			name:          "SMTP host from frontend",
			inputKey:      "smtpHost",
			inputValue:    "smtp.example.com",
			expectedKey:   "smtp-host",
			expectedValue: "smtp.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 模拟从前端接收的数据
			frontendData := map[string]interface{}{
				tt.inputKey: tt.inputValue,
			}

			// 转换为 kebab-case（这是保存到 YAML 和数据库时使用的格式）
			kebabData := convertMapKeysToKebab(frontendData)

			// 验证转换结果
			if value, exists := kebabData[tt.expectedKey]; !exists {
				t.Errorf("Expected key %q not found in converted data", tt.expectedKey)
			} else if value != tt.expectedValue {
				t.Errorf("Expected value %v, got %v", tt.expectedValue, value)
			}
		})
	}
}

// TestNestedConfigConversion 测试嵌套配置的转换
func TestNestedConfigConversion(t *testing.T) {
	// 模拟前端发送的嵌套配置
	frontendData := map[string]interface{}{
		"auth": map[string]interface{}{
			"enableOAuth2": true,
			"enableEmail":  true,
			"enableQQ":     false,
		},
		"email": map[string]interface{}{
			"smtpHost": "smtp.example.com",
			"smtpPort": 587,
		},
	}

	// 转换为 kebab-case
	kebabData := convertMapKeysToKebab(frontendData)

	// 验证顶层键转换
	authData, ok := kebabData["auth"].(map[string]interface{})
	if !ok {
		t.Fatal("auth key not found or not a map")
	}

	// 验证嵌套键转换
	expectedAuthKeys := map[string]interface{}{
		"enable-oauth2": true,
		"enable-email":  true,
		"enable-qq":     false,
	}

	for key, expectedValue := range expectedAuthKeys {
		if value, exists := authData[key]; !exists {
			t.Errorf("Expected key %q not found in auth data", key)
		} else if value != expectedValue {
			t.Errorf("For key %q: expected value %v, got %v", key, expectedValue, value)
		}
	}

	// 验证 email 配置
	emailData, ok := kebabData["email"].(map[string]interface{})
	if !ok {
		t.Fatal("email key not found or not a map")
	}

	if smtpHost, exists := emailData["smtp-host"]; !exists || smtpHost != "smtp.example.com" {
		t.Errorf("smtp-host not correctly converted: got %v", smtpHost)
	}

	if smtpPort, exists := emailData["smtp-port"]; !exists || smtpPort != 587 {
		t.Errorf("smtp-port not correctly converted: got %v", smtpPort)
	}
}
