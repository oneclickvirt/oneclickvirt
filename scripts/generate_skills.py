#!/usr/bin/env python3
"""
Auto-generate skills/SKILL.md and skills/SKILL_ZH.md from MCP server source code.
Parses server/mcp/server.go and server/constant/version.go.
"""

import re
import sys
import os
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

# ── Chinese translations for tool/resource/prompt descriptions ──
TOOL_DESC_ZH = {
    "list_instances":      "列出所有虚拟机和容器实例",
    "create_instance":     "创建新的虚拟机或容器实例",
    "start_instance":      "启动已停止的实例",
    "stop_instance":       "停止正在运行的实例",
    "restart_instance":    "重启实例",
    "delete_instance":     "删除实例（危险：不可逆）",
    "get_instance_detail": "获取指定实例的详细信息",
    "list_providers":      "列出所有虚拟化节点",
    "health_check":        "对节点执行健康检查",
    "get_instance_logs":   "获取实例的最近日志",
    "get_system_status":   "获取系统整体状态，包括实例计数和资源用量",
    "get_metrics":         "获取系统、节点或实例的监控指标",
}

TOOL_PARAM_DESC_ZH = {
    "page":        "页码（默认：1）",
    "pageSize":    "每页数量（默认：20）",
    "status":      "按状态筛选：running、stopped、deleted",
    "provider_id": "节点 ID",
    "instance_id": "实例 ID",
    "instance_type": "container 或 vm",
    "image":       "系统镜像（如 debian:12、ubuntu:22.04、alpine:latest）",
    "cpu":         "CPU 核心数",
    "memory":      "内存大小（MB）",
    "disk":        "磁盘大小（GB）",
    "name":        "实例名称（可选）",
    "confirm":     "必须为 true 才能确认删除",
    "lines":       "日志行数（默认：50）",
    "metric_type": "指标范围：system、provider 或 instance",
    "hours":       "实例指标的时间范围（小时，默认：24）",
}

RESOURCE_DESC_ZH = {
    "oneclickvirt://instances/list":   "当前所有实例的列表",
    "oneclickvirt://providers/list":   "当前所有节点的列表",
    "oneclickvirt://system/status":    "系统整体健康和指标",
    "oneclickvirt://health/status":    "公开的 OneClickVirt API 健康状态",
    "oneclickvirt://config/system":    "管理员可见的系统配置",
}

PROMPT_DESC_ZH = {
    "create_debian_container": "用于创建 Debian Linux 容器的模板",
    "create_ubuntu_vm":        "用于创建 Ubuntu 虚拟机的模板",
    "troubleshoot_instance":   "用于排查无法启动实例的模板",
    "quick_status_check":      "用于检查实例、节点和平台健康的模板",
}

PROMPT_ARG_DESC_ZH = {
    "provider_id": "节点 ID",
    "instance_id": "故障排查的实例 ID",
    "name":        "实例名称",
}


def read_file(path):
    with open(path, "r", encoding="utf-8") as f:
        return f.read()


def parse_version(go_src):
    """Extract ServerVersion from constant/version.go."""
    m = re.search(r'ServerVersion\s*=\s*"([^"]+)"', go_src)
    return m.group(1) if m else "0.0.0"


