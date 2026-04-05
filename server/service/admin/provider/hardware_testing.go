package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// pasteAPIBaseURL paste.spiritlhl.net 的API基础URL
const pasteAPIBaseURL = "https://paste.spiritlhl.net/api/cd/show"

// pasteURLPattern 匹配 paste.spiritlhl.net URL 中的文件名
var pasteURLPattern = regexp.MustCompile(`paste\.spiritlhl\.net.*?([a-zA-Z0-9]+\.txt)`)

// pasteAPIResponse paste API 响应结构
type pasteAPIResponse struct {
	Code int    `json:"code"`
	Data string `json:"data"`
	Msg  string `json:"msg"`
}

// parsePasteFileName 从粘贴板URL中提取文件名
func parsePasteFileName(pasteURL string) (string, error) {
	matches := pasteURLPattern.FindStringSubmatch(pasteURL)
	if len(matches) < 2 {
		return "", fmt.Errorf("无法从URL中提取文件名: %s", pasteURL)
	}
	return matches[1], nil
}

// fetchPasteContent 从粘贴板URL下载内容
func fetchPasteContent(pasteURL string) (string, error) {
	fileName, err := parsePasteFileName(pasteURL)
	if err != nil {
		return "", err
	}

	apiURL := fmt.Sprintf("%s?name=%s", pasteAPIBaseURL, fileName)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("请求粘贴板API失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("粘贴板API返回HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 限制2MB
	if err != nil {
		return "", fmt.Errorf("读取粘贴板响应失败: %w", err)
	}

	var apiResp pasteAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", fmt.Errorf("解析粘贴板API响应失败: %w", err)
	}

	if apiResp.Code != 0 {
		return "", fmt.Errorf("粘贴板API错误: %s", apiResp.Msg)
	}

	if strings.TrimSpace(apiResp.Data) == "" {
		return "", fmt.Errorf("粘贴板内容为空")
	}

	return apiResp.Data, nil
}

// SaveHardwareReport 保存硬件报告（通过粘贴板URL下载内容）
func (s *Service) SaveHardwareReport(ctx context.Context, providerID, userID uint, pasteURL string) (*providerModel.HardwareTestReport, error) {
	pasteURL = strings.TrimSpace(pasteURL)
	if pasteURL == "" {
		return nil, fmt.Errorf("粘贴板URL不能为空")
	}

	// 验证Provider存在
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在: %w", err)
	}

	// 从粘贴板URL下载内容
	content, err := fetchPasteContent(pasteURL)
	if err != nil {
		return nil, fmt.Errorf("获取粘贴板内容失败: %w", err)
	}

	// 查询或创建报告
	var report providerModel.HardwareTestReport
	result := global.APP_DB.Where("provider_id = ?", providerID).First(&report)
	if result.Error != nil {
		report = providerModel.HardwareTestReport{
			ProviderID: providerID,
			PasteURL:   pasteURL,
			ReportText: content,
			UpdatedBy:  userID,
		}
		if err := global.APP_DB.Create(&report).Error; err != nil {
			return nil, fmt.Errorf("创建报告失败: %w", err)
		}
	} else {
		if err := global.APP_DB.Model(&report).Updates(map[string]interface{}{
			"paste_url":   pasteURL,
			"report_text": content,
			"updated_by":  userID,
		}).Error; err != nil {
			return nil, fmt.Errorf("更新报告失败: %w", err)
		}
		report.PasteURL = pasteURL
		report.ReportText = content
		report.UpdatedBy = userID
	}

	global.APP_LOG.Info("硬件报告已保存",
		zap.Uint("providerId", providerID),
		zap.String("pasteUrl", pasteURL),
		zap.Int("contentLength", len(content)))

	return &report, nil
}

// GetHardwareTestReport 获取硬件测试报告
func (s *Service) GetHardwareTestReport(ctx context.Context, providerID uint) (*providerModel.HardwareTestReport, error) {
	var report providerModel.HardwareTestReport
	if err := global.APP_DB.Where("provider_id = ?", providerID).First(&report).Error; err != nil {
		return nil, err
	}
	return &report, nil
}

// DeleteHardwareReport 删除硬件报告
func (s *Service) DeleteHardwareReport(ctx context.Context, providerID uint) error {
	return global.APP_DB.Where("provider_id = ?", providerID).Delete(&providerModel.HardwareTestReport{}).Error
}
