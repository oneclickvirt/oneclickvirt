#[derive(Debug, Clone)]
struct CommandResult {
    success: bool,
    stdout: String,
    stderr: String,
}

trait CommandExecutor {
    fn run(
        &self,
        program: &str,
        args: &[String],
        input: Option<&str>,
    ) -> Result<CommandResult, String>;
}

struct SystemExecutor;

impl CommandExecutor for SystemExecutor {
    fn run(
        &self,
        program: &str,
        args: &[String],
        input: Option<&str>,
    ) -> Result<CommandResult, String> {
        let mut command = Command::new(program);
        command
            .args(args)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        if input.is_some() {
            command.stdin(Stdio::piped());
        }
        let mut child = command
            .spawn()
            .map_err(|e| format!("failed to start {program}: {e}"))?;
        if let (Some(data), Some(stdin)) = (input, child.stdin.as_mut()) {
            stdin
                .write_all(data.as_bytes())
                .map_err(|e| format!("failed to write {program} input: {e}"))?;
        }
        drop(child.stdin.take());
        let mut stdout = child
            .stdout
            .take()
            .ok_or_else(|| format!("failed capturing {program} stdout"))?;
        let mut stderr = child
            .stderr
            .take()
            .ok_or_else(|| format!("failed capturing {program} stderr"))?;
        let stdout_reader = std::thread::spawn(move || {
            let mut data = Vec::new();
            let _ = stdout.read_to_end(&mut data);
            data
        });
        let stderr_reader = std::thread::spawn(move || {
            let mut data = Vec::new();
            let _ = stderr.read_to_end(&mut data);
            data
        });
        let started = Instant::now();
        let status = loop {
            if let Some(status) = child
                .try_wait()
                .map_err(|e| format!("failed waiting for {program}: {e}"))?
            {
                break status;
            }
            if started.elapsed() >= COMMAND_TIMEOUT {
                let _ = child.kill();
                let _ = child.wait();
                let _ = stdout_reader.join();
                let _ = stderr_reader.join();
                return Err(format!("{program} timed out"));
            }
            std::thread::sleep(Duration::from_millis(20));
        };
        let stdout = stdout_reader
            .join()
            .map_err(|_| format!("failed reading {program} stdout"))?;
        let stderr = stderr_reader
            .join()
            .map_err(|_| format!("failed reading {program} stderr"))?;
        Ok(CommandResult {
            success: status.success(),
            stdout: String::from_utf8_lossy(&stdout).to_string(),
            stderr: String::from_utf8_lossy(&stderr).to_string(),
        })
    }
}

fn command_path(command: &str) -> Option<PathBuf> {
    let path = Path::new(command);
    if path.components().count() > 1 {
        return path.is_file().then(|| path.to_path_buf());
    }
    env::var_os("PATH").and_then(|paths| {
        env::split_paths(&paths)
            .map(|path| path.join(command))
            .find(|path| path.is_file())
    })
}

fn command_available(command: &str) -> bool {
    command_path(command).is_some()
}

fn detect_package_manager() -> Option<String> {
    ["apt-get", "dnf", "yum", "apk", "pacman", "zypper"]
        .into_iter()
        .find(|name| command_available(name))
        .map(str::to_string)
}

fn sysctl_bool(path: &str) -> bool {
    fs::read_to_string(path)
        .ok()
        .map(|value| value.trim() == "1")
        .unwrap_or(false)
}

fn write_proc_flag(path: &Path, value: &str) -> Result<(), String> {
    if !path.exists() {
        return Ok(());
    }
    fs::write(path, value).map_err(|e| format!("write {}: {e}", path.display()))
}

