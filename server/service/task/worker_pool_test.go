package task

import (
	"testing"

	"oneclickvirt/constant"
	providerModel "oneclickvirt/model/provider"
)

func TestGetProviderTaskConcurrency(t *testing.T) {
	tests := []struct {
		name     string
		provider providerModel.Provider
		want     int
	}{
		{
			name: "disabled forces serial execution",
			provider: providerModel.Provider{
				AllowConcurrentTasks: false,
				MaxConcurrentTasks:   constant.ProviderMaxConcurrentTasks,
			},
			want: constant.ProviderDefaultConcurrentTasks,
		},
		{
			name: "enabled empty uses default",
			provider: providerModel.Provider{
				AllowConcurrentTasks: true,
				MaxConcurrentTasks:   0,
			},
			want: constant.ProviderDefaultConcurrentTasks,
		},
		{
			name: "enabled valid uses configured value",
			provider: providerModel.Provider{
				AllowConcurrentTasks: true,
				MaxConcurrentTasks:   3,
			},
			want: 3,
		},
		{
			name: "enabled large value is capped",
			provider: providerModel.Provider{
				AllowConcurrentTasks: true,
				MaxConcurrentTasks:   constant.ProviderMaxConcurrentTasks + 100,
			},
			want: constant.ProviderMaxConcurrentTasks,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getProviderTaskConcurrency(tt.provider); got != tt.want {
				t.Fatalf("getProviderTaskConcurrency() = %d, want %d", got, tt.want)
			}
		})
	}
}
