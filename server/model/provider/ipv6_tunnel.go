package provider

import "time"

const (
	IPv6TunnelStatusPending  = "pending"
	IPv6TunnelStatusActive   = "active"
	IPv6TunnelStatusInactive = "inactive"
	IPv6TunnelStatusError    = "error"
)

// ProviderIPv6Tunnel stores the desired host-side IPv6-over-IPv4 tunnel
// configuration. The corresponding systemd unit is rendered on the Provider
// node and survives host reboots independently of the controller process.
type ProviderIPv6Tunnel struct {
	ID            uint       `json:"id" gorm:"primaryKey"`
	ProviderID    uint       `json:"providerId" gorm:"not null;index;uniqueIndex:idx_provider_ipv6_tunnel_interface,priority:1"`
	Name          string     `json:"name" gorm:"not null;size:64"`
	Mode          string     `json:"mode" gorm:"not null;size:16"`
	Interface     string     `json:"interfaceName" gorm:"not null;size:15;uniqueIndex:idx_provider_ipv6_tunnel_interface,priority:2"`
	LocalIPv4     string     `json:"localIpv4" gorm:"not null;size:45"`
	RemoteIPv4    string     `json:"remoteIpv4" gorm:"not null;size:45"`
	LocalIPv6     string     `json:"localIpv6" gorm:"not null;size:128"`
	RemoteIPv6    string     `json:"remoteIpv6" gorm:"not null;size:128"`
	RoutedCIDR    string     `json:"routedCidr" gorm:"size:128"`
	MTU           int        `json:"mtu" gorm:"not null;default:1480"`
	TTL           int        `json:"ttl" gorm:"not null;default:255"`
	RouteMetric   int        `json:"routeMetric" gorm:"not null;default:100"`
	DefaultRoute  bool       `json:"defaultRoute" gorm:"not null;default:false"`
	Enabled       bool       `json:"enabled" gorm:"not null;default:false;index"`
	Status        string     `json:"status" gorm:"not null;size:16;default:inactive;index"`
	LastError     string     `json:"lastError" gorm:"type:text"`
	LastCheckedAt *time.Time `json:"lastCheckedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

func (ProviderIPv6Tunnel) TableName() string { return "provider_ipv6_tunnels" }