def parse_tools(go_src):
    """Extract tools from registerTools() in server/mcp/server.go."""
    tools = []
    # Find registerTools function body
    func_match = re.search(
        r'func\s+\(s\s+\*MCPServer\)\s+registerTools\(\)\s*\{.*?\n\}',
        go_src, re.DOTALL
    )
    if not func_match:
        return tools

    body = func_match.group(0)
    # Match each tool block: Name, Description, then InputSchema (which may contain nested braces)
    # We extract at the tool level by matching the struct literal pattern
    tool_blocks = re.finditer(
        r'\{\s*\n\s*Name:\s*"([^"]+)",\s*\n\s*Description:\s*"([^"]+)",\s*\n\s*InputSchema:\s*InputSchema\{(.*?)\},?\s*\n\s*\}',
        body, re.DOTALL
    )

    for m in tool_blocks:
        name = m.group(1)
        desc = m.group(2)
        schema_block = m.group(3)

        # Parse Type and Properties from InputSchema
        type_match = re.search(r'Type:\s*"([^"]+)"', schema_block)
        schema_type = type_match.group(1) if type_match else "object"

        # Parse required fields
        required_block = re.search(r'Required:\s*\[\]string\{(.*?)\}', schema_block, re.DOTALL)
        required = []
        if required_block:
            required = re.findall(r'"([^"]+)"', required_block.group(1))

        # Parse properties
        props = {}
        prop_blocks = re.finditer(
            r'"(\w+)":\s*map\[string\]interface\{\}\{(.*?)\}',
            schema_block, re.DOTALL
        )
        for pm in prop_blocks:
            pname = pm.group(1)
            pblock = pm.group(2)
            ptype = re.search(r'"type":\s*"([^"]+)"', pblock)
            pdesc = re.search(r'"description":\s*"([^"]+)"', pblock)
            props[pname] = {
                "type": ptype.group(1) if ptype else "string",
                "description": pdesc.group(1) if pdesc else "",
            }

        tools.append({
            "name": name,
            "description": desc,
            "schema_type": schema_type,
            "properties": props,
            "required": required,
        })
    return tools


def parse_resources(go_src):
    """Extract resources from registerResources()."""
    resources = []
    func_match = re.search(
        r'func\s+\(s\s+\*MCPServer\)\s+registerResources\(\)\s*\{.*?\n\}',
        go_src, re.DOTALL
    )
    if not func_match:
        return resources

    body = func_match.group(0)
    for m in re.finditer(
        r'\{\s*\n\s*URI:\s*"([^"]+)",\s*\n\s*Name:\s*"([^"]+)",\s*\n\s*Description:\s*"([^"]+)",\s*\n\s*MimeType:\s*"([^"]+)",?\s*\n\s*\}',
        body, re.DOTALL
    ):
        resources.append({
            "uri": m.group(1),
            "name": m.group(2),
            "description": m.group(3),
            "mime_type": m.group(4),
        })
    return resources


def parse_prompts(go_src):
    """Extract prompts from registerPrompts()."""
    prompts = []
    func_match = re.search(
        r'func\s+\(s\s+\*MCPServer\)\s+registerPrompts\(\)\s*\{.*?\n\}',
        go_src, re.DOTALL
    )
    if not func_match:
        return prompts

    body = func_match.group(0)
    # Match each prompt block - may or may not have Arguments
    prompt_blocks = re.finditer(
        r'\{\s*\n\s*Name:\s*"([^"]+)",\s*\n\s*Description:\s*"([^"]+)"',
        body, re.DOTALL
    )

    for m in prompt_blocks:
        name = m.group(1)
        desc = m.group(2)

        # Find arguments after this prompt's description
        # Simple approach: look for Arguments: []PromptArgument{...} after current position
        remaining = body[m.end():]
        args_match = re.search(
            r'Arguments:\s*\[\]PromptArgument\{(.*?)\},?\s*\n\s*\}',
            remaining, re.DOTALL
        )
        arguments = []
        if args_match:
            arg_block = args_match.group(1)
            for am in re.finditer(
                r'\{(.*?)\}',
                arg_block, re.DOTALL
            ):
                arg_text = am.group(1)
                arg_name = re.search(r'Name:\s*"([^"]+)"', arg_text)
                arg_desc = re.search(r'Description:\s*"([^"]+)"', arg_text)
                arg_req = re.search(r'Required:\s*(true|false)', arg_text)
                if arg_name:
                    arguments.append({
                        "name": arg_name.group(1),
                        "description": arg_desc.group(1) if arg_desc else "",
                        "required": arg_req.group(1) == "true" if arg_req else False,
                    })

        prompts.append({
            "name": name,
            "description": desc,
            "arguments": arguments,
        })
    return prompts


