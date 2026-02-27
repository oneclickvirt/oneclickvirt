package proxmox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// getUsedInternalIPs 从iptables规则中提取已使用的内网IP地址（高效且准确）
func (p *ProxmoxProvider) getUsedInternalIPs(ctx context.Context) (map[string]bool, error) {
	usedIPs := make(map[string]bool)

	// 从 iptables DNAT 规则中提取所有目标内网IP
	// 这是最准确的方法，因为只要有端口映射就必定在 iptables 中
	cmd := fmt.Sprintf("iptables -t nat -L PREROUTING -n | grep -oP '%s\\.\\d+' | sort -u", InternalIPPrefix)
	output, err := p.sshClient.Execute(cmd)
	if err != nil {
		global.APP_LOG.Error("获取iptables规则失败",
			zap.Error(err))
		return usedIPs, err
	}

	if strings.TrimSpace(output) != "" {
		lines := strings.Split(strings.TrimSpace(output), "\n")
		for _, ip := range lines {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				usedIPs[ip] = true
			}
		}
	}

	global.APP_LOG.Debug("从iptables规则提取内网IP使用情况完成",
		zap.Int("usedIPCount", len(usedIPs)))

	return usedIPs, nil
}

// 获取下一个可用的 VMID（确保对应的IP也可用）
// 在Proxmox中，VM的VMID和Container的CTID共享同一个ID空间，因此统一分配
func (p *ProxmoxProvider) getNextVMID(ctx context.Context, instanceType string) (int, error) {
	// 并发安全保护：VMID分配必须串行化，避免多个goroutine同时分配到相同ID
	// 使用互斥锁确保同一时间只有一个goroutine在分配VMID
	p.mu.Lock()
	defer p.mu.Unlock()

	// VMID/CTID范围：100-999（Proxmox标准，VM和Container共享ID空间）
	// 使用全局常量确保一致性
	global.APP_LOG.Debug("开始分配VMID/CTID",
		zap.String("instanceType", instanceType),
		zap.Int("minVMID", MinVMID),
		zap.Int("maxVMID", MaxVMID),
		zap.Int("maxInstances", MaxInstances))

	// 1. 获取已使用的ID列表（包含VM的VMID和Container的CTID）
	usedIDs := make(map[int]bool)

	// 获取虚拟机列表（VMID）
	vmOutput, err := p.sshClient.Execute("qm list")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(vmOutput), "\n")
		for _, line := range lines[1:] { // 跳过标题行
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				if id, parseErr := strconv.Atoi(fields[0]); parseErr == nil {
					usedIDs[id] = true
				}
			}
		}
	}

	// 获取容器列表（CTID）- 与VMID共享同一ID空间
	ctOutput, err := p.sshClient.Execute("pct list")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(ctOutput), "\n")
		for _, line := range lines[1:] { // 跳过标题行
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				if id, parseErr := strconv.Atoi(fields[0]); parseErr == nil {
					usedIDs[id] = true
				}
			}
		}
	}

	// 2. 获取已使用的内网IP列表（关键：避免IP冲突）
	usedIPs, err := p.getUsedInternalIPs(ctx)
	if err != nil {
		global.APP_LOG.Warn("获取已用IP列表失败，继续分配但可能存在IP冲突风险",
			zap.Error(err))
		usedIPs = make(map[string]bool) // 继续执行，但有风险
	}

	global.APP_LOG.Debug("已扫描资源使用情况",
		zap.Int("usedIDs", len(usedIDs)),
		zap.Int("usedIPs", len(usedIPs)))

	// 检查是否已达到最大实例数量限制
	if len(usedIDs) >= MaxInstances {
		global.APP_LOG.Error("已达到最大实例数量限制",
			zap.Int("currentInstances", len(usedIDs)),
			zap.Int("maxInstances", MaxInstances))
		return 0, fmt.Errorf("已达到最大实例数量限制 (%d/%d)，无法创建新实例。请删除不用的实例或联系管理员扩展网络容量", len(usedIDs), MaxInstances)
	}

	// 3. 在指定范围内寻找同时满足ID和IP都可用的ID
	// 策略：优先从小到大查找，确保ID未被占用（无论是VM还是Container）且映射的IP也未被占用
	for id := MinVMID; id <= MaxVMID; id++ {
		// 检查ID是否已被使用（VM或Container）
		if usedIDs[id] {
			continue
		}

		// 检查该ID映射的IP是否已被占用
		mappedIP := VMIDToInternalIP(id)
		if mappedIP == "" {
			continue // 无效映射，跳过
		}

		if usedIPs[mappedIP] {
			global.APP_LOG.Debug("ID可用但映射的IP已被占用，跳过",
				zap.Int("id", id),
				zap.String("mappedIP", mappedIP))
			continue
		}

		// 找到了同时满足ID和IP都可用的ID
		global.APP_LOG.Debug("分配VMID/CTID成功（已验证IP可用）",
			zap.String("instanceType", instanceType),
			zap.Int("id", id),
			zap.String("assignedIP", mappedIP),
			zap.Int("totalUsedIDs", len(usedIDs)),
			zap.Int("totalUsedIPs", len(usedIPs)),
			zap.Int("remainingSlots", MaxInstances-len(usedIDs)))
		return id, nil
	}

	// 如果没有可用的ID（或所有ID对应的IP都被占用）
	global.APP_LOG.Error("ID范围内无可用ID或所有映射IP已被占用",
		zap.Int("minVMID", MinVMID),
		zap.Int("maxVMID", MaxVMID),
		zap.Int("usedIDs", len(usedIDs)),
		zap.Int("usedIPs", len(usedIPs)))
	return 0, fmt.Errorf("在范围 %d-%d 内没有可用的ID（已使用: %d）或所有映射的IP地址已被占用", MinVMID, MaxVMID, len(usedIDs))
}

