# 非 PVE 安装初始化排查与修复记录

日期：2026-09-14。范围：面板实际使用的 Incus、LXD、Docker、Podman、Containerd、QEMU、KubeVirt 安装入口及 CI 验收路径。

已确认存在“初始化失败但安装返回成功”的源码缺陷。Incus 在已有存储池时跳过初始化，而缺少 incusbr0 时只输出警告，这与用户反馈的“修复 local、default profile 和 incusbr0 后能创建实例”吻合。没有该节点当时的完整安装日志，无法确定具体经历了哪个分支，也没有证据把全部异常归因于 IPv6。

## 已修复的安装路径

| 环境 | 已确认的问题 | 本地修复 |
|---|---|---|
| Incus | 已有池直接返回；缺桥只警告；主流程吞存储、网络失败；结束前主动停止 daemon；重复安装覆盖 root ID 映射与 nftables 主配置 | 独立检查并补齐 profile/root/NIC/托管网桥；识别 local 等已有池；错误向上传递；重启后等待 daemon ready，取消结束时 stop；保留现有 ID 映射，防火墙规则独立保存并保留主配置 |
| LXD | 已有池跳过初始化；直接设置不存在的 lxdbr0；主流程吞错；干净 Debian 在 snapd 前被拒绝 | 同样独立补齐初始化并传播错误；先引导安装 snapd，保留自定义 DNS、地址、IPv6 禁用设置；检查脚本下载、复制和必需 IPv4 sysctl 结果 |
| Docker | daemon 不可用仍输出安装完成；包索引更新失败仍继续；修复缺失公钥后仍保留第一次失败码 | daemon、包索引或安装失败时返回非零；成功重试会正确清除旧失败码；可选 IPv6 失败仍允许 IPv4 降级 |
| Podman | 网络创建两次失败仍返回成功；最终验收不阻断缺失 runtime/network；nftables 不可用时未确认 iptables 回退 | 保留兼容创建回退，但两次失败会中止；验证 runtime、网络和可用防火墙回退；新建网络缺 aardvark 时明确禁用网络内 DNS |
| Containerd | CNI 禁用 ipMasq，却把 NAT 安装错误吞掉；重复初始化删除整个 IPv4 表；服务检查过浅 | NAT 必须成功；按规则补齐并保留既有 DNAT；检查 daemon API、CNI 网络和必需插件；配置写入失败向上传递 |
| QEMU | default.*active 同时匹配 inactive；pool/network start 失败被忽略；无主 nftables.conf 时规则不一定持久化 | 用活动资源名称精确匹配；检查 define/start/autostart 结果；创建主 nftables include 并验收默认池和网络确实 active |
| KubeVirt | local-path 缺失或默认类设置失败仍继续 | 检查面板和 onevm.sh 明确依赖的 local-path；检查其 provisioner；保留已有其他默认 StorageClass |
| 面板 CI | install_env 尾部网络修复覆盖安装失败码；只看 daemon 响应 | 保留首轮重启后重试，但最后一轮失败码必须返回；SSH 恢复失败停止；新增 profile/root/NIC/网桥验收 |
| 面板本地安装 | 过严的服务检查破坏 DRY_RUN、START_SERVICES=false 和 Homebrew；同时尝试启动两套 libvirt；仅启用 LXC 时缺依赖 | 恢复预演和仅安装依赖语义；保留活动的单体 libvirt，或按所选运行时启动模块及 socket；补齐 LXC 所需 libvirt 依赖；验证失败明确返回 |

## 本轮复查追加修复

