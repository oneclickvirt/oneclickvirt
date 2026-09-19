# 专用节点真实验收 / Disposable-node live acceptance

`live_incus_panel_test.py` 从本地构建的 all-in-one 镜像启动独立面板，完成初始化、管理员登录和有期限 API token 创建。在授权节点创建两代容器，验证面板返回的 SSH 地址、嵌套设置、删除清理和连续复用同一组四个 NAT 端口。失败时保留本轮资源并打印名称，便于诊断；成功时检查并清理本轮资源。

`hetzner_ipv6_external_probe.py` 使用项目内唯一且显式指定的 Hetzner 测试服务器作为独立 IPv6 探针。它拒绝项目内存在多台服务器，只对现有服务器重置 root 密码，不创建、不删除服务器；生产节点与 Hetzner 双向严格固定临时主机密钥并完成 SSH/HTTP。随后在生产节点创建随机命名的 Incus guest 和两个精确 IPv6 proxy，由 Hetzner 实际登录 guest、请求随机 HTTP 身份并检查 guest IPv6 出网；成功或失败均只清理本轮 guest 和映射。

`production_panel_ipv6_probe.py` 在已授权生产面板上走真实 API 任务链，使用现有 Incus/LXD Provider 创建一个随机命名的 `nat_ipv4_ipv6` 容器，再由项目内唯一 Hetzner 服务器经生产公网 IPv6 完成 HTTP 身份和 SSH 密码认证，并校验 guest IPv6 出口。驱动不创建、重建或删除 Hetzner 服务器，不修改既有实例；只删除自己创建的映射和容器，恢复临时调整的 Provider 预算开关，并比对执行前后实例集合。该验收不使用浏览器或第三方 WebSSH。

```bash
export OCV_LIVE_DISPOSABLE=yes
export OCV_HETZNER_SERVER_ID='<existing-single-server-id>'
export OCV_LIVE_HOST='<production-node-ipv4>'
export OCV_LIVE_SSH_PORT='<production-ssh-port>'
export OCV_LIVE_IPV6_TARGET='<production-public-ipv6>'
export OCV_LIVE_IMAGE='<existing-incus-image-alias-or-fingerprint>'
export OCV_LIVE_GUEST_IPV4='<unused-bridge-ipv4>'
export OCV_LIVE_GUEST_IPV6='<unused-bridge-ula>'
python3 scripts/tests/hetzner_ipv6_external_probe.py
```

Hetzner API token 与生产节点密码均从无回显提示输入，不写入环境、命令行或报告。驱动使用的宿主端口默认是 `29987`/`29988`，可通过 `OCV_LIVE_IPV6_SSH_PORT`/`OCV_LIVE_IPV6_HTTP_PORT` 修改；运行前会拒绝已占用端口和地址。

成功清理先核对 run 标签，再按不可复用的容器 ID 删除容器及其匿名数据库/存储卷；不删除命名卷。`python3 scripts/tests/live_panel_cleanup_test.py --docker` 同时执行故障契约和真实 Docker 卷回归，验证错误归属保留、匿名卷删除及命名卷保留。未带 `--docker` 仅执行本地契约测试，不计作 Docker 实测。

Agent 模式部署本地构建的真实 Rust Agent，用仅监听节点 loopback 的临时 SSH 转发连接面板，额外验证并发命令超时、WebSSH 会话隔离、双向公网流量、Agent 重启和监控器删除。已有 Agent 安装和非空监控表受到保护，标准空表会保留。这不替代安装器测试。

需要 Docker、Python 3 和 `paramiko`；Agent 模式或 WebSSH 还需 `websocket-client`。建议在临时虚拟环境中安装依赖：`python3 -m venv /tmp/oneclickvirt-live-venv && /tmp/oneclickvirt-live-venv/bin/python -m pip install -r scripts/tests/requirements-live.txt`。节点需要已初始化的运行时、systemd、nftables/iptables 查询工具和可用镜像。缺少必需参数时失败退出，未执行不计为通过。

