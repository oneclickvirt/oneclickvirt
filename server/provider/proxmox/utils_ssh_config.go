package proxmox

import (
	"fmt"
	"strings"
)

func (p *ProxmoxProvider) configureAlpineSSH(vmid int) {
	commands := []string{
		// 更新包管理器
		"apk update",
		// 安装必要软件
		"apk add --no-cache openssh-server",
		"apk add --no-cache sshpass",
		"apk add --no-cache openssh-keygen",
		"apk add --no-cache bash",
		"apk add --no-cache curl",
		"apk add --no-cache wget",
		// 生成SSH密钥
		"sh -c \"cd /etc/ssh && ssh-keygen -A\"",
		// 配置sshd_config - 使用chattr解锁
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i '/^#PermitRootLogin\\|PermitRootLogin/c PermitRootLogin yes' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i '/^#AddressFamily\\|AddressFamily/c AddressFamily any' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?\\(Port\\).*/\\1 22/' /etc/ssh/sshd_config",
		"sed -i '/^#UsePAM\\|UsePAM/c #UsePAM no' /etc/ssh/sshd_config",
		// 配置cloud-init
		"sed -E -i 's/preserve_hostname:[[:space:]]*false/preserve_hostname: true/g' /etc/cloud/cloud.cfg 2>/dev/null || true",
		"sed -E -i 's/disable_root:[[:space:]]*true/disable_root: false/g' /etc/cloud/cloud.cfg 2>/dev/null || true",
		"sed -E -i 's/ssh_pwauth:[[:space:]]*false/ssh_pwauth:   true/g' /etc/cloud/cloud.cfg 2>/dev/null || true",
		// 启动SSH服务
		"/usr/sbin/sshd",
		"rc-update add sshd default",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
	}

	p.executeContainerCommands(vmid, commands, "Alpine")
}

// configureOpenWrtSSH 配置OpenWrt容器SSH
func (p *ProxmoxProvider) configureOpenWrtSSH(vmid int) {
	commands := []string{
		// 更新包管理器
		"opkg update",
		// 安装必要软件
		"opkg install openssh-server",
		"opkg install bash",
		"opkg install openssh-keygen",
		"opkg install shadow-chpasswd",
		"opkg install chattr",
		// 生成SSH密钥
		"sh -c \"cd /etc/ssh && ssh-keygen -A\"",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"/etc/init.d/sshd enable",
		"/etc/init.d/sshd start",
	}

	p.executeContainerCommands(vmid, commands, "OpenWrt")
}

// configureArchSSH 配置Arch容器SSH
func (p *ProxmoxProvider) configureArchSSH(vmid int) {
	commands := []string{
		// 初始化GPG密钥
		"sh -c \"rm -rf /etc/pacman.d/gnupg/\"",
		"pacman-key --init",
		"pacman-key --populate archlinux",
		// 更新系统
		"pacman -Syyuu --noconfirm",
		// 安装必要软件
		"pacman -Sy --needed --noconfirm openssh",
		"pacman -Sy --needed --noconfirm bash",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"systemctl enable sshd",
		"systemctl start sshd",
	}

	p.executeContainerCommands(vmid, commands, "Arch")
}

// configureDebianBasedSSH 配置Debian/Ubuntu等基于APT的系统SSH
func (p *ProxmoxProvider) configureDebianBasedSSH(vmid int) {
	commands := []string{
		// 检查并确认APT
		"sh -c \"apt-get update 2>&1 | tee /tmp/apt_fix.txt\"",
		"sh -c \"if grep -q 'NO_PUBKEY' /tmp/apt_fix.txt; then public_keys=$(grep -oE 'NO_PUBKEY [0-9A-F]+' /tmp/apt_fix.txt | awk '{ print $2 }' | paste -sd ' '); apt-key adv --keyserver keyserver.ubuntu.com --recv-keys $public_keys; apt-get update; fi\"",
		// 确认损坏的包
		"apt-get --fix-broken install -y",
		// 更新包列表
		"apt-get update -y",
		// 安装必要软件
		"apt-get install -y openssh-server sshpass curl",
		// 生成SSH密钥
		"ssh-keygen -A",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^#UsePAM\\|UsePAM/c #UsePAM no' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		"sed -i 's/^#[[:space:]]*KbdInteractiveAuthentication.*\\|^KbdInteractiveAuthentication.*/KbdInteractiveAuthentication yes/' /etc/ssh/sshd_config",
		// 处理sshd_config.d目录中的配置文件
		"sh -c \"if [ -d /etc/ssh/sshd_config.d ]; then for file in /etc/ssh/sshd_config.d/*; do if [ -f \\\"$file\\\" ] && grep -q 'PasswordAuthentication no' \\\"$file\\\"; then sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/g' \\\"$file\\\"; fi; done; fi\"",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"systemctl enable ssh 2>/dev/null || systemctl enable sshd 2>/dev/null || true",
		"systemctl start ssh 2>/dev/null || systemctl start sshd 2>/dev/null || service ssh start || service sshd start",
		// 配置IPv6优先级
		"sed -i 's/.*precedence ::ffff:0:0\\/96.*/precedence ::ffff:0:0\\/96  100/g' /etc/gai.conf",
		// 设置motd
		"sh -c \"if [ -f /etc/motd ]; then echo '' > /etc/motd; echo 'Related repo https://github.com/oneclickvirt/pve' >> /etc/motd; echo '--by https://t.me/spiritlhl' >> /etc/motd; fi\"",
	}

	p.executeContainerCommands(vmid, commands, "Debian-based")
}

