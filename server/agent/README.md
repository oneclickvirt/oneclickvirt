# oneclickvirt-agent

A lightweight monitoring agent for virtual machine and container instances. It uses nftables named counters to track per-instance network traffic (excluding private/special-use IP ranges) and collects resource usage (CPU, memory, disk) from various providers.

The agent runs on each provider host and exposes an HTTP API for the central management server to manage monitors, query traffic data, and retrieve resource metrics.

## Requirements

- Linux with nftables (`nft` command available)
- Kernel >= 3.14 for full nftables support
- Root privileges (required for nftables manipulation)
- Rust 1.75+ for compilation

## Configuration

Configuration is done via environment variables or a `.env` file in the working directory.

| Variable | Required | Description |
|---|---|---|
| `API_TOKEN` | Yes | Authentication token. All API requests must include this value in the `x-token` header. |
| `EXTRA_EXCLUDE_CIDRS_V4` | No | Comma-separated additional IPv4 CIDRs to exclude from traffic counting. |
| `EXTRA_EXCLUDE_CIDRS_V6` | No | Comma-separated additional IPv6 CIDRs to exclude from traffic counting. |
| `RUST_LOG` | No | Log level filter (default: `info`). Examples: `debug`, `warn`, `oneclickvirt_agent=debug`. |

## Building

```bash
cargo build --release
```

The binary is output to `target/release/oneclickvirt-agent`.

Cross-compilation for Linux targets:

```bash
# amd64
cross build --release --target x86_64-unknown-linux-musl

# arm64
cross build --release --target aarch64-unknown-linux-musl
```

## Running

```bash
API_TOKEN=your-secret-token ./oneclickvirt-agent
```

The agent starts an HTTP server on `0.0.0.0:23782`.

A Swagger UI is available at `http://<host>:23782/swagger-ui/` for interactive API exploration (no authentication required for the Swagger UI endpoint).

## Data Storage

Traffic data and resource metrics are stored in a SQLite database file `traffic.db` in the working directory.

- Traffic counters are accumulated indefinitely (until the monitor is deleted or cleaned up).
- Resource metrics (CPU, memory, disk) are retained for 24 hours and automatically purged.
- Stale monitors not updated within 30 days are automatically cleaned up.

## Traffic Monitoring Architecture

The agent uses two nftables scopes to capture traffic:

- **inet** family (table `vm_traffic_monitor`, chain `forward`) -- routed L3 traffic
- **bridge** family (table `vm_traffic_monitor_br`, chain `forward`) -- bridged L2 traffic

For each monitored interface, rules are created in both scopes to count inbound and outbound IPv4/IPv6 traffic, excluding private and special-use address ranges (RFC1918, RFC6598, loopback, link-local, multicast, etc.).

Traffic counters are read every 2 seconds and accumulated in the SQLite database.

### NIC Change Handling

When an instance is rebuilt or restarted with a new network interface:

1. The management server calls `POST /api/v1/update` with the new interface name(s).
2. Old nftables rules and counters are removed; new ones are created.
3. The accumulated `total_bytes` value is preserved (not reset).
4. If a counter is detected as reset (current < previous), only the current value is added as an increment, preventing negative deltas.

### Interface Aliases

If an interface has a master bridge (detected via `/sys/class/net/<interface>/master`), rules are created for both the interface name and its bridge master name.

## Resource Monitoring

When a monitor is created with `provider_kind` and `instance_name` fields, the agent collects CPU, memory, and disk usage every 5 minutes using provider-specific methods:

| Provider | Method |
|---|---|
| Docker | `docker stats --no-stream` + `docker inspect` |
| Podman | `podman stats --no-stream` + `podman inspect` |
| Containerd | `nerdctl stats` with cgroup v2 fallback |
| LXD | `lxc info` + `lxc config show` + `lxc config device show` |
| Incus | `incus info` + `incus config show` + `incus config device show` |
| Proxmox VE | `pvesh get /nodes/<node>/lxc\|qemu/<vmid>/status/current` |

## API Reference

All endpoints except `/swagger-ui/` require the `x-token` header for authentication.

### POST /api/v1/add

Create a new traffic monitor for one or more network interfaces.

**Request:**
```json
{
  "interface": "veth1001i0",
  "provider_kind": "proxmox",
  "instance_name": "1001"
}
```

The `interface` field accepts a single string or an array of strings for multi-NIC monitoring:
```json
{
  "interface": ["veth1001i0", "veth1001i1"],
  "provider_kind": "docker",
  "instance_name": "my-container"
}
```

The `provider_kind` and `instance_name` fields are optional. When provided, the agent collects resource metrics for the instance.

**Response:**
```json
{
  "id": 1,
  "interface": ["veth1001i0"]
}
```

### POST /api/v1/update

Replace the monitored interfaces for an existing monitor. Accumulated traffic is preserved.

**Request:**
```json
{
  "id": 1,
  "new_interface": ["veth1001i0", "veth1001i1"],
  "provider_kind": "proxmox",
  "instance_name": "1001"
}
```

**Response:**
```json
{
  "id": 1,
  "interface": ["veth1001i0", "veth1001i1"]
}
```

### POST /api/v1/delete

Delete a monitor and its associated nftables rules.

**Request:**
```json
{
  "id": 1
}
```

**Response:**
```json
{
  "id": 1,
  "deleted": true
}
```

### POST /api/v1/info

Query traffic usage for a monitor. Add `?human=1` query parameter to include human-readable traffic values.

**Request:**
```json
{
  "id": 1
}
```

**Response:**
```json
{
  "id": 1,
  "interface": ["veth1001i0"],
  "used_traffic": 1073741824,
  "used_traffic_human": "1.00G",
  "last_update_time": 1711929600
}
```

### POST /api/v1/resources

Query resource monitoring history for a monitor.

**Request:**
```json
{
  "id": 1,
  "limit": 288
}
```

**Response:**
```json
{
  "id": 1,
  "data": [
    {
      "timestamp": 1711929600,
      "cpu_percent": 15.3,
      "memory_used": 134217728,
      "memory_total": 536870912,
      "disk_used": 1073741824,
      "disk_total": 10737418240
    }
  ]
}
```

### POST /api/v1/cleanup

Delete monitors that have not been updated within the specified duration.

**Request:**
```json
{
  "max_update_time": "7d"
}
```

Duration format supports combinations: `3d12h30m45s`.

**Response:**
```json
{
  "deleted": 5,
  "max_update_seconds": 604800
}
```

## Excluded IP Ranges

The following address ranges are excluded from traffic counting by default:

**IPv4:**
- 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8
- 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24
- 192.88.99.0/24, 192.168.0.0/16, 198.18.0.0/15, 198.51.100.0/24
- 203.0.113.0/24, 224.0.0.0/4, 240.0.0.0/4

**IPv6:**
- ::/128, ::1/128, fc00::/7, fe80::/10, ff00::/8, 2001:db8::/32

Additional ranges can be added via the `EXTRA_EXCLUDE_CIDRS_V4` and `EXTRA_EXCLUDE_CIDRS_V6` environment variables.

## License

Same license as the parent project.
