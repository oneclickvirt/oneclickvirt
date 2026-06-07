package utils

// StandardExtendedPath 是统一的环境变量 PATH 扩展值。
// 包含所有常见的系统二进制路径，确保通过 snap、/opt、/usr/local 等非标准
// 位置安装的命令（如 lxc、lxd、incus）在任何执行上下文中都能被找到。
//
// 路径列表（按优先级排序）：
//
//	/usr/local/sbin — 本地编译的系统管理工具
//	/usr/local/bin  — 本地编译的用户工具
//	/usr/sbin       — 系统管理工具
//	/usr/bin        — 标准用户工具
//	/sbin           — 基础系统管理工具
//	/bin            — 基础用户工具
//	/snap/bin       — Snap 包管理器安装的工具（LXD 5.x+）
//	/var/lib/snapd/snap/bin — Snap 守护进程内部路径
//	/opt/bin        — 可选软件包路径
const StandardExtendedPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin:/var/lib/snapd/snap/bin:/opt/bin"

// envLoadScript 是完整的环境加载 shell 脚本片段。
// 设计原则：
//  1. 使用 POSIX 兼容语法（. 而非 source），兼容 sh/bash/zsh
//  2. 设置 LC_ALL=C.UTF-8 防止 locale 编码问题和警告噪音
//  3. 设置 PS1 伪装交互式 shell，绕过 bashrc 中 [[ $- != *i* ]] 守卫
//  4. 加载所有可能的系统级和用户级环境文件，静默失败不中断
//  5. 加载顺序：系统级 → 用户级 → PATH 补全
//
// 加载的环境文件清单：
//
//	系统级：
//	  /etc/environment          — systemd/pam 环境变量
//	  /etc/profile              — POSIX 系统登录 profile
//	  /etc/profile.d/*.sh       — 模块化 profile 扩展（RHEL/Debian）
//	  /etc/bash.bashrc          — Debian/Ubuntu 系统 bashrc
//	  /etc/bashrc               — RHEL/CentOS 系统 bashrc
//	  /etc/zsh/zprofile         — 系统 zsh profile
//	  /etc/zsh/zshrc            — 系统 zsh rc
//
//	用户级（静默失败）：
//	  ~/.profile                — POSIX 用户 profile
//	  ~/.bash_profile           — bash 登录 profile
//	  ~/.bashrc                 — bash 交互式 rc（PS1 伪装绕过守卫）
//	  ~/.zprofile               — zsh 登录 profile
//	  ~/.zshrc                  — zsh 交互式 rc
//
//	环境扩展：
//	  ~/.config/environment.d/*.conf — systemd 用户环境片段
const envLoadScript = "" +
	// 0. 设置安全 locale，防止任何命令产生 locale 警告或编码乱码
	"export LC_ALL=C.UTF-8 LANG=C.UTF-8 LANGUAGE=C.UTF-8 2>/dev/null || true; " +
	// 1. 伪装交互式 shell，绕过 bashrc/zshrc 中的 [[ $- != *i* ]] 和 [ -z "$PS1" ] 守卫
	"export PS1='$ ' 2>/dev/null || true; " +
	// 2. 系统级环境文件（POSIX 标准顺序）
	"[ -f /etc/environment ] && . /etc/environment 2>/dev/null || true; " +
	"[ -f /etc/profile ] && . /etc/profile >/dev/null 2>&1 || true; " +
	"[ -d /etc/profile.d ] && for f in /etc/profile.d/*.sh; do [ -r \"$f\" ] && . \"$f\" >/dev/null 2>&1 || true; done 2>/dev/null || true; " +
	// 3. bash 系统级 rc（Debian/Ubuntu 和 RHEL/CentOS）
	"[ -f /etc/bash.bashrc ] && . /etc/bash.bashrc >/dev/null 2>&1 || true; " +
	"[ -f /etc/bashrc ] && . /etc/bashrc >/dev/null 2>&1 || true; " +
	// 4. zsh 系统级配置（如果存在，尽力加载）
	"[ -f /etc/zsh/zprofile ] && . /etc/zsh/zprofile >/dev/null 2>&1 || true; " +
	"[ -f /etc/zsh/zshrc ] && . /etc/zsh/zshrc >/dev/null 2>&1 || true; " +
	// 5. POSIX 用户 profile
	"[ -f ~/.profile ] && . ~/.profile >/dev/null 2>&1 || true; " +
	// 6. bash 用户级配置（PS1 已伪装交互式，所以 bashrc 不会因非交互而退出）
	"[ -f ~/.bash_profile ] && . ~/.bash_profile >/dev/null 2>&1 || true; " +
	"[ -f ~/.bashrc ] && . ~/.bashrc >/dev/null 2>&1 || true; " +
	// 7. zsh 用户级配置（尽力加载，zsh 语法在 sh 下可能报错，静默忽略）
	"[ -f ~/.zprofile ] && . ~/.zprofile >/dev/null 2>&1 || true; " +
	"[ -f ~/.zshrc ] && . ~/.zshrc >/dev/null 2>&1 || true; " +
	// 8. systemd 用户环境片段（较新的 systemd 版本支持）
	"[ -d ~/.config/environment.d ] && for f in ~/.config/environment.d/*.conf; do [ -r \"$f\" ] && . \"$f\" >/dev/null 2>&1 || true; done 2>/dev/null || true; " +
	// 9. 最终 PATH：StandardExtendedPath 前置 + 继承现有 PATH
	"export PATH=" + StandardExtendedPath + "${PATH:+:$PATH}; "

