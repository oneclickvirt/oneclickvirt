# MySQL / MariaDB compatibility verification

本报告记录源码仓库内可复现的实际验证结果。测试使用独立临时 Docker 容器和临时数据卷，不连接或清理用户数据库。

## 已执行矩阵

| 引擎 | 版本 | 连接与错配参数 | 初始化/认证 | 持久卷重启 | 结果 |
|---|---|---|---|---|---|
| MySQL | 8.0.46 | 9 个配置场景、8 个负例 | 通过 | 通过 | PASS |
| MySQL | 8.4.11 | 9 个配置场景、8 个负例 | 通过 | 通过 | PASS |
| MySQL | 9.7.2 | 9 个配置场景、8 个负例 | 通过 | 通过 | PASS |
| MariaDB | 10.11.18 | 9 个配置场景、8 个负例 | 通过 | 通过 | PASS |
| MariaDB | 11.4.13 | 9 个配置场景、8 个负例 | 通过 | 通过 | PASS |

每个版本还执行了内置入口的真实数据目录初始化、错误密码拒绝、原始配置文件哈希保留、应用用户认证、重启后 sentinel 数据读取，以及裸机安装器的服务端检测和账户配置。共享 `deploy/my.cnf` 也通过真实服务端变量核对：MariaDB 的 `innodb_log_file_size` 和 MySQL 的 `innodb_redo_log_capacity` 都实际达到 256M。MariaDB 镜像没有 `mysql` 客户端的情况已覆盖，入口会选择 `mariadb` 客户端。

## 覆盖的安全边界

- 连接类型来自 `SELECT VERSION()`；错误的 `mysql`/`mariadb` 标签不会切换数据目录或 SQL 方言。
- 已知事务变量别名和默认 UTF8MB4 排序规则按服务端能力适配；未知参数、冲突参数、错误密码、无效 TLS 和不存在的排序规则明确失败。
- 已有数据但缺系统表、或检测到另一引擎的数据标记时停止，不删除数据、不自动跨引擎接管。
- 密码不插入手拼 DSN；安装配置文件使用 YAML 安全转义和 0600 权限，连接池、超时和 TLS 参数不会被初始化表单静默覆盖。
- 裸机检测优先执行带认证的 `SELECT VERSION()`；同时安装两个引擎但服务端无法认证探测时会拒绝猜测，避免把配置和凭据写入错误的数据目录。

## 可重跑命令

```sh
OCV_TEST_DB_IMAGES='mysql:8.0 mysql:8.4 mysql:9 mariadb:10.11 mariadb:11.4' \
  bash scripts/tests/embedded_database_integration_test.sh
bash scripts/tests/database_compat_integration_test.sh
```

缺 Docker、镜像启动失败或任一断言失败都会使脚本返回非零；显式功能 `SKIP` 只用于不适用能力，不会掩盖未执行模块。
