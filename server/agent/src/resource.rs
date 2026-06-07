use crate::error::ApiError;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fs;
use std::process::Command;
use std::sync::Mutex;
use tracing::debug;
use utoipa::ToSchema;

/// Extended PATH (canonical, kept in sync with server/utils/env.go:StandardExtendedPath).
/// This value is inlined in AGENT_ENV_PREFIX below due to Rust concat! limitations.
/// When updating the PATH, change both this comment and the inline value in AGENT_ENV_PREFIX.
#[allow(dead_code)]
const AGENT_PATH: &str = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin:/var/lib/snapd/snap/bin:/opt/bin";

/// Comprehensive environment loading prefix, matching Go's utils/env.go:envLoadScript.
/// Ensures all system and user environment files are loaded before command execution,
/// so snap-installed tools, /opt binaries, locale settings, and profile.d extensions
/// are all available — exactly the same behavior as SSH command execution in Go.
///
/// Key design:
///   - LC_ALL=C.UTF-8 prevents locale warnings and encoding issues
///   - PS1='$ ' fakes interactive shell to bypass bashrc [[ $- != *i* ]] guards
///   - Loads: /etc/environment, /etc/profile, /etc/profile.d/*.sh,
///            /etc/bash.bashrc, /etc/bashrc, /etc/zsh/zprofile, /etc/zsh/zshrc,
///            ~/.profile, ~/.bash_profile, ~/.bashrc, ~/.zprofile, ~/.zshrc,
///            ~/.config/environment.d/*.conf
///   - Final PATH: AGENT_PATH prepended + inherited PATH
/// NOTE: concat! only accepts string literals, so AGENT_PATH is inlined here.
/// See AGENT_PATH constant above for the canonical path list (kept in sync with Go).
const AGENT_ENV_PREFIX: &str = concat!(
    "export LC_ALL=C.UTF-8 LANG=C.UTF-8 LANGUAGE=C.UTF-8 2>/dev/null || true; ",
    "export PS1='$ ' 2>/dev/null || true; ",
    "[ -f /etc/environment ] && . /etc/environment 2>/dev/null || true; ",
    "[ -f /etc/profile ] && . /etc/profile >/dev/null 2>&1 || true; ",
    "[ -d /etc/profile.d ] && for f in /etc/profile.d/*.sh; do [ -r \"$f\" ] && . \"$f\" >/dev/null 2>&1 || true; done 2>/dev/null || true; ",
    "[ -f /etc/bash.bashrc ] && . /etc/bash.bashrc >/dev/null 2>&1 || true; ",
    "[ -f /etc/bashrc ] && . /etc/bashrc >/dev/null 2>&1 || true; ",
    "[ -f /etc/zsh/zprofile ] && . /etc/zsh/zprofile >/dev/null 2>&1 || true; ",
    "[ -f /etc/zsh/zshrc ] && . /etc/zsh/zshrc >/dev/null 2>&1 || true; ",
    "[ -f ~/.profile ] && . ~/.profile >/dev/null 2>&1 || true; ",
    "[ -f ~/.bash_profile ] && . ~/.bash_profile >/dev/null 2>&1 || true; ",
    "[ -f ~/.bashrc ] && . ~/.bashrc >/dev/null 2>&1 || true; ",
    "[ -f ~/.zprofile ] && . ~/.zprofile >/dev/null 2>&1 || true; ",
    "[ -f ~/.zshrc ] && . ~/.zshrc >/dev/null 2>&1 || true; ",
    "[ -d ~/.config/environment.d ] && for f in ~/.config/environment.d/*.conf; do [ -r \"$f\" ] && . \"$f\" >/dev/null 2>&1 || true; done 2>/dev/null || true; ",
    "export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin:/var/lib/snapd/snap/bin:/opt/bin${PATH:+:$PATH}; ",
);

/// Run a command through `sh -c` with comprehensive environment loading.
/// This ensures the same env as Go's BuildEnvCommand — all system/user profile
/// files are sourced, locale is set, and the extended PATH is available.
fn run_with_env(cmd: &str) -> std::io::Result<std::process::Output> {
    let full_cmd = format!("{}{}", AGENT_ENV_PREFIX, cmd);
    Command::new("sh").arg("-c").arg(&full_cmd).output()
}

/// Cache for previous cgroup CPU usage_usec readings, keyed by cgroup base path.
static PREV_CPU_USEC: Mutex<Option<HashMap<String, (u64, std::time::Instant)>>> = Mutex::new(None);

