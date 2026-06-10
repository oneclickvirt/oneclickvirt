package provider

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// InstanceSnapshot records an instance snapshot created on a Provider.
type InstanceSnapshot struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	UUID      string         `json:"uuid" gorm:"uniqueIndex;not null;size:36"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index;uniqueIndex:idx_snapshot_instance_name_deleted,priority:3"`

	ProviderID   uint   `json:"providerId" gorm:"not null;index:idx_snapshot_provider_status,priority:1;index"`
	InstanceID   uint   `json:"instanceId" gorm:"not null;index:idx_snapshot_instance_status,priority:1;index;uniqueIndex:idx_snapshot_instance_name_deleted,priority:1"`
	UserID       uint   `json:"userId" gorm:"index"`
	ProviderType string `json:"providerType" gorm:"size:32;index"`
	InstanceType string `json:"instanceType" gorm:"size:16;index"`
	InstanceName string `json:"instanceName" gorm:"size:128;index"`

	Name         string `json:"name" gorm:"not null;size:128;uniqueIndex:idx_snapshot_instance_name_deleted,priority:2"`
	Description  string `json:"description" gorm:"size:512"`
	Status       string `json:"status" gorm:"size:32;default:creating;index:idx_snapshot_provider_status,priority:2;index:idx_snapshot_instance_status,priority:2"`
	Source       string `json:"source" gorm:"size:32;default:manual;index"`
	SizeBytes    int64  `json:"sizeBytes" gorm:"default:0"`
	ErrorMessage string `json:"errorMessage" gorm:"type:text"`
	Metadata     string `json:"metadata" gorm:"type:text"`
	CreatedBy    uint   `json:"createdBy" gorm:"index"`
}

func (s *InstanceSnapshot) BeforeCreate(tx *gorm.DB) error {
	s.UUID = uuid.New().String()
	return nil
}

// SnapshotSchedule records periodic snapshot rules.
type SnapshotSchedule struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	UUID      string         `json:"uuid" gorm:"uniqueIndex;not null;size:36"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	ProviderID   uint   `json:"providerId" gorm:"not null;index"`
	InstanceID   uint   `json:"instanceId" gorm:"not null;index"`
	UserID       uint   `json:"userId" gorm:"index"`
	ProviderType string `json:"providerType" gorm:"size:32;index"`
	InstanceType string `json:"instanceType" gorm:"size:16;index"`
	InstanceName string `json:"instanceName" gorm:"size:128;index"`

	Name          string     `json:"name" gorm:"not null;size:128"`
	Enabled       bool       `json:"enabled" gorm:"default:true;index"`
	IntervalHours int        `json:"intervalHours" gorm:"default:24"`
	RetentionDays int        `json:"retentionDays" gorm:"default:7"`
	MaxSnapshots  int        `json:"maxSnapshots" gorm:"default:3"`
	NextRunAt     *time.Time `json:"nextRunAt" gorm:"index"`
	LastRunAt     *time.Time `json:"lastRunAt"`
	LastError     string     `json:"lastError" gorm:"type:text"`
	CreatedBy     uint       `json:"createdBy" gorm:"index"`
}

func (s *SnapshotSchedule) BeforeCreate(tx *gorm.DB) error {
	s.UUID = uuid.New().String()
	return nil
}

// SnapshotTask tracks asynchronous snapshot create/delete/restore operations.
// Snapshot provider commands may take minutes, so API handlers and schedulers
// enqueue a task and return immediately instead of holding an HTTP request or
// a scheduler tick until the remote command completes.
type SnapshotTask struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	UUID      string         `json:"uuid" gorm:"uniqueIndex;not null;size:36"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	ProviderID  uint `json:"providerId" gorm:"not null;index"`
	InstanceID  uint `json:"instanceId" gorm:"not null;index"`
	SnapshotID  uint `json:"snapshotId" gorm:"index"`
	AdminTaskID uint `json:"adminTaskId" gorm:"index"`
	ScheduleID  uint `json:"scheduleId" gorm:"index"`
	UserID      uint `json:"userId" gorm:"index"`

	Action      string `json:"action" gorm:"not null;size:32;index"`
	Status      string `json:"status" gorm:"not null;size:32;index"`
	Source      string `json:"source" gorm:"size:32;index"`
	Name        string `json:"name" gorm:"size:128"`
	Description string `json:"description" gorm:"size:512"`

	ErrorMessage string     `json:"errorMessage" gorm:"type:text"`
	StartedAt    *time.Time `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
	CreatedBy    uint       `json:"createdBy" gorm:"index"`
}

func (s *SnapshotTask) BeforeCreate(tx *gorm.DB) error {
	s.UUID = uuid.New().String()
	return nil
}
