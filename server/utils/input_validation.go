package utils

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var sqlInjectionPattern = regexp.MustCompile(`(?i)(union\s+select|drop\s+table|delete\s+from|insert\s+into|update\s+set|exec\s*\(|\bor\s+1\s*=\s*1\b|--|/\*|\*/)`)

// ContainsSQLInjectionPattern reports whether the input contains common SQL injection markers.
func ContainsSQLInjectionPattern(input string) bool {
	return sqlInjectionPattern.MatchString(input)
}

// ValidateUsername validates user-facing usernames while preserving broad Unicode support.
func ValidateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("用户名不能为空")
	}

	if strings.TrimSpace(username) != username {
		return fmt.Errorf("用户名不能包含前后空白字符")
	}

	length := utf8.RuneCountInString(username)
	if length < 3 || length > 20 {
		return fmt.Errorf("用户名长度必须在3到20个字符之间")
	}

	for _, char := range username {
		if unicode.IsControl(char) {
			return fmt.Errorf("用户名包含非法控制字符")
		}
	}

	if ContainsHTMLTags(username) {
		return fmt.Errorf("用户名包含非法HTML内容")
	}

	if ContainsSQLInjectionPattern(username) {
		return fmt.Errorf("用户名包含危险模式")
	}

	return nil
}

// ValidateOptionalEmail validates an optional email while rejecting display-name wrappers.
func ValidateOptionalEmail(email string) error {
	if email == "" {
		return nil
	}

	if strings.TrimSpace(email) != email {
		return fmt.Errorf("邮箱格式无效")
	}

	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return fmt.Errorf("邮箱格式无效")
	}

	return nil
}
