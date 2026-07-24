package provider

import "time"

// ProviderIPv6Pool stores either an individual IPv6 address or an IPv6 range.
// Range rows are kept as prefixes instead of being expanded into millions of
// records. Allocated addresses are materialized as child rows for auditing and
// deterministic release/reuse.
type ProviderIPv6Pool struct {
	ID           uint   `json:"id" gorm:"primaryKey"`
	ProviderID   uint   `json:"provider_id" gorm:"not null;index:idx_provider_ipv6,priority:1;uniqueIndex:idx_provider_ipv6_address,priority:1"`
	Address      string `json:"address" gorm:"not null;size:128;uniqueIndex:idx_provider_ipv6_address,priority:2"`
	PrefixLength int    `json:"prefix_length" gorm:"not null;default:128"`
	IsRange      bool   `json:"is_range" gorm:"default:false;index"`
	RangeNext    string `json:"range_next" gorm:"size:128"`
	ParentID     *uint  `json:"parent_id" gorm:"index"`
	IsAllocated  bool   `json:"is_allocated" gorm:"default:false;index:idx_provider_ipv6,priority:2"`
	// PendingRetire keeps an existing binding visible after its node-file source
	// removes the address while preventing any new allocation from using it.
	PendingRetire bool `json:"pending_retire" gorm:"default:false;index"`
	// Instance IDs are globally unique. Keeping the nullable binding unique
	// prevents concurrent retries from assigning two live IPv6 rows to the same
	// instance; MySQL still permits any number of NULL values after release.
	InstanceID *uint      `json:"instance_id" gorm:"uniqueIndex:idx_provider_ipv6_instance"`
	Source     string     `json:"source" gorm:"size:32;not null;default:manual;index"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"-" gorm:"index"`
}

func (ProviderIPv6Pool) TableName() string { return "provider_ipv6_pools" }