// sshSetInstancePassword 通过SSH设置实例密码
func (p *ProxmoxProvider) sshSetInstancePassword(ctx context.Context, instanceID, password string) error {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceID)
	if err != nil {
		global.APP_LOG.Error("查找Proxmox实例失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("查找实例失败: %w", err)
	}

	// 检查实例状态
	var statusCmd string
	switch instanceType {
	case "container":
		statusCmd = fmt.Sprintf("pct status %s", vmid)
	case "vm":
		statusCmd = fmt.Sprintf("qm status %s", vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	statusOutput, err := p.sshClient.Execute(statusCmd)
	if err != nil {
		return fmt.Errorf("检查实例状态失败: %w", err)
	}

	if !strings.Contains(statusOutput, "status: running") {
		return fmt.Errorf("实例 %s (VMID: %s) 未运行，无法设置密码", instanceID, vmid)
	}

	// 根据实例类型设置密码
	var setPasswordCmd string
	switch instanceType {
	case "container":
		// LXC容器
		setPasswordCmd = fmt.Sprintf("pct exec %s -- bash -c 'echo \"root:%s\" | chpasswd'", vmid, password)
	case "vm":
		// QEMU虚拟机 - 使用cloud-init设置密码
		// 首先尝试通过cloud-init设置密码
		setPasswordCmd = fmt.Sprintf("qm set %s --cipassword '%s'", vmid, password)

		// 执行设置命令
		_, err := p.sshClient.Execute(setPasswordCmd)
		if err != nil {
			global.APP_LOG.Error("通过cloud-init设置虚拟机密码失败",
				zap.String("instanceID", instanceID),
				zap.String("vmid", vmid),
				zap.Error(err))
			return fmt.Errorf("通过cloud-init设置虚拟机密码失败: %w", err)
		}

		// 检查虚拟机状态，如果已启动则重启以应用密码更改
		statusCmd := fmt.Sprintf("qm status %s", vmid)
		statusOutput, statusErr := p.sshClient.Execute(statusCmd)
		if statusErr == nil && strings.Contains(statusOutput, "status: running") {
			// 虚拟机正在运行，尝试重启以应用密码更改
			restartCmd := fmt.Sprintf("qm reboot %s", vmid)
			_, err = p.sshClient.Execute(restartCmd)
			if err != nil {
				global.APP_LOG.Warn("重启虚拟机应用密码更改失败，可能需要手动重启",
					zap.String("instanceID", instanceID),
					zap.String("vmid", vmid),
					zap.Error(err))
				// 不返回错误，因为密码已经设置，只是可能需要手动重启
			} else {
				global.APP_LOG.Debug("已重启虚拟机以应用密码更改",
					zap.String("instanceID", instanceID),
					zap.String("vmid", vmid))
			}
		} else {
			// 虚拟机未运行，无需重启，密码将在下次启动时生效
			global.APP_LOG.Debug("虚拟机未运行，密码将在启动时生效",
				zap.String("instanceID", instanceID),
				zap.String("vmid", vmid))
		}

		global.APP_LOG.Info("QEMU虚拟机密码设置成功",
			zap.String("instanceID", utils.TruncateString(instanceID, 12)),
			zap.String("vmid", vmid))

		return nil
	default:
		return fmt.Errorf("unsupported instance type: %s", instanceType)
	}

	// 执行密码设置命令
	_, err = p.sshClient.Execute(setPasswordCmd)
	if err != nil {
		global.APP_LOG.Error("设置Proxmox实例密码失败",
			zap.String("instanceID", instanceID),
			zap.String("vmid", vmid),
			zap.String("type", instanceType),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	global.APP_LOG.Info("Proxmox实例密码设置成功",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)),
		zap.String("vmid", vmid),
		zap.String("type", instanceType))

	return nil
}

// configureInstanceSSHPasswordByVMID 专门用于设置Proxmox实例的SSH密码（使用VMID）
func (p *ProxmoxProvider) configureInstanceSSHPasswordByVMID(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	global.APP_LOG.Debug("开始配置Proxmox实例SSH密码",
		zap.String("instanceName", config.Name),
		zap.Int("vmid", vmid))

	// 生成随机密码
	password := p.generateRandomPassword()

	// 从metadata中获取密码，如果有的话
	if config.Metadata != nil {
		if metadataPassword, ok := config.Metadata["password"]; ok && metadataPassword != "" {
			password = metadataPassword
		}
	}

	// 等待实例完全启动并确认状态 - 最多等待90秒
	maxWaitTime := 90 * time.Second
	checkInterval := 10 * time.Second
	startTime := time.Now()
	vmidStr := fmt.Sprintf("%d", vmid)

	// 确定实例类型
	var statusCmd string
	if config.InstanceType == "container" {
		statusCmd = fmt.Sprintf("pct status %s", vmidStr)
	} else {
		statusCmd = fmt.Sprintf("qm status %s", vmidStr)
	}

	// 循环检查实例状态
	isRunning := false
	for {
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待实例启动超时，无法设置密码")
		}

		statusOutput, err := p.sshClient.Execute(statusCmd)
		if err == nil && strings.Contains(statusOutput, "status: running") {
			isRunning = true
			global.APP_LOG.Debug("实例已确认运行，准备设置密码",
				zap.String("instanceName", config.Name),
				zap.Int("vmid", vmid),
				zap.Duration("wait_time", time.Since(startTime)))
			break
		}

		global.APP_LOG.Debug("等待实例启动以设置密码",
			zap.Int("vmid", vmid),
			zap.Duration("elapsed", time.Since(startTime)))

		time.Sleep(checkInterval)
	}

	// 如果是容器，额外等待一些时间确保SSH服务就绪
	if config.InstanceType == "container" && isRunning {
		time.Sleep(3 * time.Second)
	}

	// 设置SSH密码，使用vmid而不是名称
	if err := p.SetInstancePassword(ctx, vmidStr, password); err != nil {
		global.APP_LOG.Error("设置实例密码失败",
			zap.String("instanceName", config.Name),
			zap.Int("vmid", vmid),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	global.APP_LOG.Info("Proxmox实例SSH密码配置成功",
		zap.String("instanceName", config.Name),
		zap.Int("vmid", vmid))

	// 更新数据库中的密码记录，确保数据库与实际密码一致
	err := global.APP_DB.Model(&providerModel.Instance{}).
		Where("name = ?", config.Name).
		Update("password", password).Error
	if err != nil {
		global.APP_LOG.Warn("更新数据库密码记录失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		// 不返回错误，因为SSH密码已经设置成功
	}

	return nil
}

// updateInstanceNotes 更新虚拟机/容器的notes，将配置信息写入到配置文件中
// 完全按照shell项目的方式实现，确保100%行为一致
func (p *ProxmoxProvider) updateInstanceNotes(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	// 根据实例类型确定配置文件路径
	var configPath string
	var instancePrefix string
	if config.InstanceType == "container" {
		configPath = fmt.Sprintf("/etc/pve/lxc/%d.conf", vmid)
		instancePrefix = "ct"
	} else {
		configPath = fmt.Sprintf("/etc/pve/qemu-server/%d.conf", vmid)
		instancePrefix = "vm"
	}

	// 1. 构建data行（字段名）和values行（字段值）
	var dataFields []string
	var valueFields []string

	// 基本信息
	dataFields = append(dataFields, "VMID")
	valueFields = append(valueFields, fmt.Sprintf("%d", vmid))

	if config.Name != "" {
		dataFields = append(dataFields, "用户名-username")
		valueFields = append(valueFields, config.Name)
	}

	// 密码从Metadata中获取
	if password, ok := config.Metadata["password"]; ok && password != "" {
		dataFields = append(dataFields, "密码-password")
		valueFields = append(valueFields, password)
	}

	if config.CPU != "" {
		dataFields = append(dataFields, "CPU核数-CPU")
		valueFields = append(valueFields, config.CPU)
	}

	if config.Memory != "" {
		dataFields = append(dataFields, "内存-memory")
		valueFields = append(valueFields, config.Memory)
	}

	if config.Disk != "" {
		dataFields = append(dataFields, "硬盘-disk")
		valueFields = append(valueFields, config.Disk)
	}

	if config.Image != "" {
		dataFields = append(dataFields, "系统-system")
		valueFields = append(valueFields, config.Image)
	}

	// 存储盘从Metadata中获取
	if storage, ok := config.Metadata["storage"]; ok && storage != "" {
		dataFields = append(dataFields, "存储盘-storage")
		valueFields = append(valueFields, storage)
	}

	// 内网IP
	internalIP := VMIDToInternalIP(vmid)
	if internalIP != "" {
		dataFields = append(dataFields, "内网IP-internal-ip")
		valueFields = append(valueFields, internalIP)
	}

	// 端口信息
	if len(config.Ports) > 0 {
		// 查找SSH端口
		for _, port := range config.Ports {
			parts := strings.Split(port, ":")
			if len(parts) >= 3 {
				hostPort := parts[len(parts)-2]
				guestPart := parts[len(parts)-1]
				guestPort := strings.SplitN(guestPart, "/", 2)[0]
				if guestPort == "22" {
					dataFields = append(dataFields, "SSH端口")
					valueFields = append(valueFields, hostPort)
					break
				}
			} else if len(parts) == 2 {
				hostPort := parts[0]
				guestPart := parts[1]
				guestPort := strings.SplitN(guestPart, "/", 2)[0]
				if guestPort == "22" {
					dataFields = append(dataFields, "SSH端口")
					valueFields = append(valueFields, hostPort)
					break
				}
			}
		}
	}

	// 2. 先将values写入临时文件（类似shell的 echo "$values" > "vm${vm_num}"）
	tmpDataFile := fmt.Sprintf("/tmp/%s%d", instancePrefix, vmid)
	valuesLine := strings.Join(valueFields, " ")

	// 使用echo写入，完全模拟shell行为
	writeValuesCmd := fmt.Sprintf("echo '%s' > %s", valuesLine, tmpDataFile)
	_, err := p.sshClient.Execute(writeValuesCmd)
	if err != nil {
		return fmt.Errorf("写入数据文件失败: %w", err)
	}

	// 3. 构建格式化的输出（模拟shell的for循环）
	tmpFormatFile := fmt.Sprintf("/tmp/temp%d.txt", vmid)

	// 使用echo逐行写入格式化内容
	var formatCommands []string
	formatCommands = append(formatCommands, fmt.Sprintf("> %s", tmpFormatFile)) // 清空文件

	for i := 0; i < len(dataFields); i++ {
		// 每个字段占两行：字段名+值，然后空行
		formatCommands = append(formatCommands,
			fmt.Sprintf("echo '%s %s' >> %s", dataFields[i], valueFields[i], tmpFormatFile))
		formatCommands = append(formatCommands,
			fmt.Sprintf("echo '' >> %s", tmpFormatFile))
	}

	// 执行格式化命令
	for _, cmd := range formatCommands {
		_, err = p.sshClient.Execute(cmd)
		if err != nil {
			global.APP_LOG.Warn("执行格式化命令失败", zap.String("cmd", cmd), zap.Error(err))
		}
	}

	// 4. 给每行添加 # 注释符（完全模拟 sed -i 's/^/# /' ）
	sedCmd := fmt.Sprintf("sed -i 's/^/# /' %s", tmpFormatFile)
	_, err = p.sshClient.Execute(sedCmd)
	if err != nil {
		return fmt.Errorf("添加注释符失败: %w", err)
	}

	// 5. 追加原配置文件内容（完全模拟 cat configPath >> tmpFile）
	catCmd := fmt.Sprintf("cat %s >> %s", configPath, tmpFormatFile)
	_, err = p.sshClient.Execute(catCmd)
	if err != nil {
		return fmt.Errorf("追加配置文件失败: %w", err)
	}

	// 6. 替换原配置文件（完全模拟 cp tmpFile configPath）
	cpCmd := fmt.Sprintf("cp %s %s", tmpFormatFile, configPath)
	_, err = p.sshClient.Execute(cpCmd)
	if err != nil {
		return fmt.Errorf("替换配置文件失败: %w", err)
	}

	// 7. 清理临时文件（完全模拟 rm -rf）
	p.sshClient.Execute(fmt.Sprintf("rm -rf %s", tmpFormatFile))
	p.sshClient.Execute(fmt.Sprintf("rm -rf %s", tmpDataFile))

	global.APP_LOG.Debug("成功更新Proxmox实例notes",
		zap.Int("vmid", vmid),
		zap.String("name", config.Name))

	return nil
}