// BuildEnvCommand 构建统一的命令环境包装前缀（含用户级配置）。
// 适用于 SSH 远程执行场景，用户级配置通常可安全加载。
func BuildEnvCommand(command string) string {
	return envLoadScript + command
}

// BuildEnvCommandNoUser 构建环境命令包装前缀（仅系统级配置）。
// 适用于 Agent WebSocket 代理执行场景：
//   - 避免加载 ~/.bashrc 中可能的长时间运行命令（如 `fortune`, `neofetch` 等）
//   - 避免 zsh 语法在 sh 下产生过多警告噪音
//   - 仅加载系统级文件和 POSIX 用户 profile（~/.profile 不含交互式命令）
func BuildEnvCommandNoUser(command string) string {
	// 仅加载系统级 + POSIX 用户 profile（不含 bashrc/zshrc）
	return "" +
		// 安全 locale
		"export LC_ALL=C.UTF-8 LANG=C.UTF-8 LANGUAGE=C.UTF-8 2>/dev/null || true; " +
		// 系统级环境文件
		"[ -f /etc/environment ] && . /etc/environment 2>/dev/null || true; " +
		"[ -f /etc/profile ] && . /etc/profile >/dev/null 2>&1 || true; " +
		"[ -d /etc/profile.d ] && for f in /etc/profile.d/*.sh; do [ -r \"$f\" ] && . \"$f\" >/dev/null 2>&1 || true; done 2>/dev/null || true; " +
		"[ -f /etc/bash.bashrc ] && . /etc/bash.bashrc >/dev/null 2>&1 || true; " +
		"[ -f /etc/bashrc ] && . /etc/bashrc >/dev/null 2>&1 || true; " +
		"[ -f /etc/zsh/zprofile ] && . /etc/zsh/zprofile >/dev/null 2>&1 || true; " +
		"[ -f /etc/zsh/zshrc ] && . /etc/zsh/zshrc >/dev/null 2>&1 || true; " +
		// POSIX 用户 profile（不含 bashrc/zshrc，避免交互式命令）
		"[ -f ~/.profile ] && . ~/.profile >/dev/null 2>&1 || true; " +
		"[ -f ~/.bash_profile ] && . ~/.bash_profile >/dev/null 2>&1 || true; " +
		// systemd 用户环境
		"[ -d ~/.config/environment.d ] && for f in ~/.config/environment.d/*.conf; do [ -r \"$f\" ] && . \"$f\" >/dev/null 2>&1 || true; done 2>/dev/null || true; " +
		// 最终 PATH
		"export PATH=" + StandardExtendedPath + "${PATH:+:$PATH}; " +
		command
}

// EnvPrefixShell 返回仅包含环境加载的 shell 前缀（不含命令）。
// 用于需要自定义后续命令的场景。使用完整的环境加载（含用户级）。
const EnvPrefixShell = "" +
	"export LC_ALL=C.UTF-8 LANG=C.UTF-8 LANGUAGE=C.UTF-8 2>/dev/null || true; " +
	"export PS1='$ ' 2>/dev/null || true; " +
	"[ -f /etc/environment ] && . /etc/environment 2>/dev/null || true; " +
	"[ -f /etc/profile ] && . /etc/profile >/dev/null 2>&1 || true; " +
	"[ -d /etc/profile.d ] && for f in /etc/profile.d/*.sh; do [ -r \"$f\" ] && . \"$f\" >/dev/null 2>&1 || true; done 2>/dev/null || true; " +
	"[ -f /etc/bash.bashrc ] && . /etc/bash.bashrc >/dev/null 2>&1 || true; " +
	"[ -f /etc/bashrc ] && . /etc/bashrc >/dev/null 2>&1 || true; " +
	"[ -f /etc/zsh/zprofile ] && . /etc/zsh/zprofile >/dev/null 2>&1 || true; " +
	"[ -f /etc/zsh/zshrc ] && . /etc/zsh/zshrc >/dev/null 2>&1 || true; " +
	"[ -f ~/.profile ] && . ~/.profile >/dev/null 2>&1 || true; " +
	"[ -f ~/.bash_profile ] && . ~/.bash_profile >/dev/null 2>&1 || true; " +
	"[ -f ~/.bashrc ] && . ~/.bashrc >/dev/null 2>&1 || true; " +
	"[ -f ~/.zprofile ] && . ~/.zprofile >/dev/null 2>&1 || true; " +
	"[ -f ~/.zshrc ] && . ~/.zshrc >/dev/null 2>&1 || true; " +
	"[ -d ~/.config/environment.d ] && for f in ~/.config/environment.d/*.conf; do [ -r \"$f\" ] && . \"$f\" >/dev/null 2>&1 || true; done 2>/dev/null || true; " +
	"export PATH=" + StandardExtendedPath + "${PATH:+:$PATH}; "
