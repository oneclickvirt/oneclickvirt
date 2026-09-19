# 专用节点真实环境验收

本记录区分真实节点执行、命令模拟回归和静态检查。只有实际执行并验证结果的组合才标为通过；未执行、受阻和运行时不支持的组合分别记录，不计为通过。

## 2026-09-19 全仓库复核补充

- Go `go test ./...`、`go test -race ./...`、`go vet ./...` 通过；Rust Agent `cargo fmt --check`、`cargo test --all-targets`（57 项）、`cargo clippy --all-targets --all-features -- -D warnings` 以及 `aarch64/x86_64-unknown-linux-musl` 检查通过；前端 75/75 用例通过且无跳过，生产构建通过。
- 修复 Rust Agent 的连接级资源隔离：exec（10）和 shell（5）限额从进程级 `OnceLock` 改为每条已认证 WebSocket 独立拥有，新增回归验证连接 A 耗尽额度不会阻塞连接 B。该边界与 Go 侧 Provider 级连接生命周期一致。
- Incus、LXD、Docker、Podman、Containerd 脚本测试逐用例执行：全部已执行用例通过；Incus/LXD 在 macOS 上仅 `flock`/隔离内核/firewalld 相关用例返回环境门槛 75，未计为通过，也没有因此停止后续用例。Linux CI 仍需执行这些门槛用例。
- 之后已在隔离 Debian Linux 容器中补跑上述门槛：Incus/LXD 的 `flock`/脚本锁、firewalld 失败传播，以及真实 nftables、iptables-nft、iptables-legacy 的双栈 NAT、幂等、NAT 禁用和卸载归属均通过；Docker、Podman、Containerd Linux 回归也通过。firewalld 守护进程重载/宿主重启仍属于真实发行版节点专项，不以容器 fixture 替代。
- 数据库真实容器兼容测试覆盖 MariaDB 与 MySQL，配置错配自动检测/修复、密码特殊字符、重启持久化均通过。Swagger 已由当前注解重新生成，主仓库与临时同步副本一致。
- Agent、任务、资源、Incus/LXD 和工具包的重点 Go 回归又以 `-race -count=20` 重复运行并通过；Rust Agent release profile 57 项也通过。macOS Go 链接器只产生已知 `LC_DYSYMTAB` 警告，未改变退出码。
- 生产节点只读复核通过：服务、Caddy、Incus 6.0.4、btrfs `default` 存储池、默认 profile、`incusbr0`、现有实例和公网 IPv6 默认路由均正常；从节点执行 `curl -6 https://ipv6.ip.sb` 返回节点公网 IPv6。本轮未修改或删除生产实例。
- Hetzner token 的只读 API 返回 401，因此没有创建或删除云服务器；专用 Unesty 测试机的既有 root 密码仍被严格 SSH 拒绝，干净系统多发行版安装/卸载和独立公网 IPv6 SSH/HTTP 端到端矩阵仍保持未完成，不以本地或生产只读证据替代。
- 本轮再次通过无浏览器 WebSSH HTTP/WebSocket 探针验证专用节点登录，服务本身可达但返回 `Authentication failed`；未猜测密码、绕过 host key 或提交重装。

## 2026-09-19 生产映射修复复验

- 生产 Linux amd64 后端已部署当前修复版本，旧二进制保留为可回滚备份；前端静态目录采用原子替换并保留旧目录。服务进程、Caddy 与 API 健康检查均通过。
- 手动创建双栈节点映射的旧生产缺陷已复现并修复：修复前 `ipv6Enabled=true` 未持久化，导致 Incus 只生成 IPv4 proxy；修复后临时端口 `19981` 的数据库记录为 `ipv6_enabled=1`，Incus 同时生成 IPv4/IPv6 proxy，两个不同 ASN 的公网 IPv6 HTTP 探针均取得临时容器校验值。
- 显式 `ipv6Enabled=false` 的临时端口 `19982` 落库为 `0`，只生成 IPv4 proxy，验证了 IPv4-only 语义没有被默认双栈配置覆盖。两组临时映射、容器服务和测试文件均已清理，并复核没有留下运行时规则。
- 本轮生产复验通过 API、SSH、Incus CLI 和 HTTP 探针完成，没有使用浏览器；本轮没有重新建立独立的公网 IPv6 SSH 认证探针，因此不能把上述 HTTP 结果表述为新的 SSH 端到端验收。此前的 WebSSH/SSH 验收证据仍按其原日期保留。
- 本轮对专用测试节点做了只读 SSH 状态复核：当前主机 ED25519 指纹与此前重装后记录一致，但此前提供的 root 密码连续两次被 SSH 拒绝；未绕过主机密钥校验、未猜测密码、未执行重装或任何破坏性操作。该节点级安装/卸载矩阵因此保持未完成，待获得新的有效凭据后才能继续。
- 同一时段对生产节点仅做只读 SSH/Incus 复核：服务进程、Caddy、Incus、btrfs `default` 存储、`default` profile、`incusbr0`、现有容器和双栈 proxy 规则均存在，未修改生产实例或端口规则。

## 对象与范围

- 用户授权的测试机：Unesty VM `de00f349-cf6b-452a-8f48-845ba5b10c54`，VMID 15597。
- 当前页面地址：IPv4 `179.61.251.239`，IPv6 `2a0f:5707:aaf1:e82a::/64`；4 vCPU、1 GiB RAM、25 GiB 磁盘。
- 此次页面中的测试机不同于此前 SSH 超时的 `179.61.253.179`。
- 环境：Incus、LXD、Docker、Podman、Containerd。每次更换环境前重装专用节点。
- 不执行 PVE、KubeVirt、QEMU 测试；Incus/LXD 的 VM 创建只做静态检查。
- 原有本地容器和数据库保留，当前源码面板使用独立测试数据。

## 宿主系统矩阵

以下是 2026-09-14 在授权页面实际可选的 Linux 系统模板；安装脚本支持情况仍需逐项验证。

| 系统 | Incus | LXD | Docker | Podman | Containerd |
|---|---|---|---|---|---|
| Debian 12 | 部分实测（纯交互干净首装，见下方记录） | 部分实测（见下方记录） | 待执行 | 待执行 | 待执行 |
| Debian 13 | 待执行 | 待执行 | 待执行 | 待执行 | 待执行 |
| Ubuntu 24.04 | 待执行 | 待执行 | 待执行 | 待执行 | 待执行 |
| Ubuntu 26.04 | 待执行 | 待执行 | 待执行 | 待执行 | 待执行 |
| AlmaLinux 10 | 待执行 | 待执行 | 待执行 | 待执行 | 待执行 |
| Rocky Linux 10 | 待执行 | 待执行 | 待执行 | 待执行 | 待执行 |
| CentOS 10 | 待执行 | 待执行 | 待执行 | 待执行 | 待执行 |
| Fedora 44 | 待执行 | 待执行 | 待执行 | 待执行 | 待执行 |

Windows Server 模板与 SystemRescue ISO 不作为这些 Linux 容器运行时的宿主安装验收对象。

## 每个受支持组合的验收项

1. 干净系统上真正通过 PTY 回答安装提示，检查运行时、存储、profile、网桥、转发和重启后的状态。
2. 运行交互卸载，检查运行时和脚本管理资源的清理；再次干净重装，使用 `noninteractive=true` 和关闭的 stdin 安装，避免依赖管道自动回答。
3. 脚本创建和删除容器，包含交互批量入口及参数/环境变量入口。
4. 本地当前源码面板分别通过 SSH 和真实 Agent 接入，执行 UI 与 token API 容器创建、访问和删除。
5. 分别验证 NAT IPv4、独立 IPv6、NAT IPv4 + 独立 IPv6；检查容器内实际地址、路由、DNS、出网及外部 SSH，不能仅以数据库配置值代替。
6. 检查 Agent 流量计数增长、重启恢复、删除后清理、重复接入和并发命令/session 隔离。
7. 检查同名创建、重复删除、失败回滚、端口复用、双栈清理和旧连接清理边界。
8. 无交互卸载并验证，然后再进入下一个组合。

## 执行记录

### 访问预检

- Firefox 原生窗口已核对 VM UUID、地址、系统模板和资源配置。
- 新节点 SSH 22 可达；用户补充当前节点凭据后，root 登录成功。
- Debian 13.6，尚未安装五种容器运行时；有既存 Komari Agent。此状态下的第一轮测试记作现状基线，不替代重新安装操作系统后的干净环境矩阵。
- 真实 IPv6 前置故障：eth0 只有公网 IPv4 和 fe80 链路本地地址；无 IPv6 默认路由，accept_ra=0。netplan 和 cloud-init 原始网络数据均仅包含 IPv4。页面分配 /64 不代表系统已配置该网段。
- 现有本地 Go/Rust/前端及 Linux 防火墙回归结果不计入本矩阵的真实环境验收。

### 独立本地面板

- 当前源码编译的 Linux arm64 后端与当前前端生产构建，独立 MariaDB 10.11。
- 容器名以 `ocv-live-20260914-` 开头；前端 `127.0.0.1:28880`、后端 `127.0.0.1:28888`，数据库不发布宿主端口。
- 初始化、管理员登录、创建有期限 API token、使用该 token 读取节点列表均已真实调用通过。尚未以此证明远端容器或 Agent 验收通过。
- 2026-09-16 复核时，仅独立数据库仍运行；此前临时前后端进程已退出，临时工作目录已不存在。上面的登录与 token 记录是历史执行证据，不代表当前仍有可用的面板会话。
- 中文、英文文档的链接检查与生产构建均通过。

### 测试工具修复

- SSH stdout/stderr 同时读取、总超时、UTF-8 分段解码、stdin EOF、退出后的尾部输出和连接关闭已补回归。真实本地 SSH transport 的 7 个测试通过，0 跳过；流式 CLI 不再重复打印输出。
- 五个安装仓库当前的 Shell 回归均已重跑通过，新增模式选择回归覆盖 24 个脚本入口。这些是模拟/本地行为回归，不计作远端安装通过。

### Debian 13 现有 Incus 环境复验（2026-09-14 18:10 CST）

- 实际服务端 Incus 6.0.4。`default` 存储池使用 dir；default profile 的 root 指向该池，eth0 指向 managed `incusbr0`。服务和 dnsmasq 均运行。
- 创建 `images:debian/13/cloud` 容器，按 IPv4 就绪状态轮询，约 2 秒后取得 `10.166.211.150/24` 与默认路由；取得 ULA IPv6 和链路本地路由，不能据此宣称公网 IPv6 可用。
- 容器内解析 `deb.debian.org`、建立该域名 80 端口 TCP 连接、读取系统 `running` 状态均通过；`security.nesting=true` 设置并读回通过。未以此替代容器内实际 Docker 嵌套运行测试。
- 删除刚创建的测试容器成功。这是现有安装的原生 Incus CLI 烟测，尚不是面板创建、脚本创建或干净重装矩阵。
- 更早的即时检查在 systemd 初始化完成前就读取地址和 system bus，产生“只有 link-local”“没有 bus”结果。等待就绪后通过，因此这些即时结果不构成 DHCP/网桥初始化失败的证据。无容器时桥显示 NO-CARRIER/linkdown 是正常状态。
- 当前主机仍无公网 IPv6 地址和默认路由，纯独立 IPv6 与 NAT IPv4 + 独立 IPv6 保持待验证。

### 独立 WebSSH 入口实测（2026-09-15 08:03 UTC）

- 已在 Firefox 实际打开用户指定的 `http://216.126.233.222:8888/`。页面是 WebSSH，提供 Hostname、Port、Username、Password、Private Key 等字段；它不是需要另行取得 SSH 凭据的探针主机登录入口。
- 通过该页面登录授权测试机 `179.61.251.239:22` 成功。远端 `SSH_CONNECTION` 显示来源为 `216.126.233.222`、目标为 `179.61.251.239:22`，证明这次 SSH 确由独立服务器发起，不只是 HTTP 页面可达。此次连接使用 IPv4，不能据此宣称 IPv6 SSH 已通过。
- 此次仅执行只读检查：Debian 13、Incus 客户端/服务端 6.0.4；`default` dir 存储池存在，default profile 的 root 与 eth0 分别绑定该池和 managed `incusbr0`；网桥 IPv4 为 `10.166.211.1/24`，DHCP 和 NAT 均开启。没有执行重装、卸载或修改网络配置。
- `ip -6 -o addr show scope global` 仅返回网桥的 `fd42:9b81:dd8f:80c::1/64`；`ip -6 route show default` 无输出。该 ULA 地址不满足独立公网 IPv6 验收条件。
- 同时在 Unesty 的 `Netzwerk & rDNS` 页面核对到：`2a0f:5707:aaf1:e82a::/64` 是委派给此 VPS 的完整前缀，页面明确注明“不会自动配置为单个 guest IP”。页面只展示 IPv4 网关，未提供 IPv6 上游网关。这属于供应商的网段交付方式，不能仅凭缺少 guest IPv6 配置判定 Incus 安装器有错，也不能猜测网关后强行写入。
- 更新验收安排：公网 IPv6 前置条件满足后，可在此 WebSSH 页面输入测试容器的完整 IPv6 地址和 SSH 端口，实际登录并核对目标及连接地址族；不再把取得 WebSSH 服务器自身的 SSH 凭据作为这项手动验收的前置条件。服务端 IPv6 出站能力仍须由那次真实连接验证。
- SSH 与 HTTP 分别验收。此网页完成 SSH 登录不代表容器测试服务的公网 HTTP 可达；HTTP 必须从另一独立、具备 IPv6 的访问端验证服务响应，不能用登录目标后在目标自身执行 curl 的结果替代。现有严格脚本的 `EXTERNAL_PROBE_HOST`/凭据是自动化 SSH 探针模式要求，不是所有验收方式的通用要求。
- 本轮本地重建端口回归 `go test ./service/task -run '^TestReset' -count=1`、全量 `go test ./...`、`go vet ./...` 与 `git diff --check` 通过；这些结果不计入公网 IPv6 或干净系统安装矩阵。

### Incus 真实映射、删除和端口复用回归（2026-09-16）

