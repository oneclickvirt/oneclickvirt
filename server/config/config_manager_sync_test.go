package config

import (
	"testing"

	"go.uber.org/zap"
)

func TestSyncToGlobalConfigUsesFullCacheSnapshot(t *testing.T) {
	cm := &ConfigManager{
		logger: zap.NewNop(),
		configCache: map[string]interface{}{
			"captcha.enabled":                 true,
			"auth.enable-public-registration": false,
		},
	}

	triggered := map[string]interface{}{}
	cm.changeCallbacks = append(cm.changeCallbacks, func(key string, oldValue, newValue interface{}) error {
		triggered[key] = newValue
		return nil
	})

	if err := cm.syncToGlobalConfig(map[string]interface{}{"captcha": map[string]interface{}{"enabled": true}}); err != nil {
		t.Fatalf("syncToGlobalConfig returned error: %v", err)
	}

	captchaConfig, ok := triggered["captcha"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected captcha callback payload, got %T", triggered["captcha"])
	}
	if enabled, ok := captchaConfig["enabled"].(bool); !ok || !enabled {
		t.Fatalf("expected captcha.enabled=true in callback payload, got %#v", captchaConfig["enabled"])
	}

	authConfig, ok := triggered["auth"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected auth callback payload, got %T", triggered["auth"])
	}
	if registrationEnabled, ok := authConfig["enable-public-registration"].(bool); !ok || registrationEnabled {
		t.Fatalf("expected auth.enable-public-registration=false in callback payload, got %#v", authConfig["enable-public-registration"])
	}
}
