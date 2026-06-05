package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	_ "time/tzdata" // 嵌入时区数据，确保 Alpine/无 tzdata 环境（如 Docker）中 Asia/Shanghai 可用

	systemAPI "oneclickvirt/api/v1/system"
	"oneclickvirt/global"
	"oneclickvirt/initialize"
	"oneclickvirt/mcp"

	_ "oneclickvirt/docs"
	_ "oneclickvirt/provider/containerd"
	_ "oneclickvirt/provider/docker"
	_ "oneclickvirt/provider/incus"
	_ "oneclickvirt/provider/kubevirt"
	_ "oneclickvirt/provider/lxd"
	_ "oneclickvirt/provider/multipass"
	_ "oneclickvirt/provider/podman"
	_ "oneclickvirt/provider/proxmox"
	_ "oneclickvirt/provider/qemu"
	_ "oneclickvirt/provider/vagrant"
	_ "oneclickvirt/provider/virtualbox"
	_ "oneclickvirt/provider/vmware"

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
// @schemes http

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

func main() {
	// Check for MCP subcommand before full initialization
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		// MCP mode: lightweight init (no DB, no full server)
		runMCP(os.Args[2:])
		return
	}

	// 确保从正确的工作目录运行
	ensureCorrectWorkingDirectory()

	// 设置系统初始化完成后的回调函数
	initialize.SetSystemInitCallback()

	// 初始化系统
	initialize.InitializeSystem()

	// 启动服务器
	runServer()
}

// runMCP 启动 MCP stdio 服务器（轻量模式，不需要数据库）
func runMCP(args []string) {
	// Parse token and API URL from args
	apiURL := "http://localhost:8888"
	apiToken := ""
	if envURL := os.Getenv("ONE_CLICK_VIRT_API_URL"); envURL != "" {
		apiURL = envURL
	}
	if envToken := os.Getenv("ONE_CLICK_VIRT_API_TOKEN"); envToken != "" {
		apiToken = envToken
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--api-url":
			if i+1 < len(args) {
				apiURL = args[i+1]
				i++
			}
		case "--token":
			if i+1 < len(args) {
				apiToken = args[i+1]
				i++
			}
		case "--help", "-h":
			fmt.Println("OneClickVirt MCP Server")
			fmt.Println("")
			fmt.Println("Usage: oneclickvirt mcp [--api-url URL] [--token TOKEN]")
			fmt.Println("")
			fmt.Println("Starts a Model Context Protocol (MCP) server via stdin/stdout.")
			fmt.Println("Environment: ONE_CLICK_VIRT_API_URL, ONE_CLICK_VIRT_API_TOKEN")
			fmt.Println("Configure your AI assistant to use this for managing OneClickVirt.")
			return
		}
	}

	fmt.Fprintf(os.Stderr, "[MCP] Starting OneClickVirt MCP server\n")
	fmt.Fprintf(os.Stderr, "[MCP] API URL: %s\n", apiURL)

	server := mcp.NewMCPServer(apiURL, apiToken)
	if err := server.RunStdio(); err != nil {
		fmt.Fprintf(os.Stderr, "[MCP] Error: %v\n", err)
		os.Exit(1)
	}
}

// ensureCorrectWorkingDirectory 确认当前工作目录合法。
// 注意：该函数在日志系统初始化之前运行，必须使用 fmt 标准输出进行错误提示。
func ensureCorrectWorkingDirectory() {
	if _, err := os.Stat("config.yaml"); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "[FATAL] 未找到 config.yaml 文件，请確保从 server 目录启动程序")
		os.Exit(1)
	}
	if err := os.MkdirAll("storage", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[FATAL] 无法创建 storage 目录: %v，请检查当前目录的写入权限\n", err)
		os.Exit(1)
	}
	if wd, err := os.Getwd(); err == nil {
		fmt.Printf("[STARTUP] 工作目录: %s\n", wd)
	}
}

// runServer 启动 HTTP 服务器。
// 在调用该函数前，日志系统已经初始化完毕，可安全使用 global.APP_LOG。
func runServer() {
	// 启动 pprof 性能监控（在调试/预生产环境可用）
	systemAPI.StartPerformanceMonitoring()

	router := initialize.Routers()
	global.APP_LOG.Debug("路由初始化完成")

	addr := global.GetAppConfig().System.Addr
	address := fmt.Sprintf(":%d", addr)
	s := initialize.InitServer(address, router)

	// 使用结构化日志输出关键启动信息，不再与 fmt.Printf 重复输出
	global.APP_LOG.Info("服务器启动成功",
		zap.Int("port", addr),
		zap.String("swagger", fmt.Sprintf("http://0.0.0.0:%d/swagger/index.html", addr)),
	)

	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		global.APP_LOG.Fatal("服务器异常退出", zap.Error(err))
	}
}
