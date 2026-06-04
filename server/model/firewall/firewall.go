package firewall

import (
	"time"

	"gorm.io/gorm"
)

type BlockRuleCategory string

const (
	BlockRuleCategoryMining    BlockRuleCategory = "mining"
	BlockRuleCategoryBT        BlockRuleCategory = "bt"
	BlockRuleCategorySpeedtest BlockRuleCategory = "speedtest"
	BlockRuleCategoryCustom    BlockRuleCategory = "custom"
)

type BlockRule struct {
	ID          uint           `gorm:"primarykey" json:"id"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index;uniqueIndex:idx_block_rule_name_deleted,priority:2" json:"-"`
	Name        string         `gorm:"size:64;not null;uniqueIndex:idx_block_rule_name_deleted,priority:1" json:"name"`
	Category    string         `gorm:"size:32;not null;index:idx_block_rule_category" json:"category"`
	Description string         `gorm:"size:512" json:"description"`
	Strings     string         `gorm:"type:text;not null" json:"strings"`
	IsBuiltin   bool           `gorm:"not null;default:false" json:"is_builtin"`
	Enabled     bool           `gorm:"not null;default:true" json:"enabled"`
}

func (BlockRule) TableName() string {
	return "block_rules"
}

type BlockRuleApplication struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
	RuleID     uint           `gorm:"not null;index:idx_bra_rule" json:"rule_id"`
	Scope      string         `gorm:"size:32;not null;index:idx_bra_scope" json:"scope"`
	TargetID   uint           `gorm:"not null;default:0;index:idx_bra_target" json:"target_id"`
	TargetName string         `gorm:"size:255" json:"target_name"`
	Status     string         `gorm:"size:16;not null;default:pending" json:"status"`
	IPVersion  string         `gorm:"size:8;not null;default:both" json:"ip_version"`
}

func (BlockRuleApplication) TableName() string {
	return "block_rule_applications"
}

type CreateBlockRuleRequest struct {
	Name        string   `json:"name" binding:"required"`
	Category    string   `json:"category" binding:"required"`
	Description string   `json:"description"`
	Strings     []string `json:"strings" binding:"required"`
	Enabled     bool     `json:"enabled"`
}

type UpdateBlockRuleRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Strings     []string `json:"strings"`
	Enabled     *bool    `json:"enabled"`
}

type ApplyBlockRuleRequest struct {
	RuleIDs   []uint `json:"rule_ids" binding:"required"`
	Scope     string `json:"scope" binding:"required"`
	TargetIDs []uint `json:"target_ids"`
	IPVersion string `json:"ip_version"` // "both" (default), "ipv4", "ipv6"
}

type RemoveBlockRuleApplicationRequest struct {
	ApplicationIDs []uint `json:"application_ids" binding:"required"`
}
