# 集成测试框架

本目录包含 OneClickVirt 平台的自动化集成测试框架，基于双节点架构，覆盖全部 API 接口的功能测试、权限测试、边界测试和安全测试。

## 在线查看

测试报告地址: [oneclickvirt.github.io/oneclickvirt](https://oneclickvirt.github.io/oneclickvirt/)

报告支持中英双语切换、亮色/暗色主题切换，标题下方显示当前测试对应的主控版本和 Agent 版本信息。

## 架构设计

测试采用双节点架构，单线程顺序执行（不使用矩阵并发），每次仅测试一个平台：

| 节点 | 用途 | 说明 |
|------|------|------|
| Master 节点 | 运行 OneClickVirt 主控服务 | 通过 Docker 容器部署完整的主控服务 |
| Worker 节点 | 运行虚拟化环境 | 安装对应的虚拟化平台，作为被纳管节点 |

两台节点均通过 AliceInit (Ephemera) API 自动创建和销毁，测试完成后自动清理资源。最多同时运行 2 个实例。

## 目录结构

```
action_tests/
  run_env_test.sh          # 主入口：环境集成测试编排器
  run_module.sh            # 模块运行器：支持选择性运行
  README.md                # 本文件
  common/
    test_framework.sh      # 测试框架核心（日志、断言、报告、状态管理、日志捕获）
    aliceinit_api.sh       # AliceInit 云平台 API 封装
    node_manager.sh        # 节点生命周期管理（创建、部署、清理）
  modules/
    01_init.sh             # 系统初始化与健康检查
    02_auth.sh             # 认证系统（登录、注册、验证码、密码管理）
    03_users.sh            # 用户管理（CRUD、批量操作、权限、登录身份切换）
    04_invite_codes.sh     # 邀请码管理（生成、批量、导出、注册验证）
    05_redemption.sh       # 兑换码管理（批量创建、兑换、状态验证）
    06_announcements.sh    # 公告管理（CRUD、批量状态切换、类型筛选）
    07_system_config.sh    # 系统配置（统一配置、等级限制、分组信息、反向测试）
    08_system_images.sh    # 系统镜像管理（CRUD、批量删除、类型筛选）
    09_providers.sh        # 节点管理（SSH 密码/密钥认证、创建、配置、健康检查、硬件报告、端口配置、IPv4 池、流量历史、反向测试）
    10_instances.sh        # 实例生命周期（创建、操作、重建、密码重置、异步任务、转移、删除）
    11_monitoring.sh       # 监控配置与代理部署（部署、卸载、同步、反向测试）
    12_traffic.sh          # 流量管理（统计、限制、同步、清理、排行、反向测试）
    13_port_mappings.sh    # 端口映射管理（CRUD、端口可用性检查、同步）
    14_block_rules.sh      # 防火墙阻断规则（CRUD、应用、移除、IPv4/IPv6）
    15_domains.sh          # 域名绑定管理（CRUD、多用户隔离）
    16_freeze.sh           # 冻结管理（节点与实例的过期、手动冻结、级联冻结/解冻）
    17_admin_isolation.sh  # 管理员隔离（普通管理员权限边界验证）
    18_user_features.sh    # 用户侧功能（资料、仪表盘、实例列表、流量、密码重置）
    19_speedtest.sh        # 速度测试与流量监控验证
    20_oauth2.sh           # OAuth2 第三方登录管理（预设/自定义提供者、CRUD）
    21_kyc.sh              # 实名认证（提交、审核、拒绝、支付宝接口、反向测试）
    22_checkin.sh          # 签到系统（配置、签到码生成、签到、记录查询）
    23_discovery.sh        # 实例发现与纳管（非纯净节点、孤儿实例检测、导入）
    24_data_isolation.sh   # 多用户数据隔离验证
    25_error_handling.sh   # 错误处理与边界测试（注入、越界、畸形请求、路径遍历）
    26_instance_types.sh   # 实例类型测试（容器与虚拟机权限分离测试）
    27_config_advanced.sh  # 高级配置与任务管理（导出、自动配置、硬件报告、版本信息）
  report/
    generate_report.sh     # HTML 可视化报告生成器（中英双语、亮暗主题、历史对比）
  reports/                 # 测试报告输出目录（运行时生成）
```

## 支持的虚拟化环境

| 环境标识 | 平台 | 支持容器 | 支持虚拟机 | 自动纠正行为 |
|---------|------|---------|-----------|------------|
| `docker` | Docker | 是 | 否 | `both`/`vm` 自动纠正为 `container` |
| `lxd` | LXD | 是 | 是 | 无需纠正 |
| `incus` | Incus | 是 | 是 | 无需纠正 |
| `podman` | Podman | 是 | 否 | `both`/`vm` 自动纠正为 `container` |
| `containerd` | Containerd | 是 | 否 | `both`/`vm` 自动纠正为 `container` |
| `proxmoxve` | Proxmox VE | 是 | 是 | 无需纠正 |

实例类型自动纠正：测试框架会根据平台能力自动纠正 `instance_types` 参数。例如选择 `docker` 平台并指定 `both`，框架会自动纠正为 `container`。纠正逻辑同时在 GitHub Actions 工作流和测试脚本中双重验证。

QEMU 和 KubeVirt 暂不纳入自动化测试。

## 核心特性

### 顺序执行

所有测试模块按编号顺序执行，不使用矩阵并发。每次运行仅测试一个虚拟化平台，避免资源竞争和状态干扰。

### 模块间状态管理

每个测试模块执行前会保存系统基准状态（系统配置、实例列表、Provider ID、测试实例 ID），模块执行后自动恢复：
- 删除测试过程中新增的实例
- 重新登录所有测试用户，刷新 Token
- 关键状态变量（`PROVIDER_ID`、`TEST_INSTANCE_ID`）在模块间正确传递
- 防止上一模块的副作用影响下一模块

### 错误日志捕获

当测试用例失败时，框架自动从 Master 节点的 OneClickVirt 服务容器中捕获时间相关的日志：
- 使用 `--since` 参数按测试开始时间过滤日志
- 日志记录在 JSON Lines 结果文件中，与失败用例关联
- 支持在 HTML 报告中展开查看每个失败用例的服务端日志
- 模块失败时自动保存完整模块日志到独立文件

### EXIT Trap 兜底

`run_env_test.sh` 注册了 EXIT trap，即使脚本异常退出也会：
- 尝试生成 HTML 报告
- 捕获 OneClickVirt 服务最后的日志
- 保存崩溃诊断信息到 `reports/crash-*.log`

## 使用方式

### 通过 GitHub Actions 运行

在仓库的 Actions 页面手动触发 `Integration Tests` 工作流，提供以下参数：

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `environment` | 虚拟化环境类型（单选，每次一个平台） | `docker` |
| `instance_types` | 测试的实例类型（会根据平台自动纠正） | `container` |
| `modules` | 运行的模块（`all`/`01-10`/`01,03,05`） | `all` |
| `node_hours` | 节点存续时间（小时） | `8` |

### 本地运行

从项目根目录执行：

```bash
# 完整环境测试
export ALICE_CLIENT_ID="your_client_id"
export ALICE_CLIENT_SECRET="your_client_secret"
export ALICE_API_BASE=""
bash action_tests/run_env_test.sh docker all container

# 仅运行部分模块（需要已启动的服务）
export SERVER_URL="http://127.0.0.1:8888"
export ADMIN_USER="admin"
export ADMIN_PASS="Admin123!@#"
bash action_tests/run_module.sh 01-05

# 运行单个模块
bash action_tests/run_module.sh 23

# 运行指定模块组合
bash action_tests/run_module.sh 01,03,09,23

# 启用调试日志
DEBUG=1 bash action_tests/run_env_test.sh docker all container
```

## 测试报告

测试完成后生成以下报告：

| 格式 | 文件 | 说明 |
|------|------|------|
| HTML | `reports/<env>-report.html` | 可视化报告，中英双语、亮暗主题、最近三次历史对比 |
| Markdown | `reports/<env>-report.md` | 文本格式报告，包含每个测试用例的状态 |
| JSON Lines | `reports/<env>-results.jsonl` | 机器可读的结构化测试结果 |
| 日志 | `reports/full-output.log` | 完整控制台输出日志 |
| 错误日志 | `reports/<env>-error-*.log` | 模块级的服务端错误日志 |

### HTML 报告功能

- 中英双语：支持中文和英文界面切换（快捷键 `L`）
- 版本信息：标题下方显示当前测试对应的主控版本和 Agent 版本
- 搜索：支持全文搜索测试名称、URL、详情（快捷键 `/`）
- 状态筛选：按通过/失败/跳过筛选（快捷键 `1`-`4`）
- 模块分组：按模块分组显示，失败模块自动展开
- 错误日志：失败用例支持展开查看关联的服务端日志
- 一键复制：复制测试摘要到剪贴板
- 亮暗主题：支持亮色和暗色主题切换（快捷键 `T`），根据系统偏好自动选择
- 历史对比：保留最近三次测试结果，支持通过率对比

HTML 报告会通过 GitHub Actions 自动推送到 `gh-pages` 分支，可通过 GitHub Pages 在线查看。

## 测试覆盖范围

### 功能测试（正向）

- 全部 200+ API 接口的正向功能测试
- 异步任务（实例创建、配置下发）的等待与状态验证
- 容器和虚拟机实例的完整生命周期操作
- SSH 密码认证和密钥认证两种方式的节点录入

### 反向测试（负向）

- 缺失必填字段的请求（返回 400）
- 不存在的资源操作（返回 404）
- 无权限访问（返回 401/403）
- 无效参数（端口越界、非法 URL、空数组等）
- 硬件报告 URL 域名白名单验证

### 权限测试

- 三级权限隔离：超级管理员、普通管理员、普通用户
- 普通管理员不可访问超级管理员专属接口（返回 403）
- 普通用户不可访问管理员接口（返回 401/403）
- 多用户间的数据隔离验证

### 非纯净节点纳管测试

测试对已有容器或实例的节点进行发现和纳管：

1. 在 Worker 节点上预先创建容器或实例
2. 通过 OneClickVirt 主控进行实例发现（discover）
3. 导入（import）发现的实例
4. 验证导入后实例的可管理性

### 错误处理与安全测试

- SQL 注入尝试
- XSS 注入尝试
- 超长字段提交
- 畸形 JSON 请求
- 负数和零值 ID
- 非数字 ID
- 分页边界值
- Content-Type 校验
- 路径遍历检测

## 环境要求

运行测试需要以下条件：

- AliceInit API Token（用于创建测试节点）
- `curl`、`jq`、`sshpass` 命令行工具
- 网络能够访问 AliceInit API 和测试节点

GitHub Actions 会自动安装所需依赖。

## 配置说明

### 密钥配置

在仓库 Settings > Secrets and variables > Actions 中配置：

| 密钥名称 | 说明 | 必需 |
|---------|------|------|
| `ALICE_CLIENT_ID` | AliceInit API Client ID（Bearer 前半部分） | 是 |
| `ALICE_CLIENT_SECRET` | AliceInit API Client Secret（Bearer 后半部分） | 是 |
| `ALICE_API_BASE` | AliceInit API 基础 URL | 是 |
| `TEST_ADMIN_PASS` | 测试管理员密码（默认 `Admin123!@#`） | 否 |

> Bearer token 由脚本自动拼接为 `ALICE_CLIENT_ID:ALICE_CLIENT_SECRET`，与 API 文档一致。

### 报告发布

HTML 报告通过 GitHub Actions 自动推送到本仓库的 `gh-pages` 分支：

1. 在仓库 Settings > Pages 中启用 GitHub Pages，源选择 `gh-pages` 分支
2. 默认使用 `GITHUB_TOKEN` 推送，无需额外配置
3. 报告按平台和时间戳组织：`reports/<env>/<timestamp>/`
