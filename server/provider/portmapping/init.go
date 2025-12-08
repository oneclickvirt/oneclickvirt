package portmapping

// InitAllProviders 初始化所有端口映射Provider
// 这个函数应该在应用启动时调用，以确保所有Provider都被注册
// 实际的Provider导入应该在main包或初始化包中进行
func InitAllProviders() {
	// 所有Provider的init函数会在包导入时自动调用
	// 这个函数只是一个显式的初始化入口点
}

// GetAllRegisteredProviderTypes 获取所有已注册的Provider类型
func GetAllRegisteredProviderTypes() []string {
	providerTypes := []string{}
	registeredProviders := GetRegisteredProviders()
	for providerType := range registeredProviders {
		providerTypes = append(providerTypes, providerType)
	}
	return providerTypes
}

// CreateManagerWithAllProviders 创建包含所有Provider的管理器
func CreateManagerWithAllProviders(config *ManagerConfig) *Manager {
	if config == nil {
		config = &ManagerConfig{
			PortRangeStart:       10000,
			PortRangeEnd:         65535,
			DefaultMappingMethod: "native",
		}
	}

	manager := NewManager(config)

	// 注册所有已注册的Provider到管理器
	registeredProviders := GetRegisteredProviders()
	for providerType, factory := range registeredProviders {
		manager.RegisterProvider(providerType, factory)
	}

	return manager
}

// GetProviderDescription 获取Provider描述信息
func GetProviderDescription(providerType string) map[string]interface{} {
	descriptions := map[string]map[string]interface{}{
		"docker": {
			"name":        "Docker",
			"description": "Docker原生端口映射，使用docker run -p参数进行端口绑定",
			"methods":     []string{"port-binding"},
			"protocols":   []string{"tcp", "udp"},
			"features":    []string{"native", "high-performance", "container-specific"},
		},
		"lxd": {
			"name":        "LXD",
			"description": "LXD原生端口映射，使用proxy device进行端口转发",
			"methods":     []string{"proxy-device"},
			"protocols":   []string{"tcp", "udp"},
			"features":    []string{"native", "flexible", "container-specific"},
		},
		"incus": {
			"name":        "Incus",
			"description": "Incus原生端口映射，使用proxy device进行端口转发",
			"methods":     []string{"proxy-device"},
			"protocols":   []string{"tcp", "udp"},
			"features":    []string{"native", "flexible", "container-specific"},
		},
		"pve": {
			"name":        "Proxmox VE",
			"description": "Proxmox VE端口映射，使用iptables NAT规则进行端口转发",
			"methods":     []string{"iptables-nat"},
			"protocols":   []string{"tcp", "udp"},
			"features":    []string{"vm-specific", "flexible", "host-level"},
		},
		"iptables": {
			"name":        "iptables",
			"description": "通用iptables NAT端口映射，适用于各种场景",
			"methods":     []string{"nat", "dnat", "snat"},
			"protocols":   []string{"tcp", "udp"},
			"features":    []string{"universal", "flexible", "host-level", "high-performance"},
		},
	}

	if desc, exists := descriptions[providerType]; exists {
		return desc
	}

	return map[string]interface{}{
		"name":        providerType,
		"description": "Unknown provider type",
		"methods":     []string{},
		"protocols":   []string{},
		"features":    []string{},
	}
}

// GetProviderCapabilities 获取Provider能力信息
func GetProviderCapabilities(providerType string) map[string]bool {
	capabilities := map[string]map[string]bool{
		"docker": {
			"auto_port_allocation": true,
			"custom_port_range":    false,
			"ipv6_support":         true,
			"protocol_tcp":         true,
			"protocol_udp":         true,
			"hot_reload":           false,
			"persistent":           true,
		},
		"lxd": {
			"auto_port_allocation": true,
			"custom_port_range":    true,
			"ipv6_support":         true,
			"protocol_tcp":         true,
			"protocol_udp":         true,
			"hot_reload":           true,
			"persistent":           true,
		},
		"incus": {
			"auto_port_allocation": true,
			"custom_port_range":    true,
			"ipv6_support":         true,
			"protocol_tcp":         true,
			"protocol_udp":         true,
			"hot_reload":           true,
			"persistent":           true,
		},
		"pve": {
			"auto_port_allocation": true,
			"custom_port_range":    true,
			"ipv6_support":         true,
			"protocol_tcp":         true,
			"protocol_udp":         true,
			"hot_reload":           true,
			"persistent":           false,
		},
		"iptables": {
			"auto_port_allocation": true,
			"custom_port_range":    true,
			"ipv6_support":         true,
			"protocol_tcp":         true,
			"protocol_udp":         true,
			"hot_reload":           true,
			"persistent":           false,
		},
	}

	if caps, exists := capabilities[providerType]; exists {
		return caps
	}

	return map[string]bool{
		"auto_port_allocation": false,
		"custom_port_range":    false,
		"ipv6_support":         false,
		"protocol_tcp":         false,
		"protocol_udp":         false,
		"hot_reload":           false,
		"persistent":           false,
	}
}
