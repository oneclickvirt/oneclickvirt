package resource

import "time"

// ResourceCheckRequest 资源校验请求
type ResourceCheckRequest struct {
	ProviderID   uint   `json:"providerId"`
	InstanceType string `json:"instanceType"` // container 或 vm
	CPU          int    `json:"cpu"`
	Memory       int64  `json:"memory"` // MB
	Disk         int64  `json:"disk"`   // MB
}

// ResourceCheckResult 资源校验结果
type ResourceCheckResult struct {
	Allowed         bool   `json:"allowed"`
	Reason          string `json:"reason,omitempty"`
	AvailableCPU    int    `json:"availableCpu"`
	AvailableMemory int64  `json:"availableMemory"`
	AvailableDisk   int64  `json:"availableDisk"`
}

// ReserveResourcesRequest 预留资源请求 - 简化设计
type ReserveResourcesRequest struct {
	UserID          uint
	ProviderID      uint
	SessionID       string // 会话标识，替代TaskID
	InstanceType    string
	CPU             int
	Memory          int64
	Disk            int64
	Bandwidth       int
	ReserveDuration time.Duration // 预留时长，默认5分钟
}

// ReserveResourcesResult 预留资源结果
type ReserveResourcesResult struct {
	Allowed       bool
	Reason        string
	ReservationID uint
	ExpiresAt     time.Time
}
