package config

// UploadConfig 文件上传配置
type UploadConfig struct {
	MaxAvatarSize int64 `json:"max_avatar_size" yaml:"max_avatar_size"` // 头像最大大小（字节）
}

// DefaultUploadConfig 默认上传配置
var DefaultUploadConfig = UploadConfig{
	MaxAvatarSize: 2 * 1024 * 1024, // 2MB，写死不可配置
}