| 环境变量 | 用途 |
|---|---|
| `OCV_LIVE_DISPOSABLE=yes` | 确认节点属于授权测试环境 |
| `OCV_LIVE_HOST` / `OCV_LIVE_PASSWORD` | 节点公网 IPv4 与 root 密码 |
| `OCV_LIVE_KNOWN_HOSTS` | 可选的严格 SSH `known_hosts` 文件；未设置时使用系统 `~/.ssh/known_hosts`，节点指纹未知或变化会直接失败 |
| `OCV_LIVE_IMAGE` | 节点支持的镜像名或缓存 fingerprint |
| `OCV_PANEL_IMAGE` | 当前源码构建的本地 all-in-one 镜像 |
| `OCV_LIVE_RUNTIME` | `incus`（默认）或 `lxd` |
| `OCV_LIVE_CONNECTION` | `ssh`（默认）或 `agent` |
| `OCV_LIVE_NETWORK_TYPE` | 面板网络模式：`nat_ipv4`、`ipv6_only`、`dedicated_ipv4_ipv6` 或 `nat_ipv4_ipv6`，默认 `nat_ipv4` |
| `OCV_LIVE_IPV6=yes` | 对 IPv6 网络模式启用严格公网 IPv6 SSH/HTTP 验收，默认 `no` |
| `OCV_HETZNER_SERVER_ID` / `OCV_LIVE_IPV6_TARGET` | Hetzner 独立探针的唯一服务器 ID 与生产节点公网 IPv6；用于 `hetzner_ipv6_external_probe.py` |
| `OCV_PRODUCTION_ACCEPTANCE=yes` / `OCV_PANEL_BASE` / `OCV_LIVE_PROVIDER_ID` | 显式授权生产 API 验收、面板基址和已有 Incus/LXD Provider ID；用于 `production_panel_ipv6_probe.py` |
| `OCV_LIVE_GUEST_IPV4` / `OCV_LIVE_GUEST_IPV6` | 独立探针临时 Incus guest 的空闲静态地址；IPv6 可为受控 ULA，通过宿主公网 IPv6 proxy 验收 |
| `OCV_LIVE_STORAGE_POOL` | 已初始化的存储池，默认 `default` |
| `OCV_LIVE_PORT` | 连续四个空闲 NAT 端口的起点，默认 `29900` |
| `OCV_AGENT_BINARY` | Agent 模式必填，与节点架构匹配的 Linux 二进制 |
| `OCV_LIVE_NESTED_DOCKER=yes` | 每代容器实际安装当前官方 Docker 并运行 hello-world，默认 `no` 只读回嵌套配置 |
| `OCV_LIVE_SIBLING_SESSIONS=yes` | Agent 模式另建并发容器，验证不同 guest 的 WebSSH/超时隔离；需连续 8 个端口，默认 `no` |
| `OCV_WEBSSH_URL` / `OCV_WEBSSH_SOURCE_IP` | 可选 WebSSH 网页及预期独立 IPv4 SSH 来源 IP，同时设置 |
| `OCV_WEBSSH_SOURCE_IPV6` | IPv6 WebSSH 严格验收时的实际 SSH 来源 IPv6；不能填网页服务的 IPv4 |
| `REMOTE_KNOWN_HOSTS` / `EXTERNAL_PROBE_KNOWN_HOSTS` | `ipv6_external_acceptance_test.sh` 使用的节点与独立探针 SSH 信任文件；默认均为本机 `~/.ssh/known_hosts`，不存在或指纹不匹配直接失败 |

在仓库根目录的 Bash 中先设置非敏感参数，再输入密码。不要将密码写入脚本或报告。

嵌套 Docker 模式要求 Debian/Ubuntu guest，容器内存为 512 MiB。它通过官方签名软件源安装当前 Docker/容器运行时，实际执行带 `net.ipv4.ip_unprivileged_port_start=0` 的 hello-world，检查退出码、真实程序输出及随机结束标记。容器保持非特权，拒绝 raw AppArmor/LXC 绕过配置；网络、软件源或运行失败均不算通过。`live_nested_docker_test.py` 仅验证探针错误处理，真正结果以节点执行记录为准。