#[derive(Debug, Clone, Serialize, Deserialize, ToSchema)]
pub struct ResourceSnapshot {
    pub cpu_percent: f64,
    pub memory_used: u64,
    pub memory_total: u64,
    pub disk_used: u64,
    pub disk_total: u64,
}

/// Provider type hint stored alongside each monitor so the agent knows how to collect resources.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ToSchema)]
#[serde(rename_all = "lowercase")]
pub enum ProviderKind {
    Docker,
    Podman,
    Containerd,
    Lxd,
    Incus,
    Proxmox,
}

impl ProviderKind {
    pub fn from_str_opt(s: &str) -> Option<Self> {
        match s.to_ascii_lowercase().as_str() {
            "docker" => Some(Self::Docker),
            "podman" => Some(Self::Podman),
            "containerd" => Some(Self::Containerd),
            "lxd" => Some(Self::Lxd),
            "incus" => Some(Self::Incus),
            "proxmox" | "pve" => Some(Self::Proxmox),
            _ => None,
        }
    }
}

/// Collect resource usage for a given instance.
/// `instance_name` is the container/VM name on the provider host.
pub fn collect_resource(kind: ProviderKind, instance_name: &str) -> Result<ResourceSnapshot, ApiError> {
    match kind {
        ProviderKind::Docker => collect_docker(instance_name),
        ProviderKind::Podman => collect_podman(instance_name),
        ProviderKind::Containerd => collect_containerd(instance_name),
        ProviderKind::Lxd => collect_lxc("lxc", instance_name),
        ProviderKind::Incus => collect_lxc("incus", instance_name),
        ProviderKind::Proxmox => collect_proxmox(instance_name),
    }
}

// ---------------------------------------------------------------------------
// Docker / Podman (CRI with similar stats JSON)
// ---------------------------------------------------------------------------

fn collect_docker(name: &str) -> Result<ResourceSnapshot, ApiError> {
    collect_oci_runtime("docker", name)
}

fn collect_podman(name: &str) -> Result<ResourceSnapshot, ApiError> {
    collect_oci_runtime("podman", name)
}

fn collect_oci_runtime(runtime: &str, name: &str) -> Result<ResourceSnapshot, ApiError> {
    // Use stats --no-stream --format json for a single snapshot
    let out = run_with_env(&format!("{runtime} stats --no-stream --format '{{{{json .}}}}' {name}"))
        .map_err(|e| ApiError::internal(format!("{runtime} stats failed: {e}")))?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        return Err(ApiError::internal(format!("{runtime} stats error: {}", stderr.trim())));
    }

    let stdout = String::from_utf8_lossy(&out.stdout);
    let line = stdout.trim();
    if line.is_empty() {
        return Err(ApiError::internal(format!("{runtime} stats returned empty output")));
    }

    let v: serde_json::Value = serde_json::from_str(line)
        .map_err(|e| ApiError::internal(format!("{runtime} stats json parse error: {e}")))?;

    let cpu_percent = parse_percent_string(v.get("CPUPerc").and_then(|v| v.as_str()).unwrap_or("0%"));

    let (mem_used, mem_total) = parse_mem_usage(
        v.get("MemUsage").and_then(|v| v.as_str()).unwrap_or("0B / 0B"),
    );

    // Disk: Docker stats doesn't directly provide disk; use inspect for rootfs size
    let (disk_used, disk_total) = get_oci_disk(runtime, name);

    Ok(ResourceSnapshot {
        cpu_percent,
        memory_used: mem_used,
        memory_total: mem_total,
        disk_used,
        disk_total,
    })
}

fn get_oci_disk(runtime: &str, name: &str) -> (u64, u64) {
    let out = run_with_env(&format!("{runtime} inspect --size --format '{{{{json .}}}}' {name}"));

    if let Ok(out) = out {
        if out.status.success() {
            let stdout = String::from_utf8_lossy(&out.stdout);
            if let Ok(v) = serde_json::from_str::<serde_json::Value>(stdout.trim()) {
                // SizeRw = writable layer size, SizeRootFs = total size
                let used = v.get("SizeRw").and_then(|v| v.as_u64()).unwrap_or(0);
                let total = v.get("SizeRootFs").and_then(|v| v.as_u64()).unwrap_or(0);
                if total > 0 {
                    return (used, total);
                }
            }
        }
    }
    (0, 0)
}

// ---------------------------------------------------------------------------
// Containerd (via ctr / nerdctl)
// ---------------------------------------------------------------------------