- 最新 `TestLiveIncusPortMappingAndNesting` 在授权节点运行 235.45 秒通过，未跳过断言。两代临时容器地址分别为 `10.166.211.36` 和 `10.166.211.190`，连续复用同一组四个宿主端口。
- 每代均通过公网 IPv4 实际访问单端口 HTTP、SSH，以及转发到 guest `18080–18081` 的偏移 TCP 区间。HTTP 和 SSH 返回当前容器的唯一身份，防止旧 DNAT 目标仍返回 200 时误判成功。TCP/UDP 区间均创建，但本次只实测了 TCP 传输，未将 UDP 创建等同于 UDP 数据验收。
- 删除单条映射后，另一条范围映射仍可访问；通过真实 Provider 删除容器后，监听器、nft 规则和 iptables NAT 中均无这四个端口残留。第二代完整复用了已释放的端口。
- 第一代 `security.nesting=true`，容器内实际安装 Docker 并执行 `docker run --rm hello-world` 成功；第二代 `security.nesting=false` 设置及读回成功。
- 原始失败根因已分别确认：NAT proxy 目标必须为实例 NIC 的静态绑定地址；偏移区间需要使用 guest 端口而非 host 端口。Incus/LXD 已统一修正网卡匹配与静态绑定，保留已有 NIC 其他属性和合法多地址配置。
- 测试工具也存在独立问题：普通 exec 中启动的后台 HTTP 进程随 exec scope 被清理，现由临时 systemd unit 管理并检查就绪；测试镜像默认 `PubkeyAuthentication no`，临时容器现明确允许测试公钥，真实 SSH 登录断言保留。仅公钥写入临时容器，私钥只在测试进程内存中，未复制节点密码。
- 此项使用 CLI 创建带归属标签的临时 fixture，由真实 Provider 执行安全配置、映射和删除，**不替代面板创建、Agent 接入或干净系统安装矩阵**。每次清理校验归属，测试结束无遗留容器。
- 本次生产改动后的全量 `go test ./...`、`go vet ./...` 和 Incus/LXD/utils 的 `go test -race` 通过。macOS race 链接器有 `LC_DYSYMTAB` 警告，测试退出码为 0。

### 当前源码 Docker ARM64 生命周期（2026-09-16）

- 当前源码的 all-in-one 和 no-db 镜像均实际构建成功。两种镜像首次启动、重启、替换同源码镜像及持久化状态检查通过。
- no-db 覆盖空 `DB_*` 环境变量沿用持久配置、带字面引号的连接变量；all-in-one 覆盖数据库和 storage 标记保留，以及带 Supervisor 敏感标点的数据库密码。
- 修正 no-db 构建时将 `CompatibleAgentVersion` 改写成主控 release/CI 标签的问题；Agent 协议兼容版本与主控发布版本应独立。生命周期测试新增实际 HTTP 版本接口断言，检查两种最终镜像的兼容版本均等于源码定义。
- 随后从公开发布镜像升级到当前源码镜像的增强测试也通过，两个最终版本接口均验证了源码 Agent 兼容版本。使用的 ARM64 发布镜像 digest：all-in-one `sha256:a1afebb8b0382cb29fa2e69a11a3e3fd02e219384ec1816d93ebe647d3fc3e81`；no-db `sha256:7acd27e9b11841129bddc6678408b0e290a93c8de5d28c2907a7ee8921a1578a`。

### 公网 IPv6 前置复核（2026-09-16）

- 现有 Incus 测试容器均已删除。节点 `eth0` 仍只有链路本地 IPv6，`incusbr0` 只有 ULA，`ip -6 route show default` 无输出。
- `rdisc6 -1 -r 3 -w 3000 eth0` 连续三次未收到路由通告。邻居表有其他链路本地地址，但未被标为路由器；不能据此猜测网关。
- `/etc/netplan/50-cloud-init.yaml` 只包含 IPv4 地址、IPv4 网关和 DNS。尚无供应商明确的 IPv6 下一跳，公网 IPv6 SSH/HTTP 保持未验收；没有将 ULA、IPv4 WebSSH 或本机回环访问计为 IPv6 通过。
- 严格 IPv6 脚本现在将清理失败计为失败，并在删除前校验归属；新增 6 个实际执行清理函数的回归，覆盖未创建、同属主、属主变化、同名前缀但不同实例、运行时查询失败和删除失败。该回归还复现并修正了单引号转义错误。语法检查和 ShellCheck 通过，已接入集成测试工作流。

### 面板 NAT SSH 地址错误复现与修复（2026-09-16）

- 独立的当前源码 all-in-one 面板完成初始化、管理员登录、创建有期限 API token、通过该 token 添加 SSH Incus 节点以及自动健康检查。指定缓存镜像后，由管理端 API 创建真实容器成功。
- 新发现：创建期间记录的 SSH 端口为 `29900`，对应 active 映射 `29900 → 22`。创建收尾后 `instances.ssh_port` 被错误写回 `22`，因此按照详情 API 登录时，连接到了宿主机 SSH，报认证失败。数据库密码与实例 `user.password` 相同，SSH 主机密钥与容器不符，排除了“容器密码没有设置”的误判。
- 根因位于普通创建收尾按 Provider 类型直接默认 22；兑换码创建也存在重复的写死逻辑。现统一读取当前实例有效的 TCP SSH 映射；没有有效映射时保留原有端口，只有尚无端口的直连网络才默认 22。数据库查询失败不覆盖已分配端口。
- 兑换码创建复用普通创建的网络信息采集，SSH/API 远程操作全部在结果写入事务外完成，保留原有取消检测与失败清理流程。
- 新回归涵盖 8 种 Provider × 容器/VM 的映射端口、9 种直连/预留/网络覆盖情形及 3 种查询/数据错误，共 28 个子用例；这些不执行 VM 创建。相关普通测试、race、全量 Go 测试和 vet 通过。
- 使用实际映射端口调用用户 WebSSH 网页相同的表单和终端接口，成功登录第一代诊断容器；`SSH_CONNECTION` 为 `216.126.233.222 60764 10.166.211.186 22`，并核对 hostname。此处使用正确端口进行根因诊断，不能代替修复后直接使用面板返回端口的复验。
- 诊断容器经归属标签校验后删除，其独立临时面板和数据库也已删除；其他本地面板/数据库保留。
- 修复后复验于 2026-09-17 完成：两代容器均由当前源码独立面板通过 token API 创建，连续复用 `29900–29903`。每代均直接使用详情 API 返回的密码和 SSH 端口，从本地公网 IPv4 及用户指定的独立 WebSSH 服务实际登录并核对 hostname。独立连接分别为 `216.126.233.222 51404 10.166.211.148 22` 和 `216.126.233.222 47328 10.166.211.49 22`。
- 两代均读回 `security.nesting=true`，通过面板 API 删除后检查 runtime、监听端口、nft 和 iptables 无对应残留；最后删除此次 Provider、独立面板及临时数据库。驱动退出码为 0。这是 SSH Provider 的面板 API 生命周期验收，尚不替代 Agent、浏览器 UI、LXD 或干净系统安装矩阵。
- 验收流程已整理为 `scripts/tests/live_incus_panel_test.py` 和 `webssh_external_probe.py`，显式要求专用节点及本地镜像，缺少前置条件报错退出。上述真实运行使用整理前的临时驱动；整理后新增的参数保护与删除记录等待另行标注，未把未执行路径计为真实通过。

### 真实 Rust Agent 接入与环境加载修复（2026-09-17）

- 使用当前源码构建的 Rust Agent 0.4.0，通过只监听节点 loopback 的临时反向 SSH 转发连接本地隔离面板。传输的是真实 WebSocket、命令、SSH tunnel 和监控协议，没有用 CI stub 代替 Agent。
- 前置检查发现节点留有标准空 `inet vm_traffic_monitor` 表，无规则和计数器；测试保留该表。初始测试文件放在 `/run` 时，实际挂载的 `noexec` 阻止执行，已改用专用 `/opt/ocv-live-agent.*` 目录。失败测试的 unit、文件和隔离面板已按归属清理。
- 真实命令复现：Agent 连上后，连 `true` 也返回 exit 2。逐文件执行确认 `/etc/bash.bashrc` 在 `sh` 中 source 后退出 2，而系统 environment/profile/profile.d 正常。根因是 `BuildEnvCommandNoUser` 无条件混用 bash/zsh 配置，并且名为 NoUser 却仍加载用户 profile。
- 现按实际 shell 选择系统和用户 rc；Agent 仅加载系统配置，保留 profile 扩展和标准 PATH。SSH 完整环境入口仍保留对应 shell 的用户配置。新增回归真实运行本机 sh、dash、bash、zsh，使用隔离配置文件验证 shell 选择、系统变量、用户配置隔离和 PATH。
- Agent 第一次 info 帧还会被刚写入的 online 心跳节流吞掉，导致版本和主机名为空。现仅对相同元数据节流，变化立即写入；SQLite 回归确认重复心跳/info 不增加更新次数，离线转换不丢失已知版本。
- 命令接口不再按错误字符串直接更新节点离线状态。真实连接断开仍由 AgentHub 在生命周期锁内处理，避免旧请求晚到的连接错误清除新连接时间。
- 最新 all-in-one/no-db ARM64 镜像构建、Go 全量测试/vet、Agent/utils/admin 的 race 通过。修复后的真实 Agent 已报告版本并通过“命令 A 超时，命令 B 成功且共享连接未重连”。
- 真实 Agent 完整驱动最终退出 0：两代容器均由面板 token API 创建，复用 `29900–29903`；两代直接使用详情 API 的端口和密码通过公网 SSH。每代均建立同一 guest 的两个面板 WebSSH 会话，关闭 A 后 B 仍能执行命令，且与普通 Agent 命令和超时请求并发时保持可用。这不等同于已测两个不同 guest 的会话。
- 第一代外部传输后的计数增量为入站 `1,107,284`、出站 `1,113,696` 字节；第二代为入站 `1,116,764`、出站 `1,136,428` 字节。两代均实际重启 Rust Agent，验证保存的计数不倒退、新连接恢复命令执行，并再次测试命令超时隔离。
- 用户指定 WebSSH 服务分别从 `216.126.233.222 48758 10.166.211.140 22`、`216.126.233.222 46952 10.166.211.149 22` 登录两代容器，核对 hostname。测试通过的是独立 IPv4 SSH；公网 IPv6 前置仍未满足。
- 两代面板删除后均检查 runtime、监听器、nft、iptables 和 Agent monitor 无对应残留。最后删除本轮 Provider、Agent unit/专用目录、隔离面板和临时数据库，保留原有空监控表。没有删除用户其他本地服务或远端软件。

### 容器脚本入口验收（2026-09-17）

- 新增 `scripts/tests/live_lxc_script_test.py`，只允许明确授权且 runtime 无已有 guest 的节点。上传并核对本地当前辅助脚本 SHA-256，备份其替换的精确文件，成功后恢复。
- 验收分别直接运行 `buildct.sh` 的 `noninteractive=true`、关闭 stdin 入口，以及清除自动化模式变量后的 `add_more.sh` PTY 逐项交互。保留真实安装依赖、下载镜像、创建、配置、SSH、DNS/出网和清理路径，不设置 `ONECLICKVIRT_TESTING`，不替换运行时命令。
- Incus 两种真实入口均通过，驱动退出 0：关闭 stdin 的 `buildct.sh` 创建 `ocvscriptdrcpbnet1`，真实 PTY 逐项回答 8 个提示的 `add_more.sh` 创建 `ocvscriptdrcpbnet2`。两代重复使用 SSH `29800` 和范围 `29801–29825`；公网 SSH、DNS、外部 HTTP 出网、CLI 删除以及监听/nft/iptables 端口释放均检查通过。
- 用户指定 WebSSH 服务的连接来源分别为 `216.126.233.222 39016 10.166.211.98 22`、`216.126.233.222 47564 10.166.211.41 22`。替换的精确辅助文件已恢复，临时目录已删除。
- Incus/LXD 仓库没有独立的单容器删除脚本，此驱动验证原生 CLI 删除与端口释放，不能将其写成卸载器或面板删除测试。LXD 路径尚未实际运行。

### Incus 卸载缺陷与安装复验（2026-09-17）

- 在实际 Debian 13 节点用 `apt-get -s` 复现：原卸载命令包含当前仓库不存在的 `incus-ui-canonical`，整个包删除事务返回 100。旧脚本吞错后仍会删除数据并打印完成。现一次性读取 dpkg 状态，只删除实际安装、部分安装或残留配置的精确 Incus 包（含 incus-agent）；包管理器失败时中止后续数据目录清理。
- 新增 `live_incus_uninstall_test.py`，通过真实 PTY 回答卸载确认。首次实际卸载已成功去除软件包，但严格后置检查返回失败：默认 profile 仍引用 root/eth0，之前的存储池/网桥删除失败被忽略，留下 `incusbr0` 与 `inet incus`；apt 自动移除 lxcfs 后也留下了其服务和挂载。此轮不能计作卸载验收通过。
- 追加修复默认 profile 解绑、socket 停止、精确 Incus 管理表清理与新持久化 include 文件删除；没有 iptables 持久化文件时不再试写不存在的目录。只在 lxcfs 可执行文件已卸载时停止其残留服务，保留其他运行时仍安装使用的 lxcfs。
- 新增并接入 Incus CI 的 `tests/uninstall_packages_test.sh` 覆盖包选择、部分安装、重复卸载、读取/删除失败、持久化规则保留和 lxcfs 共享/残留/失败；本地运行及 ShellCheck、语法检查通过。真实环境残留已根据本轮管理归属精确清理，其他防火墙表保留。
- 当前 `incus_install.sh` 在此已卸载节点重新无交互安装通过，stdin 关闭。Incus 6.0.4，实际建立 `default` btrfs 池（12 GiB）、default profile 和 incusbr0；辅助脚本下载仅将本仓库 raw main URL 指向 SSH loopback 上的本地源码镜像，记录原始和传输 SHA-256，系统包/运行时命令不做模拟，完成后恢复安装文件中的原始 URL。安装器原始 SHA-256 为 `7e8f2ad163c683c76c3f9ca556d292f8155b3317ad91b4df2b9329d75ad47832`，驱动退出 0。
- 随后实际重启宿主机，boot ID 从 `e5cd11f7-cd86-45a6-9569-280642d353bd` 变为 `17429783-71fe-4acc-ba45-8cc7bca9d355`。运行时恢复后再次执行上述两种脚本入口，两代容器均通过公网 SSH、DNS/出网、删除和端口复用；独立 WebSSH 连接分别为 `216.126.233.222 51670 10.58.254.250 22`、`216.126.233.222 57792 10.58.254.213 22`，驱动退出 0。
- 最新卸载器（SHA-256 `86d0ee2db0805e394e6e1509a85124a9cce65a1e799198c7d90dcb421b39ca4b`）用 `noninteractive=true`、关闭 stdin 实际通过，驱动退出 0。日志确认先解绑 default root/eth0，再成功删除存储池与网桥。后置检查确认 Incus 包、数据目录、网桥、管理表、持久化文件及已卸载 lxcfs 的挂载均无残留。apt 卸载 lxcfs 时仍会先提示挂载忙，随后脚本停止孤立服务并验证卸载，不能只以该中间提示判失败。
- 本次完成的是现状节点的安装、重启、脚本创建和卸载循环，不替代干净 OS 矩阵。该轮先通过无交互卸载；修复后的完整交互卸载在下方后续记录中完成。

### 交互安装重启与 NAT 隔离修复（2026-09-17）