// configureRHELBasedSSH 配置RHEL/CentOS/Fedora等基于YUM/DNF的系统SSH
func (p *ProxmoxProvider) configureRHELBasedSSH(vmid int) {
	// 检测使用yum还是dnf
	checkDnfCmd := fmt.Sprintf("pct exec %d -- sh -c \"command -v dnf >/dev/null 2>&1 && echo 'dnf' || echo 'yum'\"", vmid)
	output, _ := p.sshClient.Execute(checkDnfCmd)
	pkgCmd := strings.TrimSpace(output)
	if pkgCmd == "" {
		pkgCmd = "yum" // 默认使用yum
	}

	commands := []string{
		// 更新包管理器
		fmt.Sprintf("%s -y update", pkgCmd),
		// 安装必要软件
		fmt.Sprintf("%s -y install openssh-server curl", pkgCmd),
		// 生成SSH密钥
		"ssh-keygen -A",
		// 停止防火墙服务
		"service iptables stop 2>/dev/null || true",
		"chkconfig iptables off 2>/dev/null || true",
		// 禁用SELinux
		"sh -c \"if [ -f /etc/sysconfig/selinux ]; then sed -i.bak '/^SELINUX=/cSELINUX=disabled' /etc/sysconfig/selinux; fi\"",
		"sh -c \"if [ -f /etc/selinux/config ]; then sed -i.bak '/^SELINUX=/cSELINUX=disabled' /etc/selinux/config; fi\"",
		"setenforce 0 2>/dev/null || true",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^#UsePAM\\|UsePAM/c #UsePAM no' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		"sed -i 's/^#[[:space:]]*KbdInteractiveAuthentication.*\\|^KbdInteractiveAuthentication.*/KbdInteractiveAuthentication yes/' /etc/ssh/sshd_config",
		// 处理sshd_config.d目录中的配置文件
		"sh -c \"if [ -d /etc/ssh/sshd_config.d ]; then for file in /etc/ssh/sshd_config.d/*; do if [ -f \\\"$file\\\" ] && grep -q 'PasswordAuthentication no' \\\"$file\\\"; then sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/g' \\\"$file\\\"; fi; done; fi\"",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"systemctl enable sshd 2>/dev/null || service sshd enable 2>/dev/null || true",
		"systemctl start sshd 2>/dev/null || service sshd start",
		// 配置IPv6优先级
		"sed -i 's/.*precedence ::ffff:0:0\\/96.*/precedence ::ffff:0:0\\/96  100/g' /etc/gai.conf",
		// 设置motd
		"sh -c \"if [ -f /etc/motd ]; then echo '' > /etc/motd; echo 'Related repo https://github.com/oneclickvirt/pve' >> /etc/motd; echo '--by https://t.me/spiritlhl' >> /etc/motd; fi\"",
	}

	p.executeContainerCommands(vmid, commands, "RHEL-based")
}

// configureOpenSUSESSH 配置openSUSE系统SSH
func (p *ProxmoxProvider) configureOpenSUSESSH(vmid int) {
	commands := []string{
		// 更新包管理器
		"zypper update -y",
		// 安装必要软件
		"zypper install -y openssh-server curl",
		// 生成SSH密钥
		"ssh-keygen -A",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^#UsePAM\\|UsePAM/c #UsePAM no' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		"sed -i 's/^#[[:space:]]*KbdInteractiveAuthentication.*\\|^KbdInteractiveAuthentication.*/KbdInteractiveAuthentication yes/' /etc/ssh/sshd_config",
		// 处理sshd_config.d目录中的配置文件
		"sh -c \"if [ -d /etc/ssh/sshd_config.d ]; then for file in /etc/ssh/sshd_config.d/*; do if [ -f \\\"$file\\\" ] && grep -q 'PasswordAuthentication no' \\\"$file\\\"; then sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/g' \\\"$file\\\"; fi; done; fi\"",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"systemctl enable sshd",
		"systemctl start sshd",
		// 配置IPv6优先级
		"sed -i 's/.*precedence ::ffff:0:0\\/96.*/precedence ::ffff:0:0\\/96  100/g' /etc/gai.conf",
		// 设置motd
		"sh -c \"if [ -f /etc/motd ]; then echo '' > /etc/motd; echo 'Related repo https://github.com/oneclickvirt/pve' >> /etc/motd; echo '--by https://t.me/spiritlhl' >> /etc/motd; fi\"",
	}

	p.executeContainerCommands(vmid, commands, "openSUSE")
}
