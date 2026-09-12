package utils

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// PasswordStrengthConfig 密码强度配置
type PasswordStrengthConfig struct {
	MinLength        int  // 最小长度
	RequireUpperCase bool // 要求大写字母
	RequireLowerCase bool // 要求小写字母
	RequireDigit     bool // 要求数字
	RequireSpecial   bool // 要求特殊字符
	ForbidCommon     bool // 禁止常见弱密码
	ForbidPersonal   bool // 禁止包含个人信息（用户名等）
}

// DefaultPasswordPolicy 默认密码策略（适用于注册和邀请码注册）
var DefaultPasswordPolicy = PasswordStrengthConfig{
	MinLength:        8,    // 最小8位
	RequireUpperCase: true, // 要求大写字母
	RequireLowerCase: true, // 要求小写字母
	RequireDigit:     true, // 要求数字
	RequireSpecial:   true, // 要求特殊字符
	ForbidCommon:     true, // 禁止常见弱密码
	ForbidPersonal:   true, // 禁止包含个人信息
}

// WeakPasswords 常见弱密码列表
var WeakPasswords = []string{
	"123456", "password", "123456789", "12345678", "12345", "1234567",
	"123123", "password123", "admin", "admin123", "root", "qwerty",
	"abc123", "Password1", "password1", "123qwe", "qwe123", "1qaz2wsx",
	"welcome", "monkey", "dragon", "111111", "654321", "superman",
	"123321", "master", "football", "baseball", "basketball", "jordan",
	"harley", "ranger", "shadow", "mustang", "princess", "sunshine",
	"iloveyou", "lovely", "7777777", "888888", "computer", "charlie",
	"1234567890", "qwertyuiop", "asdfghjkl", "zxcvbnm", "abcdefgh",
}

// ValidatePasswordStrength 验证密码强度
func ValidatePasswordStrength(password string, config PasswordStrengthConfig, username ...string) error {
	// 检查最小长度
	if len(password) < config.MinLength {
		return fmt.Errorf("密码长度不能少于%d位", config.MinLength)
	}

	// 检查字符类型要求
	var hasUpper, hasLower, hasDigit, hasSpecial bool

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsDigit(char):
			hasDigit = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	if config.RequireUpperCase && !hasUpper {
		return fmt.Errorf("密码必须包含至少一个大写字母")
	}

	if config.RequireLowerCase && !hasLower {
		return fmt.Errorf("密码必须包含至少一个小写字母")
	}

	if config.RequireDigit && !hasDigit {
		return fmt.Errorf("密码必须包含至少一个数字")
	}

	if config.RequireSpecial && !hasSpecial {
		return fmt.Errorf("密码必须包含至少一个特殊字符")
	}

	// 检查是否包含常见弱密码
	if config.ForbidCommon {
		passwordLower := regexp.MustCompile(`\s+`).ReplaceAllString(strings.ToLower(password), "")
		for _, weak := range WeakPasswords {
			weakLower := strings.ToLower(weak)
			// 检查是否为完全匹配
			if passwordLower == weakLower {
				return fmt.Errorf("密码不能包含常见弱密码模式")
			}
			// 检查是否包含完整的弱密码作为子串（用边界匹配）
			if len(weak) >= 6 { // 只对较长的弱密码进行子串检查
				pattern := `\b` + regexp.QuoteMeta(weakLower) + `\b`
				if matched, _ := regexp.MatchString(`(?i)`+pattern, passwordLower); matched {
					return fmt.Errorf("密码不能包含常见弱密码模式")
				}
			}
		}
	}

	// 检查是否包含个人信息
	if config.ForbidPersonal && len(username) > 0 {
		for _, name := range username {
			if name != "" && len(name) >= 3 {
				if matched, _ := regexp.MatchString(`(?i)`+regexp.QuoteMeta(name), password); matched {
					return fmt.Errorf("密码不能包含用户名或个人信息")
				}
			}
		}
	}

	// 检查重复字符
	if hasRepeatingPattern(password, 4) { // 改为4个重复字符才触发
		return fmt.Errorf("密码不能包含过多重复字符或连续字符")
	}

	return nil
}