- 再次通过用户给定的 WebSSH 网页表单和 WebSocket 终端实际登录授权测试机；连接来源为 `216.126.233.222 42404 179.61.251.239 22`。它不要求 WebSSH 宿主机的 SSH 凭据。这是独立 IPv4 连接，不是 IPv6 验收。
- `live_lxc_install_test.py` 已补齐 Incus 交互安装的自动重启处理：必须完成真实 PTY 提示、看到成功路径的重启通知、重连并确认 boot ID 变化。第一次实际执行退出 0，boot ID 从 `17429783-71fe-4acc-ba45-8cc7bca9d355` 变为 `bcf7c7c2-b79c-4f74-9a33-787b5eff75b0`。使用的安装器 SHA-256 为 `7e8f2ad163c683c76c3f9ca556d292f8155b3317ad91b4df2b9329d75ad47832`；临时镜像 URL 和 staging 已恢复/清理。这仍是现状 Debian 13 系统复验，不是云平台干净重装。
- 新确认的生产问题：Incus 的 `inet incus_masq` 和 LXD 的 `inet lxd_nat` 旧补充规则未限定地址族/入接口；真实 Linux 数据包测试复现其改写 IPv6 源地址和另一条网络的 IPv4。现在以 nft 单次事务迁移自身链，补充 NAT 仅匹配各自网桥的 IPv4；保留原生网桥对 IPv6 NAT/路由的选择及 `ipv4.nat=false`。iptables 回退按网桥子网加归属标记，校验地址/掩码后再替换；非法子网不会删除原有规则。
- 两个仓库新增的 `tests/masquerade_kernel_test.sh` 已在独立 Linux 网络/挂载命名空间真实通过，分别覆盖 nftables、iptables-nft、iptables-legacy。包括旧规则故障复现、IPv4 NAT、IPv6 源地址保留、其他网桥流量、重复配置、显式关闭 NAT、非法 CIDR 保留工作规则，以及卸载只删自身标签。运行时配置查询使用固定测试数据，数据包和防火墙不是模拟；此证据不计作 LXD 真机安装或公网 IPv6 通过。
- LXD 自身 nft 持久化文件改为完整快照后原子替换，只在加载时清空自身表；真实内核测试连续加载两次未重复添加规则。两个卸载器保留无法判明归属的旧全局 iptables MASQUERADE/DROP，避免删除其他运行时的共用策略；清理自身新 NAT 标签已真实验证。
- 修复后的 Incus 安装器以无交互模式在现有空运行时再次执行成功（SHA-256 `b543c38291e174b2886b821166a9a8e6bc83aa6181369b2b29ab37d180d6df2d`，退出 0），保留 `default` btrfs 12 GiB 存储。随后再次实际重启，boot ID 从 `bcf7c7c2-b79c-4f74-9a33-787b5eff75b0` 变为 `4c485f6b-a67d-4b7f-946c-64d4a819d27a`；重启前后均通过真实 nft JSON 校验，安装器链只有一条规则且限定 `nfproto=ipv4`、`iifname=incusbr0`。最后的 CIDR 严格校验追加在 iptables 回退路径，已做内核测试；本节点实际使用 nftables。
- 上述重启后再跑脚本两种入口，`ocvscriptmghrbqgg1` 和 `ocvscriptmghrbqgg2` 均通过创建、公网 SSH、DNS、出网、CLI 删除与端口释放/复用。第二代通过真实 PTY 回答全部 8 个提示。独立 WebSSH 连接分别为 `216.126.233.222 59608 10.204.242.92 22`、`216.126.233.222 38948 10.204.242.93 22`。驱动退出 0，原辅助文件已恢复，fixture `/opt/ocv-live-scripts.rI7vz8` 已清理。
- 随后最新卸载器 SHA-256 `a8f1754a887903415476e3d156528ba14efe0ec121b3b93e40f0cd39562a62a6` 的完整交互路径实际通过，驱动退出 0。确认提示由 PTY 回答，存储池与网桥删除成功；后置检查确认包、运行时数据、持久化文件、网桥及 lxcfs 挂载均无残留。临时卸载 fixture 已清理，节点再次无 Incus 安装。没有执行 LXD 安装或云平台 OS 重置。
- LXD 卸载新增本机/default 项目范围检查、profile 全设备解绑、managed 网络识别和删除失败传播，避免把查询失败当作“无资源”后移除 snap。非默认项目存在时先停止，需先迁移或清理；这不是已经完成多项目卸载支持。保留性和故障注入回归通过，尚未真机验收。
- LXD 现有 iptables 持久化文件仅移除精确匹配的自身 NAT 标签，不用当前运行态覆盖管理员的完整持久策略；文件权限、其他规则、重复执行和缺少配置文件的行为已做本地回归。内核测试临时容器 `ocv-nat-kernel-20260917-01` 已删除，用户既有本地容器保留。
- 两仓库原有 Shell 工作流全部测试入口均实际执行通过；语法、全仓 ShellCheck 错误级检查和 diff 检查通过。新内核测试加入各自 CI 独立 job，没有替换或跳过原测试。中英文文档检查、构建通过（10.75 秒）。本轮没有改动 Go/Rust/前端生产源码，未将历史全量结果写作本轮新执行。

### 创建归属与脚本并发回归（2026-09-17）

- Incus/LXD 的 `buildct.sh`、`buildvm.sh`、`init.sh`、`least.sh` 和 `add_more.sh` 已接入 `instance_ownership.sh`。每次 init/copy 原子写入独立 creation token，成功与部分创建失败均读取运行时 UUID；回滚再次比较 token + UUID，不再以同名对象存在为归属依据。批量子 builder 使用父调用生成的 token，复制实例覆盖继承的旧 token。查询失败或身份变化时保留对象并报错，原有部分创建后的回滚仍然执行。
- 两仓库使用同一把持久 inode 的 flock，覆盖创建过程及失败清理，协调 `/root/log`、下载文件和辅助脚本。子 builder 校验继承的 FD 并复用同一锁，避免批量调用自锁；无效 FD 提示不会绕过加锁。锁目录须归当前用户且权限为 700，拒绝符号链接。安装器下载/复制列表、卸载器辅助文件列表和真机脚本上传列表已同步更新。
- 两仓库原有 Shell CI 测试入口均执行通过，未删减既有回滚断言。每仓库新增 30 个真实调用清理函数的故障注入用例，覆盖 CT/VM/两种批量/追加批量的部分失败、同名冲突、UUID 替换、创建查询失败、清理查询失败及删除失败；另有归属不明时禁止报告成功和非法名称检查。daemon I/O 在这些用例中为模拟，不计作容器真机通过。
- 两仓库 `script_lock_test.sh` 在独立 Linux 容器中实际执行通过，验证父子进程锁继承、不同运行时等待同一锁、陈旧 FD 提示、锁 inode 保留及不安全目录拒绝。仅该进程锁测试使用 Docker；没有重装专用测试机，也没有新建 LXD 真机容器。
- 严格边界：运行时 instance DELETE 没有按 UUID 的条件删除接口。当前锁协调这些创建脚本，不能保证外部 CLI 或面板在最终身份读取与删除之间强行替换同名对象时的原子性。两仓库 README 及文档站 Incus/LXD 中英文说明均明确此限制；未将这一跨调用者边界标为彻底解决。
- 真机驱动另修正 LXD 无 `image_lookup.sh` 却被要求上传的问题；该 helper 现在仅对 Incus 上传。修改后的驱动已做 Python 编译检查，尚待 LXD 实际执行。ShellCheck 错误级、Shell 语法和 diff 检查通过；文档站检查与最后一次构建通过（11.22 秒）。本轮没有更改 Go/Rust/API 定义，Swagger 沿用前轮已生成文件。

### iptables 到 nftables 的真实迁移回归（2026-09-17）

- 新增真实包回归先复现错误：已存在带 `oneclickvirt-incus-ipv4` 标记的 iptables 规则时，将网桥 `ipv4.nat` 设为 false 后执行 nft 配置，IPv4 源地址仍由 `10.78.1.2` 改写为 `192.0.2.1`。旧规则仍挂在内核 hook 上，清空新的 nft 自有链并不能使其失效。
- 两安装器现于 nft 事务成功后检查 iptables-nft、iptables-legacy 及系统默认 iptables，只移除各运行时精确标记的 NAT。同步过滤常见持久化文件，保留无关规则、属主/权限和符号链接。只装有 legacy 命令但内核没有 legacy NAT 表时不会判为故障；其他查询和删除失败仍传播。未删除不能确认归属的旧全局 MASQUERADE。
- 两卸载器复用上述后端清理。Incus 卸载也不再用实时规则覆盖管理员完整的持久化策略，保留不在当前运行态中的合法规则；精确清理原有 incusbr0 FORWARD 条目。独立的 `masquerade_failure_test.sh` 执行真实生产清理函数，覆盖读取失败、删除失败、过滤失败、原子替换失败及幂等保留，加入原有 CI，未删减既有测试。
- 最终版本 `masquerade_kernel_test.sh` 分别在隔离 Linux 网络/挂载 namespace 通过：两种 xtables 后端迁移到 nft 后关闭 IPv4 NAT 确实生效，IPv6 源地址和无关网桥流量保留；持久化 symlink、权限和无关策略保留。LXD 原持久化重复加载验证继续通过。运行时元数据查询用固定测试数据，实际包、nft 和 iptables 均使用真实内核。
- 最终两仓库 Shell 回归、ShellCheck 错误级及 diff 检查通过，LXD 原独立 IPv6 本地地址测试也通过。文档站中英文迁移说明已更新，检查与构建通过（10.11 秒）。`ocv-nat-migration-20260917-01/02` 临时容器均已不存在，用户既有容器保留。本轮没有提交云平台 OS 重装，也没有更改测试机网络配置或执行新的 LXD 真机安装。

### WebSSH 复核与交互验收读取修复（2026-09-17 上午）

- 重新读取 `http://216.126.233.222:8888/` 网页表单，并实际通过表单及 WebSocket 终端连接节点成功。更新后的探针返回 `216.126.233.222 55742 179.61.251.239 22`，hostname 为 `pastebin`。该服务可作为独立 WebSSH 入口，不需要其宿主机 SSH 凭据；本次仍仅是 IPv4 实测。
- 本次 SSH 只读复核仍为 Debian 13，boot ID `4c485f6b-a67d-4b7f-946c-64d4a819d27a`。五种运行时命令均不存在，无全局 IPv6 地址或 IPv6 默认路由；未执行系统重装或 LXD 安装。
- 安装、创建和卸载的旧交互循环在 stdout 持续可读时绕过总超时，同时可能饿死 stderr 或在退出码到达时漏读尾部结果。三处已统一使用双流读取器的增量回调，等待 EOF、保留中文分段解码、限制提示缓存，并在退出或超时时仅关闭当前命令 channel。保留完整提示计数、Incus 重启通知及 boot ID 检查，没有削减或跳过验收。
- 实际本地 SSH transport 的 10 个回归通过（原有 7 个全部保留），新增覆盖真实 PTY、超出 SSH 窗口的 stderr、中文提示/回复、退出码后的尾部输出、持续输出总超时以及共享 transport 保留。又在专用节点实际通过 PTY 提示/回复和“命令超时后同一 SSH transport 执行下一条命令”。这不是 Agent 并发测试，也不能算作重新执行了安装/卸载矩阵。
- 脚本创建驱动先校验并快照本地全部 helper，再创建远端 fixture；上传和 SHA-256 使用同一份内容。guest 检查加上逐命令失败传播，避免最后的 curl 成功掩盖 DNS 失败；空输出返回明确失败。
- WebSSH 校验使用每次连接的随机标记及总接收时限。IPv6 目标须匹配实际 SSH 的公网 IPv6 地址、端口和独立来源，拒绝 IPv4、ULA、IPv4 映射地址和同地址源/目标。6 个校验回归（含多组边界）通过并加入现有 CI；它们使用连接记录验证判定，不属于公网 IPv6 实测。Python 语法检查与 diff 检查通过。公网 IPv6 SSH/HTTP 和干净 OS 矩阵仍未完成。

### iptables 持久化失败保护（2026-09-17）

- 故障注入实际复现 LXD 的保存问题：`iptables-save` 写出部分内容后退出非零，旧 `/etc/iptables/rules.v4` 已被重定向截断。Incus 原相同路径还使用 `|| true`，并调用全局 `netfilter-persistent save`，会吞掉失败和连带覆盖 IPv6 文件。
- 两安装器改为同目录临时文件保存，命令完整成功后才原子替换正式 IPv4 快照；保留现有属主/权限和符号链接，新文件权限 600。拒绝悬空 symlink 和目录，复制元数据、保存、替换失败均保留旧策略并清理临时文件。此路径只修改 IPv4 NAT，不再通过全局 save 覆盖 IPv6 持久化。
- Incus 补装 nftables 后不再主动 `systemctl start nftables`。运行中的规则由后续自身 nft 事务更新，避免显式启动全局服务时加载既有 `flush ruleset` 配置、清除其他运行时的表。该检查覆盖脚本显式调用，不代表已逐个验证各发行版软件包的 post-install 行为。
- 两仓库新增 `tests/firewall_persistence_test.sh` 并接入既有 CI；真实文件操作结合命令故障注入，覆盖部分输出、元数据复制失败、替换失败、权限、symlink、悬空链接、错误文件类型、IPv6 保留以及 nftables 服务调用顺序。本地和隔离 Linux 容器均通过；原有 Shell 回归、ShellCheck 错误级和 diff 检查通过。
- 两仓库现有真实内核 NAT 测试再次通过：nftables、iptables-nft、iptables-legacy、xtables 到 nft 的迁移、IPv6 地址保持、无关网络及 NAT 禁用均保持原断言。此处仍不覆盖 firewalld、nft 到 xtables 的反向迁移或真实宿主机重启。
- 两仓库现有 `script_lock_test.sh` 在 Linux 容器重新通过，临时容器 `ocv-persist-regression-20260917-01/02/03` 均已自动删除，用户既有容器保留。
- 文档站四份 Incus/LXD 中英文安装文档已更新，保留 CRLF；`npm run check` 和 `npm run build` 通过（12.22 秒）。没有修改 Go/Rust/API 定义；Swagger 不需重新生成。没有对专用测试机执行新一轮重装、安装或卸载，完整 OS/运行时矩阵仍待完成。

### firewalld 回退与发行版持久化修复（2026-09-17）

