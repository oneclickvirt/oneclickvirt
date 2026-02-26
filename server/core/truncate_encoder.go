package core

import (
	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

// TruncateEncoder 包装原始编码器，对过长内容进行截断
type TruncateEncoder struct {
	zapcore.Encoder
	config *TruncateConfig
}

type TruncateConfig struct {
	MaxLogLength     int
	MaxArrayElements int
	MaxStringLength  int
}

// NewTruncateEncoder 创建截断编码器
func NewTruncateEncoder(enc zapcore.Encoder) zapcore.Encoder {
	config := &TruncateConfig{
		MaxLogLength:     global.GetAppConfig().Zap.MaxLogLength,
		MaxArrayElements: global.GetAppConfig().Zap.MaxArrayElements,
		MaxStringLength:  global.GetAppConfig().Zap.MaxStringLength,
	}

	// 设置默认值
	if config.MaxLogLength == 0 {
		config.MaxLogLength = utils.MaxLogLength
	}
	if config.MaxArrayElements == 0 {
		config.MaxArrayElements = utils.MaxArrayElements
	}
	if config.MaxStringLength == 0 {
		config.MaxStringLength = utils.MaxStringLength
	}

	return &TruncateEncoder{
		Encoder: enc,
		config:  config,
	}
}

// Clone 克隆编码器
func (enc *TruncateEncoder) Clone() zapcore.Encoder {
	return &TruncateEncoder{
		Encoder: enc.Encoder.Clone(),
		config:  enc.config,
	}
}

// EncodeEntry 编码日志条目，对过长内容进行截断
func (enc *TruncateEncoder) EncodeEntry(ent zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	// 处理消息内容
	if len(ent.Message) > enc.config.MaxStringLength {
		ent.Message = utils.TruncateString(ent.Message, enc.config.MaxStringLength)
	}

	// 处理字段内容
	truncatedFields := make([]zapcore.Field, len(fields))
	for i, field := range fields {
		truncatedFields[i] = enc.truncateField(field)
	}

	buf, err := enc.Encoder.EncodeEntry(ent, truncatedFields)
	if err != nil {
		return nil, err
	}

	// 检查最终日志长度
	if buf.Len() > enc.config.MaxLogLength {
		originalContent := buf.String()
		buf.Reset()
		truncatedContent := utils.TruncateString(originalContent, enc.config.MaxLogLength)
		buf.AppendString(truncatedContent)
	}

	return buf, nil
}

// truncateField 截断字段内容
func (enc *TruncateEncoder) truncateField(field zapcore.Field) zapcore.Field {
	// 特殊字段不截断（用于调试重要错误信息）
	noTruncateFields := map[string]bool{
		"complete_output": true,
		"full_output":     true,
		"debug_output":    true,
		"error_detail":    true,
	}

	if noTruncateFields[field.Key] {
		return field // 不截断这些字段
	}

	switch field.Type {
	case zapcore.StringType:
		if len(field.String) > enc.config.MaxStringLength {
			field.String = utils.TruncateString(field.String, enc.config.MaxStringLength)
		}
	case zapcore.ByteStringType:
		if len(field.Interface.([]byte)) > enc.config.MaxStringLength {
			truncated := field.Interface.([]byte)[:enc.config.MaxStringLength-3]
			field.Interface = append(truncated, '.', '.', '.')
		}
	case zapcore.ReflectType:
		// 对于复杂对象，尝试JSON化然后截断
		if field.Interface != nil {
			jsonStr := utils.TruncateJSON(field.Interface)
			if len(jsonStr) > enc.config.MaxStringLength {
				jsonStr = utils.TruncateString(jsonStr, enc.config.MaxStringLength)
			}
			field = zapcore.Field{
				Key:    field.Key,
				Type:   zapcore.StringType,
				String: jsonStr,
			}
		}
	case zapcore.ErrorType:
		if field.Interface != nil {
			if err, ok := field.Interface.(error); ok {
				errStr := utils.FormatError(err)
				field = zapcore.Field{
					Key:    field.Key,
					Type:   zapcore.StringType,
					String: errStr,
				}
			}
		}
	}

	return field
}