fn ensure_kernel_prerequisites(profiles: &[ProfileRow], bindings: &[BindingRow]) -> Vec<String> {
    if unsafe { libc::geteuid() } != 0 {
        return vec!["kernel forwarding setup requires root".to_string()];
    }
    let mut errors = Vec::new();
    let has_v4 = bindings
        .iter()
        .filter(|row| row.binding.enabled)
        .flat_map(|row| &row.networks)
        .any(|network| network.family() == Family::V4);
    let has_v6 = bindings
        .iter()
        .filter(|row| row.binding.enabled)
        .flat_map(|row| &row.networks)
        .any(|network| network.family() == Family::V6);
    if has_v4 {
        for (path, value) in [
            (Path::new("/proc/sys/net/ipv4/ip_forward"), "1"),
            (Path::new("/proc/sys/net/ipv4/conf/all/src_valid_mark"), "1"),
        ] {
            if let Err(error) = write_proc_flag(path, value) {
                errors.push(error);
            }
        }
    }
    if has_v6 {
        if let Err(error) =
            write_proc_flag(Path::new("/proc/sys/net/ipv6/conf/all/forwarding"), "1")
        {
            errors.push(error);
        }
    }
    let profile_map: HashMap<&str, &EgressProfile> = profiles
        .iter()
        .map(|row| (row.profile.id.as_str(), &row.profile))
        .collect();
    let mut interfaces = HashSet::new();
    for binding in bindings.iter().filter(|row| row.binding.enabled) {
        for interface in [
            binding.binding.interface.as_deref(),
            binding.binding.interface_v4.as_deref(),
            binding.binding.interface_v6.as_deref(),
        ]
        .into_iter()
        .flatten()
        {
            interfaces.insert(interface.to_string());
        }
        if let Some(profile) = profile_map.get(binding.binding.profile_id.as_str()) {
            interfaces.insert(profile.tunnel_interface.clone());
        }
    }
    for interface in interfaces {
        // Both interface sources were validated against Linux IFNAMSIZ and a
        // strict character allow-list before reaching this path.
        let path = Path::new("/proc/sys/net/ipv4/conf")
            .join(interface)
            .join("rp_filter");
        if let Err(error) = write_proc_flag(&path, "0") {
            errors.push(error);
        }
    }
    errors
}

pub fn detect_capabilities() -> HostCapabilities {
    let running_as_root = unsafe { libc::geteuid() == 0 };
    let ip_available = command_available("ip");
    let nft_available = command_available("nft");
    let wireguard_available = command_available("wg");
    let curl_available = command_available("curl");
    let ipv4_forwarding = sysctl_bool("/proc/sys/net/ipv4/ip_forward");
    let ipv6_forwarding = sysctl_bool("/proc/sys/net/ipv6/conf/all/forwarding");
    let apply_enabled = env_enabled(APPLY_ENV);
    let auto_install_enabled = env_enabled(AUTO_INSTALL_ENV);
    let package_manager = detect_package_manager();
    let mut missing_dependencies = Vec::new();
    if !ip_available {
        missing_dependencies.push("iproute2".to_string());
    }
    if !nft_available {
        missing_dependencies.push("nftables".to_string());
    }
    if !wireguard_available {
        missing_dependencies.push("wireguard-tools".to_string());
    }
    if !curl_available {
        missing_dependencies.push("curl".to_string());
    }
    let mut reasons = Vec::new();
    if !running_as_root {
        reasons.push("agent must run as root to reconcile host networking".to_string());
    }
    if !ip_available {
        reasons.push("ip command is unavailable".to_string());
    }
    if !nft_available {
        reasons.push("nft command is unavailable".to_string());
    }
    if !curl_available {
        reasons.push("curl is unavailable for strict public egress verification".to_string());
    }
    if !ipv4_forwarding && !ipv6_forwarding {
        reasons.push("neither IPv4 nor IPv6 forwarding is enabled".to_string());
    }
    if !apply_enabled {
        reasons.push(format!("apply guard {APPLY_ENV}=true is not enabled"));
    }
    HostCapabilities {
        supported: running_as_root
            && ip_available
            && nft_available
            && curl_available
            && (ipv4_forwarding || ipv6_forwarding),
        mode: "native".to_string(),
        running_as_root,
        ip_available,
        nft_available,
        wireguard_available,
        curl_available,
        ipv4_forwarding,
        ipv6_forwarding,
        apply_enabled,
        auto_install_enabled,
        package_manager,
        missing_dependencies,
        checked_at: now_ts(),
        reasons,
    }
}