- Incus/LXD 的 firewalld 回退改为带运行时归属标记、IPv4 源网段和出口接口限制的 direct 规则，同时更新 permanent/runtime；显式关闭 NAT 会清理自有规则。已有接口区域保留，仅未分配区域的网桥加入 trusted。旧 public 全局 masquerade 缺少归属证据，保留管理员策略，不做推测性删除。
- 使用真实 firewalld 1.3.3 发现并修复两项 API 差异：direct 规则输出会给 `!` 加引号；未分配区域返回 exit 2，`no zone` 在 stderr。查询和更新失败传播；只有 NOT_RUNNING（252）允许 offline API 修改，D-Bus 错误（36）不能当作守护进程停止。
- nft 配置成功后清理自有 firewalld 永久/运行规则，避免 reload 恢复旧 NAT。卸载仅在网桥不存在时移除 trusted 引用，保留存活网桥、自定义区域及无关规则；在线和离线清理均有实际验证。
- iptables 快照按发行版写入标准恢复路径：Debian/Ubuntu `/etc/iptables/rules.v4`、CentOS/Fedora `/etc/sysconfig/iptables`、Arch `/etc/iptables/iptables.rules`、Alpine `/etc/iptables/rules-save`。安装对应持久化包并启用启动服务，失败不吞掉，不主动 start/reload 另一套运行策略。自定义 service override 或 Alpine 自定义 `IPTABLES_SAVE` 尚未覆盖。
- 两安装器和卸载器共用 `/run/oneclickvirt-firewall-locks/firewall.lock`，保留 inode，拒绝不安全目录和 symlink，最长等待 120 秒。这只协调这些脚本的防火墙阶段，不是整个安装事务锁，也不协调管理员或其他 daemon。
- 两仓库完整真实内核测试启用 `OCV_TEST_FIREWALLD=true` 后退出 0，保留全部既有 nft/iptables-nft/iptables-legacy 断言，新增实际 firewalld NAT、IPv6 源地址保持、其他网桥保留、重复调用、NAT 禁用、reload、迁移到 nft、在线/离线 NAT 和 trusted 清理。运行于隔离 Linux mount/network namespace，不是真实公网 IPv6或宿主 OS 重启验收。
- 新增 firewalld 故障注入、防火墙阶段真实进程互斥和发行版持久化回归，均通过并接入原 CI；两仓库原 Shell 回归、创建脚本进程锁、ShellCheck 错误级及 diff 检查通过。没有减少原用例或把模拟测试算成远端环境通过。
- 四份中英文安装文档及主仓库 `LIVE_ACCEPTANCE.md` 已更新；文档站 `npm run check`、`npm run build` 通过（10.49 秒）。此次未修改 Go/Rust/API 定义，Swagger 沿用此前已生成文件。
- 临时容器 `ocv-firewalld-20260917-01` 已精确删除并确认不存在；用户其他容器保留。根目录 `copy_project.sh` 已执行成功，主报告及 `LIVE_ACCEPTANCE.md` 与目标副本逐字节一致。Firefox 再次核对授权 VM 页面，仍显示 Debian-13-Trixie；尚未提交 OS 重装。

### nftables 启动持久化路径与失败传播（2026-09-17）

- 继续复查发现 nft 分支也存在发行版路径错误：统一写入 `/etc/nftables.conf`，但 Fedora 官方 nftables.service 的 ExecStart 读取 `/etc/sysconfig/nftables.conf`；Alpine 官方 OpenRC 脚本默认读取 `/etc/nftables.nft`。即使服务 enable 成功，也不能证明旧写入文件会被加载。
- Incus/LXD 安装器新增标准路径选择，Debian/Ubuntu/Arch 保持原路径，CentOS/Fedora 和 Alpine 改用上述启动文件。保留已有主配置和其他 include，快照及 include 写完后才启用启动恢复；Alpine 明确安装 nftables-openrc 并通过现有服务抽象启用。不主动 start/reload 防火墙，服务或包失败均返回错误。
- Incus 的补装 nftables 步骤不再提前 enable；此前保存配置时忽略 enable 错误的两处也已修复。卸载器逐一清理三个标准主配置中的精确归属项，live 卸载驱动检查同步扩展，避免更换后端/发行版遗留 include。
- 两仓库回归实际执行保存调用链，覆盖六种发行版标签的路径、重复 include、启用顺序、服务失败后文件保留、Alpine 服务包失败；本地和隔离 Linux 容器均通过。所有普通 Shell 回归重跑退出 0，创建锁/防火墙锁在 Linux 容器通过，修改文件 ShellCheck 错误级、Shell 语法、Python 编译和 diff 检查通过。没有减少既有 CI 用例；这一轮未重复真实内核 NAT 包测试，沿用上一节结果。
- 中英文四份安装文档已说明标准路径和自定义 service override/rules_file 边界；文档站检查和构建通过（10.96 秒）。临时容器 `ocv-nft-persist-20260917-01` 使用 --rm，测试退出 0。
- 此轮未提交云平台重装，未实际安装 LXD，也未把文件/服务模拟回归当作宿主 OS 重启验收。公网 IPv6 和干净 OS/运行时矩阵继续保留未完成状态。

### WebSSH 网页确认与防火墙缓存复验（2026-09-17 11:39 CST）

- 已在 Firefox 打开 `http://216.126.233.222:8888/`，确认是带 Hostname、Port、Username、Password/Private Key 和 Connect 的 WebSSH 表单，而不是需要提供网页宿主机 SSH 凭据的管理面板。此前要求该宿主机 SSH 凭据的判断不适用于此入口。
- 再通过网页表单及其 WebSocket 协议实际连接授权节点，随机终端标记、hostname 和连接来源验证通过：`216.126.233.222 47884 179.61.251.239 22`，hostname 为 `pastebin`。这是网页服务发起的独立 IPv4 SSH 连接，不是公网 IPv6 通过记录。密码仅用于本次连接，未写入脚本或报告。
- 同时通过匹配本地 known_hosts 的直接 SSH 做只读复核：节点仍为 Debian 13，boot ID `4c485f6b-a67d-4b7f-946c-64d4a819d27a`，没有全局 IPv6 地址、IPv6 默认路由或已安装的五种运行时命令。此轮未重装系统或更改节点网络；公网 IPv6 SSH/HTTP 验收仍未完成，不能以网页连接成功代替。
- 防火墙初始化缓存已由当前 Manager 持有，不再按可复用的指针地址存入全局缓存；每次复用会检查三个必要链，缺失后重建，同一 Manager 的并发初始化由锁串行化。失败不缓存为成功。此锁不承诺与外部管理员/daemon 删除规则原子互斥。
- 完整重跑 `scripts/tests/firewall_integration_test.sh` 退出 0。`iptables-nft/ip6tables-nft` 和 `iptables-legacy/ip6tables-legacy` 两组均跑完 firewall、Incus、LXD 的全部 15 个顶层测试（两组共 30 次，另含子用例），无跳过，保留既有断言；包括删除整个表、单独删除链后重新添加映射、重复映射清理、双栈归属隔离、读取失败停止替换及持久化。使用独立容器的真实内核规则，不等同于真实 Incus/LXD daemon 或公网流量测试。
- `go test -race -count=1 ./provider/firewall`、6 个 WebSSH 连接判定回归及 `git diff --check` 通过。该轮临时防火墙容器已自动删除并确认不存在，其他容器保留。未改 API 定义，不需额外生成 Swagger。

### SSH 重装为干净 Debian 12（2026-09-17）

- 根据用户新授权，通过 SSH 使用 `leitbogioro/Tools` 的 InstallNET 重装，不再通过供应商网站提交重置。固定上游提交 `2b95e296be3784ad3a4c5150a8ea9878931e4513`，脚本 SHA-256 `423779ae6bbc3410fe5c3b32ae1aeaf079531e9bd00572b391095d442899c1ce`。
- 重启前验证目标为单块 25 GiB `/dev/sda`、BIOS/KVM、原 Debian 13，静态地址 `179.61.251.239/24` 与网关 `179.61.251.1`；没有 RAID，未使用用户举例中的 OVH RAID 参数。旧盘数据随授权重装清除，没有保留运行环境备份。
- 安装准备命令虽然返回 1，但上游源码成功路径本身如此；实际核对完成标记、生成的 preseed、GRUB 首项和官方 Debian 内核 SHA-256 后才提交重启。将上游生成的 `passwd/root-login boolean ture` 修正为 `true`，重新打包并校验 initrd；凭据及密码哈希没有写入此报告。
- 新系统实际验证为 Debian 12，内核 `6.1.0-50-cloud-amd64`；boot ID 从 `279803a5-38c8-4985-a19f-fde094601da9` 变为 `7f42a0e4-e0cc-4918-88f3-508152bc519f`，machine ID 与 SSH 主机密钥亦更换，安装日志存在。新凭据公网 SSH 登录、静态 IPv4/默认路由、DNS 和 HTTPS 出网通过，验证驱动退出 0。
- 重装后仍没有全局 IPv6 地址或 IPv6 默认路由。重装成功不等于 LXD 安装、容器生命周期或公网 IPv6 验收通过；后续结果分项记录。

### Debian 12 的 LXD 首次安装故障与修复（2026-09-17）

- 上述干净系统实际执行当前本地 LXD 安装器、`noninteractive=true` 且关闭 stdin。snapd 和 LXD 5.21.7 LTS 安装成功，但存储初始化失败，退出 1；首轮源码 SHA-256 `f688f393bcbbb9c0c428d8c7556e45967cb7e600958fe010a2dd2a64f0c52af9`，诊断目录 `/opt/ocv-live-install.AN8bhc` 保留。
- 真实根因是 LXD 5.21 的 `storage/network/project list` 没有 `-c` 参数。现场分别读取命令帮助确认；profile/image list 支持列参数，未盲目全量替换。安装、面板初始化及卸载改为单次 JSON 查询并校验数据结构，捕获命令失败后再解析，防止故障被当作空环境；Incus 相应的安装/面板查询亦统一兼容。真实卸载驱动的项目检查同步修正。Go 存储查询已有无列参数回退，本次未改动其行为。
- 两安装器另有确定性问题：第一次安装 btrfs/LVM/ZFS 工具后，不论 `modprobe` 成败都设置需要重启并跳过该后端。新增回归在修改前分别复现 Incus/LXD 的 fresh btrfs 失败；修正后已加载/内建或成功加载模块时继续初始化，仅不可用时保留重试标记和原有后端回退。模块、包管理和 runtime 初始化失败仍传播。
- 新增 JSON 清单回归共 50 个场景（LXD 30、Incus 20）及存储模块回归各 18 个场景，均通过并加入原 CI。原初始化、面板初始化、数据保留和其他普通 Shell 回归全部保留并通过；本轮未重跑 Linux 锁/内核包测试，没有将命令故障注入算作真机通过。
- 修复后的 LXD 安装器在同一台半安装节点重新无交互执行通过，源码 SHA-256 `3d48252311a5b6bb2ab96aacbb6d0ea24c69b4ef82ee15df35beecb4598106cb`。首次安装 btrfs 工具后实际加载模块并建立 `default` btrfs 12 GiB 池，default profile、lxdbr0 与运行时检查通过，驱动退出 0。辅助脚本已恢复原下载 URL，本轮临时安装目录已删除。这证明故障后的恢复安装，尚不等于修复版本从再次重装的空 OS 一次通过。

### LXD 创建镜像选择变量丢失（2026-09-17）

- 首次真实脚本创建下载并导入 `debian_12_bookworm_amd64_default.zip` 后失败，运行时错误为找不到 `default` 镜像。读取实际镜像清单确认 fingerprint `ad076a1b5ef78bd529afbb8553dc521d4ddd793b98764e3bef94a9a0e387a277` 已存在，节点无残留 guest；不是 IPv6 或镜像下载失败。
- 根因是 LXD `buildct.sh` 的 `use_fixed_image` 将 `image_name` 定义为局部变量，返回后 `create_container` 读取全局空值；LXD 把空镜像名解析为默认镜像。现明确保留选中的镜像名并在新选择开始时清空旧状态，避免空值或前一次选择泄漏。
- 新回归先复现旧代码失败，再验证首次导入、已有缓存、下载失败和导入失败 4 个场景，并接入原 CI；既有创建失败回滚回归继续通过。Incus 容器使用全局选中名、两种 VM 路径通过 `system` 传递短别名，没有此局部变量丢失模式；未实际创建 VM。
- 该失败轮保留并核对精确归属后恢复辅助文件，缓存镜像保留供复验使用；后续成功与否另行记录，未将失败轮计为通过。
- 修复后的真实完整驱动退出 0：`buildct.sh` SHA-256 `4914d83f1041be413ff401583331f4fd529b9ee9d83fce965fb9a701ab54a3be`。无交互、关闭 stdin 创建 `ocvscriptrsdfmrer1`；真实 PTY 回答 8 个提示创建 `ocvscriptrsdfmrer2`。两代均通过公网 SSH、DNS、外部 HTTP 出网、原生 CLI 删除和 `29800–29825` 的释放/复用。用户 WebSSH 服务分别从 `216.126.233.222 40496 10.79.108.56 22`、`216.126.233.222 41116 10.79.108.52 22` 登录并核对 hostname。
- 成功后恢复原有辅助文件，删除本轮 fixture `/opt/ocv-live-scripts.sDwIxT`，未遗留 guest。这是 Debian 12/LXD 5.21 的脚本容器生命周期实测，不是 VM、独立 IPv6、浏览器 UI 或面板生命周期验收。
- 面板复验前重新构建当前源码 all-in-one ARM64 镜像，image ID `sha256:01753791276a5884db698cd482d677f86581326a29b6bc86a9c41f131b42f71a`；以 musl 交叉编译当前 Rust Agent 0.4.0，Linux AMD64 二进制 SHA-256 `77f5eb66a8e29e415d6febf6da579d12a6a76888f957cc618925f731148eb8e7`。两项构建退出 0。中英文安装文档更新后 `npm run check`、`npm run build` 通过，CRLF 保留；本轮未改 API 定义，无新增 Swagger 生成需求。

### LXD 面板验收控制连接空闲断线（2026-09-17）

- 新构建的隔离面板通过初始化、管理员登录、API token 与 LXD SSH Provider 健康检查，并成功创建第一代容器 `ocv-panel-1789620109-e255f3-0`，任务 completed、实例 running、SSH 端口 `29900`。但验收驱动先前建立的宿主 SSH transport 在等待独立 API 任务数分钟后，打开下一条 channel 时收到 EOF；后续断言未执行，该轮不计完整通过，Agent 轮尚未开始。
- 现场确认容器运行、内存充足，宿主 `clientaliveinterval=0`、`unusedconnectiontimeout=none`；日志未显示同期 SSH 服务重启。证据定位到闲置控制连接断开，未证明具体中间网络设备或根因位置，不能把该异常称为 Provider 创建失败。
- 所有真实驱动的长生命周期控制连接及通用 SSH 工具增加 15 秒保活，不自动重连、不重试有副作用的命令，实际断线与命令超时仍失败。新增真实本地 SSH transport 测试验证命令 EOF 后的保活消息，原有 10 项全部保留；合计 11 项通过。非登录 shell 5 项和 WebSSH 连接判定 6 项亦通过。
- 诊断阶段从本轮隔离数据库在内存读取连接信息，核对 Provider、节点、实例名、runtime UUID 与密码一致后，实际公网 SSH 通过；独立 WebSSH 来源为 `216.126.233.222 34386 10.79.108.106 22`。这证明该实例连接可用，不替代完整双代面板删除/端口复用。
- 随后先停止本轮隔离面板，按身份校验删除该 guest，检查 `29900–29903` 无残留，再删除隔离面板容器；当时匿名数据库卷仍残留，后续按下文归属核验单独清理。其他本地服务保留。该手动诊断清理不算作面板 API 删除通过。保活修改后的完整复验另行记录。
- 保活版完整 SSH Provider 复验已通过：隔离面板 `ocv-panel-1789620815-e81fbc` 经 token API 创建两代真实 LXD 容器，连续复用 `29900–29903`。每代直接使用详情 API 的端口和密码通过公网 SSH；独立 WebSSH 来源分别为 `216.126.233.222 58864 10.79.108.230 22` 和 `216.126.233.222 33696 10.79.108.225 22`。两代 `security.nesting=true` 读回正确，未将该读回等同于 LXD 内实际 Docker 运行。
- 两代均由面板 API 删除，检查 runtime、监听器、nft/iptables 四个端口无残留；最后 Provider 和隔离面板容器清理完成。旧清理逻辑未删除匿名数据库卷，后续按下文单独核验清理。该 SSH 模式驱动打印完整 PASS，随后同一顺序驱动进入新的 Agent 模式，后者结果另行记录。这不是浏览器 UI 或 IPv6 验收。

