# 集成测试框架

本目录包含 OneClickVirt 平台的自动化集成测试框架，基于双节点架构，覆盖全部 API 接口的功能测试、权限测试和边界测试。

## 架构设计

测试采用双节点架构：

| 节点 | 用途 | 说明 |
|------|------|------|
| Master 节点 | 运行 OneClickVirt 主控服务 | 通过 Docker 容器部署完整的主控服务 |
| Worker 节点 | 运行虚拟化环境 | 安装对应的虚拟化平台，作为被纳管节点 |

两台节点均通过 AliceInit (Ephemera) API 自动创建和销毁，测试完成后自动清理资源。

## 目录结构

```
action_tests/
  run_env_test.sh          # 主入口：环境集成测试编排器
  run_module.sh            # 模块运行器：支持选择性运行
  README.md                # 本文件
  common/
    test_framework.sh      # 测试框架核心（日志、断言、报告生成）
    aliceinit_api.sh       # AliceInit 云平台 API 封装
    node_manager.sh        # 节点生命周期管理（创建、部署、清理）
  modules/
    01_init.sh             # 系统初始化与健康检查
    02_auth.sh             # 认证系统（登录、注册、密码管理）
    03_users.sh            # 用户管理（CRUD、批量操作、权限）
    04_invite_codes.sh     # 邀请码管理
    05_redemption.sh       # 兑换码管理
    06_announcements.sh    # 公告管理
    07_system_config.sh    # 系统配置（统一配置、等级限制）
    08_system_images.sh    # 系统镜像管理
    09_providers.sh        # 节点管理（SSH、创建、配置、健康检查）
    10_instances.sh        # 实例生命周期（创建、操作、删除、异步任务）
    11_monitoring.sh       # 监控配置与代理部署
    12_traffic.sh          # 流量管理（统计、限制、同步、清理）
    13_port_mappings.sh    # 端口映射管理
    14_block_rules.sh      # 防火墙阻断规则
    15_domains.sh          # 域名绑定管理
    16_freeze.sh           # 冻结管理（节点与实例的过期和冻结）
    17_admin_isolation.sh  # 管理员隔离（普通管理员 vs 超级管理员）
    18_user_features.sh    # 用户侧功能（资料、仪表盘、实例、流量）
    19_speedtest.sh        # 速度测试与流量监控验证
    20_oauth2.sh           # OAuth2 第三方登录管理
    21_kyc.sh              # 实名认证（提交、审核）
    22_checkin.sh          # 签到系统（签到码、签到、记录）
    23_discovery.sh        # 实例发现与纳管（非纯净节点测试）
    24_data_isolation.sh   # 多用户数据隔离验证
    25_error_handling.sh   # 错误处理与边界测试（注入、越界、畸形请求）
    26_instance_types.sh   # 实例类型测试（容器与虚拟机分别测试）
    27_config_advanced.sh  # 高级配置与任务管理
  reports/                 # 测试报告输出目录
```

## 支持的虚拟化环境

| 环境标识 | 平台 | 支持容器 | 支持虚拟机 |
|---------|------|---------|-----------|
| `docker` | Docker | 是 | 否 |
| `lxd` | LXD | 是 | 是 |
| `incus` | Incus | 是 | 是 |
| `podman` | Podman | 是 | 否 |
| `containerd` | Containerd | 是 | 否 |
| `proxmoxve` | Proxmox VE | 是 | 是 |

QEMU 和 KubeVirt 暂不纳入自动化测试。

## 使用方式

### 通过 GitHub Actions 运行

在仓库的 Actions 页面手动触发 `Integration Tests` 工作流，提供以下参数：

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `environment` | 虚拟化环境类型 | `docker` |
| `instance_types` | 测试的实例类型（`container`/`vm`/`both`） | `both` |
| `modules` | 运行的模块（`all`/`01-10`/`01,03,05`） | `all` |
| `node_hours` | 节点存续时间（小时） | `8` |
| `report_repo` | 报告发布仓库（`owner/repo` 格式） | 空 |

### 本地运行

从项目根目录执行：

```bash
# 完整环境测试
export ALICEINIT_TOKEN="your_token"
bash action_tests/run_env_test.sh docker all both

# 仅运行部分模块（需要已启动的服务）
export SERVER_URL="http://127.0.0.1:8888"
export ADMIN_USER="admin"
export ADMIN_PASS="Admin123!@#"
bash action_tests/run_module.sh 01-05

# 运行单个模块
bash action_tests/run_module.sh 23

# 运行指定模块组合
bash action_tests/run_module.sh 01,03,09,23
```

## 测试报告

测试完成后生成以下报告：

| 格式 | 文件 | 说明 |
|------|------|------|
| Markdown | `reports/<env>-report.md` | 文本格式报告，包含每个测试用例的状态 |
| HTML | `reports/<env>-report.html` | 可视化报告，支持按状态筛选、展开失败详情 |
| 日志 | `reports/<env>-output.log` | 完整控制台输出日志 |

如果配置了 `report_repo` 参数，HTML 报告会自动推送到指定仓库的 `gh-pages` 分支，可通过 GitHub Pages 在线查看。

## 测试覆盖范围

### 功能测试

- 全部 200+ API 接口的正向和反向测试
- 异步任务（实例创建、配置）的等待与状态验证
- 容器和虚拟机实例的完整生命周期操作

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
| `ALICEINIT_TOKEN` | AliceInit 云平台 API Token | 是 |
| `TEST_ADMIN_PASS` | 测试管理员密码（默认 `Admin123!@#`） | 否 |
| `PAGES_DEPLOY_TOKEN` | 报告仓库的 Personal Access Token | 否 |

### 报告发布

若需自动发布报告到 GitHub Pages：

1. 创建单独的报告仓库
2. 在报告仓库中启用 GitHub Pages（源选择 `gh-pages` 分支）
3. 创建具有该仓库写入权限的 Personal Access Token
4. 将 Token 配置为本仓库的 `PAGES_DEPLOY_TOKEN` Secret
5. 运行测试时在 `report_repo` 参数中填写 `owner/repo`
