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

struct EgressProcessOwner {
    child: Option<std::process::Child>,
    finished: bool,
}

impl Drop for EgressProcessOwner {
    fn drop(&mut self) {
        if !self.finished {
            if let Some(mut child) = self.child.take() {
                unsafe { libc::killpg(child.id() as libc::pid_t, libc::SIGKILL); }
                let _ = child.kill();
                // Reap without holding the routing reconciliation worker if
                // the kernel temporarily leaves a killed process in D-state.
                std::thread::spawn(move || { let _ = child.wait(); });
            }
        }
    }
}

fn nonblocking_pipe(fd: std::os::fd::RawFd) -> Result<(), String> {
    let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
    if flags < 0 || unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } < 0 {
        return Err(format!("configuring command pipe: {}", std::io::Error::last_os_error()));
    }
    Ok(())
}

fn drain_command_pipe(reader: &mut impl Read, data: &mut Vec<u8>) -> Result<bool, String> {
    let mut buf = [0u8; 8192];
    // Bound each polling turn so continuous output cannot starve the deadline.
    for _ in 0..64 {
        match reader.read(&mut buf) {
            Ok(0) => return Ok(true),
            Ok(n) => data.extend_from_slice(&buf[..n]),
            Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => return Ok(false),
            Err(e) if e.kind() == std::io::ErrorKind::Interrupted => continue,
            Err(e) => return Err(format!("reading command output: {e}")),
        }
    }
    Ok(false)
}

impl SystemExecutor {
    fn run_with_timeout(
        &self, program: &str, args: &[String], input: Option<&str>, timeout: Duration,
    ) -> Result<CommandResult, String> {
        use std::os::fd::AsRawFd;
        use std::os::unix::process::CommandExt;
        let mut command = Command::new(program);
        command.args(args).process_group(0).stdout(Stdio::piped()).stderr(Stdio::piped())
            .stdin(if input.is_some() { Stdio::piped() } else { Stdio::null() });
        let child = command.spawn().map_err(|e| format!("failed to start {program}: {e}"))?;
        let mut owner = EgressProcessOwner { child: Some(child), finished: false };
        let child = owner.child.as_mut().unwrap();
        let mut stdin = child.stdin.take();
        let mut stdout = child.stdout.take().unwrap();
        let mut stderr = child.stderr.take().unwrap();
        nonblocking_pipe(stdout.as_raw_fd())?;
        nonblocking_pipe(stderr.as_raw_fd())?;
        if let Some(pipe) = &stdin { nonblocking_pipe(pipe.as_raw_fd())?; }
        let input = input.unwrap_or("").as_bytes();
        let (mut input_offset, mut output, mut errors) = (0, Vec::new(), Vec::new());
        let (mut out_done, mut err_done) = (false, false);
        let started = Instant::now();
        loop {
            if started.elapsed() >= timeout { return Err(format!("{program} timed out")); }
            if let Some(pipe) = &mut stdin {
                match pipe.write(&input[input_offset..]) {
                    Ok(n) => input_offset += n,
                    Err(e) if e.kind() == std::io::ErrorKind::WouldBlock || e.kind() == std::io::ErrorKind::Interrupted => {}
                    Err(e) => return Err(format!("failed to write {program} input: {e}")),
                }
                if input_offset == input.len() { stdin = None; }
            }
            if !out_done { out_done = drain_command_pipe(&mut stdout, &mut output)?; }
            if !err_done { err_done = drain_command_pipe(&mut stderr, &mut errors)?; }
            // Do not reap the leader before descendants release their pipes;
            // keeping the PID reserved makes timeout group cleanup ABA-safe.
            if out_done && err_done {
                if let Some(status) = owner.child.as_mut().unwrap().try_wait()
                    .map_err(|e| format!("failed waiting for {program}: {e}"))? {
                    owner.finished = true;
                    return Ok(CommandResult {
                        success: status.success(),
                        stdout: String::from_utf8_lossy(&output).to_string(),
                        stderr: String::from_utf8_lossy(&errors).to_string(),
                    });
                }
            }
            std::thread::sleep(Duration::from_millis(20));
        }
    }
}

impl CommandExecutor for SystemExecutor {
    fn run(&self, program: &str, args: &[String], input: Option<&str>) -> Result<CommandResult, String> {
        self.run_with_timeout(program, args, input, COMMAND_TIMEOUT)
    }
}

#[cfg(test)]
mod command_timeout_tests {
    use super::*;

    #[test]
    fn inherited_pipe_and_blocked_stdin_cannot_bypass_deadline() {
        for (script, input) in [
            ("sleep 10 & exit 0", None),
            ("sleep 10", Some("x".repeat(1024*1024))),
        ] {
            let started = Instant::now();
            let result = SystemExecutor.run_with_timeout("sh", &["-c".into(), script.into()],
                input.as_deref(), Duration::from_millis(100));
            assert!(result.unwrap_err().contains("timed out"));
            assert!(started.elapsed() < Duration::from_secs(2));
        }
    }

    #[test]
    fn command_input_and_final_output_are_preserved() {
        let result = SystemExecutor.run_with_timeout("sh", &["-c".into(), "cat; printf tail >&2".into()],
            Some("input"), Duration::from_secs(2)).unwrap();
        assert!(result.success);
        assert_eq!(result.stdout, "input");
        assert_eq!(result.stderr, "tail");
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