### LXD Agent 完整验收和测试卷清理（2026-09-17）

- Agent 隔离面板 `ocv-panel-1789621439-14bcd3` 完整两代验收退出 0。两代均用详情 API 的密码/端口通过公网 SSH，复用 `29900–29903`，WebSSH 会话 B 在 A 关闭及并发 Agent 命令超时后仍可用。每代两个 WebSSH 会话指向同一 guest，不能扩写为两个不同 guest 并发已测。
- 两代外部传输流量增量分别为入站/出站 `1,094,724/1,105,832` 和 `1,089,464/1,108,816` 字节；每代均实际重启 Rust Agent，计数保留、命令恢复。独立 WebSSH 来源分别为 `216.126.233.222 48278 10.79.108.23 22`、`216.126.233.222 48878 10.79.108.253 22`。
- 两代面板 API 删除后 runtime、监听/nft/iptables 映射和 Agent monitor 无残留；Provider、Agent 专用 unit/目录和隔离面板容器已清理。仅验证 nesting 配置读回，尚未在 LXD guest 内实际运行 Docker。
- 发现测试工具 `docker rm -f` 遗漏镜像声明的匿名数据库/存储卷。修正为核对 run 标签后按不可复用容器 ID 执行 `docker rm -f -v`，防止同名容器替换竞争，同时保留命名卷和失败现场。新增 4 个契约回归及真实 Docker 匿名/命名卷、错误归属保留测试均通过，已接入 CI；没有替换或跳过旧测试。
- 本轮三个面板的 6 个匿名卷已在精确归属核验及重新确认无任何容器引用后删除：旧 storage 卷以日志中的唯一 run ID 识别，旧 DB 卷以 instances/providers 表或 InnoDB redo 中的唯一 run ID 识别，Agent 两卷以删除前实际容器 Mounts 识别。未使用 prune；这些临时测试数据库与日志不可恢复，其他服务/卷保留。历史更早的面板卷未在本轮作全局清理，也不能推定已清理。

### LXD 交互安装复验（2026-09-17）

- 在上述空 LXD 节点实际执行当前安装器的纯交互模式，清除无交互变量，通过真实 PTY 回答自定义路径与池大小两个提示。源码 SHA-256 `3d48252311a5b6bb2ab96aacbb6d0ea24c69b4ef82ee15df35beecb4598106cb`，驱动退出 0。
- 现有 `default` btrfs 池被保留，default profile/lxdbr0 就绪；辅助 `buildct.sh` 已更新为镜像名修复版（SHA-256 `4914d83f1041be413ff401583331f4fd529b9ee9d83fce965fb9a701ab54a3be`），临时下载 URL 已恢复，fixture `/opt/ocv-live-install.6G1gPg` 删除。该结果是交互重入/恢复安装，不是修复版干净 OS 首次安装。
- 随后实际重启，boot ID 从 `7f42a0e4-e0cc-4918-88f3-508152bc519f` 变为 `f10b9209-4e0f-4231-ad22-724961c31147`。LXD/网桥恢复，实际 nft JSON 确认自身 NAT 仍只有一条规则且限定 lxdbr0/IPv4；公网 IPv6 默认路由仍不存在。
- 重启后再次跑完整脚本两代：`ocvscriptquatqmbo1` 无交互、`ocvscriptquatqmbo2` 真实 PTY 回答全部 8 项。公网 SSH、DNS/HTTP 出网、CLI 删除、端口 `29800–29825` 释放/复用均通过。独立 WebSSH 来源分别为 `216.126.233.222 45118 10.79.108.30 22`、`216.126.233.222 43636 10.79.108.166 22`；fixture `/opt/ocv-live-scripts.MyCPeP` 已清理，辅助文件恢复。

### LXD 交互卸载驱动漏答与真实卸载（2026-09-17）

- 首次驱动只回答“确认卸载”，遗漏 LXD 特有的“是否删除存储后端文件”第二个提示，在删除开始前停止了本轮驱动。只读检查确认旧卸载进程退出、空 runtime 与 `default` btrfs 池仍在；这次不计通过，保留诊断 fixture `/opt/ocv-live-uninstall.Yyvbuv`。
- 修正为按顺序处理完整对话并要求所有提示均已回答，纯交互模式不再预设 `REMOVE_STORAGE`，通过实际第二次回答选择删除。5 个解析回归覆盖两个提示、字符级分片、合并/重复输出、Incus 单提示、有限缓存，全部通过并接入 CI；这些解析测试不算真实卸载证据。
- 同一卸载器 SHA-256 `90e8f3155b5b7e9644cc5fadd7aa6a347bc0ed21256115e12e0368b97db70333` 重新纯交互执行成功，驱动退出 0。现场两次回答均完成，default root/eth0 解绑，btrfs 池及 lxdbr0 删除，LXD snap 卸载，runtime 数据/挂载与自身持久化防火墙后置检查全部通过；共享 snapd 和自动恢复快照保留。已删除的测试容器数据/存储池不能以 snap 快照恢复承诺。
- 之后在已卸载节点执行 `noninteractive=true`、关闭 stdin 的安装器，重新安装 LXD 5.21.7、创建 btrfs 12 GiB/default profile/lxdbr0，驱动退出 0。安装源码 SHA-256 仍为 `3d48252311a5b6bb2ab96aacbb6d0ea24c69b4ef82ee15df35beecb4598106cb`，临时下载 URL 恢复，fixture `/opt/ocv-live-install.s3nDl3` 清理。由于 snapd、已装工具和模块仍在，此结果是卸载后重装，不是再次 DD 后的全新 OS 首次安装。
- 紧接着同一卸载器以 `noninteractive=true REMOVE_STORAGE=true` 且关闭 stdin 执行，退出 0，软件包/snap、存储/网桥、挂载和自身防火墙后置检查通过。成功 fixture 已清理，节点当前无 LXD 安装，保留共享 snapd 与自动恢复快照。此轮没有再创建容器，重启后容器创建证据来自上一循环。
- 最终测试工具检查：真实 SSH transport 11 项、非登录 shell 5 项、WebSSH 地址判定 6 项、卸载对话 5 项、面板清理契约 4 项及真实 Docker 卷清理均通过；未删减既有用例。本轮新增修改仅在验收工具/CI/说明中，没有新的 API 定义变更，Swagger 无需重复生成。

### 修复版干净 Debian 12 首次安装及 Rust PTY 修复（2026-09-17）

- 再次通过 SSH 在同一授权 `179.61.251.239` 的单盘 `/dev/sda` 使用固定版本 InstallNET，重启前核对磁盘、静态网络、密码哈希、Debian 官方内核 SHA-256、preseed 和 GRUB，并仅修正生成物中的 root-login 布尔拼写。未使用 RAID 参数或供应商重装网站。原监视进程句柄失效后先核对进程已退出，只新建只读验证探针，未重复提交重装。
- 新系统实际验证 Debian 12、内核 `6.1.0-50-cloud-amd64`、新 boot ID `f624122c-f1e8-4fd1-bd2e-44ce42adbb07`、machine ID `588c31131af94fdeac5d4aafc2913fb0`、SSH host key SHA256 `lQT0bMrwRLhs3EkpNVWOWfrML/+fOufgIuXwTeIGypc`。与重装前身份不同，五种待测 runtime 均不存在，公网密码 SSH/DNS/HTTPS 通过；IPv6 默认路由仍不存在。全局 known_hosts 未改动。
- 安装器源码 SHA-256 `3d48252311a5b6bb2ab96aacbb6d0ea24c69b4ef82ee15df35beecb4598106cb` 在该干净系统首次无交互执行成功。首次安装 snapd/LXD 5.21.7/btrfs-progs 后直接初始化 `default` btrfs 12 GiB、default profile、lxdbr0；驱动 PASS，临时 URL 恢复，fixture `/opt/ocv-live-install.dw7t0Z` 清理。这次未在半安装节点重跑，证明本 Debian 12/无交互组合干净首装通过；其他 OS/模式仍不能据此通过。
- 首装后脚本完整两代创建/删除通过：`ocvscriptmoffdmic1` 无交互、`ocvscriptmoffdmic2` 真实 PTY 回答全部 8 项，公网 SSH、DNS/HTTP 出网、CLI 删除及 `29800–29825` 复用/释放均通过。独立 WebSSH 来源分别为 `216.126.233.222 34062 10.29.203.215 22`、`216.126.233.222 40740 10.29.203.140 22`。驱动退出 0，原 helper 恢复，fixture `/opt/ocv-live-scripts.GlQZNj` 清理；该轮没有再次重启宿主。
- Rust 复核发现真实 PTY 资源隔离缺陷：`ptsname` 共享静态缓冲区，slave/dup fd 没有 CLOEXEC，且原始 fd 在部分失败分支泄漏。新增回归先在旧实现复现 `PTY leaked across exec`。现 Linux 使用 `ptsname_r` 独立缓冲，macOS 序列化并立即复制名字；主/从/复制 fd 使用 RAII 和原子关闭继承，初始化及 controlling-terminal 设置失败向上返回。macOS 初始窗口大小须在打开 slave 后设置，已据实测修正，不忽略失败。
- 新增 3 项真实 PTY 检查（fd/尺寸、无关子进程不能持有其他终端、8 线程共 512 次独立流分配），保留原 49 项。本机 `cargo test --locked --all-targets` 52/52 通过，最终源码交叉编译 musl 后在独立 Linux 容器实际执行亦 52/52 通过、0 跳过。新增 Linux/macOS/musl CI job，不替换旧构建。Linux AMD64 release SHA-256 `a2b5fa493003ef2ab19468f716f7be068ce3e29a3316645f7db180aa01b6485c`；此时尚待部署到专用节点复验。
- 本轮 Go 相关 6 包测试通过（多数命中当前源码缓存）；严格 Clippy 重跑仍失败，汇总 81 个去重 finding，主要是风格/冗余转换等，未将这些当作业务故障、未全局自动简化。没有宣称严格 Clippy 通过。
- 新增可选 `OCV_LIVE_NESTED_DOCKER=yes` 验收：每代面板创建的非特权 Debian/Ubuntu 容器安装当前官方 Docker，实际执行带 `net.ipv4.ip_unprivileged_port_start=0` 的 hello-world，核对 expanded config 没有 raw AppArmor/LXC 绕过。5 个本地失败契约通过，实际 guest 结果另行记录，未以这些契约测试冒充嵌套实测。

### LXD 非特权容器实际 Docker 嵌套（2026-09-17）

- SSH 模式隔离面板 `ocv-panel-1789625332-9fef6a` 的两代 token API 创建均实际通过公网 SSH、独立 WebSSH、删除和 `29900–29903` 复用。两代 expanded config 均核对为非特权、无 raw AppArmor/LXC 绕过；面板 nesting=true 配置读取正确。
- 两代均在 guest 内通过官方签名软件源安装 Docker Engine `29.8.1`、containerd `2.3.5`、runc `1.5.1`，实际 `docker run --pull=always --rm --sysctl net.ipv4.ip_unprivileged_port_start=0 hello-world` 返回 0、输出 `Hello from Docker!` 和随机结束标记。storage driver 为 overlayfs，cgroup v2，未切换 vfs 或 privileged 绕过。
- 独立 WebSSH 来源分别为 `216.126.233.222 58374 10.29.203.218 22`、`216.126.233.222 52970 10.29.203.98 22`。两代删除后监听/nft/iptables 无对应映射残留，隔离 Provider、面板和匿名卷使用修复后的清理函数删除，驱动退出 0。
- 新 PTY 修复二进制的 Agent 模式 `ocv-panel-1789626498-dcfb04` 亦完整退出 0。两代非特权 guest 都通过同版本 Docker/sysctl/hello-world。现场在第二代运行时确认 AppArmor `enabled=Y`，该 guest 的 LXD profile 为 enforce，LXD 5.21.7/LXC 6.0.6；未关闭 AppArmor 来通过测试。
- 两代仍通过同 guest WebSSH A/B 隔离、普通命令超时不重连共享 Agent、Agent 实际重启后计数与执行恢复。外部流量增量分别为 `1,090,676/1,106,696`、`1,090,376/1,103,568` 字节（入站/出站）。独立 WebSSH 来源分别为 `216.126.233.222 34912 10.29.203.41 22`、`216.126.233.222 57634 10.29.203.75 22`。
- 两代 API 删除后映射/monitor 无残留，Agent 专用 unit/目录和 Provider 清理。删除前记录 Mounts，完成后确认该面板及 `210c3c32502250aaf2635f171bb4d34571b7a9c35fe552de5bc56cfc4de27608`、`a161c01548610e65496eef4ee57e44bed4046a5ebdcc2fbfa4f779dcfd1327d4` 两匿名卷均已不存在，证明新清理逻辑在完整流程生效。

### 不同容器的真实 WebSSH 并发隔离（2026-09-17）

- 隔离面板 `ocv-panel-1789627683-5d6d4f` 使用当前 Rust PTY 修复版 Agent，开启 `OCV_LIVE_SIBLING_SESSIONS=yes`，完整驱动退出 0。保留原有同 guest 双会话、两代容器、流量、Agent 重启和端口复用检查，额外预留 `29900–29907`，第一代另创建一个 256 MiB sibling。
- 两个不同 guest 同时存在，分别通过公网 SSH 核对 hostname；各自 WebSSH 也核对身份。关闭 guest A 的 WebSSH 后，guest B 继续执行命令；普通 Agent 命令超时前后 B 仍可用，Agent 连接时间保持不变。随后通过面板 API 删除 sibling，确认对应 runtime、SSH 映射和 monitor 清除，原 guest 的既有 SSH transport 仍可执行命令。
- 原有两代容器公网 SSH 和独立 WebSSH 继续通过，独立来源分别为 `216.126.233.222 34540 10.29.203.147 22`、`216.126.233.222 53246 10.29.203.198 22`。流量入站/出站增量分别为 `1,077,888/1,092,777`、`1,094,484/1,109,268` 字节；两代实际 Agent 重启后保留计数并恢复命令执行。
- 两代面板删除后，全部预留端口无监听/nft/iptables 残留，monitor、Agent unit/目录及 Provider 已清理；隔离面板与匿名卷已删除。此轮没有开启嵌套 Docker 选项，该项真实验证来自上一节；不能将本轮额外算为嵌套测试。

### 干净首装后的重启、脚本复验和卸载（2026-09-17）

