package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type containerGPUDevices struct {
	Devices         map[string]map[string]string `json:"devices"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices"`
}

// ConfigureContainerGPU reconciles only the new container's GPU devices.
// Inherited profile devices are masked on the container, never on the profile.
func ConfigureContainerGPU(ctx context.Context, executor ShellExecutor, cli, instanceName string, enabled bool, rawIDs string) error {
	if (cli != "lxc" && cli != "incus") || !IsValidLXDInstanceName(instanceName) {
		return fmt.Errorf("无效的容器GPU配置目标")
	}
	ids, err := containerGPUSelection(rawIDs)
	if err != nil {
		return err
	}
	query := cli + " query " + ShellSingleQuote("/1.0/instances/"+url.PathEscape(instanceName))
	output, err := ExecuteShellCommandContext(ctx, executor, query)
	if err != nil {
		return fmt.Errorf("读取容器GPU设备失败: %w", err)
	}
	doc, err := parseContainerGPUDevices(output)
	if err != nil {
		return err
	}
	effective, gpuNames := effectiveContainerGPUDevices(doc)
	if containerGPUMatches(effective, gpuNames, enabled, ids) {
		return nil
	}

	desired := make(map[string]string)
	var desiredNames []string
	if enabled {
		deviceIDs := ids
		if len(deviceIDs) == 0 {
			deviceIDs = []string{""} // No selector means all physical GPUs.
		}
		for idx, id := range deviceIDs {
			name := fmt.Sprintf("gpu%d", idx)
			if id == "" {
				name = "gpu"
			}
			// Never replace a disk, NIC, proxy or an administrator's mask just
			// because it happens to have the conventional GPU device name.
			for suffix := 0; ; suffix++ {
				device, exists := effective[name]
				if !exists || device["type"] == "gpu" {
					break
				}
				name = fmt.Sprintf("oneclickvirt-gpu-%d-%d", idx, suffix)
			}
			desired[name] = id
			desiredNames = append(desiredNames, name)
		}
	}
	mutate := func(command string) error {
		output, err := ExecuteShellCommandContext(ctx, executor, command)
		if err != nil {
			return fmt.Errorf("更新容器GPU设备失败: %w; %s", err, TruncateString(output, 500))
		}
		return nil
	}
	prefix := cli + " config device "
	quotedInstance := ShellSingleQuote(instanceName)
	for _, name := range gpuNames {
		if _, local := doc.Devices[name]; local {
			if err := mutate(prefix + "remove " + quotedInstance + " " + ShellSingleQuote(name)); err != nil {
				return err
			}
		}
		if _, replace := desired[name]; !replace {
			// Mask even a formerly local GPU: a profile can carry a GPU with
			// the same name, which becomes visible again after local removal.
			if err := mutate(prefix + "add " + quotedInstance + " " + ShellSingleQuote(name) + " none"); err != nil {
				return err
			}
		}
	}
	for _, name := range desiredNames {
		command := prefix + "add " + quotedInstance + " " + ShellSingleQuote(name) + " gpu"
		if id := desired[name]; id != "" {
			command += " " + ShellSingleQuote("id="+id)
		}
		if err := mutate(command); err != nil {
			return err
		}
	}
	// An acknowledged command is not sufficient if the resulting device
	// selection still exposes a copied all-GPU device or omits a requested GPU.
	output, err = ExecuteShellCommandContext(ctx, executor, query)
	if err != nil {
		return fmt.Errorf("验证容器GPU设备失败: %w", err)
	}
	doc, err = parseContainerGPUDevices(output)
	if err != nil {
		return err
	}
	effective, gpuNames = effectiveContainerGPUDevices(doc)
	if !containerGPUMatches(effective, gpuNames, enabled, ids) {
		return fmt.Errorf("容器GPU设备与所选配置不一致")
	}
	return nil
}

func effectiveContainerGPUDevices(doc *containerGPUDevices) (map[string]map[string]string, []string) {
	effective := make(map[string]map[string]string)
	for name, device := range doc.ExpandedDevices {
		effective[name] = device
	}
	for name, device := range doc.Devices {
		effective[name] = device
	}
	var names []string
	for name, device := range effective {
		if device["type"] == "gpu" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return effective, names
}

func containerGPUSelection(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var ids []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			return nil, fmt.Errorf("GPU设备ID不能为空")
		}
		for _, char := range id {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("_-.:", char) {
				continue
			}
			return nil, fmt.Errorf("GPU设备ID包含非法字符")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}

func parseContainerGPUDevices(output string) (*containerGPUDevices, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		return nil, fmt.Errorf("读取容器GPU设备返回了无效JSON: %w", err)
	}
	if metadata, ok := envelope["metadata"]; ok {
		if err := json.Unmarshal(metadata, &envelope); err != nil {
			return nil, fmt.Errorf("读取容器GPU设备返回了无效metadata: %w", err)
		}
	}
	if envelope == nil || envelope["devices"] == nil || envelope["expanded_devices"] == nil {
		return nil, fmt.Errorf("读取容器GPU设备响应缺少设备信息")
	}
	// Unmarshal the two fields independently so a malformed device cannot be
	// mistaken for an empty configuration and trigger destructive replacement.
	doc := &containerGPUDevices{}
	for key, target := range map[string]*map[string]map[string]string{"devices": &doc.Devices, "expanded_devices": &doc.ExpandedDevices} {
		if value, ok := envelope[key]; ok {
			if err := json.Unmarshal(value, target); err != nil || *target == nil {
				return nil, fmt.Errorf("读取容器GPU设备响应的%s无效", key)
			}
			for _, device := range *target {
				if device == nil || device["type"] == "" {
					return nil, fmt.Errorf("读取容器GPU设备响应包含无效设备")
				}
			}
		}
	}
	return doc, nil
}

func containerGPUMatches(devices map[string]map[string]string, names []string, enabled bool, ids []string) bool {
	if !enabled {
		return len(names) == 0
	}
	if len(ids) == 0 {
		if len(names) != 1 {
			return false
		}
		selector, physical := containerGPUSelector(devices[names[0]])
		return physical && selector == ""
	}
	if len(names) != len(ids) {
		return false
	}
	existing := make(map[string]bool)
	for _, name := range names {
		selector, physical := containerGPUSelector(devices[name])
		if !physical || selector == "" || existing[selector] {
			return false
		}
		existing[selector] = true
	}
	for _, id := range ids {
		if !existing[id] {
			return false
		}
	}
	return true
}

func containerGPUSelector(device map[string]string) (string, bool) {
	for _, key := range []string{"pci", "vendorid", "productid", "mdev", "mig.uuid", "mig.gi", "mig.ci"} {
		if device[key] != "" {
			return "", false
		}
	}
	if kind := device["gputype"]; kind != "" && kind != "physical" {
		return "", false
	}
	return device["id"], true
}