fn run_quiet_with_timeout(program: &str, args: &[&str], timeout: Duration) -> Result<(), ApiError> {
    let mut child = Command::new(program)
        .args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|e| ApiError::internal(format!("failed to start dependency installer: {e}")))?;
    let started = Instant::now();
    loop {
        if let Some(status) = child
            .try_wait()
            .map_err(|e| ApiError::internal(format!("dependency installer wait error: {e}")))?
        {
            return if status.success() {
                Ok(())
            } else {
                Err(ApiError::internal("dependency installation failed"))
            };
        }
        if started.elapsed() >= timeout {
            let _ = child.kill();
            let _ = child.wait();
            return Err(ApiError::internal("dependency installation timed out"));
        }
        std::thread::sleep(Duration::from_millis(100));
    }
}

fn dependency_command_plan(
    manager: &str,
    include_wireguard: bool,
) -> Result<Vec<Vec<String>>, ApiError> {
    let mut packages: Vec<&str> = match manager {
        "apt-get" | "apk" | "pacman" | "zypper" => vec!["iproute2", "nftables", "curl"],
        "dnf" | "yum" => vec!["iproute", "nftables", "curl"],
        _ => return Err(ApiError::bad_request("no supported package manager found")),
    };
    if include_wireguard {
        packages.push("wireguard-tools");
    }
    let mut plan = Vec::new();
    if manager == "apt-get" {
        plan.push(vec!["update".to_string(), "-qq".to_string()]);
    }
    let mut args: Vec<String> = match manager {
        "apt-get" => vec![
            "install".into(),
            "-y".into(),
            "--no-install-recommends".into(),
        ],
        "dnf" | "yum" => vec!["install".into(), "-y".into()],
        "apk" => vec!["add".into(), "--no-cache".into()],
        "pacman" => vec!["-Sy".into(), "--noconfirm".into()],
        "zypper" => vec![
            "--non-interactive".into(),
            "install".into(),
            "--no-recommends".into(),
        ],
        _ => unreachable!(),
    };
    args.extend(packages.into_iter().map(str::to_string));
    plan.push(args);
    Ok(plan)
}

fn install_dependency_set(package_set: &str) -> Result<String, ApiError> {
    if !env_enabled(AUTO_INSTALL_ENV) {
        return Err(ApiError::bad_request(format!(
            "{AUTO_INSTALL_ENV}=true is required"
        )));
    }
    if unsafe { libc::geteuid() } != 0 {
        return Err(ApiError::bad_request(
            "dependency installation requires root",
        ));
    }
    let manager = detect_package_manager()
        .ok_or_else(|| ApiError::bad_request("no supported package manager found"))?;
    let include_wireguard = match package_set {
        "native" => false,
        "wireguard" => true,
        _ => {
            return Err(ApiError::bad_request(
                "package_set must be native or wireguard",
            ));
        }
    };
    for args in dependency_command_plan(&manager, include_wireguard)? {
        let refs: Vec<&str> = args.iter().map(String::as_str).collect();
        run_quiet_with_timeout(&manager, &refs, INSTALL_TIMEOUT)?;
    }
    Ok(manager)
}

pub async fn capabilities(
    State(_state): State<AppState>,
) -> Result<Json<HostCapabilities>, ApiError> {
    Ok(Json(detect_capabilities()))
}

pub async fn ensure_dependencies(
    State(_state): State<AppState>,
    Json(req): Json<DependencyEnsureRequest>,
) -> Result<Json<DependencyEnsureResponse>, ApiError> {
    let package_set = req
        .package_set
        .unwrap_or_else(|| "wireguard".to_string())
        .trim()
        .to_ascii_lowercase();
    if !matches!(package_set.as_str(), "native" | "wireguard") {
        return Err(ApiError::bad_request(
            "package_set must be native or wireguard",
        ));
    }
    let requested = package_set.clone();
    let package_manager = tokio::task::spawn_blocking(move || install_dependency_set(&requested))
        .await
        .map_err(|e| ApiError::internal(format!("dependency installer task failed: {e}")))??;
    let capabilities = detect_capabilities();
    let installed = capabilities.ip_available
        && capabilities.nft_available
        && capabilities.curl_available
        && (package_set == "native" || capabilities.wireguard_available);
    let message = if installed {
        "dependencies are available"
    } else {
        "installer completed but required commands are still unavailable"
    };
    Ok(Json(DependencyEnsureResponse {
        attempted: true,
        installed,
        package_set,
        package_manager: Some(package_manager),
        capabilities,
        message: message.to_string(),
    }))
}