1. **修复前轮引入的 Debian 安装回归。** Containerd/QEMU 的旧更新命令是 `! apt-get update && ...`。前轮直接把这个命令的非零返回设为致命错误，导致第一次更新成功时反而中止安装。现在先正常更新，失败才进入修复和重试；执行测试同时覆盖正常成功、修复成功、更新持续失败、修复失败及包安装失败。
2. **修复两份 panel_init 的真实控制流。** 原先 `query | jq` 在未启用 pipefail 时会吞掉 query 的失败码，缺 profile 时不一定执行创建。现在先查询 profile 名单，仅在确认缺失时创建，再分别检查查询状态和 JSON 内容。查询失败、空内容、错误响应不会触发设备修改。
3. **修复多池选择和 DNS 保留。** 存储存在性检查不再提前要求唯一池；已有 default profile 引用 local 时可与其他池共存。缺 root 时仍拒绝猜测未选定的多个池。设置 dns.mode 时不再覆盖已有 raw.dnsmasq。
4. **补齐系统差异与合理容错。** 主安装器和面板接入入口都检查 newuidmap、newgidmap 两个命令，并按发行版安装 uidmap/shadow 包。Fedora 不再尝试必需的 EPEL 安装；lsb_release 缺失可使用已有 os-release 路径。Incus 的可选 systemd DNS 服务在 OpenRC 上跳过。全系统 sysctl 因无关配置报错时，单独加载转发配置并验证生效值；真正无法启用转发仍失败。
5. **保护重复安装中的宿主机配置。** Incus 不再删除已有 root subuid/subgid 区间。其 nftables 规则先完整读取，再原子更新独立文件；主配置只补 include，读取失败保留上一次快照，重复保存不重复追加 include。

安装脚本位置：

- [Incus](/Volumes/Additional/个人数据/GitHub/incus/scripts/incus_install.sh)
- [LXD](/Volumes/Additional/个人数据/GitHub/lxd/scripts/lxdinstall.sh)
- [Docker](/Volumes/Additional/个人数据/GitHub/docker/scripts/dockerinstall.sh)
- [Podman](/Volumes/Additional/个人数据/GitHub/podman/podmaninstall.sh)
- [Containerd](/Volumes/Additional/个人数据/GitHub/containerd/containerdinstall.sh)
- [QEMU](/Volumes/Additional/个人数据/GitHub/qemu/qemuinstall.sh)
- [KubeVirt](/Volumes/Additional/个人数据/GitHub/kubevirt/kubevirtinstall.sh)
- [CI 安装入口](/Volumes/Additional/个人数据/GitHub/oneclickvirt/action_tests/common/node_manager.sh)
- [CI 只读初始化检查](/Volumes/Additional/个人数据/GitHub/oneclickvirt/action_tests/common/runtime_readiness.sh)

## 数据与兼容性边界

补齐初始化不会删除已有池、实例或自定义 NIC，也不会强制改写自定义地址、DNS 和已有 IPv6 禁用设置。自定义 NIC 的外部路由仍需由其配置者保证。显式 `ipv4.nat=false` 可能用于路由网段或外部 MASQUERADE，两种入口均保留并提示由外部配置保证连通性；配置验收不能证明此类网络实际出网。多个未选定存储池、同名非托管网桥、冲突设备或显式禁用默认 IPv4/DHCP 时，脚本报出原因并保留配置，不擅自接管。

可选 IPv6、镜像源、QEMU 无 KVM 的既有降级行为继续保留。KubeVirt 的两条创建路径目前明确使用 local-path，因此检查该类属于现有产品契约；本次没有把它替换成任意默认类。

Incus/LXD 的 panel_scripts/panel_init.sh 主要是已有运行时的接入配置入口，但节点可能处于“daemon 已安装、尚未完成 init”的半初始化状态。现在两条入口会先等待 daemon，发现 storage pool 为空时分别执行 `incus admin init --auto` 或 `lxd init --auto`，必要时创建安全的 dir pool，再修复 profile 和网桥；已有池、设备和显式网络设置仍保留。CI 上传本地安装入口后，入口下载的辅助脚本仍可能来自远端 main；只修改或同步面板仓库不会自动发布七个安装仓库的修改。

## 面板此前改动的收尾

核对上游 Instance / InstanceState / InstancePut 定义后，修正了 Incus/LXD API 端口配置路径：

- 运行时地址从 /1.0/instances/<name>/state 获取；普通 InstanceGet 不包含 state.network。
- PUT 设备配置保留原有 config、profiles、root、描述、架构和状态字段，并携带 ETag；停机后重新读取配置和 ETag，再按最新配置构造设备，避免停机修改 volatile.last_state.power 后旧 ETag 必然失效的路径。
- 更新被取消后的恢复启动使用独立且有超时的 context。
- 校验代理地址、范围和不同 map 类型的既有设备冲突。
- 删除实例或清理端口行时按完整端口区间回收 iptables/nftables 规则，避免只删除范围起始端口。

原来只调用设备构造器、却声称覆盖 API 初始化的测试已改成数据库加 HTTP 请求流程测试，断言真实路径、请求顺序、配置保留、ETag 和取消后的恢复。其他已存在的修复和回归测试保持保留。

