package provider

import "time"

const (
	InstanceExpiryActionDelete = "delete"
	InstanceExpiryActionFreeze = "freeze"
	InstanceExpiryActionStop   = "stop"
	InstanceExpiryActionExtend = "extend"

	TrafficOverLimitActionStop       = "stop"
	TrafficOverLimitActionSpeedLimit = "speed_limit"
	TrafficOverLimitActionFreeze     = "freeze"
	TrafficOverLimitActionMarkOnly   = "mark_only"
)

// InstanceShareLink stores a short-lived, single-instance management grant.
// Only the token hash is persisted; the raw token is returned once on creation.
type InstanceShareLink struct {
	ID            uint       `json:"id" gorm:"primarykey"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	InstanceID    uint       `json:"instanceId" gorm:"not null;index:idx_share_instance"`
	OwnerUserID   uint       `json:"ownerUserId" gorm:"index"`
	CreatedByID   uint       `json:"createdById" gorm:"index"`
	CreatedByType string     `json:"createdByType" gorm:"size:16;not null"`
	TokenHash     string     `json:"-" gorm:"uniqueIndex;not null;size:64"`
	TokenPrefix   string     `json:"tokenPrefix" gorm:"size:12;index"`
	ExpiresAt     time.Time  `json:"expiresAt" gorm:"not null;index"`
	RevokedAt     *time.Time `json:"revokedAt" gorm:"index"`
	LastUsedAt    *time.Time `json:"lastUsedAt"`
	UseCount      int        `json:"useCount" gorm:"default:0"`
}
