package config

import (
	"testing"
)

// TestInviteCodeSync 测试 inviteCode 配置同步
func TestInviteCodeSync(t *testing.T) {
	// 测试 setNestedValue 是否正确构建嵌套结构
	t.Run("setNestedValue for invite-code", func(t *testing.T) {
		config := make(map[string]interface{})

		// 设置 invite-code.enabled
		setNestedValue(config, "invite-code.enabled", true)

		// 验证结构
		inviteCodeConfig, ok := config["invite-code"].(map[string]interface{})
		if !ok {
			t.Fatal("invite-code 不是 map 类型")
		}

		enabled, ok := inviteCodeConfig["enabled"].(bool)
		if !ok {
			t.Fatal("enabled 不是 bool 类型")
		}

		if !enabled {
			t.Error("enabled 应该为 true")
		}
	})

	// 测试 convertMapKeysToKebab 转换
	t.Run("convert inviteCode to invite-code", func(t *testing.T) {
		input := map[string]interface{}{
			"inviteCode": map[string]interface{}{
				"enabled": true,
			},
		}

		result := convertMapKeysToKebab(input)

		inviteCodeConfig, ok := result["invite-code"].(map[string]interface{})
		if !ok {
			t.Fatal("invite-code 转换失败")
		}

		enabled, ok := inviteCodeConfig["enabled"].(bool)
		if !ok {
			t.Fatal("enabled 字段丢失")
		}

		if !enabled {
			t.Error("enabled 值错误")
		}
	})

	// 测试完整流程：前端数据 -> kebab -> flat -> unflatten
	t.Run("full flow: frontend to nested", func(t *testing.T) {
		// 前端数据
		frontendData := map[string]interface{}{
			"inviteCode": map[string]interface{}{
				"enabled": true,
			},
		}

		// 1. 转换为 kebab-case
		kebabData := convertMapKeysToKebab(frontendData)

		// 2. 扁平化（模拟 flattenConfig）
		flatData := make(map[string]interface{})
		for key, value := range kebabData {
			if mapValue, ok := value.(map[string]interface{}); ok {
				for subKey, subValue := range mapValue {
					flatData[key+"."+subKey] = subValue
				}
			} else {
				flatData[key] = value
			}
		}

		// 验证扁平化结果
		if flatData["invite-code.enabled"] != true {
			t.Error("扁平化后 invite-code.enabled 应该为 true")
		}

		// 3. 重新构建嵌套结构（模拟重启后从数据库加载）
		nestedData := make(map[string]interface{})
		for key, value := range flatData {
			setNestedValue(nestedData, key, value)
		}

		// 验证重建的嵌套结构
		inviteCodeConfig, ok := nestedData["invite-code"].(map[string]interface{})
		if !ok {
			t.Fatal("重建后 invite-code 不是 map 类型")
		}

		enabled, ok := inviteCodeConfig["enabled"].(bool)
		if !ok {
			t.Fatal("重建后 enabled 不是 bool 类型")
		}

		if !enabled {
			t.Error("重建后 enabled 应该为 true")
		}
	})
}
