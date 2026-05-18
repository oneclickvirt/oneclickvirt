package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	providerPkg "oneclickvirt/provider"
	adminProvider "oneclickvirt/service/admin/provider"
	"oneclickvirt/service/provider"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// gpuCacheEntry GPU/NPU 检测缓存条目
type gpuCacheEntry struct {
	gpus         []map[string]string
	npus         []map[string]string
	accelerators []map[string]string
	rawInfo      string
	cachedAt     time.Time
}

// gpuDetectionCache GPU 检测结果缓存（5分钟有效期）
var gpuDetectionCache sync.Map

var normalizedPCIBusRegex = regexp.MustCompile(`(?i)([0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7])$`)

// DetectGPUs 检测Provider节点上的GPU/NPU设备
// 支持SSH与Agent模式，优先使用lxc/incus资源信息并结合nvidia-smi/lspci/npu-smi等多源检测
func DetectGPUs(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	providerService := adminProvider.NewService()
	p, err := providerService.GetProviderByID(uint(id), middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	if p.Type != "lxd" && p.Type != "incus" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "GPU检测仅支持 lxd 和 incus 类型的Provider"))
		return
	}

	// 检查缓存：优先内存缓存（5分钟TTL），其次持久化DB缓存
	forceRefresh := c.DefaultQuery("forceRefresh", "false") == "true"
	if !forceRefresh {
		// 1. 内存缓存（5分钟TTL）
		if cached, ok := gpuDetectionCache.Load(uint(id)); ok {
			if entry, ok2 := cached.(gpuCacheEntry); ok2 && time.Since(entry.cachedAt) < 5*time.Minute {
				normalizedAccelerators := normalizeDetectedAccelerators(entry.accelerators)
				normalizedGPUs, normalizedNPUs := splitStringGPUsNPUs(normalizedAccelerators)
				global.APP_LOG.Debug("GPU检测：命中内存缓存",
					zap.Uint("providerID", uint(id)),
					zap.Duration("age", time.Since(entry.cachedAt)))
				common.ResponseSuccess(c, map[string]interface{}{
					"gpus":         normalizedGPUs,
					"npus":         normalizedNPUs,
					"accelerators": normalizedAccelerators,
					"rawInfo":      entry.rawInfo,
					"cached":       true,
				}, "GPU/NPU检测完成（缓存）")
				return
			}
		}
		// 2. 持久化DB缓存（跨重启有效）
		if p.GpuInfo != "" {
			var cachedGpus []map[string]string
			if json.Unmarshal([]byte(p.GpuInfo), &cachedGpus) == nil && len(cachedGpus) > 0 {
				normalizedGpus := normalizeDetectedAccelerators(cachedGpus)
				if len(normalizedGpus) != len(cachedGpus) {
					if gpuInfoBytes, err := json.Marshal(normalizedGpus); err == nil {
						global.APP_DB.Model(&providerModel.Provider{}).
							Where("id = ?", uint(id)).
							Update("gpu_info", string(gpuInfoBytes))
					}
				}
				global.APP_LOG.Debug("GPU检测：命中DB持久化缓存",
					zap.Uint("providerID", uint(id)))
				common.ResponseSuccess(c, map[string]interface{}{
					"gpus":         normalizedGpus,
					"npus":         []map[string]string{},
					"accelerators": normalizedGpus,
					"rawInfo":      "",
					"cached":       true,
				}, "GPU/NPU检测完成（持久化缓存）")
				return
			}
		}
	}

	execCtx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	providerInstance, err := provider.EnsureProviderConnected(execCtx, uint(id))
	if err != nil {
		global.APP_LOG.Error("GPU检测：Provider连接不可用", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider连接不可用: "+err.Error()))
		return
	}

	accelerators, rawInfo, detectErr := detectAccelerators(execCtx, providerInstance, p.Type)
	if detectErr != nil {
		global.APP_LOG.Warn("GPU/NPU检测失败", zap.Error(detectErr), zap.Uint("providerID", p.ID), zap.String("providerType", p.Type))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "设备检测失败: "+detectErr.Error()))
		return
	}

	accelerators = normalizeDetectedAccelerators(accelerators)
	gpus, npus := splitStringGPUsNPUs(accelerators)

	// 写入内存缓存
	gpuDetectionCache.Store(uint(id), gpuCacheEntry{
		gpus:         gpus,
		npus:         npus,
		accelerators: accelerators,
		rawInfo:      strings.TrimSpace(rawInfo),
		cachedAt:     time.Now(),
	})

	// 持久化到 Provider 表，供用户端免检测直接展示 GPU 选项
	gpuInfoBytes, _ := json.Marshal(gpus)
	global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", uint(id)).
		Update("gpu_info", string(gpuInfoBytes))

	common.ResponseSuccess(c, map[string]interface{}{
		"gpus":         gpus,
		"npus":         npus,
		"accelerators": accelerators,
		"rawInfo":      strings.TrimSpace(rawInfo),
		"cached":       false,
	}, "GPU/NPU检测完成")
}

