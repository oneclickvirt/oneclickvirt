package initialize

import (
	"testing"

	"oneclickvirt/config"
	"oneclickvirt/global"
)

func TestSyncQuotaConfigExpiryDays(t *testing.T) {
	tests := []struct {
		name           string
		quotaConfig    map[string]interface{}
		wantExpiryDays int
		level          int
	}{
		{"expiry-days positive int", map[string]interface{}{"level-limits": map[string]interface{}{"2": map[string]interface{}{"max-instances": float64(3), "max-traffic": float64(204800), "expiry-days": float64(30)}}}, 30, 2},
		{"expiry-days zero means no expiry", map[string]interface{}{"level-limits": map[string]interface{}{"1": map[string]interface{}{"max-instances": float64(1), "max-traffic": float64(102400), "expiry-days": float64(0)}}}, 0, 1},
		{"expiry-days as native int", map[string]interface{}{"level-limits": map[string]interface{}{"3": map[string]interface{}{"max-instances": 5, "max-traffic": int64(307200), "expiry-days": 7}}}, 7, 3},
		{"missing expiry-days defaults to 0", map[string]interface{}{"level-limits": map[string]interface{}{"4": map[string]interface{}{"max-instances": float64(10), "max-traffic": float64(409600)}}}, 0, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Server{}
			syncQuotaConfig(cfg, tt.quotaConfig)
			limitInfo, ok := cfg.Quota.LevelLimits[tt.level]
			if !ok {
				t.Fatalf("level %d not written to LevelLimits", tt.level)
			}
			if limitInfo.ExpiryDays != tt.wantExpiryDays {
				t.Errorf("ExpiryDays: want %d, got %d", tt.wantExpiryDays, limitInfo.ExpiryDays)
			}
		})
	}
}

func TestSyncQuotaConfigReplacesLevelLimits(t *testing.T) {
	cfg := &config.Server{
		Quota: config.Quota{
			LevelLimits: map[int]config.LevelLimitInfo{
				1: {MaxInstances: 1},
				2: {MaxInstances: 3},
			},
		},
	}

	syncQuotaConfig(cfg, map[string]interface{}{
		"level-limits": map[string]interface{}{
			"2": map[string]interface{}{
				"max-instances": 4,
				"max-traffic":   204800,
			},
		},
	})

	if _, ok := cfg.Quota.LevelLimits[1]; ok {
		t.Fatalf("expected removed level 1 to be absent after full level-limits sync")
	}
	if got := cfg.Quota.LevelLimits[2].MaxInstances; got != 4 {
		t.Fatalf("level 2 MaxInstances: want 4, got %d", got)
	}
}

func TestGetDefaultConfigCaptchaDisabled(t *testing.T) {
	cfg := getDefaultConfig()

	if cfg.Captcha.Enabled {
		t.Fatalf("expected captcha to be disabled by default")
	}

	if cfg.Captcha.Width != 120 || cfg.Captcha.Height != 40 || cfg.Captcha.Length != 4 || cfg.Captcha.ExpireTime != 5 {
		t.Fatalf("unexpected default captcha config: %+v", cfg.Captcha)
	}
}

func TestSyncConfigToGlobalCaptchaEnabled(t *testing.T) {
	global.SetAppConfig(getDefaultConfig())

	if err := syncConfigToGlobal("captcha", nil, map[string]interface{}{"enabled": true}); err != nil {
		t.Fatalf("syncConfigToGlobal returned error: %v", err)
	}

	if !global.GetAppConfig().Captcha.Enabled {
		t.Fatalf("expected captcha to be enabled after sync")
	}
}