fn collect_containerd(name: &str) -> Result<ResourceSnapshot, ApiError> {
    // Try nerdctl first, then ctr for cgroup-based stats
    if let Ok(snap) = collect_oci_runtime("nerdctl", name) {
        return Ok(snap);
    }

    // Fallback: read cgroup stats directly
    collect_cgroup_stats(name)
}

fn collect_cgroup_stats(name: &str) -> Result<ResourceSnapshot, ApiError> {
    // Try various cgroup v2 path patterns
    let patterns = [
        format!("/sys/fs/cgroup/system.slice/containerd-{name}.scope"),
        format!("/sys/fs/cgroup/{name}"),
        format!("/sys/fs/cgroup/default/{name}"),
        format!("/sys/fs/cgroup/system.slice/nerdctl-{name}.scope"),
    ];

    for path in &patterns {
        if let Ok(mut snap) = read_cgroup_v2(path) {
            // Try to get disk from ctr snapshots
            if snap.disk_used == 0 {
                let (du, dt) = get_containerd_disk(name);
                snap.disk_used = du;
                snap.disk_total = dt;
            }
            return Ok(snap);
        }
    }

    Err(ApiError::internal(format!(
        "could not find cgroup stats for containerd instance {name}"
    )))
}

/// Get disk usage for containerd via `ctr snapshots usage`.
fn get_containerd_disk(name: &str) -> (u64, u64) {
    let out = run_with_env(&format!("ctr -n default snapshots usage {name}"));
    if let Ok(out) = out {
        if out.status.success() {
            let stdout = String::from_utf8_lossy(&out.stdout);
            // Output format: "KEY    SIZE    INODES\n<key>  <size>  <inodes>"
            for line in stdout.lines().skip(1) {
                let parts: Vec<&str> = line.split_whitespace().collect();
                if parts.len() >= 2 {
                    let used = parse_size_string(parts[1]);
                    if used > 0 {
                        return (used, 0);
                    }
                }
            }
        }
    }
    (0, 0)
}

fn read_cgroup_v2(base: &str) -> Result<ResourceSnapshot, ApiError> {
    let cpu_stat = fs::read_to_string(format!("{base}/cpu.stat"))
        .map_err(|e| ApiError::internal(format!("read cpu.stat: {e}")))?;

    let mem_current = fs::read_to_string(format!("{base}/memory.current"))
        .map_err(|e| ApiError::internal(format!("read memory.current: {e}")))?
        .trim()
        .parse::<u64>()
        .unwrap_or(0);

    let mem_max = fs::read_to_string(format!("{base}/memory.max"))
        .ok()
        .and_then(|s| s.trim().parse::<u64>().ok())
        .unwrap_or(0);

    // CPU usage from cpu.stat (usage_usec)
    let cpu_usec = cpu_stat
        .lines()
        .find(|l| l.starts_with("usage_usec"))
        .and_then(|l| l.split_whitespace().nth(1))
        .and_then(|v| v.parse::<u64>().ok())
        .unwrap_or(0);

    // Compute CPU% from delta between consecutive samples
    let cpu_percent = {
        let mut guard = PREV_CPU_USEC.lock().unwrap_or_else(|e| e.into_inner());
        let map = guard.get_or_insert_with(HashMap::new);
        let now = std::time::Instant::now();
        let pct = if let Some((prev_usec, prev_time)) = map.get(base) {
            let elapsed = now.duration_since(*prev_time);
            let wall_usec = elapsed.as_micros() as u64;
            if wall_usec > 0 && cpu_usec >= *prev_usec {
                let delta_usec = cpu_usec - *prev_usec;
                (delta_usec as f64 / wall_usec as f64) * 100.0
            } else {
                0.0
            }
        } else {
            0.0
        };
        map.insert(base.to_owned(), (cpu_usec, now));
        pct
    };

    Ok(ResourceSnapshot {
        cpu_percent,
        memory_used: mem_current,
        memory_total: mem_max,
        disk_used: 0,
        disk_total: 0,
    })
}

// ---------------------------------------------------------------------------
// LXD / Incus (via lxc/incus info)
// ---------------------------------------------------------------------------

