package auth

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

func TestGetCaptchaEnabled(t *testing.T) {
	tests := []struct {
		name     string
		getter   configGetter
		fallback bool
		want     bool
	}{
		{name: "nil getter uses fallback", getter: nil, fallback: false, want: false},
		{name: "typed nil getter uses fallback", getter: (*configpkg.ConfigManager)(nil), fallback: true, want: true},
		{name: "getter overrides fallback", getter: stubConfigGetter{values: map[string]interface{}{"captcha.enabled": true}}, fallback: false, want: true},
		{name: "missing key uses fallback", getter: stubConfigGetter{values: map[string]interface{}{}}, fallback: true, want: true},
		{name: "wrong type uses fallback", getter: stubConfigGetter{values: map[string]interface{}{"captcha.enabled": "true"}}, fallback: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getCaptchaEnabled(tt.getter, tt.fallback); got != tt.want {
				t.Fatalf("getCaptchaEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