def generate_skill_md(version, tools, resources, prompts, lang="en"):
    """Generate SKILL.md content."""
    is_zh = lang == "zh"

    def t(en_text, zh_text):
        return zh_text if is_zh else en_text

    lines = []
    lines.append("---")
    name = "oneclickvirt"
    desc_en = "OneClickVirt operations skill for managing containers, virtual machines, provider nodes, health checks, and metrics through MCP."
    desc_zh = "OneClickVirt 操作 Skill，通过 MCP 管理容器、虚拟机、节点、健康检查和监控指标。"
    lines.append(f"name: {name}")
    lines.append(f"description: {t(desc_en, desc_zh)}")
    lines.append(f"version: {version}")
    lines.append("author: oneclickvirt")
    lines.append("homepage: https://github.com/oneclickvirt/oneclickvirt")
    lines.append("tags:")
    for tag in ["virtualization", "containers", "vms", "lxd", "incus", "docker", "podman", "proxmox", "kubevirt"]:
        lines.append(f"  - {tag}")
    lines.append("applyTo:")
    lines.append("  - mcp")
    lines.append("  - cli")
    lines.append("platforms:")
    lines.append("  - linux")
    lines.append("  - macos")
    lines.append("mcpServers:")
    lines.append("  oneclickvirt:")
    lines.append("    command: oneclickvirt")
    lines.append("    args:")
    lines.append("      - mcp")
    lines.append("    env:")
    token_hint = t("YOUR_API_TOKEN", "你的API_TOKEN")
    lines.append('      ONE_CLICK_VIRT_API_URL: ""')
    lines.append(f'      ONE_CLICK_VIRT_API_TOKEN: "{token_hint}"')
    lines.append("---")
    lines.append("")

    # Title
    lines.append("# OneClickVirt Skill")
    lines.append("")

    # Overview
    overview_title = t("## Overview", "## 概述")
    lines.append(overview_title)
    lines.append("")
    overview_text_en = (
        "This skill helps an AI assistant operate a OneClickVirt deployment through the built-in MCP server. "
        "It is useful for routine instance management, provider inspection, platform health checks, and guided troubleshooting."
    )
    overview_text_zh = (
        "此 Skill 帮助 AI 助手通过 OneClickVirt 内置 MCP Server 操作 OneClickVirt 部署，"
        "适用于实例管理、节点巡检、平台健康检查和故障排查。"
    )
    lines.append(t(overview_text_en, overview_text_zh))
    lines.append("")
    what_can_do_title = t("What this skill can do:", "此 Skill 可以完成：")
    lines.append(what_can_do_title)
    lines.append("")
    bullets_en = [
        "List, create, start, stop, restart, inspect, and delete instances.",
        "List providers and run provider health checks.",
        "Read system status, health, configuration, and monitoring resources.",
        "Use prompt templates for common container, VM, status, and troubleshooting workflows.",
    ]
    bullets_zh = [
        "列出、创建、启动、停止、重启、查看和删除实例。",
        "列出节点并执行节点健康检查。",
        "读取系统状态、健康状态、配置和监控资源。",
        "使用提示模板完成容器、虚拟机、状态检查和故障排查工作流。",
    ]
    for i, (b_en, b_zh) in enumerate(zip(bullets_en, bullets_zh)):
        lines.append(f"- {t(b_en, b_zh)}")
    lines.append("")

    # Prerequisites
    prereq_title = t("## Prerequisites", "## 前置条件")
    lines.append(prereq_title)
    lines.append("")
    prereq_items_en = [
        "A working `oneclickvirt` binary in `PATH`, or an absolute binary path in the MCP client config.",
        "A running OneClickVirt API endpoint.",
        "An administrator API token from the Web UI under Profile -> API Tokens.",
        "HTTPS for remote OneClickVirt deployments.",
    ]
    prereq_items_zh = [
        "`oneclickvirt` 二进制已在 `PATH` 中，或在 MCP 客户端配置中使用绝对路径。",
        "OneClickVirt API 正在运行。",
        "从 Web 界面个人中心的 API Tokens 获取管理员 API Token。",
        "远程部署必须使用 HTTPS。",
    ]
    for i, (pe, pz) in enumerate(zip(prereq_items_en, prereq_items_zh)):
        lines.append(f"- {t(pe, pz)}")
    lines.append("")

    # Installation
    install_title = t("## Installation", "## 安装")
    lines.append(install_title)
    lines.append("")
    lines.append(t("Use a local MCP client configuration:", "使用本地 MCP 客户端配置："))
    lines.append("")
    lines.append("```json")
    lines.append("{")
    lines.append('  "mcpServers": {')
    lines.append('    "oneclickvirt": {')
    lines.append('      "command": "oneclickvirt",')
    lines.append('      "args": ["mcp"],')
    lines.append('      "env": {')
    lines.append('        "ONE_CLICK_VIRT_API_URL": "https://your-domain.com",')
    token_val = t("YOUR_API_TOKEN", "你的API_TOKEN")
    lines.append(f'        "ONE_CLICK_VIRT_API_TOKEN": "{token_val}"')
    lines.append("      }")
    lines.append("    }")
    lines.append("  }")
    lines.append("}")
    lines.append("```")
    lines.append("")
    lines.append(t("Manual install from a release archive:", "从 Release 压缩包手动安装："))
    lines.append("")
    lines.append("```bash")
    lines.append("mkdir -p ~/.claude/skills/oneclickvirt")
    lines.append(f"tar -xzf oneclickvirt-skill-v{version}.tar.gz -C ~/.claude/skills/oneclickvirt")
    lines.append("```")
    lines.append("")
    lines.append(t("Development install from a clone:", "从本地仓库开发安装："))
    lines.append("")
    lines.append("```bash")
    lines.append("mkdir -p ~/.claude/skills/oneclickvirt")
    lines.append("cp -R skills/. ~/.claude/skills/oneclickvirt/")
    lines.append("```")
    lines.append("")

    # Usage - Tools
    usage_title = t("## Usage", "## 使用方法")
    lines.append(usage_title)
    lines.append("")
    lines.append(t("Available MCP tools:", "可用 MCP Tools："))
    lines.append("")
    for tool in tools:
        name = tool["name"]
        desc = t(tool["description"], TOOL_DESC_ZH.get(name, tool["description"]))
        lines.append(f"- `{name}` — {desc}")
    lines.append("")

    # Usage - Resources
    lines.append(t("Available resources:", "可用资源："))
    lines.append("")
    for res in resources:
        uri = res["uri"]
        desc = t(res["description"], RESOURCE_DESC_ZH.get(uri, res["description"]))
        lines.append(f"- `{uri}` — {desc}")
    lines.append("")

    # Usage - Prompts
    lines.append(t("Available prompt templates:", "可用 Prompt 模板："))
    lines.append("")
    for prompt in prompts:
        name = prompt["name"]
        desc = t(prompt["description"], PROMPT_DESC_ZH.get(name, prompt["description"]))
        lines.append(f"- `{name}` — {desc}")
    lines.append("")

    # Examples
    examples_title = t("## Examples", "## 示例")
    lines.append(examples_title)
    lines.append("")
    examples_en = [
        '- "List running instances and summarize anything frozen or stopped."',
        '- "Create a Debian 12 container on provider 1 with 2 CPU, 1 GB memory, and 20 GB disk."',
        '- "Run a quick status check for the platform."',
        '- "Check provider 3 health and summarize capacity concerns."',
        '- "Troubleshoot instance 42 and suggest the safest next action."',
    ]
    examples_zh = [
        '“列出运行中的实例，并总结冻结或停止的异常项。”',
        '“在节点 1 创建 Debian 12 容器，2 核 CPU、1 GB 内存、20 GB 磁盘。”',
        '“快速检查平台状态。”',
        '“检查节点 3 健康状态并总结容量风险。”',
        '“排查实例 42，并建议最安全的下一步操作。”',
    ]
    for i, (ee, ez) in enumerate(zip(examples_en, examples_zh)):
        lines.append(f"- {t(ee, ez)}")
    lines.append("")

    # Troubleshooting
    ts_title = t("## Troubleshooting", "## 故障排查")
    lines.append(ts_title)
    lines.append("")
    ts_items_en = [
        "- `command not found: oneclickvirt`: use an absolute binary path in the MCP config.",
        "- `401 Unauthorized`: verify `ONE_CLICK_VIRT_API_TOKEN` is an active administrator token.",
        "- `Connection refused`: verify `ONE_CLICK_VIRT_API_URL` and that `/api/v1/health` is reachable.",
        "- Tools do not appear: restart the MCP client and inspect its MCP server logs.",
    ]
    ts_items_zh = [
        "- `command not found: oneclickvirt`：在 MCP 配置中使用二进制绝对路径。",
        "- `401 Unauthorized`：确认 `ONE_CLICK_VIRT_API_TOKEN` 是有效管理员 Token。",
        "- `Connection refused`：确认 `ONE_CLICK_VIRT_API_URL` 正确且 `/api/v1/health` 可访问。",
        "- 工具未出现：重启 MCP 客户端并检查 MCP Server 日志。",
    ]
    for i, (te, tz) in enumerate(zip(ts_items_en, ts_items_zh)):
        lines.append(f"{t(te, tz)}")
    lines.append("")

    # Security
    sec_title = t("## Security", "## 安全性")
    lines.append(sec_title)
    lines.append("")
    sec_items_en = [
        "- Prefer environment variables over command-line token arguments.",
        "- Use a dedicated token with the minimum required permissions.",
        "- Review destructive requests such as `delete_instance` before confirming.",
        "- Use HTTPS for remote API endpoints.",
    ]
    sec_items_zh = [
        "- 优先使用环境变量传递 Token，避免放入命令行参数。",
        "- 使用专用 Token，并授予最小必要权限。",
        "- 对 `delete_instance` 等破坏性请求先审阅再确认。",
        "- 远程 API 端点必须使用 HTTPS。",
    ]
    for i, (se, sz) in enumerate(zip(sec_items_en, sec_items_zh)):
        lines.append(f"{t(se, sz)}")
    lines.append("")

    return "\n".join(lines) + "\n"