- 第二次 DD 干净 Debian 12/LXD 首装完成后的首次宿主重启已实测通过：boot ID 从 `f624122c-f1e8-4fd1-bd2e-44ce42adbb07` 变为 `19c5d459-1db2-4ce3-bf9d-e16cb1927ed2`，machine ID 保持。重启前后 runtime API 返回的 default 存储池、lxdbr0、default profile 完整快照一致；nft 自有表恰有一条限定 IPv4/lxdbr0 的 NAT 规则。公网 IPv6 默认路由仍无。
- 重启后两代脚本创建 `ocvscriptgzooznoo1/2` 实际通过；第一代无交互，第二代真实 PTY 回答全部 8 项。公网 SSH、DNS/HTTP 出网、CLI 删除及端口释放/复用均验证，独立 WebSSH 分别为 `216.126.233.222 51056 10.29.203.226 22`、`216.126.233.222 53684 10.29.203.226 22`。DHCP 两代复用同一 guest 地址不视为异常，身份通过 hostname 分别核对。原 helper 恢复，fixture 清理。
- 无交互卸载使用已记录的 `90e8f315...` 完整 SHA-256 版本，实际解绑 profile、删除 btrfs 存储池/lxdbr0、移除 LXD snap 和自有规则；驱动退出 0。共享 snapd、宿主转发设置及 snap 恢复快照保留；不能承诺恢复已删除的测试 guest/存储池。
- Firefox 实际打开独立面板 `ocv-ui-1789628910-427a6e` 首页并显示 1 个节点、0 个容器；未完成登录、创建、编辑或删除，不计作 UI 生命周期验收。重启使该 fixture 的 SSH transport 失效，正常清理驱动因此拒绝继续。重新只读核对节点无五类 runtime、无 LXD 数据和 Agent，数据库现存实例计数为 0 后，按 immutable ID/归属删除本轮空面板及匿名卷，未留下节点轮询。
- 收尾新执行：`server` 下全量 Go 测试与 vet 通过（普通测试多为缓存），全量 race 通过，macOS 链接器仍有 `LC_DYSYMTAB` 警告；Rust fmt 和 52 项测试通过，前端当时的 67 项单测、0 跳过及生产构建通过。该数字是历史执行时的数量，当前前端测试已扩展为 75/75；两安装仓库的防火墙锁测试在 macOS 因缺 Linux `flock` 失败，在真实 Linux 容器执行后通过，没有修改断言或将环境失败计作通过。
- 本轮真实 MariaDB 10.11.18/MySQL 8.4.11 连接兼容和重启、embedded 初始化/认证/配置保留/重启、ARM64 no-db/all-in-one 生命周期均通过。`embedded_database_live_test.sh` 是 integration 驱动的容器内入口，直接在 macOS 执行被安全前置拒绝；此前由 integration 驱动在两种真实数据库容器内的结果有效，不能单独把主机拒绝记为产品缺陷。

### Debian 12 jq 1.6 空响应缺陷与完整脚本回归（2026-09-17）

