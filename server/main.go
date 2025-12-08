package main

import (
	"fmt"
	_ "net/http/pprof"
	"os"

	systemAPI "oneclickvirt/api/v1/system"
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

// @host 0.0.0.0:8888
// @BasePath /api/v1
// @schemes http https

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

func main() {
	// 确保从正确的工作目录运行
	ensureCorrectWorkingDirectory()

	// 设置系统初始化完成后的回调函数
	initialize.SetSystemInitCallback()

	// 初始化系统
	initialize.InitializeSystem()

	// 启动服务器
	runServer()
}

// ensureCorrectWorkingDirectory 确保从正确的工作目录启动
func ensureCorrectWorkingDirectory() {
	if _, err := os.Stat("config.yaml"); os.IsNotExist(err) {
		fmt.Println("[ERROR] 未找到 config.yaml 文件")
		fmt.Println("[HINT] 请确保从项目的 server 目录启动程序")
		os.Exit(1)
	}
	if err := os.MkdirAll("storage", 0755); err != nil {
		fmt.Printf("[ERROR] 无法创建 storage 目录: %v\n", err)
		fmt.Println("[HINT] 请检查当前目录的写入权限")
		os.Exit(1)
	}
	if wd, err := os.Getwd(); err == nil {
		fmt.Printf("[SYSTEM] 工作目录: %s\n", wd)
	}
}

func runServer() {
	// 启动性能监控
	systemAPI.StartPerformanceMonitoring()

	router := initialize.Routers()
	global.APP_LOG.Debug("路由初始化完成")
	address := fmt.Sprintf(":%d", global.APP_CONFIG.System.Addr)
	s := initialize.InitServer(address, router)
	fmt.Printf("[SUCCESS] 服务器启动成功，监听端口: %d\n", global.APP_CONFIG.System.Addr)
	fmt.Printf("[INFO] API文档路径: /swagger/index.html\n")
	fmt.Printf("[INFO] 性能监控(pprof)端点: /debug/pprof/\n")
	global.APP_LOG.Info("服务器启动成功", zap.Int("port", global.APP_CONFIG.System.Addr))
	if err := s.ListenAndServe(); err != nil {
		global.APP_LOG.Fatal("服务器启动失败", zap.Error(err))
	}
}