// hasRepeatingPattern 检查是否有重复或连续的字符模式
func hasRepeatingPattern(password string, maxRepeat int) bool {
	// 检查连续重复字符
	count := 1
	for i := 1; i < len(password); i++ {
		if password[i] == password[i-1] {
			count++
			if count >= maxRepeat {
				return true
			}
		} else {
			count = 1
		}
	}

	// 检查连续递增或递减序列
	if len(password) >= maxRepeat {
		for i := 0; i <= len(password)-maxRepeat; i++ {
			isIncreasing := true
			isDecreasing := true

			for j := 1; j < maxRepeat; j++ {
				if password[i+j] != password[i+j-1]+1 {
					isIncreasing = false
				}
				if password[i+j] != password[i+j-1]-1 {
					isDecreasing = false
				}
			}

			if isIncreasing || isDecreasing {
				return true
			}
		}
	}

	return false
}

// GenerateStrongPassword 生成符合策略的强密码（仅包含数字和大小写英文字母）
// Kept for instance/admin paths whose contract deliberately uses alphanumerics.
func GenerateStrongPassword(length int) string {
	if length < 8 {
		length = 8
	}

	const (
		uppercase = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		lowercase = "abcdefghijklmnopqrstuvwxyz"
		digits    = "0123456789"
	)

	all := uppercase + lowercase + digits
	password := make([]byte, length)

	// 确保每种字符类型至少有一个
	password[0] = uppercase[secureRandInt(len(uppercase))]
	password[1] = lowercase[secureRandInt(len(lowercase))]
	password[2] = digits[secureRandInt(len(digits))]

	// 填充其余位置
	for i := 3; i < length; i++ {
		password[i] = all[secureRandInt(len(all))]
	}

	// 打乱顺序
	for i := len(password) - 1; i > 0; i-- {
		j := secureRandInt(i + 1)
		password[i], password[j] = password[j], password[i]
	}

	return string(password)
}

// GenerateAccountPassword honors the login password policy, including special
// characters. It must not silently fall back to predictable entropy.
func GenerateAccountPassword(length int, username string) (string, error) {
	if length < DefaultPasswordPolicy.MinLength {
		length = DefaultPasswordPolicy.MinLength
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#%+-_=.?"
	for attempt := 0; attempt < 128; attempt++ {
		password := make([]byte, length)
		for i := range password {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if err != nil {
				return "", fmt.Errorf("生成安全密码失败: %w", err)
			}
			password[i] = alphabet[n.Int64()]
		}
		if ValidatePasswordStrength(string(password), DefaultPasswordPolicy, username) == nil {
			return string(password), nil
		}
	}
	return "", fmt.Errorf("无法生成符合当前策略的密码")
}

// GenerateInstancePassword 为容器/虚拟机生成随机密码（小写英文开头，后面随机小写英文和数字混合，长度不低于8位）
func GenerateInstancePassword() string {
	const (
		lowercase = "abcdefghijklmnopqrstuvwxyz"
		digits    = "0123456789"
	)

	all := lowercase + digits
	length := 12 // 默认12位长度
	password := make([]byte, length)

	// 确保首字符是小写英文字母
	password[0] = lowercase[secureRandInt(len(lowercase))]

	// 确保至少包含一个数字（从第二个位置开始）
	password[1] = digits[secureRandInt(len(digits))]

	// 填充其余位置（从第三个位置开始）
	for i := 2; i < length; i++ {
		password[i] = all[secureRandInt(len(all))]
	}

	// 打乱第二位开始的字符（保持首字符为小写英文）
	for i := len(password) - 1; i > 1; i-- {
		j := secureRandInt(i-1) + 1 // 确保j不会是0，保护首字符
		password[i], password[j] = password[j], password[i]
	}

	return string(password)
}

// secureRandInt 安全随机数生成
func secureRandInt(max int) int {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		// 如果加密随机数生成失败，使用时间戳作为种子的伪随机数作为后备
		return int(time.Now().UnixNano()) % max
	}
	return int(n.Int64())
}
