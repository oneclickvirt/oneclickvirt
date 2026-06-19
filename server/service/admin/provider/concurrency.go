package provider

import (
	"oneclickvirt/constant"
	providerModel "oneclickvirt/model/provider"
)

func normalizeProviderConcurrencySettings(provider *providerModel.Provider) {
	if provider.MaxConcurrentTasks <= 0 {
		provider.MaxConcurrentTasks = constant.ProviderDefaultConcurrentTasks
		return
	}
	if provider.MaxConcurrentTasks > constant.ProviderMaxConcurrentTasks {
		provider.MaxConcurrentTasks = constant.ProviderMaxConcurrentTasks
	}
}
