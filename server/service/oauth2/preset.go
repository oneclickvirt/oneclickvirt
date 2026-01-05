package oauth2

// PresetConfig 预设OAuth2配置
type PresetConfig struct {
	Name            string
	DisplayName     string
	ProviderType    string
	AuthURL         string
	TokenURL        string
	UserInfoURL     string
	UserIDField     string
	UsernameField   string
	EmailField      string
	AvatarField     string
	NicknameField   string
	TrustLevelField string
	Scopes          []string
	DefaultLevel    int
	LevelMapping    map[string]int
}

// GetPresetConfigs 获取所有预设配置
func GetPresetConfigs() map[string]PresetConfig {
	return map[string]PresetConfig{
		"linuxdo": {
			Name:            "linuxdo",
			DisplayName:     "Linux.do",
			ProviderType:    "preset",
			AuthURL:         "https://connect.linux.do/oauth2/authorize",
			TokenURL:        "https://connect.linux.do/oauth2/token",
			UserInfoURL:     "https://connect.linux.do/api/user",
			UserIDField:     "id",
			UsernameField:   "username",
			EmailField:      "email",
			AvatarField:     "avatar_url",
			NicknameField:   "name",
			TrustLevelField: "trust_level",
			Scopes:          []string{"read"},
			DefaultLevel:    1,
			LevelMapping: map[string]int{
				"0": 1,
				"1": 1,
				"2": 1,
				"3": 1,
				"4": 1,
				"5": 1,
				"6": 1,
			},
		},
		"idcflare": {
			Name:            "idcflare",
			DisplayName:     "IDCFlare",
			ProviderType:    "preset",
			AuthURL:         "https://connect.idcflare.com/oauth2/authorize",
			TokenURL:        "https://connect.idcflare.com/oauth2/token",
			UserInfoURL:     "https://connect.idcflare.com/api/user",
			UserIDField:     "id",
			UsernameField:   "username",
			EmailField:      "email",
			AvatarField:     "avatar_url",
			NicknameField:   "name",
			TrustLevelField: "trust_level",
			Scopes:          []string{"read"},
			DefaultLevel:    1,
			LevelMapping: map[string]int{
				"0": 1,
				"1": 1,
				"2": 1,
				"3": 1,
				"4": 1,
				"5": 1,
				"6": 1,
			},
		},
		"github": {
			Name:          "github",
			DisplayName:   "GitHub",
			ProviderType:  "preset",
			AuthURL:       "https://github.com/login/oauth/authorize",
			TokenURL:      "https://github.com/login/oauth/access_token",
			UserInfoURL:   "https://api.github.com/user",
			UserIDField:   "id",
			UsernameField: "login",
			EmailField:    "email",
			AvatarField:   "avatar_url",
			NicknameField: "name",
			Scopes:        []string{"user:email", "read:user"},
			DefaultLevel:  1,
			LevelMapping:  map[string]int{},
		},
		"generic": {
			Name:          "generic",
			DisplayName:   "通用OAuth2",
			ProviderType:  "generic",
			AuthURL:       "",
			TokenURL:      "",
			UserInfoURL:   "",
			UserIDField:   "id",
			UsernameField: "username",
			EmailField:    "email",
			AvatarField:   "avatar",
			NicknameField: "name",
			Scopes:        []string{"openid", "profile", "email"},
			DefaultLevel:  1,
			LevelMapping:  map[string]int{},
		},
	}
}

// GetPresetConfig 获取指定预设配置
func GetPresetConfig(name string) (PresetConfig, bool) {
	presets := GetPresetConfigs()
	config, exists := presets[name]
	return config, exists
}

// GetPresetNames 获取所有预设名称列表
func GetPresetNames() []string {
	return []string{"linuxdo", "idcflare", "github", "generic"}
}

// IsPresetProvider 判断是否为预设类型
func IsPresetProvider(providerType string) bool {
	return providerType == "preset"
}

// IsGenericProvider 判断是否为通用类型
func IsGenericProvider(providerType string) bool {
	return providerType == "generic"
}