func splitStringGPUsNPUs(devices []map[string]string) ([]map[string]string, []map[string]string) {
	gpus := make([]map[string]string, 0)
	npus := make([]map[string]string, 0)
	for _, d := range devices {
		if strings.EqualFold(strings.TrimSpace(d["kind"]), "npu") {
			npus = append(npus, d)
		} else {
			gpus = append(gpus, d)
		}
	}
	return gpus, npus
}

func normalizeDetectedAccelerators(devices []map[string]string) []map[string]string {
	merged := make([]map[string]string, 0, len(devices))
	mergedIndex := make(map[string]int)
	for _, raw := range devices {
		device := map[string]string{}
		for key, value := range raw {
			device[key] = strings.TrimSpace(value)
		}
		if strings.TrimSpace(device["kind"]) == "" {
			device["kind"] = "gpu"
		}
		if strings.TrimSpace(device["product"]) == "" && strings.TrimSpace(device["name"]) != "" {
			device["product"] = device["name"]
		}
		if strings.TrimSpace(device["source"]) == "" {
			device["source"] = "unknown"
		}

		key := acceleratorMergeKey(device)
		if idx, ok := mergedIndex[key]; ok {
			mergeAcceleratorRecord(merged[idx], device)
			continue
		}
		merged = append(merged, device)
		mergedIndex[key] = len(merged) - 1
	}

	sort.SliceStable(merged, func(i, j int) bool {
		a := merged[i]
		b := merged[j]
		if a["kind"] != b["kind"] {
			return a["kind"] < b["kind"]
		}
		if a["id"] != b["id"] {
			return a["id"] < b["id"]
		}
		if normalizePCIBus(a["bus"]) != normalizePCIBus(b["bus"]) {
			return normalizePCIBus(a["bus"]) < normalizePCIBus(b["bus"])
		}
		return a["name"] < b["name"]
	})

	return merged
}

