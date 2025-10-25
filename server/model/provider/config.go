package provider

import "time"

// ProviderAuthConfig 统一的Provider认证配置结构
type ProviderAuthConfig struct {
	Type string `json:"type"` // lxd, incus, proxmox, docker

	// 通用字段
	Endpoint string `json:"endpoint"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// SSH连接信息
	SSH *SSHConfig `json:"ssh,omitempty"`

	// 证书认证（LXD/Incus）
	Certificate *CertConfig `json:"certificate,omitempty"`

	// Token认证（Proxmox）
	Token *TokenConfig `json:"token,omitempty"`

	// Docker配置
	Docker *DockerConfig `json:"docker,omitempty"`

	// 其他配置
	Extra map[string]interface{} `json:"extra,omitempty"`
}

// SSHConfig SSH连接配置
type SSHConfig struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`
	KeyPath    string `json:"keyPath,omitempty"`
	KeyContent string `json:"keyContent,omitempty"` // SSH私钥内容
}

// CertConfig 证书配置
type CertConfig struct {
	CertPath        string `json:"certPath"`
	KeyPath         string `json:"keyPath"`
	CertFingerprint string `json:"certFingerprint"`
	CertContent     string `json:"certContent,omitempty"` // 用于API调用
	KeyContent      string `json:"keyContent,omitempty"`  // 用于API调用
}

// TokenConfig Token配置
type TokenConfig struct {
	TokenID     string `json:"tokenId"`
	TokenSecret string `json:"tokenSecret"`
	Username    string `json:"username,omitempty"`
}

// DockerConfig Docker配置
type DockerConfig struct {
	SocketPath string            `json:"socketPath,omitempty"`
	Host       string            `json:"host,omitempty"`
	TLS        bool              `json:"tls,omitempty"`
	CertPath   string            `json:"certPath,omitempty"`
	KeyPath    string            `json:"keyPath,omitempty"`
	CAPath     string            `json:"caPath,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// ConfigBackup 配置备份结构（用于导出到其他程序）
type ConfigBackup struct {
	ProviderID    uint                `json:"providerId"`
	ProviderUUID  string              `json:"providerUuid"`
	ProviderName  string              `json:"providerName"`
	ProviderType  string              `json:"providerType"`
	AuthConfig    *ProviderAuthConfig `json:"authConfig"`
	Status        string              `json:"status"`
	LastUpdated   time.Time           `json:"lastUpdated"`
	ConfigVersion int                 `json:"configVersion"`
	CreatedAt     time.Time           `json:"createdAt"`
	UpdatedAt     time.Time           `json:"updatedAt"`
}
