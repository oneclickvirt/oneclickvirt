package source

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const localQEMUProviderName = "本机"

// SeedLocalQEMUProviderIfAvailable creates a first-use local node when the
// controller is deployed directly on a Linux host with libvirt/QEMU available.
// It is intentionally skipped inside Docker/Podman style containers because
// the controller container usually cannot manage the host hypervisor safely.
func SeedLocalQEMUProviderIfAvailable() {
	if global.APP_DB == nil {
		return
	}
	if isControllerRunningInContainer() {
		global.APP_LOG.Debug("检测到控制端运行在容器中，跳过本机QEMU节点初始化")
		return
	}
	if !localCommandExists("virsh") || !localCommandExists("qemu-img") {
		global.APP_LOG.Debug("本机缺少virsh或qemu-img，跳过本机QEMU节点初始化")
		return
	}

	vmAvailable := localCommandOK("virsh", "-c", "qemu:///system", "uri")
	lxcAvailable := localCommandOK("virsh", "-c", "lxc:///", "uri")
	if !vmAvailable && !lxcAvailable {
		global.APP_LOG.Debug("本机libvirt qemu:///system 与 lxc:/// 均不可用，跳过本机QEMU节点初始化")
		return
	}

	var existing providerModel.Provider
	err := global.APP_DB.Where("name = ? OR (type = ? AND connection_type = ?)", localQEMUProviderName, "qemu", "local").First(&existing).Error
	if err == nil {
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		global.APP_LOG.Warn("检查本机QEMU节点是否存在失败", zap.Error(err))
		return
	}

	version := localCommandOutput("virsh", "version", "--short")
	if version == "" {
		version = localCommandOutput("virsh", "--version")
	}
	if version == "" {
		version = "local-libvirt"
	}

	provider := providerModel.Provider{
		Name:                     localQEMUProviderName,
		Description:              "自动初始化的本机 libvirt/QEMU 节点，可创建 libvirt-lxc 容器和 QEMU/KVM 虚拟机",
		Type:                     "qemu",
		Endpoint:                 "127.0.0.1",
		PortIP:                   "",
		SSHPort:                  0,
		Username:                 "root",
		Status:                   "active",
		Region:                   "Local",
		Country:                  "Local",
		CountryCode:              "LOCAL",
		City:                     "Localhost",
		Version:                  version,
		ContainerEnabled:         lxcAvailable,
		VirtualMachineEnabled:    vmAvailable,
		SupportedTypes:           localSupportedTypes(lxcAvailable, vmAvailable),
		AllowClaim:               true,
		Architecture:             normalizeRuntimeArch(runtime.GOARCH),
		StoragePool:              "local",
		StoragePoolPath:          "/var/lib/libvirt",
		IPv4PortMappingMethod:    "iptables",
		IPv6PortMappingMethod:    "native",
		NetworkType:              "nat_ipv4",
		DefaultPortCount:         10,
		PortRangeStart:           10000,
		PortRangeEnd:             65535,
		NextAvailablePort:        10000,
		ConnectionType:           "local",
		APIStatus:                "online",
		SSHStatus:                "online",
		ExecutionRule:            "auto",
		SSHConnectTimeout:        10,
		SSHExecuteTimeout:        300,
		EnableTaskPolling:        true,
		TaskPollInterval:         60,
		MaxContainerInstances:    0,
		MaxVMInstances:           0,
		ContainerLimitCPU:        false,
		ContainerLimitMemory:     false,
		ContainerLimitDisk:       true,
		VMLimitCPU:               true,
		VMLimitMemory:            true,
		VMLimitDisk:              true,
		DefaultInboundBandwidth:  300,
		DefaultOutboundBandwidth: 300,
		MaxInboundBandwidth:      1000,
		MaxOutboundBandwidth:     1000,
		MaxTraffic:               1048576,
		TrafficCountMode:         "both",
		TrafficMultiplier:        1.0,
		TrafficStatsMode:         providerModel.TrafficStatsModeLight,
		TrafficSyncMethod:        "pmacct",
		TrafficOverLimitAction:   providerModel.TrafficOverLimitActionStop,
		InstanceExpiryAction:     providerModel.InstanceExpiryActionDelete,
	}

	if err := global.APP_DB.Create(&provider).Error; err != nil {
		global.APP_LOG.Warn("自动创建本机QEMU节点失败", zap.Error(err))
		return
	}
	global.APP_LOG.Info("已自动创建本机QEMU节点",
		zap.Uint("id", provider.ID),
		zap.Bool("lxc", lxcAvailable),
		zap.Bool("vm", vmAvailable),
		zap.String("arch", provider.Architecture))
}

func isControllerRunningInContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "docker") || strings.Contains(lower, "kubepods") || strings.Contains(lower, "containerd") || strings.Contains(lower, "podman")
}

func localCommandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func localCommandOK(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	return cmd.Run() == nil
}

func localCommandOutput(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return utils.TruncateString(line, 32)
		}
	}
	return ""
}

func localSupportedTypes(containerEnabled, vmEnabled bool) string {
	types := make([]string, 0, 2)
	if containerEnabled {
		types = append(types, "container")
	}
	if vmEnabled {
		types = append(types, "vm")
	}
	return strings.Join(types, ",")
}

func normalizeRuntimeArch(arch string) string {
	switch strings.ToLower(arch) {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "s390x":
		return "s390x"
	default:
		return arch
	}
}
