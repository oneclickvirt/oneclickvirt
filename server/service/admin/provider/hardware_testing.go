package provider

import (
	"context"
	"fmt"
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
		return fmt.Errorf("该节点已有运行中的硬件测试")
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
			"status":    "running",
			"tested_by": userID,
			"error_msg": "",
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

// executeHardwareTest 执行硬件测试
func (s *Service) executeHardwareTest(ctx context.Context, providerID uint, report *providerModel.HardwareTestReport) {
	// 获取已连接的Provider实例
	p, err := providerService.GetProviderInstanceByID(providerID)
	if err != nil {
		s.failReport(report, fmt.Sprintf("获取Provider实例失败: %v", err))
		return
	}

	// 先检查架构
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

	// 下载并执行ECS (使用GitHub release)
	downloadCmd := fmt.Sprintf(
		"mkdir -p /tmp/ecs_test && cd /tmp/ecs_test && "+
			"curl -sL \"https://github.com/oneclickvirt/ecs/releases/latest/download/goecs_linux_%s.zip\" -o goecs.zip && "+
			"unzip -o goecs.zip && chmod +x goecs && "+
			"echo \"1\" | timeout 1800 ./goecs 2>&1; "+
			"rm -rf /tmp/ecs_test",
		ecsArch)

	output, err := p.ExecuteSSHCommand(ctx, downloadCmd)
	if err != nil {
		// 即使命令返回非零退出码，输出可能仍然有价值
		if output != "" {
			now := time.Now()
			global.APP_DB.Model(report).Updates(map[string]interface{}{
				"status":      "completed",
				"report_text": output,
				"tested_at":   &now,
				"error_msg":   fmt.Sprintf("测试完成但有警告: %v", err),
			})
			return
		}
		s.failReport(report, fmt.Sprintf("执行ECS测试失败: %v", err))
		return
	}

	now := time.Now()
	global.APP_DB.Model(report).Updates(map[string]interface{}{
		"status":      "completed",
		"report_text": output,
		"tested_at":   &now,
		"error_msg":   "",
	})

	global.APP_LOG.Info("硬件测试完成",
		zap.Uint("providerId", report.ProviderID),
		zap.Int("reportLength", len(output)))
}

func (s *Service) failReport(report *providerModel.HardwareTestReport, msg string) {
	global.APP_LOG.Error("硬件测试失败",
		zap.Uint("providerId", report.ProviderID),
		zap.String("error", msg))
	global.APP_DB.Model(report).Updates(map[string]interface{}{
		"status":    "failed",
		"error_msg": msg,
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
