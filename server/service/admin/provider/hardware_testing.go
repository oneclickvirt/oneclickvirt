package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	providerPkg "oneclickvirt/provider"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

// goecsBinaryPaths maps arch to the locally-built goecs binary path
// (built from ECS public branch source during Docker image build)
var goecsBinaryPaths = map[string]string{
	"amd64": "/app/goecs_amd64",
	"arm64": "/app/goecs_arm64",
}

// RunHardwareTest 在Provider节点上运行ECS硬件测试
func (s *Service) RunHardwareTest(ctx context.Context, providerID, userID uint) error {
	var existing providerModel.HardwareTestReport
	if err := global.APP_DB.Where("provider_id = ? AND status = ?", providerID, "running").First(&existing).Error; err == nil {
		return fmt.Errorf("该节点已有运行中的硬件测试 (PID %d)", existing.RemotePID)
	}

	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}

	var report providerModel.HardwareTestReport
	result := global.APP_DB.Where("provider_id = ?", providerID).First(&report)
	if result.Error != nil {
		report = providerModel.HardwareTestReport{
			ProviderID: providerID,
			Status:     "running",
			TestedBy:   userID,
		}
		global.APP_DB.Create(&report)
	} else {
		global.APP_DB.Model(&report).Updates(map[string]interface{}{
			"status":     "running",
			"tested_by":  userID,
			"error_msg":  "",
			"remote_pid": 0,
		})
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("硬件测试panic", zap.Any("recover", r))
				global.APP_DB.Model(&report).Updates(map[string]interface{}{
					"status":    "failed",
					"error_msg": fmt.Sprintf("测试异常: %v", r),
				})
			}
		}()
		s.executeHardwareTest(context.Background(), providerID, &report)
	}()

	return nil
}

// executeHardwareTest 执行硬件测试
// 优先使用Docker构建阶段从ECS public分支编译的goecs二进制。
// 若本地二进制不存在则回退到CDN下载。
func (s *Service) executeHardwareTest(ctx context.Context, providerID uint, report *providerModel.HardwareTestReport) {
	p, err := providerService.GetProviderInstanceByID(providerID)
	if err != nil {
		s.failReport(report, fmt.Sprintf("获取Provider实例失败: %v", err))
		return
	}

	// Step 1: 获取CPU架构
	archOutput, err := p.ExecuteSSHCommand(ctx, "uname -m")
	if err != nil {
		s.failReport(report, fmt.Sprintf("获取架构信息失败: %v", err))
		return
	}
	arch := strings.TrimSpace(archOutput)
	var ecsArch string
	switch {
	case strings.Contains(arch, "x86_64") || strings.Contains(arch, "amd64"):
		ecsArch = "amd64"
	case strings.Contains(arch, "aarch64") || strings.Contains(arch, "arm64"):
		ecsArch = "arm64"
	default:
		s.failReport(report, fmt.Sprintf("不支持的架构: %s", arch))
		return
	}

	// Step 2: 尝试上传本地构建的goecs二进制
	deployed := false
	localPath := goecsBinaryPaths[ecsArch]
	if localPath != "" {
		if _, statErr := os.Stat(localPath); statErr == nil {
			if uploadErr := s.uploadBinaryViaSSH(ctx, p, localPath, "/tmp/ecs_test/goecs"); uploadErr == nil {
				deployed = true
				global.APP_LOG.Info("使用本地构建的goecs二进制",
					zap.Uint("providerId", providerID),
					zap.String("arch", ecsArch))
			} else {
				global.APP_LOG.Warn("上传本地goecs二进制失败，回退到CDN下载",
					zap.Uint("providerId", providerID),
					zap.Error(uploadErr))
			}
		}
	}

	// Step 3: 回退到CDN下载
	if !deployed {
		script := buildFallbackDownloadScript(ecsArch)
		encoded := base64.StdEncoding.EncodeToString([]byte(script))
		uploadCmd := fmt.Sprintf(
			"mkdir -p /tmp/ecs_test && printf '%%s' '%s' | base64 -d > /tmp/ecs_test/download.sh && chmod +x /tmp/ecs_test/download.sh",
			encoded,
		)
		if _, err = p.ExecuteSSHCommand(ctx, uploadCmd); err != nil {
			s.failReport(report, fmt.Sprintf("上传下载脚本失败: %v", err))
			return
		}
		dlOutput, dlErr := p.ExecuteSSHCommand(ctx, "cd /tmp/ecs_test && bash download.sh 2>&1")
		if dlErr != nil || strings.Contains(dlOutput, "DOWNLOAD_FAILED") {
			s.failReport(report, fmt.Sprintf("CDN下载goecs失败: %v\n%s", dlErr, dlOutput))
			return
		}
		checkOutput, _ := p.ExecuteSSHCommand(ctx, "test -x /tmp/ecs_test/goecs && echo ok || echo missing")
		if strings.TrimSpace(checkOutput) != "ok" {
			s.failReport(report, fmt.Sprintf("goecs二进制下载后不存在或不可执行\n%s", dlOutput))
			return
		}
	}

	// Step 4: 直接前台执行goecs（避免nohup+PID轮询的不可靠性）
	global.APP_DB.Model(report).Update("remote_pid", -1)
	global.APP_LOG.Info("开始执行goecs测试", zap.Uint("providerId", providerID))

	// 用timeout限制25分钟，-m 1 为全测模式，-l en 英文输出
	execCmd := "cd /tmp/ecs_test && " +
		"(command -v timeout >/dev/null 2>&1 && timeout 1500 ./goecs -m 1 -l en 2>&1 || ./goecs -m 1 -l en 2>&1)"
	output, execErr := p.ExecuteSSHCommand(ctx, execCmd)

	// 如果stdout为空，尝试读取goecs.txt
	if strings.TrimSpace(output) == "" {
		txtOutput, _ := p.ExecuteSSHCommand(ctx, "cat /tmp/ecs_test/goecs.txt 2>/dev/null")
		if strings.TrimSpace(txtOutput) != "" {
			output = txtOutput
		}
	}

	// 清理
	_, _ = p.ExecuteSSHCommand(ctx, "rm -rf /tmp/ecs_test")

	if strings.TrimSpace(output) == "" {
		errMsg := "ECS测试未产生输出"
		if execErr != nil {
			errMsg = fmt.Sprintf("ECS测试执行失败: %v", execErr)
		}
		diagCmd := "echo '=== Memory ===' && free -m 2>/dev/null | head -3 && " +
			"echo '=== Disk ===' && df -h /tmp 2>/dev/null | tail -1"
		diagOutput, _ := p.ExecuteSSHCommand(ctx, diagCmd)
		if diagOutput != "" {
			errMsg = fmt.Sprintf("%s\n诊断信息:\n%s", errMsg, strings.TrimSpace(diagOutput))
		}
		s.failReport(report, errMsg)
	} else {
		now := time.Now()
		global.APP_DB.Model(report).Updates(map[string]interface{}{
			"status":      "completed",
			"report_text": output,
			"tested_at":   &now,
			"error_msg":   "",
			"remote_pid":  0,
		})
		global.APP_LOG.Info("硬件测试完成",
			zap.Uint("providerId", report.ProviderID),
			zap.Int("reportLength", len(output)))
	}
}

