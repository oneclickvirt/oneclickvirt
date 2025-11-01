package system

import (
	"time"

	"gorm.io/gorm"
)

// JWTSecret JWT密钥配置表
type JWTSecret struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	SecretKey string         `gorm:"type:varchar(512);not null;uniqueIndex;comment:JWT签名密钥" json:"secret_key"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (JWTSecret) TableName() string {
	return "jwt_secrets"
}