- 在隔离 Debian 12 全回归中，LXD 原有面板初始化测试真实失败：空网络响应被当成成功，profile 还出现空值整数比较。根因为 jq 1.6 对空输入即使使用 `-e` 也可能成功退出，本机 jq 1.7.1 未暴露相同行为；未删减或跳过失败用例。
- Incus/LXD 安装器和面板初始化统一先 slurp 并要求恰好一个对象，再校验同步 envelope、profile devices 和网桥配置的字段类型。查询命令先单独检查退出码，防止没有 pipefail 时错误被后续 JSON 处理吞掉。空白、null、标量、数组、多 document、错误 envelope、错误字段和失败命令均停止当前步骤。测试框架 runtime readiness 同步修复，保留直接 metadata/envelope 两种合法返回，以及自定义 DNS、关闭 NAT、IPv6 none、既有存储池/NIC 语义。
- 保留各面板原 19 场景，各追加 30 个；各安装器追加 16 个；面板测试框架追加 22 个，共增加 114 个边界场景。本机 jq 1.7.1 全部通过。另加 Debian 12/jq 1.6 CI job，不替换原工作流和原测试。
- 在真实 Linux Debian 12/jq 1.6 容器执行两个仓库的全部 tests/*.sh：LXD 21 个文件、Incus 20 个文件均退出 0，无跳过文件。包含 nftables、iptables-nft、iptables-legacy、实际 firewalld 的内核数据包/重载/迁移/卸载隔离测试，及 Linux 进程锁。全部 shell 语法、ShellCheck error 级和面板框架 readiness 回归通过。故障注入测试中预期的 jq/回滚报错不计为失败，也未吞掉顶层失败退出码。
- Go 侧真实防火墙集成脚本亦退出 0：nft/legacy 两个后端下 firewall、Incus、LXD 包通过。此处隔离 IPv6 包转发不是公网 IPv6 验收。
- 四份 Incus/LXD 中英文安装说明补充响应校验与保留语义，文档 check/build 通过；工作流 YAML 解析通过，尚未实际触发线上 GitHub Actions。文档原有 CRLF/混合换行，diff 检查按 cr-at-eol 检查，未为格式目的重写整篇。

### 第三次 SSH DD 与 Incus 干净首装（2026-09-17）

- 唯一目标仍为授权节点 `179.61.251.239`。固定版本 InstallNET 完成准备后，重新从 Debian 官方 SHA256SUMS 核验安装内核；核对 `/boot/grub2` 是 `/boot/grub` 的符号链接、单盘 /dev/sda 25 GiB、静态 IPv4、密码哈希、preseed 与 GRUB，再提交一次重启。没有 RAID 参数，没有使用供应商网站。
- 实际新系统为 Debian 12 / `6.1.0-50-cloud-amd64`，boot ID `0541a592-6551-4e49-8ea8-784935eb3c33`，machine ID `0d9c98bfb1c443d6bb3a75d01f73a118`，SSH host key SHA256 `8yi2OlGvj3I9KhLe0ncaHMCxkGKnvUaZYGcXIcDNy7g`，均与重装前不同。公网密码 SSH、DNS、IPv4 HTTPS 通过，五类待测 runtime 与 Agent 均不存在。原测试系统已擦除；公网 IPv6 默认路由仍不存在。
- 在该干净系统运行 Incus 纯交互首装，真实 PTY 且清除所有无交互/存储预设变量，实际回答两项提示。源码 SHA-256 `0c4c1a46a01877bce412745e7f7cb674141e0b0fb2d98ead85190bfc02ef2b95`，包与镜像使用真实源，当前辅助脚本仅通过 SSH loopback 镜像传输。完成后按安装器设计自动重启，boot ID 变为 `84506874-c31e-4f6e-b787-bda003d7e765`，Incus 6.0.4 / default dir 存储池、default profile、incusbr0 的严格检查通过。辅助脚本 URL 已恢复，fixture `/opt/ocv-live-install.0mUWtD` 清理。此结果是 dir 回退可用，不是首选 Btrfs 初始化成功。
- 真机进一步发现原生包 Incus 的驱动缓存时序问题：daemon 在 Btrfs/LVM 工具安装前启动，工具和内核支持已可用时，/1.0 的 storage_supported_drivers 仍只有 dir；Btrfs/LVM 初始化失败，随后额外尝试 ZFS/Ceph 并最终回退 dir。安装器重启 daemon 后，同一接口才列出 btrfs/lvm。ZFS 因新装内核版本与当前运行内核不同而不可加载是正常后端回退，未把它与驱动缓存问题混为一谈。
- Incus 新增受保护的驱动刷新：严格读取 daemon 能力列表，已有驱动不重启；缺失驱动且存储池确实为空时才 restart + waitready，并重新核验一次。已有存储池、无效响应、查询失败均禁止自动刷新；重启/等待失败不忽略。保留后端回退与已有环境复用，不删除/重建存储池。新增 22 个回归及原 18 个内核/软件包场景在本机与 Debian 12/jq 1.6 均通过，原初始化/面板/存储保留回归也重跑通过。新源码 SHA-256 `2bccfc8e6524d7904257538f4cb5dc4e1961cf92759c3c501913ec6751fbdfb5` 尚待再次干净首装验证，不能套用上一版本的现场结果。
- dir 回退环境上的脚本容器两代 `ocvscriptzokhrptx1/2` 均通过：第一代无交互创建，第二代真实 PTY 回答全部 8 项；两代均完成公网 SSH、DNS/HTTP、独立 WebSSH、CLI 删除与端口释放/复用。独立来源分别为 `216.126.233.222 38436 10.153.101.17 22` 和 `216.126.233.222 49402 10.153.101.100 22`。驱动完整退出 0，原辅助文件恢复，`/opt/ocv-live-scripts.b9w9CC` fixture 清理；没有实际开设 VM。此轮没有面板创建或 Agent 测试，不能并入此项通过。
- 后续驱动刷新补丁的中英文说明再次 check/build 通过；根目录 copy 脚本执行成功，逐个 cmp 核对 196 个现存修改/未跟踪交付文件与同步仓库一致。没有 API 定义新增，Swagger 沿用已生成版本。尚未提交或推送。

### 第四次 SSH DD 与 Btrfs 首装专项（2026-09-17）

- 第三次 DD 的空 Incus dir 环境已实际无交互卸载，卸载器 SHA-256 `ccf4c2f28ec8706ff866677de86a77d82f3acff5afdd014d4f655d4dc5fba080`；驱动退出 0，软件包、网桥、runtime 数据及自有防火墙持久化检查通过。随后仅对同一授权单盘再次 SSH DD，未使用供应商网站或 RAID 参数。
- 新系统已真实验证 Debian 12 / `6.1.0-50-cloud-amd64`，boot ID `3d439c3c-94dd-4492-a05b-be124a4cf9f3`，machine ID `1204a70067fd4a3186b68a5cbf904819`，SSH host key SHA256 `YtPCeH0SfwAW9xDcl2Fzd6AYthMFdses1pMsoVuIHsY`。公网密码 SSH、DNS 和 IPv4 HTTPS 通过，原系统已擦除；没有公网 IPv6 默认路由。
- 最新 Incus 全部 21 个 Shell 测试文件在 Debian 12/jq 1.6 Linux 环境再次退出 0、无跳过，包含新增驱动刷新 22 场景、原内核支持 18 场景以及真实 nft/xtables/firewalld 内核隔离测试。完整原测试保留，未将专项替换为少量烟测。
- 首装验收新增可选的 `OCV_LIVE_EXPECT_STORAGE_DRIVER`，本次指定 Btrfs，意外回退到 dir 不算此专项通过。7 项本地契约测试通过，覆盖预期后端、错误回退、空/无效/重复池，同时保留未指定时合法回退和自定义池的容错语义；已接入原 CI。它们不是实际安装证据。
- 修复版纯交互 Incus 首装实际通过：源码 SHA-256 `2bccfc8e6524d7904257538f4cb5dc4e1961cf92759c3c501913ec6751fbdfb5`，真实 PTY 回答两项提示，首次即建成 `default` Btrfs 池，未回退 dir。安装器自动重启后 boot ID 为 `254d636d-7c75-4825-b66d-33cc62b6d23e`，Incus 6.0.4、存储、default profile 和 incusbr0 严格检查通过；原 helper URL 恢复，`/opt/ocv-live-install.g5Cd5w` 清理。脚本容器验收另行记录。
- 脚本两代 `ocvscriptebaahimp1/2` 已完整退出 0：第一代无交互创建，第二代 PTY 回答全部 8 项；两代均通过公网 SSH、DNS/HTTP 出网、CLI 删除与 `29800–29825` 释放/复用。独立 WebSSH 来源分别为 `216.126.233.222 60452 10.239.82.169 22`、`216.126.233.222 48524 10.239.82.199 22`。原辅助文件恢复，`/opt/ocv-live-scripts.WR8b2G` 清理。

### 用户授权后的 IPv6 路由自愈（2026-09-17）

- 用户明确授权缺失默认 IPv6 路由时自行尝试添加并请求 `ipv6.ip.sb`。只在当前授权节点操作，没有把网关猜测推广为生产安装器默认行为。
- 新鲜检查确认 eth0 无全局 IPv6/默认路由，IPv4 网关 `179.61.251.1` 的 MAC 为 `e4:1d:2d:74:15:20`。`fe80::1` 不通；由该网关 MAC 推导出的 `fe80::e61d:2dff:fe74:1520` 实际 ping 成功，邻居表随后标为 router。未把其他租户的邻居地址作为网关。
- 在已分配的 `2a0f:5707:aaf1:e82a::/64` 内临时配置 `::2/128`，DAD 通过，临时默认路由指向上述链路本地网关。`curl --noproxy '*' -6 --interface 2a0f:5707:aaf1:e82a::2 https://ipv6.ip.sb` 实际返回完全一致的公网地址。临时配置按精确地址/路由清理，IPv4 未修改。
- 验证后写入独立 `/etc/network/interfaces.d/ocv-public-ipv6`，由原 interfaces 的通配 include 加载；ifquery 同时解析原 IPv4 与新增 IPv6 正确。应用后再次强制 IPv6 请求通过。该节点的宿主 IPv6 出网前置已解除；重启持久化、独立 IPv6 SSH/HTTP、容器纯 IPv6/双栈仍需各自验证，不能沿用此前“无上游路由”的阻塞结论，也不能仅凭出网即记完整通过。
- 两个独立德国探针实际 IPv6 ICMP 通过（Globalping `2tj4JrIalSfGbq1tF000219SA`）。初次 HTTP 检查 Netcup 取回唯一服务标记，但 Hetzner 超时，因此该轮严格两探针检查失败，临时服务已清理。随后两个已确认 TCP 可达的独立探针均通过 SSH 22 的 TCP 握手（`2dbPPaV0uGyxb51dq000219Tj`），并在新的临时 IPv6 HTTP 服务返回 200 和相同的本轮随机标记（`2lXiTVwCcNVNTZ6fI000219Tm`）。服务使用并发 HTTP server 避免单连接阻塞，设置 180 秒最长寿命，结束后 unit 和 `29980` 监听器均清除；这些都是宿主实测，尚不等于容器验收。
- 独立 WebSSH IPv6 登录未通过。精确目标 TCP 22 抓包确认 `2606:a8c0:3:391::` 的 SYN 已到达本节点，本节点多次发出 SYN-ACK，但没有收到最终 ACK；同时 Hetzner/Netcup 完成三次握手并收到 SSH banner。该证据把当前问题定位在该外部来源的回程/端到端路径，不能归因于密码、Incus 初始化或 SSH 未监听，也不能只凭 TCP 探针宣称 SSH 身份认证成功。

### IPv6 保活对宿主计划任务的破坏修复（2026-09-17）

- 静态复查确认 Incus/LXD 的保活安装先用正则查找包含 `*` 的 cron 行，找不到便将单条任务直接 pipe 到 `crontab -`，会覆盖 root 的其他计划任务。两 Provider 已改用同一个独立 cron.d 文件、有限时进程锁、原子写入；已存在的相同文件幂等复用，其他自定义文件、符号链接和目录拒绝覆盖。不读写 root crontab；缺可选依赖或写入失败只告警，不把保活失败升级为容器创建失败。
- 8 个真实 Shell 文件行为场景覆盖创建、重复、12 个并发调用、自定义内容、符号链接、目录、缺 cron 与发布失败，核验其他计划任务及临时文件清理。macOS 使用实际内核文件锁兼容系统没有 flock 命令的情况，Linux 使用真实 flock；两环境通过。race 首轮发现测试辅助进程的 Go race 退出延迟持锁一秒会人为累积超时，已仅在该辅助进程关闭退出睡眠，保留生产 10 秒锁期限和全部并发断言；后续 utils/Incus/LXD race 均通过。Go 全量测试和 vet 也通过。
- 当前正在运行的 NAT 面板真机镜像构建早于这项 cron 补丁，不能将其 NAT 结果当作新 IPv6 保活路径的实际部署证据。后续 IPv6 面板测试需重建镜像。

### routed IPv6 的 NDP 自愈补强（2026-09-17）

- 复查发现隧道桥接路径在 `setupRoutedNetworkDeviceIPv6` 中只做了网桥/隧道健康检查，没有像原生 IPv6 路径一样先应用 `net.ipv6.conf.all.proxy_ndp=1`；在 Incus/LXD 上这会导致 routed NIC 被运行时拒绝，表现为实例创建后启动失败并回滚。
- Incus/LXD 现在在附加 routed `eth1` 前原子写入独立 `/etc/sysctl.d/99-oneclickvirt-ipv6-routed.conf`，立即应用 all/default forwarding、全局 proxy NDP 以及网桥/隧道接口的 forwarding/proxy NDP；原生路径也补齐 default forwarding。隧道服务生成的持久 sysctl 和状态检查同步要求 proxy NDP，避免重启或网络重载后回归。
- provider、隧道服务回归及全量 Go 测试已通过；该修复尚未在新的 `nat_ipv4_ipv6` 面板镜像上完成真机矩阵，后续仍需用重建镜像分别验证纯 IPv6 与双栈容器、公网 IPv6 SSH/HTTP 和删除回滚。
- 进一步修复多隧道并发边界：Incus/LXD 的 routed sysctl 文件现在按网桥与隧道接口生成独立路径，第二条隧道不会覆盖第一条接口的持久化项；新增双调用回归通过，并将平台检查置于 sysctl 修复之前，非 Linux/网桥缺失时保留明确前置错误。

### Rust PTY 并发分配容错（2026-09-17）

- Agent 的并发 PTY 回归在 macOS/资源受限环境偶发 `posix_openpt: ENXIO`，属于系统伪终端资源瞬时竞争，不是会话串流交叉。`open_pty` 现在仅对 `ENXIO`、`EAGAIN`、`ENOMEM` 做最多 100 次、每次 1ms 的有界重试；其他错误仍立即返回，避免无限等待或吞掉真实配置错误。
- Rust Agent workspace 52 项测试现已全部通过；之前的 PTY 描述符、子进程继承、UTF-8 边界及跨会话隔离用例均保留。

### 本轮验证边界（2026-09-18）

- `go test ./...`、`go vet ./...`、Rust `cargo test --workspace`（55/55，含重复 shell ID、控制队列满回收、PTY errno 重试与隧道关闭 ID 校验）、文档站 `npm run check`/`npm run build`、防火墙双后端隔离集成和可执行 Shell 回归均通过；根目录 `copy_project.sh` 已再次执行并核对新增文件同步到 `/Volumes/Additional/个人数据/temp/oneclickvirt`。
- 本轮针对 Rust Agent 的 clippy 机械告警完成修复（冗余导入、无效转换、可折叠条件、类型别名和文档列表格式），`cargo clippy --workspace --all-targets --all-features -- -D warnings`、`cargo fmt --all -- --check` 与 `cargo test --locked --all-targets` 均通过；未改变 Agent 会话、PTY 或网络生命周期语义。`ipv6_external_acceptance_test.sh` 仍因明确要求独立外部凭据而未执行，属于环境前置，不计通过。`embedded_database_live_test.sh` 已在隔离 ARM64 Docker 容器中完成双引擎实测，结果见上节。
- Agent 回归工作流已加入同等的格式检查和 `cargo clippy --workspace --all-targets --all-features -- -D warnings` 门禁，避免后续提交重新引入已清零的编译器/风格阻断项。
- Live 验收契约测试在隔离 Python 虚拟环境安装 `paramiko` 后 43/43 通过；集成工作流同时补装 `websocket-client`，确保 Agent/WebSSH 分支不会因测试依赖缺失而在导入阶段失败。
- 复查 ARM controller 工作流发现其曾把缺失退出码、基础设施失败和全量 SKIP 当作成功；现已改为 fail-closed，缺失/非零退出码、空或非法 JSONL、未知状态及全量 SKIP 均使工作流失败，并由 workflow gate 回归覆盖。
- Incus/LXD 两个脚本仓库的真实 firewall lock 并发测试在 Debian 12 容器中均通过（包含失败释放、锁 inode 保持和符号链接拒绝）；macOS 本机因没有 Linux `flock` 仅能报告环境前置失败，未将其计为通过。
- 复查网络模式矩阵发现失败分支曾固定 `exit 0`，导致端口映射/SSH 失败只留在报告中而不阻断调用方；现改为 `exit 1`，并由 action harness 回归检查失败传播。
- 本轮只读复核专用节点时，SSH 返回的新 ED25519 主机指纹与本地 `known_hosts` 记录不一致；严格校验因此拒绝连接。未绕过校验、未更新信任记录，也未执行 DD/卸载/远端删除，真机验收继续保持未通过状态。
- 本轮新增 Rust Agent 回归覆盖重复 shell ID 的原子注册/资源回收，以及隧道关闭帧的完整 ID 校验；两项均通过，未改变共享连接或其他会话的生命周期。
- 静态审计曾将 `runtime_readiness.sh` 中跨行 jq 过滤器误报为未检查错误；现将过滤器提取为变量并保留显式 `|| return 1`，同时给审计器补充命令替换错误传播识别。严格静态审计现为 0 个高风险 jq、0 个管道风险、0 个工作流发现；运行时边界回归仍为 22 个附加场景全部通过。
- 追加 `go test -race -count=1 ./...` 全量通过；macOS 链接器仅输出已知的 `LC_DYSYMTAB` 警告，没有 race detector 报告或测试失败。
- 当时前端 `npm run test:unit` 67/67 通过，`npm run build` 成功；这是该日期的历史结果，不能代表当前测试数量；仅保留既有大 chunk 体积提示，没有构建错误。
- 已使用本机 `aarch64-linux-musl-gcc` 交叉链接器构建 Linux ARM64 Agent：`server/agent/target/aarch64-unknown-linux-musl/release/oneclickvirt-agent`，确认是静态 ELF aarch64；此前默认 `cargo build --target` 会误用 macOS linker，已记录为工具链前置，不把 Mach-O 产物用于 Linux live 测试。
- 已基于当前源码重建 ARM64 面板镜像 `oneclickvirt-ci:arm64-ipv6-keepalive-v3`（digest `sha256:a24657aad845b4e123d5a9bfd17d2d47ddd44b260b59409d5d0341d87bc57f34`），包含 routed IPv6 NDP、多隧道隔离和最新前端/数据库修复；尚未在专用节点执行新的面板 IPv6 live 矩阵。

### 追加并发/安装失败路径修复（2026-09-18）

- Rust Agent 在 macOS 高并发 PTY 分配时可能返回负号形式的 `ENXIO`；`open_pty` 现在对正负 errno 统一识别，仅对 `ENXIO`、`EAGAIN`、`ENOMEM` 做有界重试，连续 5 轮 workspace 测试均为 55/55 通过、0 ignored。
- Rust shell 控制队列已满时，初始化路径现在显式终止并 wait 子进程，再释放 session/permit，新增回归覆盖无僵尸和 permit 不泄漏。
- Full installer 在归档解压、服务端二进制复制/权限设置失败时立即终止；OneClickVirt 服务启动命令失败不再被吞掉，临时文件会清理后返回失败。数据库双引擎配置保护和安装生命周期回归仍通过。

### 嵌入式数据库隔离容器复验（2026-09-18）

- 在本机 ARM64 Docker 隔离容器中直接执行 `embedded_database_live_test.sh`，分别使用 `mariadb:10.11` 和 `mysql:8.4`；源码只读挂载，数据目录随容器销毁。
- 两种引擎均实际完成初始化、带特殊字符的 root 密码配置、引擎识别、配置转换/保留、应用用户认证、哨兵表写入、错误密码拒绝、停止后重启及数据读取复验，均退出 0，无跳过。
- 该结果补齐此前“宿主直接执行因隔离容器前置拒绝”的验证边界，但只证明数据库初始化/配置生命周期，不替代远端节点安装或面板完整 UI 验收。

### 追加静态/重复运行复验（2026-09-18）

- `go test -race -count=1 ./...` 全量通过；随后以 `go test -count=2 ./...` 和重点包 `-count=3` 复跑，发现并修复了测试夹具的固定 SQLite 内存 DSN、全局缓存键和外部任务处理器注册污染。修复后重复运行全部通过，避免把第二轮失败误判为源码回归。
- Incus/LXD API 列表请求现在对非 2xx、缺失 `metadata` 或错误 metadata 类型 fail-closed；新增 HTTP transport 回归覆盖 502、缺字段和错误类型，防止上游错误被当成“空实例列表”触发误删除或错误回退。
- 集成工作流结果门禁对测试步骤缺少退出码、无 JSONL 或空结果统一判为 INCOMPLETE/失败，不再将被跳过或提前中止的运行标记为成功；新增 workflow gate 回归并接入原有 harness 检查。

### Python live 驱动导入与完整发现修复（2026-09-19）

- `live_lxc_install_test.py`、`live_lxc_script_test.py`、`live_incus_panel_test.py`、`live_incus_uninstall_test.py` 不再在模块导入阶段检查节点凭据或直接 `SystemExit`；这些前置条件现在只在实际 `main()` 执行时校验，避免 `unittest discover` 因未请求 live 验收而整套失败。
- `paramiko` 及动作测试 SSH helper 改为可选依赖的运行时检查。直接执行 live 驱动仍会明确失败并提示安装 `scripts/tests/requirements-live.txt`，离线单测则安全导入；缺少依赖时 SSH 传输测试显示为明确 skip，不会伪装成通过。
- 系统 Python 的 `python3 -m unittest discover -s scripts/tests -p '*_test.py' -v`：45 项通过、13 项因可选依赖缺失明确跳过、0 项错误；隔离 venv 安装 `paramiko`/`websocket-client` 后同一命令 45/45 通过、0 skip。`python3 -m compileall -q scripts/tests action_tests/common` 和 `git diff --check` 均通过。

### 2026-09-19 最终复验

- Go `go test ./...`、`go test -race ./...`、`go vet ./...` 均通过；macOS 链接器仅输出已知 `LC_DYSYMTAB` 警告，无 race 报告或失败。
- Rust Agent `cargo test --workspace --locked --all-targets` 为 57/57，`cargo fmt --all -- --check` 和 `cargo clippy --workspace --all-targets --all-features -- -D warnings` 均通过。
- 前端 `npm run test:unit` 为 75/75、0 skip；`npm run build` 成功，仅保留既有大 chunk 体积提示。
- 所有本地脚本/隔离容器回归（数据库双引擎、ARM 生命周期、防火墙 nft/iptables 双后端、Incus/LXD、安装卸载、IPv6 清理、工作流门禁）均退出 0。`static_audit.py --strict` 为 0 高风险 jq、0 管道风险、0 workflow/retry 发现；ShellCheck 仍有测试夹具和平台适配层的既有 warning/info，不构成运行时失败。
- 根目录 `copy_project.sh` 已执行；关键 Python 驱动、SSH helper、验收报告与目标 `/Volumes/Additional/个人数据/temp/oneclickvirt` 逐文件 `cmp` 一致。
- `REMOTE_PORT` 的非法值不再在 SSH helper 导入阶段阻断测试发现；真实连接时仍 fail-closed，CLI 继续在参数解析阶段给出明确错误。带 `REMOTE_PORT=not-a-port` 的完整 SSH 回归 13/13 通过，完整 Python discovery 仍为 45/45。
- 重新执行相邻脚本仓库回归：Incus 23 个测试入口中 20 个退出 0，3 个仅因 macOS 缺少 Linux `flock` 或网络 namespace 权限返回标准环境码 75；LXD 22 个入口中 19 个退出 0，3 个同样是标准环境码 75。两仓库的初始化、存储池保留、双栈 IPv6、防火墙持久化/失败传播、实例归属回滚、非交互模式和卸载清理均无失败；ECS 的 `noninteractive` 统一开关测试通过。此前同步的 Linux CI 门槛仍需在 Linux runner 实际执行，不能将 macOS 的环境码当作通过。
- 文档仓库 `/Volumes/Additional/个人数据/GitHub/oneclickvirt.github.io` 的 `npm run check` 与 `npm run build` 均通过；中英文环境模式、数据库兼容和容器安装文档链接/locale/元数据保持一致。
- 集成工作流的严格静态审计原先仍标记为 `continue-on-error`，会把高风险发现降级为 warning；现已改为写入摘要后传播非零退出码，并新增 workflow gate 回归验证审计命令、阻断错误和退出码传播，避免静态门禁再次被绿色工作流掩盖。
- 继续执行远程只读复核时，专用节点 `179.61.251.239` 的当前 ED25519 指纹与本地受信记录不一致，严格 SSH 返回 `BadHostKeyException`；生产节点 `192.3.64.219:1777` 当前凭据返回认证失败。未绕过指纹、未尝试猜测凭据、未执行 DD/卸载/删除，真实节点矩阵仍应标记为外部访问前置阻塞。
- 在 Debian 12 `--privileged` 隔离容器中补跑此前 macOS 环境门槛：Incus 与 LXD 的 `firewall_lock_test.sh`、`script_lock_test.sh`、`masquerade_kernel_test.sh` 均通过。每个运行时均实际覆盖文件锁/子进程互斥、xtables 到 nft 迁移、IPv4 NAT、routed IPv6、显式禁用 NAT、无关流量保护、iptables-nft 与 iptables-legacy 双后端及卸载归属；不再把这 6 项记为未执行。

### 2026-09-19 cron.d 共享计划任务修复与复验

- 复查 Incus/LXD 全部 IPv6 构建、容器/虚拟机脚本、面板修改脚本及 Incus CPU 监控脚本，发现多处仍通过 `crontab -l | crontab -` 读改写计划任务。宿主 IPv6 保活现在统一写入独立 `/etc/cron.d/oneclickvirt-ipv6`，带 `root` 用户字段；写入使用锁、临时文件和原子 `mv`，保留已有内容，拒绝 cron 目录、目标文件、锁目录和锁文件符号链接，重复调用幂等，不再读写 root 用户 crontab。
- 容器内的 IPv6 保活入口也改为独立 `/etc/cron.d/oneclickvirt-ipv6`，使用 `/run/lock` 下的有限等待目录锁和原子更新；没有 cron.d 的 Alpine/OpenWrt 等镜像将跳过这个可选保活，不因不支持的 cron 布局阻断实例开设；恶意符号链接和非普通目标文件仍 fail-closed。Incus CPU 监控脚本的安装/卸载任务也改为独立 cron.d 文件，并在自有文件含自定义内容时拒绝删除。
- 新增 Incus/LXD `ipv6_cron_d_test.sh`：既有任务保留、重复幂等、8 路并发只产生一条任务、目标/目录/锁符号链接拒绝；Debian 12 隔离容器通过。两仓库全量 Shell 回归在补齐 `iproute2/nftables/procps` 后通过，真实 nftables、iptables-nft、iptables-legacy、NAT 和 routed IPv6 命名空间测试均通过。
- 主仓库移除 IPv6 keepalive 命令中不再使用的 `crontab` 依赖，并拒绝 `/etc/cron.d`、`/run/lock` 父目录符号链接；Go IPv6 keepalive 测试覆盖 Linux `flock`、macOS 文件锁兼容、cron 目录/锁目录/目标文件边界，主仓库 Go 全量、race、vet，Rust 57/57、clippy，前端 75/75 与生产构建均通过。`copy_project.sh` 已在本轮修改后重新执行并逐文件校验。
- 生产节点只读连接未执行：`192.3.64.219:1777` TCP 可达，但当前 live ED25519 指纹与本地受信 `known_hosts` 不一致；未绕过主机密钥校验，也未使用凭据进行未验证主机登录。公网 IPv6、面板生命周期和真实重装仍保持外部验收阻塞。

### 生产节点与 Hetzner 公网 IPv6/NAT 实测（2026-09-19）

- 未使用浏览器。通过严格 `known_hosts` 登录生产节点，确认宿主公网 IPv6、默认路由、`ipv6.ip.sb` 出口、Incus `default` 存储、`default` profile 和 `incusbr0` 均正常；原有 3 个实例只读核对，未停止、修改或删除。
- Hetzner API 项目内只有 1 台最低规格测试服务器。按授权原地重装 Debian 12 后只重置该服务器 root 密码继续测试，未创建第二台、未删除服务器；主机密钥从已认证生产节点临时固定，未降低 SSH 主机密钥校验。
- 双向宿主验收通过：生产节点以公网 IPv6 SSH 认证到 Hetzner，Hetzner 出口精确返回其分配 IPv6；生产节点读取 Hetzner 临时随机 HTTP 身份；Hetzner 再通过生产节点公网 IPv6/非标准 SSH 端口完成反向认证。
- 生产 Incus NAT-v6 端到端通过：创建随机命名、显式静态 IPv4 + ULA IPv6 的临时 guest，添加宿主公网 IPv6 到 guest ULA 的 SSH/HTTP `nat=true` proxy。Hetzner 从不同公网节点取得随机 HTTP 身份，并使用从控制连接读取后固定的 guest SSH host key 完成密码认证；guest 内 `$SSH_CONNECTION` 显示来源为 Hetzner 公网 IPv6、目标为 guest ULA:22，`curl -6 ipv6.ip.sb` 返回生产宿主公网 IPv6。
- 最终成功运行使用临时端口 `29991`/`29992`。HTTP 服务先检查 guest 监听，再检查宿主 proxy 身份，外部请求使用有限重试，避免用固定 sleep 制造偶发失败。临时 HTTP 进程、proxy 设备和 guest 均已清理，复核实例列表只剩原有 3 个实例，测试端口不再监听。
- 根因同步确认：Incus/LXD 的 `nat_ipv4_ipv6` 后端明确使用“guest ULA + 宿主公网 IPv6 proxy”，但控制层此前仍从 `/64` 节点文件/地址池分配 `static_ipv6`；后端会正确拒绝该独立地址，表现为创建失败并回滚。现仅对 Incus/LXD 此 NAT 模式跳过控制端 IPv6 池，其他 provider 和独立/纯 IPv6 模式保持原分配语义；前端隐藏该组合下的地址池输入并说明无需填写 `/64`。中英文文档同步修正。

### 生产面板 API 完整 Incus NAT-v6 验收（2026-09-19）

- 将最新 Linux amd64 后端与前端安全部署到生产节点，远端时间戳备份和自动回滚保护保留。最终运行后端 SHA-256 为 `2c3221a5de0fdd3b2c5410fa241f80d93ef16005182fabace86f83688a2e17c7`，服务、Caddy 和 API 健康检查通过。
- 真实面板 API 创建 `nat_ipv4_ipv6` Incus 容器并完成任务链。Provider 继续保留 `/etc/oneclickvirt/ipv6-pool.txt`，但本 NAT 模式不再消费文件内的 `/64`；guest 取得精确 ULA，Hetzner 经生产公网 IPv6 取得随机 HTTP 身份、密码登录精确 guest，guest 内 `curl -6 ipv6.ip.sb` 返回生产公网 IPv6。本轮未使用浏览器或 `216.126.233.222:8888`。
- 其余实际根因为：Debian 13 `ip` 输出的 ANSI 颜色码污染 IPv6 前缀解析；managed NAT-v6 错误要求宿主必须有可分配前缀，而 proxy 监听只需宿主可用的公网 `/128`；双栈自动端口在 ULA 尚未生成时同时安装两个地址族 proxy；停止后只依赖相对路径 `*_v6` 文件恢复 guest ULA；以及容器停止后才按 DHCPv6 ULA 回绑 NIC，此时无法再用运行态地址/MAC 唯一匹配设备。
- 最终顺序为：首先只安装 IPv4 映射，启动 guest 并获取 ULA，运行态将 ULA 固定到精确 NIC 并持久化到实例数据库，然后停止 guest 安装 IPv6 映射；旧临时文件仅作兼容兜底。Incus/LXD 两条路径同步修复，回归覆盖地址族顺序、数据库持久化、ANSI 输出和 `/128` proxy 语义。
- 验收收尾通过面板 API 删除手工映射与测试容器，恢复 Provider 资源预算开关。最终只读复核显示预算为 `false/false/true`，活动数据库记录与 Incus 运行态均只有原 3 个实例，活动测试端口、任务、proxy 和监听残留均为 0；Hetzner 项目仍只有原有 1 台服务器。
- 最新源码再次完整执行 `server/go test ./...` 通过；21 个 Python 验收/动作驱动以无缓存内存编译检查通过，Swagger/API 合同同步测试与 `git diff --check` 通过。根目录 `copy_project.sh` 重新执行，主要验收文件、Swagger 三件套和已跟踪的默认 `server/config.yaml` 在目标副本中哈希一致；目标副本不含 `__pycache__`/`*.pyc`、`.env` 或运行时数据文件。

### Hetzner 多系统兼容性实测（2026-09-19）

- Hetzner API 项目安全校验始终返回恰好 1 台现有服务器（`cx23`、x86_64）；本轮只使用原地 rebuild/reset，未创建或删除服务器。Debian 12、Debian 13、Ubuntu 22.04、Ubuntu 24.04 和 Rocky 8 已通过真实 root SSH、systemd、包管理器、IPv6 全局地址、默认路由以及 `curl --noproxy '*' -6 https://ipv6.ip.sb`。
- Debian 13 真实 Incus 首装（非交互）通过：原生包、btrfs 存储池、默认 profile、`incusbr0` 和运行态 readiness 均通过；随后两代脚本分别以关闭 stdin 和真实 PTY 开设容器，验证公网 SSH、DNS、HTTP 出网、CLI 删除及端口释放，最后非交互卸载通过并确认包、挂载、网桥和持久化规则清理。
- Debian 12 真实 LXD 首装（非交互）同样通过：snap LXD 5.21.7、btrfs 存储池、默认 profile、`lxdbr0`、两代脚本容器的公网 SSH/DNS/出网/删除/端口复用，以及完整非交互卸载均通过；snap、存储、网桥、挂载和归属防火墙规则均已核对清理。
- 重装后的临时 root 密码可能在首个非 PTY 登录中处于过期状态；驱动先等待 guest-agent 可用再调用 `reset_password`。显式 `OCV_LIVE_KNOWN_HOSTS` 时不再合并本机旧 `known_hosts`，避免同一 IP 重装后的新 host key 产生假阴性。
- Rocky 9/10 在当前镜像上未在限定窗口恢复可认证 SSH，记录为“系统访问前置失败”，不是安装器通过或失败结论；Alma、CentOS Stream、Fedora、openSUSE 的后续完整 runtime 验收仍需可用 SSH 入口后执行。该限制不会被基础 IPv6 探测结果覆盖。

### Hetzner 矩阵收尾（2026-09-19）

- Fedora 44 镜像完成基础兼容性前置检查：root SSH、systemd、IPv6 全局地址、默认路由和 `curl --noproxy '*' -6 https://ipv6.ip.sb` 均通过。由于本轮运行窗口已结束，未将该结果扩展为 Incus/LXD、Docker、Podman 或 Containerd 的干净安装验收。
- openSUSE 16 在当前镜像的限定窗口内未恢复可认证 SSH（`NoValidConnectionsError`），因此没有执行任何安装器、容器创建、卸载或网络断言；该结果只记录为镜像访问前置阻塞，不判定源码或安装器不兼容。
- 矩阵收尾前通过 Hetzner API 原地将项目内唯一服务器从 openSUSE 16 恢复为 Debian 12，并在新的隔离 host-key 文件下完成严格 root SSH 验证。Debian 12 的 systemd、全局 IPv6、默认路由及 `curl --noproxy '*' -6 https://ipv6.ip.sb` 均通过；最终 API 复核仍为 1 台 running 服务器，未创建或删除服务器。

### 尚未完成的验收（截至本轮）

- 干净首装已证明 Debian 12/LXD 无交互、Debian 12/Incus 纯交互 Btrfs 组合；相反首次安装模式及剩余受支持 OS/运行时组合仍需实际执行。后续重装按用户要求使用 SSH DD，不再走供应商网站。
- 按最新要求不再把浏览器 UI 或第三方 WebSSH 当作本轮 IPv6 验收条件。最新源码的生产 Incus SSH Provider 面板 API 任务链已通过；尚未完成的是最新 Agent 连接模式在生产的完整面板矩阵，及非 Incus/LXD NAT 模式的真实实机组合。
- Incus/LXD NAT IPv6 已通过独立 Hetzner SSH/HTTP 与 guest 出网验收。纯独立 IPv6以及其他需要路由 `/128` 的 provider/模式仍需使用真实可分配前缀完成 guest 直连 SSH/HTTP，不能用本轮 ULA + proxy 结果代替。
- 实际 daemon 下跨调用者同名替换、并发创建/删除与失败回滚；脚本 flock 不等于面板/外部 CLI 的条件删除原子保证。
- 不同 OS/防火墙后端的宿主重启与持久化矩阵；已通过的 Linux namespace 数据包回归不能替代宿主实际安装。

### 早前复查记录（历史状态，后续进展见上文）

- 2026-09-17 11:51 CST 只读 SSH 再次核对：仍为 Debian 13，boot ID `4c485f6b-a67d-4b7f-946c-64d4a819d27a` 未变化；常规 PATH 和两个标准 snap 路径均未发现 LXD，也没有其他待测运行时、全局 IPv6 地址或 IPv6 默认路由。本地没有安装/创建/卸载验收进程运行。后续浏览器重装操作连续未能完成，已请求用户将授权节点重装为 Ubuntu 24.04 并告知登录凭据是否变化；干净 OS 矩阵暂时受阻，不以当前空运行时状态代替系统重装。所有待执行项继续保持未通过；公网 IPv6 还需供应商明确可用的上游配置及独立 SSH/HTTP 实测。

- 2026-09-17 后续 LXD 验收准备发现驱动环境边界：安装器的 `ensure_lxc_path` 仅 export 当前进程 PATH，新 SSH exec 不继承该修改；四个真机驱动却直接调用 `lxc`。现统一在节点命令（含 PTY）追加两个标准 snap 路径，保留原 PATH、不 source 用户启动文件、不掩盖原命令退出码，安装前保护也使用相同环境。5 个新增真实非登录 shell 回归通过，原有 10 个真实本地 SSH transport 回归全部保留并通过；新增用例已接入原 CI。临时 `lxc` 仅用于路径定位故障复现，不能记作实际 LXD 安装/创建通过。此轮尚未提交 OS 重装或安装 LXD。

- 2026-09-17 02:46 CST 再次真实 SSH 复核：授权地址 `179.61.251.239` 可登录，仍为 Debian 13，boot ID 为 `4c485f6b-a67d-4b7f-946c-64d4a819d27a`。五种运行时命令均不存在，没有全局 IPv6 地址或默认路由；此前卸载后的空运行时状态保持。Firefox 页面中的 VM UUID/VMID/IP 均与授权节点一致，仍显示 Debian 13；尚未提交系统重装。
- 卸载验收驱动已扩展支持 LXD（保留 `live_incus_uninstall_test.py` 文件名），区分两种真实确认提示，清除继承的自动化变量，检查 LXD snap、网桥、挂载及精确归属的防火墙表，并保留共享 snapd/恢复快照。Python 编译和 diff 检查通过；LXD 实际执行仍待安装后的验收，未记作通过。

- 创建回滚已补归属与进程锁回归，仍需在真实 daemon 验证 init/copy 标记与 UUID、跨调用者协调，以及 LXD 干净 OS 安装后的整个创建/删除流程；本地故障注入和两代串行复用不能代替这些验收。
- firewalld 单独回退、禁用和重载已在隔离 Linux 内核测试通过；nft 反向切换到 xtables、不同版本 firewalld，以及实际 OS 重启后的跨后端持久化仍需复验。公网 IPv6 仍缺宿主机明确的上游默认路由，未将隔离命名空间中的 IPv6 数据包验证计作公网通过。
- iptables 发行版标准路径、包和服务选择已修复并有故障回归；各发行版真正安装与重启、自定义持久化配置仍待验证，不能仅凭文件写入与服务 enable 的模拟断言计为通过。
- LXD 卸载器的主 nft 配置覆盖、全局关闭 IPv4 转发、宽泛 btrfs 挂载与 fstab 清理已做本地修复；snap 查询/卸载失败不再被当作已卸载。新增实际执行配置清理函数的保留性/幂等回归，已接入 CI，ShellCheck、语法及回归通过。LXD 真机创建和完整卸载仍待执行，不计为通过。
- Incus/LXD 中英文安装文档已补充卸载保留语义，文档站 `npm run check` 和 `npm run build` 通过。

## 源码与文档发现

- 五个仓库文档已有 `noninteractive=true` 主入口，但大写和 Incus 旧变量的兼容规则不统一，需检查子进程继承及显式关闭语义。
- 既有安装器会从远端 main 下载辅助脚本；本次验收必须记录并核对运行的本地修改版本，防止主脚本与辅助脚本版本混用。
- `action_tests/common/remote.py` 原有等待退出码后读取和单流读取问题已修复，安装/创建/卸载交互路径也已统一使用修复后的读取器；10 个真实本地 SSH transport 回归和上述专用节点 PTY 检查通过。尚未执行的安装组合不能因此计为通过。