不同容器会话模式保留原来的同 guest 双会话与双代端口复用测试，再实际创建 256 MiB 的 sibling guest，通过独立 SSH 核对 hostname。关闭 A 的 WebSSH 后，在普通 Agent 命令超时期间检查 B；删除 sibling 后还检查原有 guest 和 SSH transport 继续可用。失败保留本轮面板及 guest，默认关闭此附加模式不会被记录为通过。

```bash
read -r -s -p 'Node root password: ' OCV_LIVE_PASSWORD
export OCV_LIVE_PASSWORD
export OCV_LIVE_DISPOSABLE=yes
python3 -B scripts/tests/live_incus_panel_test.py
unset OCV_LIVE_PASSWORD
```

`webssh_external_probe.py` 使用网页表单和终端协议，核对 guest hostname 与 `$SSH_CONNECTION` 来源。它需要目标 guest 的凭据，不需要 WebSSH 服务宿主机的 SSH 凭据。HTTP 和 WebSocket 使用同一网络路径；HTTPS 验证系统 CA。

设置 `OCV_LIVE_NETWORK_TYPE=ipv6_only`、`dedicated_ipv4_ipv6` 或 `nat_ipv4_ipv6` 且 `OCV_LIVE_IPV6=yes` 时，驱动不会把 IPv4 或 ULA 当作 IPv6 通过：容器内必须能请求 `ipv6.ip.sb`，两个独立公网 HTTP 探针必须返回本轮随机标记，并且 WebSSH（若启用）必须以公网 IPv6 目标和 `OCV_WEBSSH_SOURCE_IPV6` 实际完成 SSH 登录。缺少独立 IPv6 条件会失败，不会静默跳过。

IPv6 目标自动检查实际 SSH 连接的地址族、公共地址、目标地址和端口；也可显式传入 `require_ipv6=True`。此时 `expected_source_ip` 必须是 WebSSH 服务实际发起 SSH 的公网 IPv6，不能用网页的 IPv4 地址代替。ULA、IPv4 映射地址、错误端口及同地址的源/目标不能通过。每次终端校验使用随机标记、有限输出缓存和总接收时限。这些校验不替代独立公网 HTTP 验证。宿主公网 IPv6 映射到 ULA guest 的 NAT 验收使用 Hetzner 驱动，不依赖 WebSSH。

`live_lxc_script_test.py` 使用同样的节点授权/凭据变量，另需 `OCV_SCRIPT_REPO` 指向当前 Incus 或 LXD 仓库，`OCV_SCRIPT_SYSTEM` 默认 `debian13`。它要求 runtime 没有已有容器，核对上传脚本的 SHA-256 后测试关闭 stdin 的 `buildct.sh` 和真实 PTY 的 `add_more.sh`，并验证公网 SSH、DNS、出网、原生 CLI 删除和端口复用。默认保留 `29800–29825`，可用 `OCV_LIVE_PORT` 调整；此脚本不需要面板镜像。替换的辅助脚本精确备份，成功恢复，失败保留归属目录供诊断。这不是环境安装/卸载或浏览器 UI 验收。

`live_lxc_install_test.py` 使用 `OCV_SCRIPT_REPO` 和 `OCV_LIVE_RUNTIME` 选择当前安装器，`OCV_LIVE_MODE=noninteractive` 关闭 stdin。辅助脚本使用 SSH loopback 镜像读取本地源码，仅改写本仓库下载 URL，并记录原始/传输校验值；成功后将安装文件还原为原始 URL。系统包与镜像仍从真实来源下载。已执行的模式及边界以验收报告为准；此驱动不执行云平台 OS 重置。

`OCV_LIVE_MODE=interactive` 使用 PTY 逐项回答安装提示。Incus 正常交互安装会主动重启；驱动要求看到成功路径上的重启通知、完成所有交互并重新连接，确认 boot ID 变化后再检查初始化结果。普通断线不计为安装成功。

