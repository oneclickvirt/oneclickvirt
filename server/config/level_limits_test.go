package config

import (
	"testing"

	"go.uber.org/zap"
)

// TestValidateLevelLimitsWithDefaults 测试等级限制验证和默认值自动填充
func TestValidateLevelLimitsWithDefaults(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cm := &ConfigManager{
		logger: logger,
	}

	tests := []struct {
		name        string
		input       map[string]interface{}
		expectError bool
		checkFields map[string]map[string]interface{} // level -> field -> expected value
	}{
		{
			name: "完整配置 - 应该通过验证",
			input: map[string]interface{}{
				"1": map[string]interface{}{
					"max-instances": 1,
					"max-traffic":   102400,
					"max-resources": map[string]interface{}{
						"cpu":       1,
						"memory":    350,
						"disk":      1024,
						"bandwidth": 100,
					},
				},
			},
			expectError: false,
		},
		{
			name: "缺少 max-instances - 应该自动填充",
			input: map[string]interface{}{
				"2": map[string]interface{}{
					"max-traffic": 204800,
					"max-resources": map[string]interface{}{
						"cpu":       2,
						"memory":    1024,
						"disk":      20480,
						"bandwidth": 200,
					},
				},
			},
			expectError: false,
			checkFields: map[string]map[string]interface{}{
				"2": {
					"max-instances": 3, // 应该被自动填充为默认值
				},
			},
		},
		{
			name: "缺少 max-traffic - 应该自动填充",
			input: map[string]interface{}{
				"3": map[string]interface{}{
					"max-instances": 5,
					"max-resources": map[string]interface{}{
						"cpu":       4,
						"memory":    2048,
						"disk":      40960,
						"bandwidth": 500,
					},
				},
			},
			expectError: false,
			checkFields: map[string]map[string]interface{}{
				"3": {
					"max-traffic": 307200, // 应该被自动填充
				},
			},
		},
		{
			name: "缺少整个 max-resources - 应该自动填充",
			input: map[string]interface{}{
				"4": map[string]interface{}{
					"max-instances": 10,
					"max-traffic":   409600,
				},
			},
			expectError: false,
			checkFields: map[string]map[string]interface{}{
				"4": {
					"max-resources": map[string]interface{}{
						"cpu":       8,
						"memory":    4096,
						"disk":      81920,
						"bandwidth": 1000,
					},
				},
			},
		},
		{
			name: "max-resources 缺少部分字段 - 应该自动填充缺失字段",
			input: map[string]interface{}{
				"5": map[string]interface{}{
					"max-instances": 20,
					"max-traffic":   512000,
					"max-resources": map[string]interface{}{
						"cpu":    16,
						"memory": 8192,
						// 缺少 disk 和 bandwidth
					},
				},
			},
			expectError: false,
			checkFields: map[string]map[string]interface{}{
				"5": {
					"max-resources.disk":      163840, // 应该被自动填充
					"max-resources.bandwidth": 2000,   // 应该被自动填充
				},
			},
		},
		{
			name: "完全空的配置 - 应该全部自动填充",
			input: map[string]interface{}{
				"1": map[string]interface{}{},
			},
			expectError: false,
			checkFields: map[string]map[string]interface{}{
				"1": {
					"max-instances": 1,
					"max-traffic":   102400,
				},
			},
		},
		{
			name: "未知等级 - 应该报错（没有默认值）",
			input: map[string]interface{}{
				"99": map[string]interface{}{},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 执行验证
			err := cm.validateLevelLimits(tt.input)

			// 检查错误
			if tt.expectError {
				if err == nil {
					t.Errorf("期望验证失败，但成功了")
				}
				return
			}

			if err != nil {
				t.Errorf("验证失败: %v", err)
				return
			}

			// 检查字段值
			if tt.checkFields != nil {
				for level, fields := range tt.checkFields {
					levelMap, ok := tt.input[level].(map[string]interface{})
					if !ok {
						t.Errorf("等级 %s 的配置不是 map 类型", level)
						continue
					}

					for field, expectedValue := range fields {
						var actualValue interface{}

						// 处理嵌套字段（如 max-resources.cpu）
						if len(field) > 13 && field[:13] == "max-resources" {
							resourcesMap, ok := levelMap["max-resources"].(map[string]interface{})
							if !ok {
								t.Errorf("等级 %s 的 max-resources 不是 map 类型", level)
								continue
							}
							resourceField := field[14:] // 去掉 "max-resources."
							actualValue = resourcesMap[resourceField]
						} else {
							actualValue = levelMap[field]
						}

						// 比较值（考虑类型转换）
						if !compareValues(actualValue, expectedValue) {
							t.Errorf("等级 %s 的 %s: 期望 %v, 实际 %v", level, field, expectedValue, actualValue)
						}
					}
				}
			}
		})
	}
}

// compareValues 比较两个值是否相等（处理数值类型转换）
func compareValues(a, b interface{}) bool {
	// 处理 map 类型
	if mapA, okA := a.(map[string]interface{}); okA {
		if mapB, okB := b.(map[string]interface{}); okB {
			if len(mapA) != len(mapB) {
				return false
			}
			for k, v := range mapA {
				if !compareValues(v, mapB[k]) {
					return false
				}
			}
			return true
		}
		return false
	}

	// 处理数值类型
	numA := toFloat64(a)
	numB := toFloat64(b)
	if numA != 0 || numB != 0 {
		return numA == numB
	}

	// 其他类型直接比较
	return a == b
}

// toFloat64 将各种数值类型转换为 float64
func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case float64:
		return val
	case float32:
		return float64(val)
	default:
		return 0
	}
}