fn collect_lxc(cli: &str, name: &str) -> Result<ResourceSnapshot, ApiError> {
    // lxc/incus info <name> --resources outputs resource usage
    let out = run_with_env(&format!("{cli} info {name}"))
        .map_err(|e| ApiError::internal(format!("{cli} info failed: {e}")))?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        return Err(ApiError::internal(format!("{cli} info error: {}", stderr.trim())));
    }

    let stdout = String::from_utf8_lossy(&out.stdout);

    // Parse CPU, memory, disk from lxc/incus info output
    // Memory usage lines: "  Memory (current): 123.45MiB"
    // Disk usage lines:   "  root: ..." (under Disk usage:)
    let mut mem_used: u64 = 0;
    let mut mem_total: u64 = 0;
    let mut cpu_seconds: f64 = 0.0;
    let mut disk_used: u64 = 0;

    let mut in_memory = false;
    let mut in_disk = false;
    let mut in_cpu = false;

    for line in stdout.lines() {
        let trimmed = line.trim();

        if trimmed.starts_with("Memory usage:") || trimmed == "Memory usage:" {
            in_memory = true;
            in_disk = false;
            in_cpu = false;
            continue;
        }
        if trimmed.starts_with("Disk usage:") || trimmed == "Disk usage:" {
            in_disk = true;
            in_memory = false;
            in_cpu = false;
            continue;
        }
        if trimmed.starts_with("CPU usage:") || trimmed == "CPU usage:" {
            in_cpu = true;
            in_memory = false;
            in_disk = false;
            continue;
        }
        if trimmed.starts_with("Network usage:") {
            in_cpu = false;
            in_memory = false;
            in_disk = false;
            continue;
        }

        if in_memory {
            if let Some(val) = extract_value_after(trimmed, "Memory (current):") {
                mem_used = parse_size_string(&val);
            }
            if let Some(val) = extract_value_after(trimmed, "Memory (peak):") {
                // peak as total approximation; real limit comes from config
                if mem_total == 0 {
                    mem_total = parse_size_string(&val);
                }
            }
        }
        if in_cpu {
            if let Some(val) = extract_value_after(trimmed, "CPU usage (in seconds):") {
                cpu_seconds = val.trim().parse::<f64>().unwrap_or(0.0);
            }
        }
        if in_disk {
            if let Some(val) = extract_value_after(trimmed, "root:") {
                disk_used = parse_size_string(&val);
            }
        }
    }

    // Get memory limit from config
    let config_out = run_with_env(&format!("{cli} config show {name}"));
    if let Ok(config_out) = config_out {
        if config_out.status.success() {
            let config_str = String::from_utf8_lossy(&config_out.stdout);
            for line in config_str.lines() {
                let trimmed = line.trim();
                if trimmed.starts_with("limits.memory:") {
                    if let Some(val) = trimmed.strip_prefix("limits.memory:") {
                        mem_total = parse_size_string(val.trim());
                    }
                }
            }
        }
    }

    // Get disk limit
    let storage_out = run_with_env(&format!("{cli} config device show {name}"));
    if let Ok(storage_out) = storage_out {
        if storage_out.status.success() {
            let storage_str = String::from_utf8_lossy(&storage_out.stdout);
            for line in storage_str.lines() {
                let trimmed = line.trim();
                if trimmed.starts_with("size:") {
                    if let Some(val) = trimmed.strip_prefix("size:") {
                        let disk_total_val = parse_size_string(val.trim());
                        if disk_total_val > 0 {
                            return Ok(ResourceSnapshot {
                                cpu_percent: 0.0, // CPU % needs delta computation
                                memory_used: mem_used,
                                memory_total: mem_total,
                                disk_used,
                                disk_total: disk_total_val,
                            });
                        }
                    }
                }
            }
        }
    }

    let _ = cpu_seconds;

    Ok(ResourceSnapshot {
        cpu_percent: 0.0,
        memory_used: mem_used,
        memory_total: mem_total,
        disk_used,
        disk_total: 0,
    })
}

// ---------------------------------------------------------------------------
// Proxmox VE (via pvesh)
// ---------------------------------------------------------------------------

