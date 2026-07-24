package monitoring

import "time"

// EgressDesiredProfile is the controller-authoritative profile definition.
// ConfigJSON is sanitized; write-only tunnel secrets are stored separately as
// AES-GCM ciphertext and are never exposed through model JSON serialization.
type EgressDesiredProfile struct {
	ID                     uint      `gorm:"primarykey" json:"-"`
	CreatedAt              time.Time `json:"-"`
	UpdatedAt              time.Time `json:"-"`
	ProviderID             uint      `gorm:"uniqueIndex:uk_egress_profile,priority:1;index:idx_egress_profile_provider;not null" json:"-"`
	ProfileID              string    `gorm:"uniqueIndex:uk_egress_profile,priority:2;size:128;not null" json:"-"`
	ConfigJSON             string    `gorm:"type:text;not null" json:"-"`
	PrivateKeyCiphertext   string    `gorm:"type:text" json:"-"`
	PresharedKeyCiphertext string    `gorm:"type:text" json:"-"`
}

func (EgressDesiredProfile) TableName() string {
	return "egress_desired_profiles"
}

// EgressDesiredBinding records both active binding intent and pending cleanup.
// PendingDelete is retained until the Agent confirms deletion, so an offline
// node cannot strand a stale binding after a controller-side unbind.
type EgressDesiredBinding struct {
	ID          uint      `gorm:"primarykey" json:"-"`
	CreatedAt   time.Time `json:"-"`
	UpdatedAt   time.Time `json:"-"`
	InstanceID  uint      `gorm:"uniqueIndex;not null" json:"-"`
	InstanceKey string    `gorm:"size:128;not null" json:"-"`
	ProviderID  uint      `gorm:"index:idx_egress_binding_provider;not null" json:"-"`
	ProfileID   string    `gorm:"index:idx_egress_binding_profile;size:128;not null" json:"-"`
	SourcesJSON string    `gorm:"type:text;not null" json:"-"`
	// ExplicitSourcesJSON keeps administrator-supplied selectors separate from
	// addresses derived from mutable instance inventory. Refresh can therefore
	// replace stale automatic addresses without discarding explicit selectors.
	ExplicitSourcesJSON string `gorm:"type:text" json:"-"`
	Interface           string `gorm:"size:15" json:"-"`
	InterfaceV4         string `gorm:"size:15" json:"-"`
	InterfaceV6         string `gorm:"size:15" json:"-"`
	Enabled             bool   `gorm:"not null;default:true" json:"-"`
	PendingDelete       bool   `gorm:"index:idx_egress_binding_provider;not null;default:false" json:"-"`
}

func (EgressDesiredBinding) TableName() string {
	return "egress_desired_bindings"
}
