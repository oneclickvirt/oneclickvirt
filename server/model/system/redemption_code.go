package system

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RedemptionCode 兑换码模型
// 状态流转：pending_create → pending_use → used
// 失败时直接硬删除记录
const (
	RedemptionStatusPendingCreate = "pending_create" // 待创建：任务已提交，排队等待执行
	RedemptionStatusCreating      = "creating"       // 创建中：实例创建任务正在运行
	RedemptionStatusPendingUse    = "pending_use"    // 待使用：实例已就绪，等待用户兑换
	RedemptionStatusUsed          = "used"           // 已使用：用户已兑换，实例已转移
	RedemptionStatusDeleting      = "deleting"       // 删除中：暂态，实例正在清理
)

// RedemptionCode 兑换码
type RedemptionCode struct {
	ID        uint      `json:"id" gorm:"primarykey"`
	UUID      string    `json:"uuid" gorm:"uniqueIndex;not null;size:36"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// 兑换码本身
	Code string `json:"code" gorm:"uniqueIndex;not null;size:32"` // 兑换码字符串（唯一）

	// 状态
	Status string `json:"status" gorm:"not null;default:pending_create;size:32;index"` // 见上方常量

	// 关联节点
	ProviderID   uint   `json:"providerId" gorm:"not null;index"` // 节点 ID
	ProviderName string `json:"providerName" gorm:"size:64"`      // 节点名称（冗余存储，便于展示）

	// 关联实例（创建成功后填充）
	InstanceID *uint `json:"instanceId" gorm:"index"` // nil = 尚未创建实例

	// 兑换者（兑换后填充）
	UserID *uint `json:"userId" gorm:"index"` // nil = 尚未被兑换

	// 实例配置（用于创建实例）
	InstanceType string `json:"instanceType" gorm:"not null;size:16"` // container / vm
	ImageId      uint   `json:"imageId" gorm:"not null"`
	CPUId        string `json:"cpuId" gorm:"size:32"`
	MemoryId     string `json:"memoryId" gorm:"size:32"`
	DiskId       string `json:"diskId" gorm:"size:32"`
	BandwidthId  string `json:"bandwidthId" gorm:"size:32"`

	// 创建任务关联
	TaskID *uint `json:"taskId" gorm:"index"` // 创建实例的任务 ID

	// 管理
	CreatedBy  uint       `json:"createdBy" gorm:"not null;index"` // 创建该批次的管理员 ID
	RedeemedAt *time.Time `json:"redeemedAt"`                      // 兑换时间

	// 备注（可选）
	Remark string `json:"remark" gorm:"size:255"`
}

func (r *RedemptionCode) BeforeCreate(tx *gorm.DB) error {
	r.UUID = uuid.New().String()
	return nil
}

func (RedemptionCode) TableName() string {
	return "redemption_codes"
}