func detectAccelerators(ctx context.Context, providerInstance providerPkg.Provider, providerType string) ([]map[string]string, string, error) {
	devices := make([]map[string]string, 0)
	rawSections := make([]string, 0)
	mergedIndex := make(map[string]int)
	hadAnySource := false

	addDevice := func(kind, id, name, vendor, bus, source, card string) {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" {
			kind = "gpu"
		}
		id = strings.TrimSpace(id)
		name = strings.TrimSpace(name)
		vendor = strings.TrimSpace(vendor)
		bus = strings.TrimSpace(bus)
		source = strings.TrimSpace(source)
		card = strings.TrimSpace(card)
		if name == "" {
			name = card
		}
		if source == "" {
			source = "unknown"
		}

		d := map[string]string{
			"kind":    kind,
			"id":      id,
			"name":    name,
			"product": name,
			"vendor":  vendor,
			"bus":     bus,
			"source":  source,
		}
		if card != "" {
			d["card"] = card
		}

		key := acceleratorMergeKey(d)
		if idx, ok := mergedIndex[key]; ok {
			mergeAcceleratorRecord(devices[idx], d)
			return
		}

		devices = append(devices, d)
		mergedIndex[key] = len(devices) - 1
	}

	appendRaw := func(title, output string) {
		output = strings.TrimSpace(output)
		if output == "" {
			return
		}
		rawSections = append(rawSections, title+"\n"+output)
	}

	resourceCmd := "lxc info --resources 2>/dev/null | awk '/^GPU:/,/^[A-Z]/' | head -120"
	if providerType == "incus" {
		resourceCmd = "incus info --resources 2>/dev/null | awk '/^GPU:/,/^[A-Z]/' | head -120"
	}
	if output, err := providerInstance.ExecuteSSHCommand(ctx, resourceCmd); err == nil {
		hadAnySource = true
		appendRaw("[lxc/incus resources]", output)
		for _, d := range parseLXDGPUInfo(output) {
			addDevice("gpu", d["id"], d["name"], d["vendor"], d["device"], "lxc-resources", d["card"])
		}
	} else {
		global.APP_LOG.Debug("GPU检测：lxc/incus资源命令执行失败", zap.Error(err))
	}

	if output, err := providerInstance.ExecuteSSHCommand(ctx, "nvidia-smi --query-gpu=index,name,pci.bus_id --format=csv,noheader 2>/dev/null || true"); err == nil {
		hadAnySource = true
		appendRaw("[nvidia-smi]", output)
		for _, d := range parseNvidiaSMI(output) {
			addDevice("gpu", d["id"], d["name"], "NVIDIA", d["bus"], "nvidia-smi", "")
		}
	}

	if output, err := providerInstance.ExecuteSSHCommand(ctx, "lspci -Dnn 2>/dev/null || true"); err == nil {
		hadAnySource = true
		appendRaw("[lspci]", output)
		for _, d := range parseLspciAccelerators(output) {
			addDevice(d["kind"], d["id"], d["name"], d["vendor"], d["bus"], "lspci", "")
		}
	}

	if output, err := providerInstance.ExecuteSSHCommand(ctx, "npu-smi info 2>/dev/null || true"); err == nil {
		hadAnySource = true
		appendRaw("[npu-smi]", output)
		for _, d := range parseNPUSmiInfo(output) {
			addDevice("npu", d["id"], d["name"], d["vendor"], d["bus"], "npu-smi", "")
		}
	}

	if !hadAnySource {
		return nil, strings.Join(rawSections, "\n\n"), fmt.Errorf("未能执行任何检测命令，请检查节点连接状态与命令执行权限")
	}

	sort.SliceStable(devices, func(i, j int) bool {
		a := devices[i]
		b := devices[j]
		if a["kind"] != b["kind"] {
			return a["kind"] < b["kind"]
		}
		if a["id"] != b["id"] {
			return a["id"] < b["id"]
		}
		if a["bus"] != b["bus"] {
			return a["bus"] < b["bus"]
		}
		return a["name"] < b["name"]
	})

	return devices, strings.Join(rawSections, "\n\n"), nil
}

func acceleratorMergeKey(device map[string]string) string {
	kind := strings.ToLower(strings.TrimSpace(device["kind"]))
	vendor := strings.ToLower(strings.TrimSpace(device["vendor"]))
	id := strings.ToLower(strings.TrimSpace(device["id"]))
	name := normalizeAcceleratorName(device["name"])
	bus := normalizePCIBus(device["bus"])

	switch {
	case bus != "":
		return kind + "|bus|" + bus
	case vendor != "" && id != "":
		return kind + "|vendor-id|" + vendor + "|" + id
	case vendor != "" && name != "":
		return kind + "|vendor-name|" + vendor + "|" + name
	case name != "":
		return kind + "|name|" + name
	default:
		return kind + "|raw|" + strings.ToLower(strings.TrimSpace(device["source"])) + "|" + strings.ToLower(strings.TrimSpace(device["card"]))
	}
}

func mergeAcceleratorRecord(dst, src map[string]string) {
	for _, field := range []string{"id", "name", "product", "vendor", "bus", "card"} {
		if strings.TrimSpace(dst[field]) == "" && strings.TrimSpace(src[field]) != "" {
			dst[field] = strings.TrimSpace(src[field])
		}
	}

	if normalizePCIBus(dst["bus"]) == "" && normalizePCIBus(src["bus"]) != "" {
		dst["bus"] = strings.TrimSpace(src["bus"])
	}

	if acceleratorSourceRank(src["source"]) > acceleratorSourceRank(dst["source"]) {
		dst["source"] = strings.TrimSpace(src["source"])
	}
}

func acceleratorSourceRank(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "lxc-resources":
		return 4
	case "nvidia-smi", "npu-smi":
		return 3
	case "lspci":
		return 2
	case "unknown":
		return 1
	default:
		return 0
	}
}

func normalizeAcceleratorName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Join(strings.Fields(name), " ")
	return name
}

func normalizePCIBus(bus string) string {
	bus = strings.ToLower(strings.TrimSpace(bus))
	if bus == "" {
		return ""
	}
	if matched := normalizedPCIBusRegex.FindStringSubmatch(bus); len(matched) > 1 {
		return matched[1]
	}
	return bus
}

