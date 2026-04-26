package config

import (
	"testing"

	configpkg "oneclickvirt/config"
)

type stubConfigGetter struct {
	values map[string]interface{}
}

func (s stubConfigGetter) GetConfig(key string) (interface{}, bool) {
	value, exists := s.values[key]
	return value, exists
}

func TestGetConfigBool(t *testing.T) {
	tests := []struct {
		name     string
		getter   configGetter
		key      string
		fallback bool
		want     bool
	}{
		{name: "nil getter uses fallback", getter: nil, key: "captcha.enabled", fallback: false, want: false},
		{name: "typed nil getter uses fallback", getter: (*configpkg.ConfigManager)(nil), key: "captcha.enabled", fallback: true, want: true},
		{name: "getter overrides fallback", getter: stubConfigGetter{values: map[string]interface{}{"captcha.enabled": true}}, key: "captcha.enabled", fallback: false, want: true},
		{name: "missing key uses fallback", getter: stubConfigGetter{values: map[string]interface{}{}}, key: "captcha.enabled", fallback: true, want: true},
		{name: "wrong type uses fallback", getter: stubConfigGetter{values: map[string]interface{}{"captcha.enabled": "true"}}, key: "captcha.enabled", fallback: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getConfigBool(tt.getter, tt.key, tt.fallback); got != tt.want {
				t.Fatalf("getConfigBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetConfigString(t *testing.T) {
	tests := []struct {
		name     string
		getter   configGetter
		key      string
		fallback string
		want     string
	}{
		{name: "nil getter uses fallback", getter: nil, key: "kyc.method", fallback: "manual", want: "manual"},
		{name: "typed nil getter uses fallback", getter: (*configpkg.ConfigManager)(nil), key: "kyc.method", fallback: "manual", want: "manual"},
		{name: "getter overrides fallback", getter: stubConfigGetter{values: map[string]interface{}{"kyc.method": "alipay"}}, key: "kyc.method", fallback: "manual", want: "alipay"},
		{name: "missing key uses fallback", getter: stubConfigGetter{values: map[string]interface{}{}}, key: "kyc.method", fallback: "manual", want: "manual"},
		{name: "wrong type uses fallback", getter: stubConfigGetter{values: map[string]interface{}{"kyc.method": true}}, key: "kyc.method", fallback: "manual", want: "manual"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getConfigString(tt.getter, tt.key, tt.fallback); got != tt.want {
				t.Fatalf("getConfigString() = %q, want %q", got, tt.want)
			}
		})
	}
}
