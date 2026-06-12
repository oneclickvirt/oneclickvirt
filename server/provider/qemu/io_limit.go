package qemu

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *QEMUProvider) applyLibvirtIOLimits(ctx context.Context, uri, instanceName, preferredTarget string, config provider.InstanceConfig) {
	if ctx.Err() != nil {
		return
	}
	readRaw := pointerString(config.ReadIOLimit)
	writeRaw := pointerString(config.WriteIOLimit)
	if readRaw == "" && writeRaw == "" {
		return
	}

	args := make([]string, 0, 2)
	if readRaw != "" {
		if readBytes, ok := parseLibvirtIORate(readRaw); ok {
			args = append(args, fmt.Sprintf("--read-bytes-sec %d", readBytes))
		} else {
			global.APP_LOG.Warn("QEMU/libvirt读取IO限速格式不支持，已跳过",
				zap.String("instance", instanceName),
				zap.String("limit", readRaw))
		}
	}
	if writeRaw != "" {
		if writeBytes, ok := parseLibvirtIORate(writeRaw); ok {
			args = append(args, fmt.Sprintf("--write-bytes-sec %d", writeBytes))
		} else {
			global.APP_LOG.Warn("QEMU/libvirt写入IO限速格式不支持，已跳过",
				zap.String("instance", instanceName),
				zap.String("limit", writeRaw))
		}
	}
	if len(args) == 0 {
		return
	}

	target := strings.TrimSpace(preferredTarget)
	if target == "" {
		target = p.detectLibvirtBlockTarget(uri, instanceName)
	}
	if target == "" {
		global.APP_LOG.Warn("未找到libvirt块设备，跳过IO限速",
			zap.String("instance", instanceName),
			zap.String("uri", uri))
		return
	}

	baseCmd := fmt.Sprintf("virsh -c %s blkdeviotune %s %s %s",
		shellSingleQuote(uri),
		shellSingleQuote(instanceName),
		shellSingleQuote(target),
		strings.Join(args, " "))
	if output, err := p.sshClient.Execute(baseCmd + " --live --config 2>&1"); err != nil {
		if fallbackOutput, fallbackErr := p.sshClient.Execute(baseCmd + " --config 2>&1"); fallbackErr != nil {
			global.APP_LOG.Warn("应用QEMU/libvirt IO限速失败，继续执行",
				zap.String("instance", instanceName),
				zap.String("target", target),
				zap.String("output", utils.TruncateString(output+"\n"+fallbackOutput, 500)),
				zap.Error(fallbackErr))
			return
		}
	}
	global.APP_LOG.Info("已应用QEMU/libvirt IO限速",
		zap.String("instance", instanceName),
		zap.String("target", target),
		zap.String("readLimit", readRaw),
		zap.String("writeLimit", writeRaw))
}

func (p *QEMUProvider) detectLibvirtBlockTarget(uri, instanceName string) string {
	output, err := p.sshClient.Execute(fmt.Sprintf("virsh -c %s domblklist %s --details 2>/dev/null",
		shellSingleQuote(uri),
		shellSingleQuote(instanceName)))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || strings.EqualFold(fields[0], "Type") || strings.HasPrefix(fields[0], "-") {
			continue
		}
		if fields[1] == "disk" || fields[1] == "file" || fields[1] == "block" {
			return fields[2]
		}
	}
	return ""
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func parseLibvirtIORate(raw string) (int64, bool) {
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
