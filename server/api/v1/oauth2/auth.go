package oauth2

import (
	"fmt"
	"net/http"
	"net/url"
	"oneclickvirt/global"
	"oneclickvirt/model/common"
	oauth2Model "oneclickvirt/model/oauth2"
	oauth2Svc "oneclickvirt/service/oauth2"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var oauthService = oauth2Svc.NewService()

// OAuth2Login OAuth2登录跳转
// @Summary OAuth2登录
// @Description 跳转到OAuth2提供商的授权页面
// @Tags 认证
// @Accept json
// @Produce json
// @Param provider query string false "提供商名称（如linuxdo）"
// @Param provider_id query int false "提供商ID"
// @Success 302 {string} string "重定向到OAuth2授权页面"
// @Router /auth/oauth2/login [get]
func OAuth2Login(c *gin.Context) {
	// 检查是否启用OAuth2
	if !global.APP_CONFIG.Auth.EnableOAuth2 {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "OAuth2登录未启用"))
		return
	}

	// 获取提供商
	providerName := c.Query("provider")
	providerIDStr := c.Query("provider_id")

	var provider *oauth2Model.OAuth2Provider
	var err error

	if providerIDStr != "" {
		providerID, _ := strconv.ParseUint(providerIDStr, 10, 32)
		provider, err = oauthService.GetProviderByID(uint(providerID))
	} else if providerName != "" {
		provider, err = oauthService.GetProviderByName(providerName)
	} else {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请指定provider或provider_id参数"))
		return
	}

	if err != nil {
		global.APP_LOG.Error("获取OAuth2提供商失败", zap.Error(err))
		if customErr, ok := err.(*common.AppError); ok {
			common.ResponseWithError(c, customErr)
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取提供商失败"))
		}
		return
	}

	// 生成state令牌
	state, err := oauthService.GenerateStateToken(provider.ID)
	if err != nil {
		global.APP_LOG.Error("生成state令牌失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "生成令牌失败"))
		return
	}

	// 生成OAuth2授权URL
	oauth2Cfg := oauthService.GetOAuth2Config(provider)
	authURL := oauth2Cfg.AuthCodeURL(state)

	global.APP_LOG.Info("OAuth2登录跳转",
		zap.String("provider", provider.Name),
		zap.String("state", state))

	c.Redirect(302, authURL)
}

// OAuth2Callback OAuth2回调处理
// @Summary OAuth2回调
// @Description 处理OAuth2提供商的回调
// @Tags 认证
// @Accept json
// @Produce json
// @Param code query string true "授权码"
// @Param state query string true "状态令牌"
// @Success 200 {object} common.Response{data=map[string]interface{}}
// @Router /auth/oauth2/callback [get]
func OAuth2Callback(c *gin.Context) {
	// 检查是否启用OAuth2
	if !global.APP_CONFIG.Auth.EnableOAuth2 {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "OAuth2登录未启用"))
		return
	}

	code := c.Query("code")
	state := c.Query("state")

	if code == "" || state == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "缺少必要参数"))
		return
	}

	// 验证state令牌
	providerID, valid := oauthService.ValidateStateToken(state)
	if !valid {
		global.APP_LOG.Error("无效的state令牌", zap.String("state", state))
		common.ResponseWithError(c, common.NewError(common.CodeOAuth2Failed, "无效的state令牌"))
		return
	}

	// 处理回调
	usr, token, err := oauthService.HandleCallback(providerID, code)
	if err != nil {
		global.APP_LOG.Error("OAuth2回调处理失败",
			zap.Uint("provider_id", providerID),
			zap.Error(err))

		// 如果是自定义错误，直接返回
		if customErr, ok := err.(*common.AppError); ok {
			common.ResponseWithError(c, customErr)
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeOAuth2Failed, "认证失败"))
		}
		return
	}

	global.APP_LOG.Info("OAuth2登录成功",
		zap.Uint("provider_id", providerID),
		zap.String("username", usr.Username),
		zap.Uint("user_id", usr.ID))

	// 获取前端URL配置，如果没有配置，尝试智能检测
	frontendURL := global.APP_CONFIG.System.FrontendURL

	// 返回HTML页面，通过JavaScript跳转并携带token参数
	// localStorage在不同端口/域名下是隔离的，所以必须通过URL参数传递
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>登录处理中...</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        body {
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
            background-color: #f5f7fa;
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
        }
        .container {
            width: 400px;
            padding: 40px;
            background-color: #fff;
            border-radius: 8px;
            box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
            text-align: center;
        }
        .spinner {
            border: 3px solid #f3f3f3;
            border-top: 3px solid #409eff;
            border-radius: 50%%;
            width: 50px;
            height: 50px;
            animation: spin 1s linear infinite;
            margin: 0 auto 20px;
        }
        @keyframes spin {
            0%% { transform: rotate(0deg); }
            100%% { transform: rotate(360deg); }
        }
        h2 {
            font-size: 24px;
            color: #303133;
            margin-bottom: 10px;
        }
        p {
            font-size: 14px;
            color: #909399;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="spinner"></div>
        <h2>登录成功</h2>
        <p>正在跳转到应用...</p>
    </div>
    <script>
        (function() {
            try {
                var token = '%s';
                var username = '%s';
                var configuredFrontendURL = '%s';
                
                // 使用配置的前端URL
                var frontendURL = configuredFrontendURL;
                
                // 如果配置为空，使用相对路径（同域名下的前端）
                if (!frontendURL) {
                    frontendURL = window.location.origin + '/';
                }
                
                // 确保URL以/结尾
                if (!frontendURL.endsWith('/')) {
                    frontendURL += '/';
                }
                
                // 将token作为URL参数传递（解决跨域localStorage隔离问题）
                var redirectURL = frontendURL + '?oauth2_token=' + encodeURIComponent(token) + 
                                  '&username=' + encodeURIComponent(username);
                
                console.log('OAuth2跳转到:', redirectURL);
                
                // 延迟跳转，让用户看到成功提示
                setTimeout(function() {
                    window.location.href = redirectURL;
                }, 500);
            } catch (err) {
                console.error('OAuth2回调处理失败:', err);
                alert('登录处理失败: ' + err.message);
            }
        })();
    </script>
</body>
</html>`, token, url.QueryEscape(usr.Username), frontendURL)

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, html)
}