// splitGPUsNPUs 从缓存的设备列表中分离 GPU 和 NPU
func splitGPUsNPUs(devices []map[string]interface{}) ([]map[string]interface{}, []map[string]interface{}) {
	gpus := make([]map[string]interface{}, 0)
	npus := make([]map[string]interface{}, 0)
	for _, d := range devices {
		kind, _ := d["kind"].(string)
		if strings.EqualFold(strings.TrimSpace(kind), "npu") {
			npus = append(npus, d)
		} else {
			gpus = append(gpus, d)
		}
	}
	return gpus, npus
}

// parseLXDGPUInfo 解析 lxc/incus info --resources 中的GPU片段
func parseLXDGPUInfo(raw string) []map[string]string {
	gpus := make([]map[string]string, 0)
	current := map[string]string{}

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "GPU:" {
			continue
		}
		if strings.HasPrefix(trimmed, "Card ") {
			if len(current) > 0 {
				gpus = append(gpus, current)
			}
			current = map[string]string{"card": trimmed}
			continue
		}
		if idx := strings.Index(trimmed, ": "); idx != -1 {
			key := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
			val := strings.TrimSpace(trimmed[idx+2:])
			current[key] = val
			if key == "product" {
				current["name"] = val
			}
		}
	}
	if len(current) > 0 {
		gpus = append(gpus, current)
	}

	return gpus
}

func parseNvidiaSMI(raw string) []map[string]string {
	devices := make([]map[string]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		bus := ""
		if len(parts) >= 3 {
			bus = strings.TrimSpace(parts[2])
		}
		devices = append(devices, map[string]string{
			"id":   id,
			"name": name,
			"bus":  bus,
		})
	}
	return devices
}

func parseLspciAccelerators(raw string) []map[string]string {
	devices := make([]map[string]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)

		isNPU := containsAny(lower,
			" npu",
			"neural",
			"habanalabs",
			"gaudi",
			"ascend",
			"cambricon",
			"kunlun",
			"mlu",
			"processing accelerators",
		)
		isGPU := containsAny(lower,
			"vga compatible controller",
			"3d controller",
			"display controller",
		)
		if !isGPU && !isNPU {
			continue
		}

		kind := "gpu"
		if isNPU {
			kind = "npu"
		}

		bus := ""
		name := line
		if idx := strings.Index(line, " "); idx > 0 {
			bus = strings.TrimSpace(line[:idx])
		}
		if idx := strings.Index(line, ": "); idx >= 0 {
			name = strings.TrimSpace(line[idx+2:])
		}

		vendor := inferVendor(name)
		devices = append(devices, map[string]string{
			"kind":   kind,
			"id":     "",
			"name":   name,
			"vendor": vendor,
			"bus":    bus,
		})
	}
	return devices
}

func parseNPUSmiInfo(raw string) []map[string]string {
	devices := make([]map[string]string, 0)
	idRegex := regexp.MustCompile(`\b([0-9]{1,2})\b`)

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			continue
		}
		lower := strings.ToLower(line)
		if !containsAny(lower, "npu", "ascend") {
			continue
		}

		id := ""
		if m := idRegex.FindStringSubmatch(line); len(m) > 1 {
			id = m[1]
		}
		devices = append(devices, map[string]string{
			"id":     id,
			"name":   line,
			"vendor": inferVendor(line),
			"bus":    "",
		})
	}

	return devices
}

func containsAny(s string, keywords ...string) bool {
	for _, k := range keywords {
		if strings.Contains(s, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func inferVendor(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "nvidia"):
		return "NVIDIA"
	case strings.Contains(lower, "advanced micro devices"), strings.Contains(lower, " amd "), strings.HasPrefix(lower, "amd"):
		return "AMD"
	case strings.Contains(lower, "intel"):
		return "Intel"
	case strings.Contains(lower, "huawei"), strings.Contains(lower, "ascend"):
		return "Huawei"
	case strings.Contains(lower, "cambricon"), strings.Contains(lower, "mlu"):
		return "Cambricon"
	case strings.Contains(lower, "habanalabs"), strings.Contains(lower, "gaudi"):
		return "Habana"
	case strings.Contains(lower, "kunlun"):
		return "Baidu"
	default:
		return ""
	}
}
