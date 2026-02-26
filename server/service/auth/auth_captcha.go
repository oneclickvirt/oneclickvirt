package auth

import (
	"errors"

	"oneclickvirt/global"
	"oneclickvirt/model/auth"

	"github.com/mojocn/base64Captcha"
)

// GenerateCaptcha 生成图形验证码
func (s *AuthService) GenerateCaptcha(width, height int) (*auth.CaptchaResponse, error) {
	captchaLen := global.GetAppConfig().Captcha.Length
	if captchaLen <= 0 {
		captchaLen = 4
	}
	// 确保宽度和高度是有效的正整数
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 40
	}
	// 设置验证码配置难度
	driver := base64Captcha.NewDriverDigit(height, width, captchaLen, 0.4, 40)
	// 使用全局LRU缓存存储
	c := base64Captcha.NewCaptcha(driver, global.APP_CAPTCHA_STORE)
	id, b64s, _, err := c.Generate()
	if err != nil {
		return nil, err
	}
	// 返回验证码信息
	return &auth.CaptchaResponse{
		CaptchaId: id,
		ImageData: b64s,
	}, nil
}

func (s *AuthService) verifyCaptcha(captchaId, code string) error {
	if captchaId == "" || code == "" {
		return errors.New("验证码参数不完整")
	}

	// 开发环境下允许测试验证码
	if global.GetAppConfig().System.Env == "development" && code == "test" {
		return nil
	}

	// 使用全局LRU缓存验证
	match := global.APP_CAPTCHA_STORE.Verify(captchaId, code, true)
	if !match {
		return errors.New("验证码错误或已过期")
	}
	return nil
}
