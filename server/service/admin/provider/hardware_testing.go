package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

// RunHardwareTest 在Provider节点上运行ECS硬件测试
func (s *Service) RunHardwareTest(ctx context.Context, providerID, userID uint) error {
	// 检查是否已有运行中的测试
	var existing providerModel.HardwareTestReport
	if err := global.APP_DB.Where("provider_id = ? AND status = ?", providerID, "running").First(&existing).Error; err == nil {
		return fmt.Errorf("该节点已有运行中的硬件测试 (PID %d)", existing.RemotePID)
	}

	// 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 创建或更新测试记录
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

	// 异步执行测试
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

// buildECSScript 生成完整的ECS测试脚本内容
// 包含依赖检查、多镜像下载、错误处理等
func buildECSScript(arch string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
WORKDIR="/tmp/ecs_test"
mkdir -p "$WORKDIR"
cd "$WORKDIR"

# 依赖检查
check_cmd() {
    command -v "$1" >/dev/null 2>&1
}

if ! check_cmd curl && ! check_cmd wget; then
    echo "ERROR: curl or wget is required" >> "$WORKDIR/result.txt"
    exit 1
fi

# 下载函数，支持curl和wget
download() {
    local url="$1" output="$2"
    if check_cmd curl; then
        curl -sSL --connect-timeout 30 --max-time 120 -o "$output" "$url"
    else
        wget -q --timeout=30 -O "$output" "$url"
    fi
}

# 多镜像下载尝试
FILENAME="goecs_linux_%s"
URLS="
https://github.com/oneclickvirt/ecs/releases/latest/download/${FILENAME}.zip
https://cdn.spiritlhl.net/https://github.com/oneclickvirt/ecs/releases/latest/download/${FILENAME}.zip
https://ghproxy.com/https://github.com/oneclickvirt/ecs/releases/latest/download/${FILENAME}.zip
"

DOWNLOADED=0
for url in $URLS; do
    if download "$url" goecs.zip 2>/dev/null; then
        if [ -f goecs.zip ] && [ "$(wc -c < goecs.zip)" -gt 1000 ]; then
            DOWNLOADED=1
            break
        fi
    fi
    rm -f goecs.zip
done

if [ "$DOWNLOADED" -eq 0 ]; then
    # 尝试直接下载二进制（无zip）
    URLS_BIN="
https://github.com/oneclickvirt/ecs/releases/latest/download/${FILENAME}
https://cdn.spiritlhl.net/https://github.com/oneclickvirt/ecs/releases/latest/download/${FILENAME}
"
    for url in $URLS_BIN; do
        if download "$url" goecs 2>/dev/null; then
            if [ -f goecs ] && [ "$(wc -c < goecs)" -gt 1000 ]; then
                DOWNLOADED=2
                break
            fi
        fi
        rm -f goecs
    done
fi

if [ "$DOWNLOADED" -eq 0 ]; then
    echo "ERROR: Failed to download goecs binary from all mirrors" >> "$WORKDIR/result.txt"
    exit 1
fi

# 如果是zip格式则解压
if [ "$DOWNLOADED" -eq 1 ]; then
    if check_cmd unzip; then
        unzip -o goecs.zip 2>/dev/null || true
    elif check_cmd busybox; then
        busybox unzip -o goecs.zip 2>/dev/null || true
    else
        # 尝试用python解压
        python3 -c "import zipfile; zipfile.ZipFile('goecs.zip').extractall('.')" 2>/dev/null || \
        python -c "import zipfile; zipfile.ZipFile('goecs.zip').extractall('.')" 2>/dev/null || {
            echo "ERROR: No unzip tool available (tried unzip, busybox, python)" >> "$WORKDIR/result.txt"
            exit 1
        }
    fi
fi

# 检查二进制是否存在
if [ ! -f goecs ]; then
    # 可能解压后名称不同，查找可执行文件
    FOUND=$(find . -maxdepth 1 -name "goecs*" -type f ! -name "*.zip" | head -1)
    if [ -n "$FOUND" ]; then
        mv "$FOUND" goecs
    else
        echo "ERROR: goecs binary not found after extraction" >> "$WORKDIR/result.txt"
        ls -la "$WORKDIR/" >> "$WORKDIR/result.txt"
        exit 1
    fi
fi

chmod +x goecs

# 检查是否有timeout命令
if check_cmd timeout; then
    timeout 900 ./goecs -m 1
else
    # 没有timeout就直接运行，依赖外部20分钟超时
    ./goecs -m 1
fi
`, arch)
}

// executeHardwareTest 执行硬件测试
//
// 流程：
//  1. 单条短SSH命令获取架构
//  2. 将完整测试脚本通过 base64 编码写入远端，不依赖SFTP
//  3. nohup 后台启动脚本并立即获取PID，SSH连接随即断开
//  4. 定期以 kill -0 $PID 轮询进程是否存活（每次连接极短）
//  5. 进程退出后读取结果文件并清理
func (s *Service) executeHardwareTest(ctx context.Context, providerID uint, report *providerModel.HardwareTestReport) {
	p, err := providerService.GetProviderInstanceByID(providerID)
	if err != nil {
		s.failReport(report, fmt.Sprintf("获取Provider实例失败: %v", err))
		return
	}

	// Step 1: 获取CPU架构（短命令，不会超时）
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

	// Step 2: 将测试脚本通过 base64 编码写入远端（仍是短命令，脚本很小）
	script := buildECSScript(ecsArch)
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	uploadCmd := fmt.Sprintf(
		"mkdir -p /tmp/ecs_test && printf '%%s' '%s' | base64 -d > /tmp/ecs_test/run.sh && chmod +x /tmp/ecs_test/run.sh",
		encoded,
	)
	if _, err = p.ExecuteSSHCommand(ctx, uploadCmd); err != nil {
		s.failReport(report, fmt.Sprintf("上传测试脚本失败: %v", err))
		return
	}

	// Step 3: nohup 后台运行，立即获取PID，SSH连接不阻塞
	launchCmd := "nohup /tmp/ecs_test/run.sh > /tmp/ecs_test/result.txt 2>&1 & echo $!"
	pidOutput, err := p.ExecuteSSHCommand(ctx, launchCmd)
	if err != nil {
		s.failReport(report, fmt.Sprintf("启动测试进程失败: %v", err))
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(pidOutput))
	if pid == 0 {
		s.failReport(report, "未能获取测试进程PID")
		return
	}

	// 将PID保存到DB，供前端展示
	global.APP_DB.Model(report).Update("remote_pid", pid)
	global.APP_LOG.Info("ECS测试进程已在后台启动",
		zap.Uint("providerId", providerID),
		zap.Int("pid", pid))

	// Step 4: 轮询 kill -0 $PID 检查进程是否仍在运行
	// 每次只建立一条极短的SSH连接，不保持长连接
	pollCmd := fmt.Sprintf("kill -0 %d 2>/dev/null && echo running || echo done", pid)
	deadline := time.Now().Add(20 * time.Minute)
	pollInterval := 30 * time.Second

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		status, pollErr := p.ExecuteSSHCommand(ctx, pollCmd)
		if pollErr != nil {
			global.APP_LOG.Debug("轮询进程状态失败，将重试", zap.Error(pollErr))
			continue
		}
		if strings.TrimSpace(status) == "done" {
			break
		}
	}

	// Step 5: 读取结果文件并清理
	output, readErr := p.ExecuteSSHCommand(ctx, "cat /tmp/ecs_test/result.txt 2>/dev/null")
	if readErr != nil {
		// SSH命令本身失败 - 尝试重试一次
		time.Sleep(5 * time.Second)
		output, readErr = p.ExecuteSSHCommand(ctx, "cat /tmp/ecs_test/result.txt 2>/dev/null")
	}

	// 如果结果为空，尝试获取脚本退出码和目录内容作为诊断信息
	if strings.TrimSpace(output) == "" {
		diagCmd := "echo '=== Directory ===' && ls -la /tmp/ecs_test/ 2>/dev/null && echo '=== Disk ===' && df /tmp 2>/dev/null | tail -1"
		diagOutput, _ := p.ExecuteSSHCommand(ctx, diagCmd)
		_, _ = p.ExecuteSSHCommand(ctx, "rm -rf /tmp/ecs_test")

		errMsg := "ECS测试未产生输出或已超时（20分钟）"
		if readErr != nil {
			errMsg = fmt.Sprintf("读取结果文件失败: %v", readErr)
		}
		if diagOutput != "" {
			errMsg = fmt.Sprintf("%s\n诊断信息:\n%s", errMsg, strings.TrimSpace(diagOutput))
		}
		s.failReport(report, errMsg)
	} else {
		_, _ = p.ExecuteSSHCommand(ctx, "rm -rf /tmp/ecs_test")
		now := time.Now()
		global.APP_DB.Model(report).Updates(map[string]interface{}{
			"status":      "completed",
			"report_text": output,
			"tested_at":   &now,
			"error_msg":   "",
			"remote_pid":  0,
		})
	}

	global.APP_LOG.Info("硬件测试完成",
		zap.Uint("providerId", report.ProviderID),
		zap.Int("reportLength", len(output)))
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
