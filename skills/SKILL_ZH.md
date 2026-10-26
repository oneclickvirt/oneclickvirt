---
name: oneclickvirt
description: OneClickVirt 操作 Skill，通过 MCP 管理容器、虚拟机、节点、健康检查和监控指标。
metadata:
  version: "0.3.0"
  author: oneclickvirt
  homepage: https://github.com/oneclickvirt/oneclickvirt
---

# OneClickVirt Skill

## 概述

此 Skill 帮助 AI 助手通过 OneClickVirt 内置 MCP Server 操作 OneClickVirt 部署，适用于实例管理、节点巡检、平台健康检查和故障排查。

此 Skill 可以完成：

- 列出、创建、启动、停止、重启、查看和删除实例。
- 列出节点并执行节点健康检查。
- 读取系统状态、健康状态、配置和监控资源。
- 使用提示模板完成容器、虚拟机、状态检查和故障排查工作流。

## 前置条件

- 安装器需要 Python 3.9 以上版本。预编译 MCP 客户端支持 Linux/Windows 的 amd64 和 ARM64；其他平台使用已有二进制或 SSH 连接。
- OneClickVirt API 正在运行。
- 从 Web 界面个人中心的 API Tokens 获取管理员 API Token。
- 远程部署必须使用 HTTPS。

## 安装

从 GitHub 安装或更新最新正式版：

```bash
curl -fL --retry 3 https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/main/scripts/install_skill.py -o install_skill.py
python3 install_skill.py --client claude
```

也可从 CNB 公共源码镜像下载安装器：

```bash
curl -fL --retry 3 https://cnb.cool/oneclickvirt/oneclickvirt/-/git/raw/main/scripts/install_skill.py -o install_skill.py
python3 install_skill.py --client claude
```

使用其他客户端时，将参数改为 `--client codex` 或 `--client openclaw`。默认目录分别为 `~/.claude/skills/oneclickvirt`、`~/.agents/skills/oneclickvirt` 和 `~/.openclaw/skills/oneclickvirt`，也可用 `--skill-dir` 指定目录。Windows 下载同一个脚本，使用 Python 3.9 以上版本执行 `py -3 install_skill.py --client claude`。

两个入口都会解析一次 GitHub 正式 Release，校验同版本 Skill 和二进制后一起安装。CNB 提供源码镜像，Release 压缩包仍从 GitHub 下载。安装后 `SKILL.md` 直接位于 Skill 目录，独立 MCP 客户端 `oneclickvirt-mcp` 放在 `~/.local/bin`。安装器不启动主控，不修改系统服务、数据库、客户端设置或已有 API Token。

后续更新时重复下载及安装命令即可。需要固定或返回某个版本时，用 `--version` 指定完整的已发布标签，Skill 和二进制会使用同一版本；已安装版本记录在 `install.json`。复用已有主控二进制时可加 `--mcp-command /opt/oneclickvirt/server/oneclickvirt-server`，只安装 Skill 文件时加 `--skip-binary`。仅安装 Skill 不会自动配置或启动 MCP。

将安装目录中 `mcp.example.json` 的 `mcpServers` 配置合并到客户端设置，保留其中的二进制绝对路径，填写 API 地址和 Token。对应模板为：

```json
{
  "mcpServers": {
    "oneclickvirt": {
      "command": "oneclickvirt-mcp",
      "args": ["mcp"],
      "env": {
        "ONE_CLICK_VIRT_API_URL": "https://your-domain.com",
        "ONE_CLICK_VIRT_API_TOKEN": "你的API_TOKEN"
      }
    }
  }
}
```

客户端必须能够找到该命令，图形界面客户端可能不会继承终端的 `PATH`。项目安装的主控路径为 `/opt/oneclickvirt/server/oneclickvirt-server`，不是 `/opt/oneclickvirt/server/oneclickvirt`。macOS 没有预编译主控产物，可用 `--skip-binary` 安装 Skill，再配置已有的原生编译版本或到 Linux 的 SSH MCP 连接。配置后重启 MCP 客户端。压缩包安装及 SSH 示例见[安装说明](https://www.spiritlhl.net/guide/oneclickvirt/oneclickvirt_advanced_install.html#skill-与-mcp)。

## 使用方法

可用 MCP Tools：

- `list_instances` — 列出所有虚拟机和容器实例
- `create_instance` — 创建新的虚拟机或容器实例
- `start_instance` — 启动已停止的实例
- `stop_instance` — 停止正在运行的实例
- `restart_instance` — 重启实例
- `delete_instance` — 删除实例（危险：不可逆）
- `get_instance_detail` — 获取指定实例的详细信息
- `list_providers` — 列出所有虚拟化节点
- `health_check` — 对节点执行健康检查
- `get_instance_logs` — 获取实例的最近日志
- `get_system_status` — 获取系统整体状态，包括实例计数和资源用量
- `get_metrics` — 获取系统、节点或实例的监控指标

可用资源：

- `oneclickvirt://instances/list` — 当前所有实例的列表
- `oneclickvirt://providers/list` — 当前所有节点的列表
- `oneclickvirt://system/status` — 系统整体健康和指标
- `oneclickvirt://health/status` — 公开的 OneClickVirt API 健康状态
- `oneclickvirt://config/system` — 管理员可见的系统配置

可用 Prompt 模板：

- `create_debian_container` — 用于创建 Debian Linux 容器的模板
- `create_ubuntu_vm` — 用于创建 Ubuntu 虚拟机的模板
- `troubleshoot_instance` — 用于排查无法启动实例的模板
- `quick_status_check` — 用于检查实例、节点和平台健康的模板

## 示例

- “列出运行中的实例，并总结冻结或停止的异常项。”
- “在节点 1 创建 Debian 12 容器，2 核 CPU、1 GB 内存、20 GB 磁盘。”
- “快速检查平台状态。”
- “检查节点 3 健康状态并总结容量风险。”
- “排查实例 42，并建议最安全的下一步操作。”

## 故障排查

- `command not found: oneclickvirt-mcp`：使用已安装的 `mcp.example.json` 中的二进制绝对路径配置客户端。
- `401 Unauthorized`：确认 `ONE_CLICK_VIRT_API_TOKEN` 是有效管理员 Token。
- `Connection refused`：确认 `ONE_CLICK_VIRT_API_URL` 正确且 `/api/v1/health` 可访问。
- 工具未出现：重启 MCP 客户端并检查 MCP Server 日志。

## 安全性

- 优先使用环境变量传递 Token，避免放入命令行参数。
- 使用专用 Token，并授予最小必要权限。
- 对 `delete_instance` 等破坏性请求先审阅再确认。
- 远程 API 端点必须使用 HTTPS。