// uploadBinaryViaSSH 通过base64分块传输上传二进制文件
func (s *Service) uploadBinaryViaSSH(ctx context.Context, p providerPkg.Provider, localPath, remotePath string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}

	dir := remotePath[:strings.LastIndex(remotePath, "/")]
	if _, err := p.ExecuteSSHCommand(ctx, fmt.Sprintf("mkdir -p %s", dir)); err != nil {
		return fmt.Errorf("create remote dir: %w", err)
	}
	if _, err := p.ExecuteSSHCommand(ctx, fmt.Sprintf("true > %s", remotePath)); err != nil {
		return fmt.Errorf("clear target file: %w", err)
	}

	// 分块传输（每块512KB）
	chunkSize := 512 * 1024
	for offset := 0; offset < len(data); {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		encoded := base64.StdEncoding.EncodeToString(data[offset:end])
		cmd := fmt.Sprintf("printf '%%s' '%s' | base64 -d >> %s", encoded, remotePath)
		if _, err := p.ExecuteSSHCommand(ctx, cmd); err != nil {
			return fmt.Errorf("transfer chunk(%d-%d): %w", offset, end, err)
		}
		offset = end
	}

	if _, err := p.ExecuteSSHCommand(ctx, fmt.Sprintf("chmod +x %s", remotePath)); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	sizeOutput, err := p.ExecuteSSHCommand(ctx, fmt.Sprintf("wc -c < %s", remotePath))
	if err != nil {
		return fmt.Errorf("verify size: %w", err)
	}
	remoteSize, _ := strconv.Atoi(strings.TrimSpace(sizeOutput))
	if remoteSize != len(data) {
		return fmt.Errorf("size mismatch: local=%d remote=%d", len(data), remoteSize)
	}
	return nil
}

// buildFallbackDownloadScript CDN回退下载脚本
func buildFallbackDownloadScript(arch string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
WORKDIR="/tmp/ecs_test"
mkdir -p "$WORKDIR" && cd "$WORKDIR"
check_cmd() { command -v "$1" >/dev/null 2>&1; }
download() {
    if check_cmd curl; then curl -sSL --connect-timeout 30 --max-time 180 -o "$2" "$1"
    else wget -q --timeout=30 -O "$2" "$1"; fi
}
FN="goecs_linux_%s"
for url in \
  "https://github.com/oneclickvirt/ecs/releases/latest/download/${FN}.zip" \
  "https://cdn.spiritlhl.net/https://github.com/oneclickvirt/ecs/releases/latest/download/${FN}.zip" \
  "https://ghproxy.com/https://github.com/oneclickvirt/ecs/releases/latest/download/${FN}.zip"; do
    if download "$url" goecs.zip 2>/dev/null && [ -f goecs.zip ] && [ "$(wc -c < goecs.zip)" -gt 1000 ]; then
        if check_cmd unzip; then unzip -o goecs.zip 2>/dev/null
        elif check_cmd busybox; then busybox unzip -o goecs.zip 2>/dev/null
        else python3 -c "import zipfile;zipfile.ZipFile('goecs.zip').extractall('.')" 2>/dev/null; fi
        F=$(find . -maxdepth 1 -name "goecs*" -type f ! -name "*.zip" ! -name "*.sh" | head -1)
        if [ -n "$F" ]; then mv "$F" goecs; chmod +x goecs; exit 0; fi
    fi
    rm -f goecs.zip
done
echo "DOWNLOAD_FAILED"; exit 1
`, arch)
}

func (s *Service) failReport(report *providerModel.HardwareTestReport, msg string) {
	global.APP_LOG.Error("硬件测试失败",
		zap.Uint("providerId", report.ProviderID),
		zap.String("error", msg))
	global.APP_DB.Model(report).Updates(map[string]interface{}{
		"status":     "failed",
		"error_msg":  msg,
		"remote_pid": 0,
	})
}

// GetHardwareTestReport 获取硬件测试报告
func (s *Service) GetHardwareTestReport(ctx context.Context, providerID uint) (*providerModel.HardwareTestReport, error) {
	var report providerModel.HardwareTestReport
	if err := global.APP_DB.Where("provider_id = ?", providerID).First(&report).Error; err != nil {
		return nil, err
	}
	return &report, nil
}