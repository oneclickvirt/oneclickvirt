package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func providerIOLimitsChanged(before providerModel.Provider, after providerModel.Provider) bool {
	return before.ContainerReadIOLimit != after.ContainerReadIOLimit ||
		before.ContainerWriteIOLimit != after.ContainerWriteIOLimit ||
		before.VMReadIOLimit != after.VMReadIOLimit ||
		before.VMWriteIOLimit != after.VMWriteIOLimit
}

func (s *Service) syncProviderIOLimits(providerID uint) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("同步Provider IO限速时发生panic",
				zap.Uint("providerID", providerID),
				zap.Any("panic", r))
		}
	}()
	if global.APP_DB == nil {
		return
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		global.APP_LOG.Warn("同步Provider IO限速失败：节点不存在",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return
	}

	providerType := strings.ToLower(strings.TrimSpace(dbProvider.Type))
	if providerType != "lxd" && providerType != "incus" && providerType != "qemu" {
		global.APP_LOG.Info("Provider类型暂不支持动态同步IO限速，已跳过",
			zap.Uint("providerID", providerID),
			zap.String("type", providerType))
		return
	}

	var instances []providerModel.Instance
	if err := global.APP_DB.
		Where("provider_id = ? AND deleted_at IS NULL AND status NOT IN ?",
			providerID, constant.GetTerminalStatuses()).
		Find(&instances).Error; err != nil {
		global.APP_LOG.Warn("查询Provider实例以同步IO限速失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return
	}
	if len(instances) == 0 {
		return
	}

	api := &providerService.ProviderApiService{}
	prov, _, err := api.GetProviderByID(providerID)
	if err != nil {
		global.APP_LOG.Warn("同步Provider IO限速失败：节点不可连接",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	successCount := 0
	failCount := 0
	skippedCount := 0
	for _, instance := range instances {
		if ctx.Err() != nil {
			break
		}
		readLimit, writeLimit := limitsForInstanceType(dbProvider, instance.InstanceType)
		cmd, ok := buildProviderIOLimitCommand(providerType, instance.Name, instance.InstanceType, readLimit, writeLimit)
		if !ok {
			skippedCount++
			continue
		}
		output, err := prov.ExecuteSSHCommand(ctx, cmd)
		if err != nil {
			failCount++
			global.APP_LOG.Warn("同步实例IO限速失败，继续处理后续实例",
				zap.Uint("providerID", providerID),
				zap.Uint("instanceID", instance.ID),
				zap.String("instanceName", instance.Name),
				zap.String("output", utils.TruncateString(output, 500)),
				zap.Error(err))
			continue
		}
		successCount++
	}

	global.APP_LOG.Info("Provider IO限速同步完成",
		zap.Uint("providerID", providerID),
		zap.Int("success", successCount),
		zap.Int("failed", failCount),
		zap.Int("skipped", skippedCount))
}

func limitsForInstanceType(dbProvider providerModel.Provider, instanceType string) (string, string) {
	if strings.EqualFold(strings.TrimSpace(instanceType), "vm") {
		return strings.TrimSpace(dbProvider.VMReadIOLimit), strings.TrimSpace(dbProvider.VMWriteIOLimit)
	}
	return strings.TrimSpace(dbProvider.ContainerReadIOLimit), strings.TrimSpace(dbProvider.ContainerWriteIOLimit)
}

func buildProviderIOLimitCommand(providerType, instanceName, instanceType, readLimit, writeLimit string) (string, bool) {
	switch providerType {
	case "lxd":
		return buildLXDStyleIOLimitCommand("lxc", instanceName, readLimit, writeLimit), true
	case "incus":
		return buildLXDStyleIOLimitCommand("incus", instanceName, readLimit, writeLimit), true
	case "qemu":
		return buildLibvirtIOLimitCommand(instanceName, instanceType, readLimit, writeLimit)
	default:
		return "", false
	}
}

func buildLXDStyleIOLimitCommand(binary, instanceName, readLimit, writeLimit string) string {
	return strings.Join([]string{
		buildLXDStyleIOLimitPart(binary, instanceName, "limits.read", readLimit),
		buildLXDStyleIOLimitPart(binary, instanceName, "limits.write", writeLimit),
	}, " && ")
}

func buildLXDStyleIOLimitPart(binary, instanceName, key, value string) string {
	if strings.TrimSpace(value) == "" {
		return fmt.Sprintf("%s config device unset %s root %s 2>/dev/null || true",
			binary,
			shellQuote(instanceName),
			shellQuote(key))
	}
	return fmt.Sprintf("(%s config device set %s root %s=%s 2>/dev/null || %s config device set %s root %s %s)",
		binary,
		shellQuote(instanceName),
		shellQuote(key),
		shellQuote(value),
		binary,
		shellQuote(instanceName),
		shellQuote(key),
		shellQuote(value))
}

func buildLibvirtIOLimitCommand(instanceName, instanceType, readLimit, writeLimit string) (string, bool) {
	args := make([]string, 0, 2)
	if readArg, ok := buildLibvirtIOLimitArg("--read-bytes-sec", readLimit); ok {
		args = append(args, readArg)
	}
	if writeArg, ok := buildLibvirtIOLimitArg("--write-bytes-sec", writeLimit); ok {
		args = append(args, writeArg)
	}
	if len(args) == 0 {
		return "", false
	}

	uri := "qemu:///system"
	if !strings.EqualFold(strings.TrimSpace(instanceType), "vm") {
		uri = "lxc:///"
	}
	targetScript := fmt.Sprintf("target=$(virsh -c %s domblklist %s --details 2>/dev/null | awk 'NR>2 && ($2==\"disk\" || $2==\"file\" || $2==\"block\"){print $3; exit}')",
		shellQuote(uri),
		shellQuote(instanceName))
	tuneScript := fmt.Sprintf("test -n \"$target\" && (virsh -c %s blkdeviotune %s \"$target\" %s --live --config 2>/dev/null || virsh -c %s blkdeviotune %s \"$target\" %s --config 2>/dev/null)",
		shellQuote(uri),
		shellQuote(instanceName),
		strings.Join(args, " "),
		shellQuote(uri),
		shellQuote(instanceName),
		strings.Join(args, " "))
	return targetScript + " && " + tuneScript, true
}

func buildLibvirtIOLimitArg(name, raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return name + " 0", true
	}
	bytes, ok := parseProviderIOLimitBytes(raw)
	if !ok {
		global.APP_LOG.Warn("libvirt IO限速格式不支持，已跳过该方向",
			zap.String("limit", raw))
		return "", false
	}
	return fmt.Sprintf("%s %d", name, bytes), true
}

func parseProviderIOLimitBytes(raw string) (int64, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" || strings.Contains(value, "iops") {
		return 0, false
	}
	multiplier := int64(1)
	for _, suffix := range []struct {
		unit string
		mul  int64
	}{
		{"gib", 1024 * 1024 * 1024},
		{"gb", 1000 * 1000 * 1000},
		{"g", 1024 * 1024 * 1024},
		{"mib", 1024 * 1024},
		{"mb", 1000 * 1000},
		{"m", 1024 * 1024},
		{"kib", 1024},
		{"kb", 1000},
		{"k", 1024},
		{"b", 1},
	} {
		if strings.HasSuffix(value, suffix.unit) {
			value = strings.TrimSpace(strings.TrimSuffix(value, suffix.unit))
			multiplier = suffix.mul
			break
		}
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number <= 0 {
		return 0, false
	}
	bytes := int64(number * float64(multiplier))
	return bytes, bytes > 0
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