对于已确认后端条件的专用干净系统，可设置 `OCV_LIVE_EXPECT_STORAGE_DRIVER=btrfs`（或 `dir`、`lvm`、`zfs`、`ceph`），严格核对 default 池实际后端；回退到其他后端不计该专项通过。不设置时保留合法回退及自定义池，不据此声称首选后端安装通过。对应 Python 单测只是验收契约，不代替真机首装。

安装、创建和卸载驱动共用双流读取逻辑，持续输出也受总超时约束；读取退出码之后的输出直至 EOF。超时关闭当前命令 channel，保留 SSH transport，交互日志仅保留有界提示/诊断缓存。`remote_ssh_io_test.py` 使用真实本地 SSH transport 回归这些边界；它不等于运行时安装验收。

SSH 控制连接每 15 秒发送保活，避免等待独立面板 API 任务时长期无数据。保活不会重连或自动重试有副作用的命令，真实断线仍报错。回归包含命令 EOF 后的实际 SSH 保活消息，并保留原有命令超时与共享连接隔离检查。

安装、脚本创建、面板验收和卸载驱动的每个节点命令都会保留当前 PATH 并追加 `/snap/bin` 与 `/var/lib/snapd/snap/bin`，包括真实 PTY 命令。安装器进程中的 export 不会影响下一条 SSH exec；此处理避免把 snap 的 `lxc` 不在默认 PATH 误判成未安装，也让安装前的跨运行时保护能发现它。不会通过 source 用户 profile/rc 来获得环境。`live_node_shell_test.py` 在真实非登录 shell 验证命令发现与失败传播，其中临时可执行文件仅模拟路径定位，不模拟或证明 LXD daemon 可用。

`live_incus_uninstall_test.py` 保留原文件名，通过 `OCV_LIVE_RUNTIME=incus|lxd`（默认 Incus）选择对应的 `OCV_SCRIPT_REPO`。要求相同的节点授权/凭据，拒绝卸载有已有 guest 的运行时，LXD 还会拒绝存在其他项目的情况。`OCV_LIVE_MODE=interactive`（默认）通过真实 PTY 回答确认；`noninteractive` 关闭 stdin。它验证软件包或 snap、数据目录、网桥、挂载及自身防火墙持久化清理；LXD 无交互模式设置 `REMOVE_STORAGE=true` 测试完整存储卸载，但保留共享 snapd 和正常恢复快照。失败保留明确命名的诊断文件，实际执行记录见验收报告。

这些是 token API 与实际网络测试，不等于浏览器 UI 或干净系统安装验收。IPv4 成功也不代表 IPv6 成功。公网 IPv6 SSH 和 HTTP 必须分别通过独立外部请求验证。`ipv6_external_acceptance_test.sh` 使用独立 SSH 探针主机，节点和探针连接均严格校验 `known_hosts`；容器公钥通过已验证的节点连接固定到探针后再发起 SSH。缺少信任文件或指纹变化会失败，不会自动接受。WebSSH 网页可完成 SSH 验收，但不能替代外部 HTTP 请求。

修复缺失的宿主 IPv6 路由时，先确认节点获分配的前缀与网关依据，临时添加精确地址/路由并检查 DAD，再用 `curl --noproxy '*' -6 -fsS --connect-timeout 10 --max-time 25 https://ipv6.ip.sb` 验证实际公网源地址。失败删除本轮新增项；成功才写入当前系统的持久配置并验证重启。不能从某个邻居可 ping 推断它可作为网关，也不能把宿主 `/128` 擅自扩为可分配 `/64`。若已确认供应商委派完整前缀，可另行按该前缀配置容器地址池。出网不等于入站；公网 TCP 握手不等于 SSH 登录，宿主 HTTP 不等于容器 HTTP。

