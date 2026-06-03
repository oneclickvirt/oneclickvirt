---
name: oneclickvirt
description: OneClickVirt 操作 Skill，通过 MCP 管理容器、虚拟机、节点、健康检查和监控指标。
version: 0.3.0
author: oneclickvirt
homepage: https://github.com/oneclickvirt/oneclickvirt
tags:
  - virtualization
  - containers
  - vms
  - lxd
  - incus
  - docker
  - podman
  - proxmox
  - kubevirt
applyTo:
  - mcp
  - cli
platforms:
  - linux
  - macos
mcpServers:
  oneclickvirt:
    command: oneclickvirt
    args:
      - mcp
    env:
      ONE_CLICK_VIRT_API_URL: ""
      ONE_CLICK_VIRT_API_TOKEN: "你的API_TOKEN"
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

- `oneclickvirt` 二进制已在 `PATH` 中，或在 MCP 客户端配置中使用绝对路径。
- OneClickVirt API 正在运行。
- 从 Web 界面个人中心的 API Tokens 获取管理员 API Token。
- 远程部署必须使用 HTTPS。

## 安装

使用本地 MCP 客户端配置：

```json
{
  "mcpServers": {
    "oneclickvirt": {
      "command": "oneclickvirt",
      "args": ["mcp"],
      "env": {
        "ONE_CLICK_VIRT_API_URL": "https://your-domain.com",
        "ONE_CLICK_VIRT_API_TOKEN": "你的API_TOKEN"
      }
    }
  }
}
```

从 Release 压缩包手动安装：

```bash
mkdir -p ~/.claude/skills/oneclickvirt
tar -xzf oneclickvirt-skill-v0.3.0.tar.gz -C ~/.claude/skills/oneclickvirt
```

从本地仓库开发安装：

```bash
mkdir -p ~/.claude/skills/oneclickvirt
cp -R skills/. ~/.claude/skills/oneclickvirt/
```

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

- `command not found: oneclickvirt`：在 MCP 配置中使用二进制绝对路径。
- `401 Unauthorized`：确认 `ONE_CLICK_VIRT_API_TOKEN` 是有效管理员 Token。
- `Connection refused`：确认 `ONE_CLICK_VIRT_API_URL` 正确且 `/api/v1/health` 可访问。
- 工具未出现：重启 MCP 客户端并检查 MCP Server 日志。

## 安全性

- 优先使用环境变量传递 Token，避免放入命令行参数。
- 使用专用 Token，并授予最小必要权限。
- 对 `delete_instance` 等破坏性请求先审阅再确认。
- 远程 API 端点必须使用 HTTPS。