## NAT proxy 与双栈端口的追加修复

对照 [Incus 6.0 LTS proxy 实现](https://github.com/lxc/incus/blob/stable-6.0/internal/server/device/proxy.go)、[Incus 6.0 LTS 文档](https://github.com/lxc/incus/blob/stable-6.0/doc/reference/devices_proxy.md)及 [LXD proxy 文档](https://github.com/canonical/lxd/blob/main/doc/reference/devices_proxy.md)，确认以下运行时要求：NAT proxy 的 listen 不能使用通配地址；目标必须对应实例 bridged/routed NIC 的静态地址；connect 使用 0.0.0.0 或 :: 则是有效的静态 NIC 自动选择功能。较新 Incus 的动态地址支持不能直接套用到 LTS/LXD。

这次继续修复了以下问题，其中双栈记录的误判和停机前 ETag 的复用也属于前序修复需要纠正的部分：

1. API 和 SSH proxy 路径按地址族选择具体宿主机监听地址；IPv4 PortIP 不再阻止 IPv6 发现，反之亦然。地址可从配置、节点本地接口或 API environment.addresses 获得；解析和发现有超时，没有有效地址时明确失败，不再生成 LTS 不支持的通配 listen。
2. API 创建 NAT proxy 前，依据实例实时网络状态与 MAC 固定对应 NIC 地址。VM 内 enp5s0 与 profile 设备名不同也能匹配；继承的 NIC 只在该实例覆盖，保留网络、MTU、限速和其他字段。地址冲突、MAC 不符或多 NIC 歧义不会被默认 eth0 覆盖。保留合法的 wildcard connect 自动选择。
3. SSH IPv4 绑定改为在停机前读取实际网卡，使用结构化查询替代截取几行 YAML 和猜测 eth0，检查写入是否生效。保留旧版 key/value 语法回退和原有可选配置容错。带宽配置仍先于地址覆盖，避免新建的本地 NIC 覆盖破坏原有 profile 操作顺序。
4. API 在停机后重新读取配置，保留停机期间的无冲突修改，并阻止覆盖本地及继承 profile 中的同名冲突设备。重新查询、提交或启动失败/取消时，用独立且有超时的 context 尝试恢复启动，并保留原错误。
5. LXD 的普通端口范围 proxy 路径补齐 nat=true，使其符合 VM 仅支持 NAT proxy 的约束。IPv4/IPv6 同端口使用不同设备名，并补齐对应的删除名称。
6. CreateDefaultPortMappings 在 nat_ipv4_ipv6 节点将同一自动 range_mapped 记录的 IPv6Enabled 置为 true，它表示双栈能力。新增统一的地址族展开逻辑，供 API 创建、端口修复和删除清理使用，防止误将 IPv4 映射替换成 IPv6。手动指定 IPv6 的记录仍保持单地址族；控制端转发保留自己的目标选择；原生 IPv6 不产生额外 NAT 配置，也不阻塞 IPv4 映射。双栈修复先清理旧规则，再创建两种地址族，防止后一个地址族删除刚创建的前一个。
7. 同时修复 LXD panel_scripts/modify.sh 仍使用通配 NAT listen 的遗漏。替换旧 proxy 前先确定宿主机 IPv4，并检查或按实际 MAC 固定实例 NIC；缺少地址、配置查询失败、网卡歧义或绑定失败会返回错误。新增九个模拟执行场景并接入 LXD CI。

回归测试不只检查构造字符串：API 用数据库中实际的自动双栈记录走完整 HTTP 配置路径，模拟停机修改 ETag、并发修改、profile 冲突和取消；SSH 绑定模拟查询、修改及回读；端口修复与删除通过实际 firewall manager 生成两种地址族的规则，检查清理与重建顺序。另追加了下述真实 Linux 防火墙测试；Incus/LXD daemon 与实例联网仍未完成真实节点验收。

## 防火墙清理、端口回收和重启恢复的追加修复

继续沿失败回滚排查，确认还有独立于安装初始化的错误：

1. 原先按注释删除只检查 IPv4 nft 表；IPv6 nft 删除只按协议和端口匹配，客户机都使用 SSH 22 端口时可能误删另一实例的 FORWARD 规则。现在使用结构化 nft JSON 与参数化解析的 xtables 规则清单，限定地址族、托管表、精确归属及目标地址；删除所有重复规则并复查结果。迁移后同时存在原生 nft 与兼容规则也会检查。单端口删除保留同一实例的其他宿主端口，以及仍被其他 DNAT 使用的旧共享放行规则。
2. Incus 的重试路径按注释清理时会删除刚建好的另一地址族映射。现在 Incus/LXD 创建重试、手动修复、删除和回滚均传递具体地址族；proxy 的单地址族清理也不再同时删除另一族设备。客户机 IP 尚未保存时可以按完整归属清理；相同端口存在无归属旧 DNAT 时明确拒绝猜测。
3. 读取、删除、复查和保存失败原先可能被当作成功。现在将这些防火墙错误向上传递，端口删除任务在远程失败时保留记录，避免释放后复用到旧规则；已有节点的临时连接失败与节点已不存在的孤立记录分别处理。孤立记录清理功能保留，并补了数据库执行回归；删除允许访问被冻结节点，延续原有删除操作契约。QEMU 与 KubeVirt 的防火墙清理失败也会阻止后续实例数据删除；QEMU 的旧式 xtables 新建规则补上实例归属。
4. SaveRules 原先按 IPv4 所选后端保存，遗漏原生 IPv6 nft 表及 nft 节点上实际使用的 ip6tables 规则，并可能先截断旧文件再读取失败。现在先取得完整快照，再逐文件原子替换，同时保存两个地址族和实际可用后端。只保存 xtables 时不因系统装有 nft 就启用无关的 nft 服务。已有发行版服务钩子仍保留，钩子失败记录警告；文件恢复测试不替代主机启动服务验收。
5. 实测还发现 nft 文本语法不接受任意 Go 转义字符串。单端口原生 nft 写入改为 JSON，保留注释中的引号、反斜杠及 shell 字符，并将该映射的多条规则放在同一事务中提交。

新增独立 CI job 调用 [firewall_integration_test.sh](/Volumes/Additional/个人数据/GitHub/oneclickvirt/scripts/tests/firewall_integration_test.sh)，不会取代原有远端环境矩阵或减少旧测试。它创建无挂载、独立网络命名空间、仅增加 NET_ADMIN 能力的 Debian 12 临时容器，执行真实 nftables/iptables 命令，结束后删除测试容器。覆盖双栈重试、混合后端、重复规则、缺失 IP、精确实例匹配、端口复用、共享服务、特殊字符、读取/写入故障以及规则保存后清空并两次恢复。此处的“清空”仅发生在这个专用测试容器中。

## 验证结果

| 检查 | 本轮结果 | 能证明的范围 |
|---|---|---|
| Shell 语法 | 逐文件检查通过：七安装仓库前序 142 个脚本，本次面板 77 个脚本 | 语法；已纠正此前一次把多个文件作为 bash -n 参数的无效验证方式 |
| ShellCheck | 七安装仓库全部 Shell 的 error 级检查通过；面板本次修改的安装与回归脚本通过 | 静态检查 |
| 安装仓库测试 | 累计 33 份 Shell 测试脚本通过；前序 32 份全部运行，本次新增并重跑 LXD 全部 9 份；Containerd smoke 含 Python 镜像归档测试 | 模拟命令、故障注入、临时文件和脚本控制流，不是真实容器/VM 验收 |
| Incus/LXD 接入回归 | 各 19 个场景通过；发行版、helper 与 sysctl 分支组合测试分别 73、71 个通过；Incus 宿主配置保留 8 个场景通过 | 缺失 profile、查询失败/空响应、已有多池、自定义 DNS、IPv6 降级、包名差异、转发检查和持久化边界 |
| Debian 更新回归 | Containerd 基础依赖、QEMU 基础依赖/运行时各 5 个成功及失败路径；Docker 公钥恢复 4 个路径通过 | 更新成功不会误失败，恢复成功清除旧失败码，持续失败仍终止 |
| 面板安装回归 | 初始化 13 个场景、本地安装模式 16 个场景及既有安装生命周期、升级回滚、action harness 测试通过 | 安装失败码、重启恢复、缺桥和空 profile、可选安装模式、libvirt 启动互斥 |
| Go | 本次追加修复后 go test ./...、go vet ./... 通过 | 源码测试与静态分析；不是只运行 5–7 个用例 |
| Go race | 最终执行 go test -race -count=1 -json ./...：全项目 1,225 个测试及子测试通过，0 skipped，0 failed，无 DATA RACE 报告 | 全部现有 Go 测试包，含共享 WebSocket、session 隔离、数据库端口保留与防火墙故障回归 |
| 真实 Linux 防火墙 | Debian 12、nftables 1.0.6、iptables 1.8.9；iptables-nft 和 iptables-legacy 两种模式各 18 个测试及子测试通过，共 36 个，0 skipped、0 failed | 真实规则写入、查询、删除、文件保存/恢复和 Incus/LXD 调用路径；不包含真实 Incus/LXD daemon、虚拟机、SSH 连通或主机重启 |
| Rust | 本次任务前序 cargo fmt --check、cargo test 通过，49 项通过、0 ignored；此次追加修复未改 Rust | 本地 Rust 测试；未把历史 strict clippy lint 声称为已清零 |
| 前端 | 当前 `npm run test:unit` 75/75 项测试通过、0 skipped；生产构建通过 | 本地组件逻辑与构建；有 bundle 体积提示 |
| 面板严格静态审计 | 高风险发现 0，近似路由字面覆盖 84.48%，超过 82% 门槛 | 代码文字覆盖，不是线上请求覆盖率 |
| Swagger | 重新生成成功，生成文件无差异 | 文档已与当前声明一致；存在依赖常量解析警告 |
| 真实节点 | 本轮再次连接指定测试节点，SSH 22 端口仍在认证前超时 | 未完成真实 Linux 节点安装、重启和实例联网验收 |

新增安装器回归已接入各仓库的 CI，面板回归已加入 integration-tests 工作流，没有删除或跳过已有测试。LXD 现在会在干净 Debian 上先安装 snapd，再检查 LXD snap；QEMU 在没有主 nftables 配置文件时会创建 include。macOS 默认 Bash 下 Containerd 子脚本测试首次失败，改用 Homebrew Bash 5 并传递 PATH 后，原测试与 smoke 均通过；未通过修改断言来掩盖失败。

追加测试初次运行也暴露了测试夹具问题：SQLite 的索引命名范围与 MySQL 不同，进度日志需要 MySQL 字符串函数，任务完成需要初始化状态管理器；KubeVirt 容器识别实际查询 Deployment，原模拟写成 Pod。已修正夹具和服务初始化，保留实际删除流程及断言，最终全项目 race 通过，未跳过失败用例。

严格静态审计对 jq 的判断原来只认 stderr 丢弃或忽略错误，没有识别明确的非零 return 和 if 分支。已补齐判断与回归；同时补上初始化验收中缺少的解析错误检查，保留诊断输出。

## 交付与尚未完成的验收

### 2026-09-18 跨脚本仓库复验补充

在 macOS 工作站重新执行了 LXD/Incus 两个仓库的全部 42 个本地 Shell 回归：36 个通过，6 个明确标记为环境门控（macOS 没有 Linux `flock` 或网络 namespace 权限），没有源码失败。锁测试和 Linux namespace 测试不再以无诊断的退出码 1 结束；现在会输出 `SKIP` 并返回 75，调用方可以区分环境不可用与断言失败。随后在 Debian 12 容器中实际运行 LXD/Incus 的四个锁测试，全部通过；在带隔离网络 namespace 的特权 Debian 12 容器中，LXD/Incus 两套真实 nftables、iptables-nft、iptables-legacy、NAT 与 routed IPv6 测试也全部通过。其他 Docker、Podman、Containerd、QEMU、KubeVirt、PVE 仓库的 26 个本地回归也全部通过。所有涉及脚本均重新做 Bash 语法检查和 diff 检查。

本次修改保存在面板和七个安装脚本仓库，尚未提交或推送。面板已同步到公开镜像工作树，并逐文件核对修改文件、新文件和三份 Swagger 输出；该同步不负责同级安装仓库的发布。本地安装脚本修改需要发布后才能被在线下载入口使用。

没有在用户已恢复使用的节点执行重装、卸载、清空防火墙或删除实例。真实验收仍需要可达的专用测试节点：完成安装和重启后，分别创建容器与 VM，检查 DHCP、出网、SSH 端口、删除重建后的映射回收、容器嵌套 Docker，再用两个会话测试 WebSSH 与普通命令并发。

“网页不能编辑密码、端口范围”属于面板表单/API 的能力与权限问题，环境初始化修复不能使这些输入项自动出现。本记录没有把该问题归为安装故障，也没有声称已实现相应编辑功能。
