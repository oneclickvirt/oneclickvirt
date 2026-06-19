package provider

import (
	"testing"

	"oneclickvirt/constant"
	providerModel "oneclickvirt/model/provider"
)

func TestNormalizeProviderConcurrencySettings(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "empty uses default", in: 0, want: constant.ProviderDefaultConcurrentTasks},
		{name: "negative uses default", in: -3, want: constant.ProviderDefaultConcurrentTasks},
		{name: "valid value is preserved", in: 4, want: 4},
		{name: "large value is capped", in: constant.ProviderMaxConcurrentTasks + 1, want: constant.ProviderMaxConcurrentTasks},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := providerModel.Provider{MaxConcurrentTasks: tt.in}
			normalizeProviderConcurrencySettings(&provider)
			if provider.MaxConcurrentTasks != tt.want {
				t.Fatalf("MaxConcurrentTasks = %d, want %d", provider.MaxConcurrentTasks, tt.want)
			}
		})
	}
}