LXD 纯交互卸载必须回答“确认卸载”和“删除存储后端文件”两次提问，不能只收到第一次就判完成；此模式不预设 `REMOVE_STORAGE`，完整存储清理由真实第二次回答选择。仅无交互模式使用 `REMOVE_STORAGE=true`。`live_uninstall_prompts_test.py` 回归分片、合并、重复输出和不完整问答，不替代实际卸载验收。

Incus/LXD 仓库各自的 `tests/masquerade_kernel_test.sh` 另行验证安装器 NAT。以 root 运行时，它先创建独立的网络和挂载命名空间，再通过真实 IPv4/IPv6 数据包验证源地址、重复配置、关闭 NAT 和卸载规则归属。只有运行时配置读取使用测试数据，nftables 与两种 iptables 后端均为真实执行；缺少内核功能时报错，不跳过。这项测试不能替代公网 IPv6 验收。

设置 `OCV_TEST_FIREWALLD=true` 并安装 firewalld、dbus 后，该测试还在隔离命名空间启动真实守护进程，验证重载、永久配置、迁移至 nftables、运行中/离线卸载和网桥区域保留。两个脚本仓库的 CI 已启用此模式。它验证守护进程重启，不替代不同宿主 OS 的安装、系统重启及公网访问验收。

## English

Every node command in the installation, script, panel and uninstall drivers preserves PATH and appends `/snap/bin` and `/var/lib/snapd/snap/bin`, including PTY commands. An export inside the installer cannot affect the next SSH exec channel. This avoids false missing-LXD results and makes the pre-install cross-runtime guard see snap commands without sourcing user startup files. `live_node_shell_test.py` executes real non-login shells; its temporary executable tests discovery only, not a LXD daemon or a clean-OS installation.

`live_incus_panel_test.py` starts an isolated panel from a local all-in-one image, initializes it, signs in, creates an expiring API token, and creates two generations of real Incus/LXD containers. It checks the panel-reported SSH endpoint, nesting, deletion and reuse of four NAT ports. Failures retain this run's named fixtures for diagnosis; successful runs verify cleanup.

`hetzner_ipv6_external_probe.py` uses the explicitly selected and only server in a Hetzner project as an independent IPv6 probe. It refuses projects with multiple servers, resets only the existing server's root password, and never creates or deletes a server. Production and Hetzner authenticate bidirectionally with temporary pinned host keys, then Hetzner reaches a random Incus guest through exact production IPv6 SSH/HTTP proxy devices and verifies guest IPv6 egress. Every exit path removes only this run's guest and proxy devices.

`production_panel_ipv6_probe.py` exercises the real production panel API task chain against an existing Incus/LXD provider. It creates one randomly named `nat_ipv4_ipv6` container, has the project's single Hetzner server verify its HTTP identity and authenticate over SSH through the production public IPv6 address, and checks guest IPv6 egress. It never creates, rebuilds or deletes a Hetzner server and never mutates existing instances. Cleanup removes only the owned mapping and container, restores temporarily changed provider budget switches, and compares the instance set before and after the run. This acceptance does not use a browser or third-party WebSSH.

Successful cleanup verifies the run label and deletes by immutable container ID, including attached anonymous database/storage volumes while preserving named volumes. `python3 scripts/tests/live_panel_cleanup_test.py --docker` runs both failure contracts and real Docker checks for wrong-owner preservation, anonymous-volume removal and named-volume preservation. Without `--docker`, only local contract tests run; that is not Docker runtime evidence.

Set `OCV_LIVE_CONNECTION=agent` and provide a node-compatible Linux `OCV_AGENT_BINARY` to exercise the real Rust Agent. A temporary reverse SSH forward exposes the local panel only on node loopback. Additional checks cover command timeout isolation, concurrent WebSSH sessions, external traffic in both directions, Agent restart and monitor cleanup. Existing Agent installations and nonempty monitor tables are protected.

