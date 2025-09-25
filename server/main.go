package main

import (
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/initialize"

	_ "oneclickvirt/docs"
	_ "oneclickvirt/provider/docker"
	_ "oneclickvirt/provider/incus"
	_ "oneclickvirt/provider/lxd"
	_ "oneclickvirt/provider/proxmox"

	"go.uber.org/zap"
)

// @title OneClickVirt API
// @version 1.0
// @description 一键虚拟化管理平台API接口文档
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @host localhost:8888
// @BasePath /api/v1
// @schemes http https

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

func main() {
	// 设置系统初始化完成后的回调函数
	initialize.SetSystemInitCallback()

	// 初始化系统
	initialize.InitializeSystem()

	// 启动服务器
	runServer()
}

func runServer() {
	router := initialize.Routers()
	global.APP_LOG.Debug("路由初始化完成")
	address := fmt.Sprintf(":%d", global.APP_CONFIG.System.Addr)
	s := initialize.InitServer(address, router)
	global.APP_LOG.Info("服务器启动成功", zap.String("address", address))
	global.APP_LOG.Info("API文档地址", zap.String("url", fmt.Sprintf("http://127.0.0.1%s/swagger/index.html", address)))
	if err := s.ListenAndServe(); err != nil {
		global.APP_LOG.Fatal("服务器启动失败", zap.Error(err))
	}
}
