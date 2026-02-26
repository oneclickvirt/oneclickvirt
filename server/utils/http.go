package utils

import "oneclickvirt/global"

// GetCDNEndpoints 从配置中获取CDN端点列表
// 该函数确保基础端点在列表中，并且可以被多个provider复用
func GetCDNEndpoints() []string {
	// 从配置中获取CDN端点
	cdnEndpoints := make([]string, 0)

	// 先添加配置文件中的额外端点
	if global.GetAppConfig().CDN.Endpoints != nil {
		cdnEndpoints = append(cdnEndpoints, global.GetAppConfig().CDN.Endpoints...)
	}

	// 确保基础端点在列表中
	baseEndpoint := global.GetAppConfig().CDN.BaseEndpoint
	if baseEndpoint == "" {
		baseEndpoint = "https://cdn.spiritlhl.net/" // 默认基础端点
	}

	// 检查基础端点是否已经在列表中
	found := false
	for _, endpoint := range cdnEndpoints {
		if endpoint == baseEndpoint {
			found = true
			break
		}
	}

	// 如果基础端点不在列表中，添加到最后
	if !found {
		cdnEndpoints = append(cdnEndpoints, baseEndpoint)
	}

	return cdnEndpoints
}

// GetBaseCDNEndpoint 获取基础CDN端点
func GetBaseCDNEndpoint() string {
	baseEndpoint := global.GetAppConfig().CDN.BaseEndpoint
	if baseEndpoint == "" {
		baseEndpoint = "https://cdn.spiritlhl.net/" // 默认基础端点
	}
	return baseEndpoint
}