`OCV_LIVE_HOST`, `OCV_LIVE_PASSWORD`, `OCV_LIVE_IMAGE`, `OCV_PANEL_IMAGE` and `OCV_LIVE_DISPOSABLE=yes` are mandatory. Runtime, connection, pool and first port default to `incus`, `ssh`, `default` and `29900`. Install the dependencies in a temporary virtual environment with `python3 -m venv /tmp/oneclickvirt-live-venv && /tmp/oneclickvirt-live-venv/bin/python -m pip install -r scripts/tests/requirements-live.txt`; Agent or WebSSH checks also require `websocket-client`. Node SSH uses strict host-key verification and never auto-accepts a changed key; set `OCV_LIVE_KNOWN_HOSTS` to an isolated trusted file when the system file is not appropriate. Run the Bash example after setting the nonsecret variables in the table.

The optional WebSSH probe uses the target guest's credentials through the web form and terminal protocol. `OCV_WEBSSH_SOURCE_IP` verifies its independent source. SSH access to the WebSSH server itself is unnecessary.

`OCV_LIVE_NESTED_DOCKER=yes` installs current official Docker in every Debian/Ubuntu guest (512 MiB memory) and runs hello-world with `net.ipv4.ip_unprivileged_port_start=0`. Success requires exit status, actual program output and a random terminal marker. Guests remain nonprivileged, without raw AppArmor/LXC bypasses. Network, repository and runtime failures are not passes. The default `no` only reads back nesting configuration. `live_nested_docker_test.py` validates probe failure handling, not actual nested execution.

`OCV_LIVE_SIBLING_SESSIONS=yes` adds a simultaneous 256 MiB guest in Agent mode and reserves eight consecutive ports. It keeps the original same-guest sessions and two-generation reuse tests, additionally verifies each guest identity over public SSH, checks guest B after closing guest A's WebSSH and during command timeout, then deletes the sibling and checks the original guest/SSH transport survive. Failed fixtures remain for diagnosis. The default `no` does not count this extra scenario as passed.

An IPv6 target automatically requires an actual public IPv6 SSH connection to the exact address and port; callers can also pass `require_ipv6=True`. The expected source must be the WebSSH service's actual public IPv6 SSH source, not its web endpoint's IPv4 address. ULA, IPv4-mapped addresses, wrong ports and identical source/destination addresses fail. Per-session random markers, bounded output retention and a receive deadline protect terminal verification. This does not replace the independent public HTTP check. Host-public-IPv6-to-ULA NAT acceptance uses the Hetzner driver and does not depend on WebSSH.

`live_lxc_script_test.py` uses the same node authorization/credential variables plus `OCV_SCRIPT_REPO` pointing to the current Incus/LXD checkout. `OCV_SCRIPT_SYSTEM` defaults to `debian13`. It requires an empty runtime, verifies uploaded source hashes, runs unattended `buildct.sh` with closed stdin and answers real `add_more.sh` PTY prompts. It checks public SSH, DNS, outbound HTTP, native CLI deletion and port reuse. Its default port range is `29800–29825`; override the start with `OCV_LIVE_PORT`. It does not require a panel image. Replaced helpers are backed up and restored on success; failed fixtures remain for diagnosis. This does not cover environment installation/uninstallation or browser UI.

`live_lxc_install_test.py` selects the current installer with `OCV_SCRIPT_REPO` and `OCV_LIVE_RUNTIME`. `OCV_LIVE_MODE=noninteractive` closes stdin. A mirror accessible only through SSH loopback serves the local helpers, replacing only this repository's download URLs and logging source/transport hashes. Successful runs restore original URLs in installed files. Packages and images use their real download sources. Consult the acceptance report for modes actually executed; this driver does not reset the cloud VM's OS.

`OCV_LIVE_MODE=interactive` answers the real PTY prompts. Incus reboots after a successful interactive installation. The driver requires the expected final reboot announcement, every prompt, reconnection and a changed boot ID before checking runtime initialization. An ordinary disconnect is not counted as success.

For a dedicated clean-OS fixture with known backend support, set `OCV_LIVE_EXPECT_STORAGE_DRIVER=btrfs` (or `dir`, `lvm`, `zfs`, `ceph`). The driver then requires the default pool to use that backend; an unexpected fallback fails this specific acceptance. Leaving it unset preserves valid fallback/custom-pool behavior and makes no claim about a preferred backend. `live_install_storage_test.py` checks this acceptance contract, not real installation.