def main():
    server_go = ROOT / "server" / "mcp" / "server.go"
    version_go = ROOT / "server" / "constant" / "version.go"

    if not server_go.exists():
        print(f"ERROR: {server_go} not found", file=sys.stderr)
        sys.exit(1)
    if not version_go.exists():
        print(f"ERROR: {version_go} not found", file=sys.stderr)
        sys.exit(1)

    server_src = read_file(server_go)
    version_src = read_file(version_go)

    version = parse_version(version_src)
    tools = parse_tools(server_src)
    resources = parse_resources(server_src)
    prompts = parse_prompts(server_src)

    print(f"Parsed: {len(tools)} tools, {len(resources)} resources, {len(prompts)} prompts, version={version}")

    # Validate we got meaningful data
    if len(tools) < 5:
        print(f"ERROR: Only {len(tools)} tools parsed — parsing may have failed", file=sys.stderr)
        sys.exit(1)
    if len(resources) < 3:
        print(f"ERROR: Only {len(resources)} resources parsed", file=sys.stderr)
        sys.exit(1)

    # Generate SKILL.md (English)
    en_content = generate_skill_md(version, tools, resources, prompts, lang="en")
    en_path = ROOT / "skills" / "SKILL.md"
    en_path.parent.mkdir(parents=True, exist_ok=True)
    with open(en_path, "w", encoding="utf-8") as f:
        f.write(en_content)
    print(f"Wrote {en_path}")

    # Generate SKILL_ZH.md (Chinese)
    zh_content = generate_skill_md(version, tools, resources, prompts, lang="zh")
    zh_path = ROOT / "skills" / "SKILL_ZH.md"
    with open(zh_path, "w", encoding="utf-8") as f:
        f.write(zh_content)
    print(f"Wrote {zh_path}")


if __name__ == "__main__":
    main()
