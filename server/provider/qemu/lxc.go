package qemu

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/firewall"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *QEMUProvider) sshCreateLXCContainer(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	updateProgress := func(percent int, message string) {
		if progressCallback != nil {
			progressCallback(percent, message)
		}
	}

	if strings.TrimSpace(config.ImageURL) == "" {
		return fmt.Errorf("QEMU/LXC container requires a rootfs image URL")
	}

	name := qemuSafeFileComponent(config.Name)
	rootfs := fmt.Sprintf("%s/%s/rootfs", LXCBaseDir, name)
	imageDir := fmt.Sprintf("%s/images", LXCBaseDir)
	systemName := config.Image
	if strings.TrimSpace(systemName) == "" {
		systemName = "container-rootfs"
	}
	arch := p.config.Architecture
	if strings.TrimSpace(arch) == "" {
		arch = "amd64"
	}
	imageFile := fmt.Sprintf("%s/%s-%s.tar", imageDir, qemuSafeFileComponent(systemName), qemuSafeFileComponent(arch))
	password := "password"
	if pw, ok := config.Metadata["password"]; ok && pw != "" {
		password = pw
	}

	cpu, _ := strconv.Atoi(config.CPU)
	if cpu <= 0 {
		cpu = 1
	}
	memoryMB := parseConfigMB(config.Memory)
	if memoryMB <= 0 {
		memoryMB = 512
	}

	sshPort := 0
	startPort := 0
	endPort := 0
	if len(config.Ports) >= 1 {
		sshPort, _ = strconv.Atoi(config.Ports[0])
	}
	if len(config.Ports) >= 2 {
		startPort, _ = strconv.Atoi(config.Ports[1])
	}
	if len(config.Ports) >= 3 {
		endPort, _ = strconv.Atoi(config.Ports[2])
	}

	updateProgress(5, "检查 libvirt-lxc 环境")
	preflightCmd := "command -v virsh >/dev/null 2>&1 && virsh -c lxc:/// uri >/dev/null 2>&1 && (command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1)"
	if output, err := p.sshClient.Execute(preflightCmd + " 2>&1"); err != nil {
		return fmt.Errorf("libvirt-lxc is not available or curl/wget is missing: %s, %w", utils.TruncateString(output, 300), err)
	}

	updateProgress(10, "准备 default 网络和容器目录")
	p.sshClient.Execute("virsh -c qemu:///system net-start default 2>/dev/null || true")
	p.sshClient.Execute("virsh -c qemu:///system net-autostart default 2>/dev/null || true")
	p.sshClient.Execute(fmt.Sprintf("mkdir -p %s %s", shellSingleQuote(rootfs), shellSingleQuote(imageDir)))

	updateProgress(15, "获取 LXC 根文件系统镜像")
	checkOutput, _ := p.sshClient.Execute(fmt.Sprintf("test -s %s && echo exists", shellSingleQuote(imageFile)))
	if strings.TrimSpace(checkOutput) != "exists" {
		tmpPath := imageFile + ".download"
		downloadURL := config.ImageURL
		if config.UseCDN && qemuIsGitHubURL(downloadURL) {
			downloadURL = utils.GetBaseCDNEndpoint() + downloadURL
		}
		downloadCmd := fmt.Sprintf("if command -v curl >/dev/null 2>&1; then curl -4 -fL --connect-timeout 30 --max-time 900 -o %s %s; else wget -4 -O %s %s; fi", shellSingleQuote(tmpPath), shellSingleQuote(downloadURL), shellSingleQuote(tmpPath), shellSingleQuote(downloadURL))
		output, err := p.sshClient.ExecuteWithTimeout(downloadCmd+" 2>&1", 20*time.Minute)
		if err != nil && config.UseCDN && downloadURL != config.ImageURL {
			p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(tmpPath)))
			downloadCmd = fmt.Sprintf("if command -v curl >/dev/null 2>&1; then curl -4 -fL --connect-timeout 30 --max-time 900 -o %s %s; else wget -4 -O %s %s; fi", shellSingleQuote(tmpPath), shellSingleQuote(config.ImageURL), shellSingleQuote(tmpPath), shellSingleQuote(config.ImageURL))
			output, err = p.sshClient.ExecuteWithTimeout(downloadCmd+" 2>&1", 20*time.Minute)
		}
		if err != nil {
			p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(tmpPath)))
			return fmt.Errorf("failed to download LXC rootfs: %s, %w", utils.TruncateString(output, 300), err)
		}
		if _, err := p.sshClient.Execute(fmt.Sprintf("test -s %s && mv %s %s", shellSingleQuote(tmpPath), shellSingleQuote(tmpPath), shellSingleQuote(imageFile))); err != nil {
			p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(tmpPath)))
			return fmt.Errorf("downloaded LXC rootfs is empty or cannot be moved: %w", err)
		}
	}

	updateProgress(30, "解包 LXC 根文件系统")
	p.sshClient.Execute(fmt.Sprintf("rm -rf %s && mkdir -p %s", shellSingleQuote(rootfs), shellSingleQuote(rootfs)))
	extractCmd := fmt.Sprintf("tar -xf %s -C %s --numeric-owner 2>&1", shellSingleQuote(imageFile), shellSingleQuote(rootfs))
	if output, err := p.sshClient.ExecuteWithTimeout(extractCmd, 20*time.Minute); err != nil {
		p.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(fmt.Sprintf("%s/%s", LXCBaseDir, name))))
		return fmt.Errorf("failed to extract LXC rootfs: %s, %w", utils.TruncateString(output, 300), err)
	}

	updateProgress(45, "配置容器账户和网络")
	if password != "" {
		p.sshClient.Execute(fmt.Sprintf("chroot %s /bin/sh -c %s 2>/dev/null || true", shellSingleQuote(rootfs), shellSingleQuote("echo root:"+password+" | chpasswd")))
	}
	macOutput, err := p.sshClient.Execute("printf '52:54:%02x:%02x:%02x:%02x\n' $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256))")
	if err != nil {
		return fmt.Errorf("failed to generate MAC address: %w", err)
	}
	containerMAC := strings.TrimSpace(macOutput)
	p.ipMu.Lock()
	containerIP, err := p.allocateIP()
	if err != nil {
		p.ipMu.Unlock()
		return fmt.Errorf("failed to allocate LXC IP: %w", err)
	}
	p.setupDHCPReservation(config.Name, containerMAC, containerIP)
	p.ipMu.Unlock()

	updateProgress(55, "配置端口转发")
	fwMgr := firewall.NewManager(p.sshClient, NFTTableName, InternalSubnet)
	if _, err := fwMgr.DetectBackend(FWBackendFile); err != nil {
		return fmt.Errorf("防火墙后端检测失败: %w", err)
	}
	if err := fwMgr.InitTable(); err != nil {
		return fmt.Errorf("防火墙初始化失败: %w", err)
	}
	if sshPort > 0 {
		if err := fwMgr.AddDNAT(config.Name, containerIP, sshPort, startPort, endPort); err != nil {
			return fmt.Errorf("端口转发规则添加失败: %w", err)
		}
	}
	fwMgr.SaveRules()

	updateProgress(70, "定义 libvirt-lxc 容器")
	xmlPath := fmt.Sprintf("/tmp/oneclickvirt-lxc-%s.xml", name)
	emulatorCmd := "command -v libvirt_lxc 2>/dev/null || find /usr/lib /usr/lib64 /usr/libexec -name libvirt_lxc 2>/dev/null | head -1 || echo /usr/libexec/libvirt_lxc"
	emulatorOutput, _ := p.sshClient.Execute(emulatorCmd)
	emulator := strings.TrimSpace(emulatorOutput)
	if emulator == "" {
		emulator = "/usr/libexec/libvirt_lxc"
	}
	xml := fmt.Sprintf(`<domain type='lxc'>
  <name>%s</name>
  <memory unit='MiB'>%d</memory>
  <currentMemory unit='MiB'>%d</currentMemory>
  <vcpu placement='static'>%d</vcpu>
  <os>
    <type arch='x86_64'>exe</type>
    <init>/sbin/init</init>
  </os>
  <features>
    <privnet/>
  </features>
  <devices>
    <emulator>%s</emulator>
    <filesystem type='mount' accessmode='passthrough'>
      <source dir='%s'/>
      <target dir='/'/>
    </filesystem>
    <interface type='network'>
      <mac address='%s'/>
      <source network='default'/>
    </interface>
    <console type='pty'/>
  </devices>
</domain>
`, xmlEscape(config.Name), memoryMB, memoryMB, cpu, xmlEscape(emulator), xmlEscape(rootfs), xmlEscape(containerMAC))
	if err := p.sshClient.UploadContent(xml, xmlPath, 0600); err != nil {
		return fmt.Errorf("failed to upload LXC XML: %w", err)
	}
	defineCmd := fmt.Sprintf("virsh -c lxc:/// define %s 2>&1", shellSingleQuote(xmlPath))
	if output, err := p.sshClient.Execute(defineCmd); err != nil {
		p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(xmlPath)))
		return fmt.Errorf("failed to define LXC container: %s, %w", utils.TruncateString(output, 500), err)
	}
	p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(xmlPath)))

	updateProgress(85, "启动 libvirt-lxc 容器")
	if output, err := p.sshClient.Execute(fmt.Sprintf("virsh -c lxc:/// start %s 2>&1", shellSingleQuote(config.Name))); err != nil {
		p.sshDeleteLXCContainer(context.Background(), config.Name)
		return fmt.Errorf("failed to start LXC container: %s, %w", utils.TruncateString(output, 500), err)
	}
	p.applyLibvirtIOLimits(ctx, "lxc:///", config.Name, "", config)
	p.sshClient.Execute(fmt.Sprintf("virsh -c lxc:/// autostart %s 2>/dev/null || true", shellSingleQuote(config.Name)))

	logLine := fmt.Sprintf("%s %d *** %d %d 0 %d %d %s %s", config.Name, sshPort, cpu, memoryMB, startPort, endPort, systemName, containerIP)
	p.sshClient.Execute(fmt.Sprintf("printf '%%s\\n' %s >> %s", shellSingleQuote(logLine), shellSingleQuote(VMLogDir)))

	updateProgress(100, "QEMU/LXC容器创建完成")
	global.APP_LOG.Info("QEMU/LXC容器创建成功", zap.String("name", config.Name), zap.String("ip", containerIP))
	return nil
}