Installation, creation and removal drivers share the dual-stream reader. Continuous output cannot bypass the deadline, and output after exit status is drained through EOF. Timeout closes the command channel while preserving the SSH transport; prompt/diagnostic buffers remain bounded. `remote_ssh_io_test.py` covers these boundaries with real local SSH transports, which does not constitute runtime installation acceptance.

SSH control transports send keepalives every 15 seconds while separate panel API tasks run. This does not reconnect or automatically retry commands with side effects; actual disconnections still fail. Regression coverage observes a real SSH keepalive after command EOF and retains the deadline and shared-transport isolation checks.

`live_incus_uninstall_test.py` retains its historical filename and selects the matching `OCV_SCRIPT_REPO` with `OCV_LIVE_RUNTIME=incus|lxd` (default: Incus). It requires the same node authorization/credentials and refuses runtimes with existing guests; LXD also rejects other projects. `OCV_LIVE_MODE=interactive` (default) answers the real PTY confirmation; `noninteractive` closes stdin. Postconditions cover packages or snap, runtime data, bridges, mounts and owned firewall persistence. Unattended LXD removal uses `REMOVE_STORAGE=true` to exercise storage removal while preserving shared snapd and normal recovery snapshots. Failures retain named diagnostic files; consult the acceptance report for combinations actually executed.

Pure interactive LXD removal must answer both the uninstall and backing-storage confirmations; the first answer alone is incomplete. This mode does not preset `REMOVE_STORAGE`: the second actual answer selects storage removal. Only unattended mode sets `REMOVE_STORAGE=true`. `live_uninstall_prompts_test.py` covers split, coalesced, repeated and incomplete dialogue output, not actual runtime removal.

These checks do not constitute browser UI or clean-OS installer coverage. IPv4 success does not establish IPv6 connectivity. Public IPv6 SSH and HTTP each require an independent external request. The strict IPv6 shell script uses a separate SSH probe host and verifies both node/probe host keys from `REMOTE_KNOWN_HOSTS` and `EXTERNAL_PROBE_KNOWN_HOSTS` (default: the local `~/.ssh/known_hosts`). The disposable container's public host keys are read through the already trusted node connection and pinned in a temporary probe-side known_hosts file before the IPv6 SSH check. Unknown or changed keys fail instead of being accepted. WebSSH is an alternative SSH probe, but cannot replace the HTTP check. Missing prerequisites fail instead of counting as passing tests.

When repairing a missing host IPv6 route, establish the assigned prefix and the basis for a gateway candidate, temporarily add exact addresses/routes, check DAD, and verify the public source with `curl --noproxy '*' -6 -fsS --connect-timeout 10 --max-time 25 https://ipv6.ip.sb`. Remove this run's additions on failure; persist only a successful configuration and test reboot recovery. A neighbor answering ping is not proof that it is a usable gateway. Never expand a host `/128` into an allocation `/64` without a confirmed delegated prefix. Configure the container pool separately from that verified delegation. Egress is not ingress, a TCP handshake is not SSH authentication, and host HTTP is not container HTTP.

Each Incus/LXD repository also provides `tests/masquerade_kernel_test.sh`. Run it as root with Linux network and mount namespace privileges. It isolates itself before testing actual IPv4/IPv6 packets, repeated configuration, NAT disablement and uninstall ownership using nftables and both iptables backends. Only runtime configuration reads use fixture data; missing kernel features fail rather than skip. This does not constitute public IPv6 acceptance.

With `OCV_TEST_FIREWALLD=true` and firewalld/dbus installed, the test also starts real daemons inside isolated namespaces and checks reload, permanent policy, migration to nftables, online/offline removal and bridge-zone preservation. Both script repositories enable this mode in CI. Daemon restart coverage does not replace installation, host reboot and public connectivity acceptance across operating systems.
