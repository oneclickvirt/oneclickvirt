package provider

import (
	"time"
)

// ProviderInstanceResponse API响应用的实例信息
type ProviderInstanceResponse struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Status   string            `json:"status"`
	Type     string            `json:"type"`
	Image    string            `json:"image"`
	IP       string            `json:"ip"`
	CPU      string            `json:"cpu"`
	Memory   string            `json:"memory"`
	Disk     string            `json:"disk"`
	Created  time.Time         `json:"created"`
	Metadata map[string]string `json:"metadata"`
}

// ProviderImageResponse API响应用的镜像信息
type ProviderImageResponse struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Tag         string            `json:"tag"`
	Size        string            `json:"size"`
	Created     time.Time         `json:"created"`
	Description string            `json:"description"`
	Metadata    map[string]string `json:"metadata"`
}

// InstanceCreateRequest 创建实例的请求
type InstanceCreateRequest struct {
	Name         string            `json:"name" binding:"required"`
	Image        string            `json:"image" binding:"required"`
	ImageURL     string            `json:"image_url"`
	ImagePath    string            `json:"image_path"`
	CPU          string            `json:"cpu"`
	Memory       string            `json:"memory"`
	Disk         string            `json:"disk"`
	Network      string            `json:"network"`
	Ports        []string          `json:"ports"`
	Env          map[string]string `json:"env"`
	Metadata     map[string]string `json:"metadata"`
	InstanceType string            `json:"instance_type"`
}

// NodeConfigRequest 节点配置请求
type NodeConfigRequest struct {
	UUID                  string   `json:"uuid"`
	Name                  string   `json:"name" binding:"required"`
	Host                  string   `json:"host" binding:"required"`
	Port                  int      `json:"port"`
	Username              string   `json:"username"`
	Password              string   `json:"password"`
	Token                 string   `json:"token"`
	TokenID               string   `json:"token_id"`
	CertPath              string   `json:"cert_path"`
	KeyPath               string   `json:"key_path"`
	Country               string   `json:"country"`
	Architecture          string   `json:"architecture"`
	Type                  string   `json:"type" binding:"required"`
	SupportedTypes        []string `json:"supported_types"`
	ContainerEnabled      bool     `json:"container_enabled"`
	VirtualMachineEnabled bool     `json:"vm_enabled"`

	// 容器资源限制配置（Provider层面）
	ContainerLimitCPU    bool `json:"containerLimitCpu"`    // 容器是否限制CPU数量
	ContainerLimitMemory bool `json:"containerLimitMemory"` // 容器是否限制内存大小
	ContainerLimitDisk   bool `json:"containerLimitDisk"`   // 容器是否限制硬盘大小

	// 虚拟机资源限制配置（Provider层面）
	VMLimitCPU    bool `json:"vmLimitCpu"`    // 虚拟机是否限制CPU数量
	VMLimitMemory bool `json:"vmLimitMemory"` // 虚拟机是否限制内存大小
	VMLimitDisk   bool `json:"vmLimitDisk"`   // 虚拟机是否限制硬盘大小
}
