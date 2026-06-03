---
name: oneclickvirt
description: OneClickVirt operations skill for managing containers, virtual machines, provider nodes, health checks, and metrics through MCP.
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
      ONE_CLICK_VIRT_API_TOKEN: "YOUR_API_TOKEN"
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

- A working `oneclickvirt` binary in `PATH`, or an absolute binary path in the MCP client config.
- A running OneClickVirt API endpoint.
- An administrator API token from the Web UI under Profile -> API Tokens.
- HTTPS for remote OneClickVirt deployments.

## Installation

Use a local MCP client configuration:

```json
{
  "mcpServers": {
    "oneclickvirt": {
      "command": "oneclickvirt",
      "args": ["mcp"],
      "env": {
        "ONE_CLICK_VIRT_API_URL": "https://your-domain.com",
        "ONE_CLICK_VIRT_API_TOKEN": "YOUR_API_TOKEN"
      }
    }
  }
}
```

Manual install from a release archive:

```bash
mkdir -p ~/.claude/skills/oneclickvirt
tar -xzf oneclickvirt-skill-v0.3.0.tar.gz -C ~/.claude/skills/oneclickvirt
```

Development install from a clone:

```bash
mkdir -p ~/.claude/skills/oneclickvirt
cp -R skills/. ~/.claude/skills/oneclickvirt/
```

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

- - "List running instances and summarize anything frozen or stopped."
- - "Create a Debian 12 container on provider 1 with 2 CPU, 1 GB memory, and 20 GB disk."
- - "Run a quick status check for the platform."
- - "Check provider 3 health and summarize capacity concerns."
- - "Troubleshoot instance 42 and suggest the safest next action."

## Troubleshooting

- `command not found: oneclickvirt`: use an absolute binary path in the MCP config.
- `401 Unauthorized`: verify `ONE_CLICK_VIRT_API_TOKEN` is an active administrator token.
- `Connection refused`: verify `ONE_CLICK_VIRT_API_URL` and that `/api/v1/health` is reachable.
- Tools do not appear: restart the MCP client and inspect its MCP server logs.

## Security

- Prefer environment variables over command-line token arguments.
- Use a dedicated token with the minimum required permissions.
- Review destructive requests such as `delete_instance` before confirming.
- Use HTTPS for remote API endpoints.