func (p *QEMUProvider) isLXCInstance(id string) bool {
	_, err := p.sshClient.Execute(fmt.Sprintf("virsh -c lxc:/// dominfo %s >/dev/null 2>&1", shellSingleQuote(id)))
	return err == nil
}

func (p *QEMUProvider) sshDeleteLXCContainer(ctx context.Context, id string) error {
	global.APP_LOG.Info("开始删除QEMU/LXC容器", zap.String("id", utils.TruncateString(id, 32)))
	p.sshClient.Execute(fmt.Sprintf("virsh -c lxc:/// destroy %s 2>/dev/null || true", shellSingleQuote(id)))
	containerIP := p.getVMIPAddress(ctx, id)
	fwMgr := firewall.NewManager(p.sshClient, NFTTableName, InternalSubnet)
	if _, err := fwMgr.DetectBackend(FWBackendFile); err == nil {
		if fwMgr.GetBackend() == firewall.BackendNft {
			fwMgr.DeleteRulesByComment(fmt.Sprintf("vm:%s", id))
		}
		if containerIP != "" {
			fwMgr.DeleteRulesByIP(containerIP)
		}
		fwMgr.SaveRules()
	}
	p.removeDHCPReservation(id, containerIP)
	p.sshClient.Execute(fmt.Sprintf("virsh -c lxc:/// undefine %s 2>/dev/null || true", shellSingleQuote(id)))
	p.sshClient.Execute(fmt.Sprintf("rm -rf %s 2>/dev/null || true", shellSingleQuote(fmt.Sprintf("%s/%s", LXCBaseDir, qemuSafeFileComponent(id)))))
	p.sshClient.Execute(fmt.Sprintf("grep -v '^%s ' %s > %s.tmp 2>/dev/null && mv %s.tmp %s 2>/dev/null || true", utils.SanitizeShellArg(id), VMLogDir, VMLogDir, VMLogDir, VMLogDir))
	return nil
}

func qemuIsGitHubURL(rawURL string) bool {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	return strings.Contains(lower, "github.com") || strings.Contains(lower, "githubusercontent.com")
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
