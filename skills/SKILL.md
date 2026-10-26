---
name: oneclickvirt
description: OneClickVirt operations skill for managing containers, virtual machines, provider nodes, health checks, and metrics through MCP.
metadata:
  version: "0.3.0"
  author: oneclickvirt
  homepage: https://github.com/oneclickvirt/oneclickvirt
---

# OneClickVirt Skill

## Overview

This skill helps an AI assistant operate a OneClickVirt deployment through the built-in MCP server. It is useful for routine instance management, provider inspection, platform health checks, and guided troubleshooting.

What this skill can do:

- List, create, start, stop, restart, inspect, and delete instances.
- List providers and run provider health checks.
- Read system status, health, configuration, and monitoring resources.
- Use prompt templates for common container, VM, status, and troubleshooting workflows.

## Prerequisites

- Python 3.9+ for the installer. Prebuilt MCP binaries support Linux/Windows amd64 and ARM64; other platforms can use an existing binary or SSH transport.
- A running OneClickVirt API endpoint.
- An administrator API token from the Web UI under Profile -> API Tokens.
- HTTPS for remote OneClickVirt deployments.

## Installation

Install or update the latest stable release from GitHub:

```bash
curl -fL --retry 3 https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/main/scripts/install_skill.py -o install_skill.py
python3 install_skill.py --client claude
```

The same installer is available from the CNB public source mirror:

```bash
curl -fL --retry 3 https://cnb.cool/oneclickvirt/oneclickvirt/-/git/raw/main/scripts/install_skill.py -o install_skill.py
python3 install_skill.py --client claude
```

Choose `--client codex` or `--client openclaw` for those clients. The default directories are `~/.claude/skills/oneclickvirt`, `~/.agents/skills/oneclickvirt`, and `~/.openclaw/skills/oneclickvirt`; `--skill-dir` overrides the destination. On Windows, download the same script and run it with Python 3.9+ (`py -3 install_skill.py --client claude`).

Both entry points resolve one published GitHub Release, verify its Skill and binary checksums, and install them together. CNB mirrors source; these release archives are downloaded from GitHub. The installer creates `SKILL.md` directly in the Skill directory and a separate `oneclickvirt-mcp` binary in `~/.local/bin`. It does not start the controller or alter services, databases, client settings, or existing API tokens.

Repeat the download and installation commands to update. Pass `--version` with an exact published tag to pin a release or return to it; the Skill and binary use that same tag. `install.json` records the installed version. Reuse an existing controller binary with `--mcp-command /opt/oneclickvirt/server/oneclickvirt-server`, or use `--skip-binary` to install only the Skill files. A Skill alone does not configure or start MCP.

Copy the `mcpServers` entry from the installed `mcp.example.json` into your MCP client's settings, retaining its absolute binary path, and set your API URL/token. The equivalent template is:

```json
{
  "mcpServers": {
    "oneclickvirt": {
      "command": "oneclickvirt-mcp",
      "args": ["mcp"],
      "env": {
        "ONE_CLICK_VIRT_API_URL": "https://your-domain.com",
        "ONE_CLICK_VIRT_API_TOKEN": "YOUR_API_TOKEN"
      }
    }
  }
}
```

The command must be visible to the MCP client; GUI clients may not inherit your shell's `PATH`. A controller installed by the project uses `/opt/oneclickvirt/server/oneclickvirt-server`, not `/opt/oneclickvirt/server/oneclickvirt`. macOS has no prebuilt controller asset: use `--skip-binary` and configure an existing native build or an SSH MCP transport to Linux. Restart the MCP client after configuration. See the [installation guide](https://www.spiritlhl.net/en/guide/oneclickvirt/oneclickvirt_advanced_install.html#skill-and-mcp) for release archives and SSH examples.

## Usage

Available MCP tools:

- `list_instances` — List all virtual machine and container instances in OneClickVirt
- `create_instance` — Create a new virtual machine or container instance
- `start_instance` — Start a stopped instance
- `stop_instance` — Stop a running instance
- `restart_instance` — Restart an instance
- `delete_instance` — Delete an instance (DANGER: irreversible)
- `get_instance_detail` — Get detailed information about a specific instance
- `list_providers` — List all virtualization provider nodes
- `health_check` — Run a health check on a provider node
- `get_instance_logs` — Get recent logs from an instance
- `get_system_status` — Get overall system status including instance counts and resource usage
- `get_metrics` — Get monitoring metrics for the system, a provider, or an instance

Available resources:

- `oneclickvirt://instances/list` — Current list of all instances
- `oneclickvirt://providers/list` — Current list of all provider nodes
- `oneclickvirt://system/status` — Overall system health and metrics
- `oneclickvirt://health/status` — Public OneClickVirt API health status
- `oneclickvirt://config/system` — Administrator-visible system configuration

Available prompt templates:

- `create_debian_container` — Template for creating a Debian Linux container
- `create_ubuntu_vm` — Template for creating an Ubuntu virtual machine
- `troubleshoot_instance` — Template for troubleshooting an instance that won't start
- `quick_status_check` — Template for checking instance, provider, and platform health

## Examples

- "List running instances and summarize anything frozen or stopped."
- "Create a Debian 12 container on provider 1 with 2 CPU, 1 GB memory, and 20 GB disk."
- "Run a quick status check for the platform."
- "Check provider 3 health and summarize capacity concerns."
- "Troubleshoot instance 42 and suggest the safest next action."

## Troubleshooting

- `command not found: oneclickvirt-mcp`: use the absolute command path from the installed `mcp.example.json` in the client config.
- `401 Unauthorized`: verify `ONE_CLICK_VIRT_API_TOKEN` is an active administrator token.
- `Connection refused`: verify `ONE_CLICK_VIRT_API_URL` and that `/api/v1/health` is reachable.
- Tools do not appear: restart the MCP client and inspect its MCP server logs.

## Security

- Prefer environment variables over command-line token arguments.
- Use a dedicated token with the minimum required permissions.
- Review destructive requests such as `delete_instance` before confirming.
- Use HTTPS for remote API endpoints.