fn collect_proxmox(vmid: &str) -> Result<ResourceSnapshot, ApiError> {
    // Try API via pvesh: pvesh get /nodes/localhost/qemu/<vmid>/status/current --output-format json
    // or /nodes/localhost/lxc/<vmid>/status/current
    let node = get_proxmox_node();

    // Try LXC first, then QEMU
    for kind in &["lxc", "qemu"] {
        let out = run_with_env(&format!(
            "pvesh get /nodes/{node}/{kind}/{vmid}/status/current --output-format json"
        ));

        if let Ok(out) = out {
            if out.status.success() {
                let stdout = String::from_utf8_lossy(&out.stdout);
                if let Ok(v) = serde_json::from_str::<serde_json::Value>(stdout.trim()) {
                    let data = if v.get("data").is_some() {
                        v.get("data").unwrap()
                    } else {
                        &v
                    };

                    let cpu = data.get("cpu").and_then(|v| v.as_f64()).unwrap_or(0.0) * 100.0;
                    let mem_used = data.get("mem").and_then(|v| v.as_u64()).unwrap_or(0);
                    let mem_total = data.get("maxmem").and_then(|v| v.as_u64()).unwrap_or(0);
                    let disk_used = data.get("disk").and_then(|v| v.as_u64()).unwrap_or(0);
                    let disk_total = data.get("maxdisk").and_then(|v| v.as_u64()).unwrap_or(0);

                    return Ok(ResourceSnapshot {
                        cpu_percent: cpu,
                        memory_used: mem_used,
                        memory_total: mem_total,
                        disk_used,
                        disk_total,
                    });
                }
            }
        }
    }

    Err(ApiError::internal(format!(
        "could not collect proxmox resource stats for VMID {vmid}"
    )))
}

fn get_proxmox_node() -> String {
    // Read local hostname as node name
    fs::read_to_string("/etc/hostname")
        .map(|s| s.trim().to_owned())
        .unwrap_or_else(|_| "localhost".to_owned())
}

// ---------------------------------------------------------------------------
// Batch collection for all monitors
// ---------------------------------------------------------------------------

/// Collect resource snapshots for all monitors that have provider_kind and instance_name set.
/// Returns a map of monitor_id -> ResourceSnapshot.
pub fn collect_all_resources(
    monitors: &[(i64, Option<String>, Option<String>)], // (monitor_id, provider_kind, instance_name)
) -> HashMap<i64, ResourceSnapshot> {
    let mut results = HashMap::new();

    for (monitor_id, kind_str, instance_name) in monitors {
        let kind = match kind_str.as_deref().and_then(ProviderKind::from_str_opt) {
            Some(k) => k,
            None => continue,
        };
        let name = match instance_name.as_deref() {
            Some(n) if !n.is_empty() => n,
            _ => continue,
        };

        match collect_resource(kind, name) {
            Ok(snap) => {
                results.insert(*monitor_id, snap);
            }
            Err(e) => {
                debug!(monitor_id, instance_name = name, error = %e.message, "resource collection failed");
            }
        }
    }

    results
}

// ---------------------------------------------------------------------------
// Parsing helpers
// ---------------------------------------------------------------------------

fn parse_percent_string(s: &str) -> f64 {
    s.trim_end_matches('%').trim().parse::<f64>().unwrap_or(0.0)
}

fn parse_mem_usage(s: &str) -> (u64, u64) {
    // Format: "123.4MiB / 1.5GiB"  or  "123456 / 456789"
    let parts: Vec<&str> = s.split('/').collect();
    if parts.len() != 2 {
        return (0, 0);
    }
    (parse_size_string(parts[0].trim()), parse_size_string(parts[1].trim()))
}

fn parse_size_string(s: &str) -> u64 {
    let s = s.trim();
    if s.is_empty() || s == "max" {
        return 0;
    }

    // Try parsing as pure number first
    if let Ok(v) = s.parse::<u64>() {
        return v;
    }

    // Extract numeric prefix and unit suffix
    let mut num_end = 0;
    for (i, c) in s.char_indices() {
        if c.is_ascii_digit() || c == '.' {
            num_end = i + c.len_utf8();
        } else {
            break;
        }
    }

    if num_end == 0 {
        return 0;
    }

    let num: f64 = match s[..num_end].parse() {
        Ok(v) => v,
        Err(_) => return 0,
    };

    let unit = s[num_end..].trim().to_ascii_lowercase();
    let multiplier: f64 = match unit.as_str() {
        "b" | "" => 1.0,
        "k" | "kb" | "kib" => 1024.0,
        "m" | "mb" | "mib" => 1024.0 * 1024.0,
        "g" | "gb" | "gib" => 1024.0 * 1024.0 * 1024.0,
        "t" | "tb" | "tib" => 1024.0 * 1024.0 * 1024.0 * 1024.0,
        _ => 1.0,
    };

    (num * multiplier) as u64
}

fn extract_value_after(line: &str, prefix: &str) -> Option<String> {
    if let Some(pos) = line.find(prefix) {
        Some(line[pos + prefix.len()..].trim().to_owned())
    } else {
        None
    }
}
