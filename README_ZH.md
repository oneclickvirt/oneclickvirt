# OneClickVirt 虚拟化管理平台

[![Build and Release oneclickvirt](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/build.yml/badge.svg)](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/build.yml) 

[![Build and Push Docker Images](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/build_docker.yml/badge.svg)](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/build_docker.yml)

[![Integration Tests](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/integration-tests.yml/badge.svg)](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/integration-tests.yml)

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt.svg?type=shield&issueType=license)](https://app.fossa.com/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt?ref=badge_shield&issueType=license) [![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt.svg?type=shield&issueType=security)](https://app.fossa.com/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt?ref=badge_shield&issueType=security)

一个可扩展的通用虚拟化管理平台，支持 LXD、Incus、Docker、Podman、Containerd、Proxmox VE、QEMU/KVM 和 KubeVirt。

前端控制台基于 Vue 3、Vite 和 Element Plus 构建，已针对桌面、平板、安卓尺寸和 iOS 尺寸视口进行响应式布局检查。

## **语言**

[English Docs](README.md) | [中文文档](README_ZH.md)

## 详细说明

[www.spiritlhl.net](https://www.spiritlhl.net/guide/oneclickvirt/oneclickvirt_precheck.html)

## 集成测试报告

自动化集成测试报告地址: [oneclickvirt.github.io/oneclickvirt](https://oneclickvirt.github.io/oneclickvirt/)

报告支持中英双语显示、亮色/暗色主题切换、Git ref/SHA/run 元数据和失败用例服务端日志展开，覆盖 200+ API 接口的功能测试、权限测试、边界测试和安全测试。详见 [`action_tests/`](action_tests/) 目录。

## 支持的虚拟化平台

| 类型标识 | 平台 | 实例类型 | 仓库地址 |
|---------|------|---------|---------|
| `lxd` | LXD | container, vm | [oneclickvirt/lxd](https://github.com/oneclickvirt/lxd) |
| `incus` | Incus | container, vm | [oneclickvirt/incus](https://github.com/oneclickvirt/incus) |
| `docker` | Docker | container | [oneclickvirt/docker](https://github.com/oneclickvirt/docker) |
| `podman` | Podman | container | [oneclickvirt/podman](https://github.com/oneclickvirt/podman) |
| `containerd` | Containerd (nerdctl) | container | [oneclickvirt/containerd](https://github.com/oneclickvirt/containerd) |
| `proxmox` | Proxmox VE | container, vm | [oneclickvirt/pve](https://github.com/oneclickvirt/pve) |
| `qemu` | QEMU | vm | [oneclickvirt/qemu](https://github.com/oneclickvirt/qemu) |
| `kubevirt` | KubeVirt | vm | [oneclickvirt/kubevirt](https://github.com/oneclickvirt/kubevirt) |

后端还包含面向本地或桌面虚拟化实验场景的适配器，例如 `orbstack`、`multipass`、`vagrant`、`virtualbox` 和 `vmware`。实现细节和支持范围见 [`server/provider/README.md`](server/provider/README.md)。

## 快速部署

尽量不要自行编译，推荐使用二进制文件分离部署或直接docker拉取镜像部署

### 方式零：使用 1Panel 第三方应用商店

[okxlin/appstore](https://github.com/okxlin/appstore) 已收录 OneClickVirt。已安装 1Panel 的用户，可以按该仓库说明添加或同步本地应用商店，然后在本地应用列表中选择 `oneclickvirt` 部署。

### 方式一：使用预构建镜像

使用已构建好的多架构镜像，会自动根据当前系统架构下载对应版本。

**镜像标签说明：**

| 镜像标签 | 说明 | 适用场景 |
|---------|------|---------|
| `oneclickvirt/oneclickvirt:latest` | 一体化版本（内置数据库）最新版 | 快速部署 |
| `oneclickvirt/oneclickvirt:20260830` | 一体化版本特定日期版本 | 需要固定版本 |
| `oneclickvirt/oneclickvirt:no-db` | 独立数据库版本最新版 | 不内置数据库 |
| `oneclickvirt/oneclickvirt:no-db-20260830` | 独立数据库版本特定日期 | 不内置数据库 |

所有镜像均支持 `linux/amd64` 和 `linux/arm64` 架构。

<details>
<summary>展开查看一体化版本（内置数据库）</summary>

**基础使用（不配置域名）：**

```bash
docker run -d \
  --name oneclickvirt \
  -p 80:80 \
  -v oneclickvirt-data:/var/lib/mysql \
  -v oneclickvirt-storage:/app/storage \
  --restart unless-stopped \
  oneclickvirt/oneclickvirt:latest
```

**配置域名访问：**

如果你需要配置域名，需要设置 `FRONTEND_URL` 环境变量：

```bash
docker run -d \
  --name oneclickvirt \
  -p 80:80 \
  -e FRONTEND_URL="https://your-domain.com" \
  -v oneclickvirt-data:/var/lib/mysql \
  -v oneclickvirt-storage:/app/storage \
  --restart unless-stopped \
  oneclickvirt/oneclickvirt:latest
```

或者使用 GitHub Container Registry：

```bash
docker run -d \
  --name oneclickvirt \
  -p 80:80 \
  -e FRONTEND_URL="https://your-domain.com" \
  -v oneclickvirt-data:/var/lib/mysql \
  -v oneclickvirt-storage:/app/storage \
  --restart unless-stopped \
  ghcr.io/oneclickvirt/oneclickvirt:latest
```

</details>

<details>
<summary>展开查看独立数据库版本</summary>

使用外部数据库，镜像更小，启动更快：

```bash
docker run -d \
  --name oneclickvirt \
  -p 80:80 \
  -e FRONTEND_URL="https://your-domain.com" \
  -e DB_HOST="your-mysql-host" \
  -e DB_PORT="3306" \
  -e DB_NAME="oneclickvirt" \
  -e DB_USER="root" \
  -e DB_PASSWORD="your-password" \
  -v oneclickvirt-storage:/app/storage \
  --restart unless-stopped \
  oneclickvirt/oneclickvirt:no-db
```

**环境变量说明：**
- `FRONTEND_URL`: 前端访问地址（必填，支持 http/https）
- `DB_HOST`: 数据库主机地址
- `DB_PORT`: 数据库端口（默认 3306）
- `DB_NAME`: 数据库名称
- `DB_USER`: 数据库用户名
- `DB_PASSWORD`: 数据库密码

`no-db` 镜像会将运行时配置保存到 `oneclickvirt-storage` 卷内的 `/app/storage/config.yaml`。更新镜像或重建容器时必须继续挂载同一个存储卷；初始化页面写入的数据库配置和系统级配置会随该卷保留。非空的 `DB_*` 环境变量优先于配置文件，因此重建时也可继续传入同一组数据库环境变量。显式挂载 `/app/config.yaml` 的部署仍会优先使用该文件。

</details>

> **说明**：`FRONTEND_URL` 用于配置前端访问地址，影响 CORS、OAuth2 回调等功能。系统会自动检测 HTTP/HTTPS 协议并调整相应配置，协议头可以是http或https。

### 方式二：使用 Docker Compose

<details>
<summary>展开查看 Docker Compose 部署</summary>

使用 Docker Compose 可以一键部署完整的开发环境，采用**分容器部署**架构，包括独立的前端容器、后端容器和数据库容器：

```bash
git clone https://github.com/oneclickvirt/oneclickvirt.git
cd oneclickvirt
cat > .env << 'EOF'
MYSQL_ROOT_PASSWORD=change-this-root-password
MYSQL_PASSWORD=change-this-app-password
EOF
docker-compose up -d --build || docker compose up -d --build
```

**默认配置说明：**

- 前端服务：`http://localhost:8888`
- 后端 API：通过前端代理访问
- MariaDB 数据库：端口 3306，数据库名 `oneclickvirt`
- 数据库凭据：来自 `.env` 的 `MYSQL_ROOT_PASSWORD` 和 `MYSQL_PASSWORD`
- 数据持久化：
  - 数据库数据：Docker volume `mysql_data`
  - 应用存储：`./data/app/`

**初始化配置：**

首次访问时会进入初始化界面，数据库配置请填写：
- 数据库地址：`mysql`（容器名称，不是 127.0.0.1）
- 数据库端口：`3306`
- 数据库名称：`oneclickvirt`
- 数据库用户：`oneclickvirt`
- 数据库密码：使用 `.env` 中的 `MYSQL_PASSWORD`

**自定义端口（可选）：**

如果需要修改前端访问端口，编辑 `docker-compose.yaml` 文件中的 ports 配置：

```yaml
services:
  web:
    ports:
      - "你的端口:80"  # 例如 "80:80" 或 "8080:80"
```

**停止服务：**

```bash
docker-compose down
```

**查看日志：**

```bash
docker-compose logs -f
```

**清理数据：**

```bash
docker-compose down
rm -rf ./data
```

</details>

### 方式三：裸机全依赖安装

<details>
<summary>展开查看全量安装脚本</summary>

`scripts/install_full.sh` 会在一个流程中安装数据库、反向代理、TLS 配置、前端、后端和系统服务，支持 MySQL 兼容本地数据库（MySQL 或 MariaDB）以及 Caddy/Nginx/OpenResty。

安装器会自动识别常见 Linux 与类 Unix 目标，包括 Debian/Ubuntu、RHEL/CentOS/Rocky/Alma/Fedora/Amazon Linux、openSUSE/SLES、Arch/Manjaro、Alpine 和 BSD 包管理器；同时识别 systemd、OpenRC、rc.d/service 和无 init 环境。在原生 MySQL 包不可用或不稳定的发行版上，安装器会自动回退到 MariaDB 作为 MySQL 兼容后端；如需禁用该行为可使用 `--no-db-fallback`。BSD 安装需要存在对应 OS/架构的 release 二进制，否则请使用 Docker/Linux 或从源码构建服务端。

域名输入会自动识别协议前缀：输入 `https://panel.example.com` 自动启用 TLS，输入 `http://panel.example.com` 自动关闭 TLS，无前缀则交互询问。

```bash
curl -fsSL https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/main/scripts/install_full.sh -o install_full.sh
bash install_full.sh
```

非交互部署示例：

```bash
# HTTPS 自动启用 TLS
bash install_full.sh \
  --non-interactive \
  --domain https://panel.example.com \
  --email admin@example.com \
  --db-type mariadb \
  --proxy caddy

# HTTP 纯端口模式，不启用 TLS
bash install_full.sh \
  --non-interactive \
  --domain http://192.168.1.100 \
  --proxy caddy
```

常用自动化参数：

```bash
bash install_full.sh --version v1.2.3 --db-wait-timeout 300
bash install_full.sh --db-type mysql --no-db-fallback
```

安装脚本默认要求至少 10 GB 可用磁盘和 2 GB 内存（内存与 Swap 合计）。生成的数据库密码会在安装摘要中输出，请在关闭终端前保存。

安装完成后，可下载通用安装脚本管理 systemd 服务方式部署的 OneClickVirt：

```bash
curl -fsSL https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/main/scripts/install.sh -o install.sh
chmod +x install.sh

./install.sh status
./install.sh logs --lines 200
./install.sh logs --follow
./install.sh upgrade
./install.sh uninstall
```

`uninstall` 默认删除服务、程序和 Web 文件，但保留 `config.yaml` 与 `storage`；`uninstall --purge` 会删除整个应用目录。两种卸载方式都不会删除可能与其他应用共用的数据库、反向代理或 TLS 证书。无交互卸载必须额外指定 `--yes`。

</details>

### 主控面板版本管理

登录超级管理员后，可在主控页面页脚的“升级管理”中查看当前版本、可用 Release、远程回退版本、本地备份和对应的人工命令。

- 面板仅会在 Linux、root 用户、受控 systemd 服务、主控二进制和待更新 Web 目录均位于受控安装根目录且无符号链接时执行自动升级、回退或重启。切换前会创建最多五份本地备份，失败时尝试恢复主控和 Web 资产；数据库迁移不会自动逆向回退。
- Release 必须包含 `SHA256SUMS` 资产。面板先下载校验清单，再校验所选 Linux 主控包和（部署配置为管理受控静态目录时的）`web-dist.zip`，随后才会解包或修改本机文件。历史 Release 没有该清单时会显示为不可自动应用，仍可使用原脚本手动处理。
- Docker、Docker Compose 和源码部署不会由面板写入运行环境，只会显示命令。Compose 当前项目使用 `docker compose up -d --build --force-recreate api web` 重建 API 与 Web，保留 `mysql_data` 命名卷；Docker 自定义端口、域名、环境变量或 `no-db` 部署应先导出并复用原始容器参数。
- 已配置的反向代理只有在 `ONECLICKVIRT_PROXY_SERVICES` 明确列出时才会在受控 systemd 重启后执行 `reload`。Release 元数据和资产下载会依次使用 GitHub、`ONECLICKVIRT_UPDATE_API_ENDPOINTS` 和现有 CDN/API 反代端点；所有面板下载地址均要求 HTTPS。

常用覆盖项（建议写入受控服务的环境文件后重启服务）：

| 变量 | 用途 |
| --- | --- |
| `ONECLICKVIRT_UPDATE_ENABLED=false` | 完全禁用面板升级、回退和重启按钮。 |
| `ONECLICKVIRT_UPDATE_MODE` | 强制部署模式：`systemd`、`docker`、`compose`、`source`、`embedded`、`unknown` 或 `disabled`。Compose 容器无法可靠自识别时可显式设为 `compose`。 |
| `ONECLICKVIRT_UPDATE_PROXY` | 逗号分隔的 HTTPS 发布资产/CDN 反代前缀。 |
| `ONECLICKVIRT_UPDATE_API_ENDPOINTS` | 逗号分隔的 HTTPS GitHub API 或 API 反代根地址。 |
| `ONECLICKVIRT_UPDATE_REPO` | Release 仓库，默认 `oneclickvirt/oneclickvirt`。 |
| `ONECLICKVIRT_UPDATE_FLAVOR` | `standalone` 或 `allinone`；通常由安装器记录的 `SERVER_ASSET` 自动识别。 |
| `ONECLICKVIRT_UPDATE_WEB` | 显式启用或禁用受控静态 Web 目录的替换。`install_full.sh` 会设为 `true`，因为其反向代理会提供 `web-dist.zip` 静态文件。 |
| `ONECLICKVIRT_INSTALL_ROOT`、`ONECLICKVIRT_SERVER_BIN`、`ONECLICKVIRT_WEB_DIR` | 受控安装根目录、主控二进制和受管理的静态 Web 目录。自动模式要求相应更新目标在安装根目录内。 |
| `ONECLICKVIRT_SERVICE_NAME`、`ONECLICKVIRT_SERVICE_FILE` | 受控 systemd 服务及其 unit 文件。 |
| `ONECLICKVIRT_PROXY_SERVICES` | 逗号分隔的 Nginx/OpenResty/Caddy 等 systemd 服务名，升级或重启后执行 `reload`。 |
| `ONECLICKVIRT_UPDATE_SCRIPT` | 页脚“命令”页使用的原安装脚本路径；未找到时显示官方下载后执行的命令。 |
| `ONECLICKVIRT_UPDATE_HEALTH_PORT` | 重启后的本机 `/api/v1/health` 检查端口，默认读取主控配置或使用 `8888`。 |
| `ONECLICKVIRT_UPDATE_ALLOW_UNVERIFIED=true` | 仅用于受控恢复场景，允许缺少 `SHA256SUMS` 的旧 Release；生产环境不建议设置。 |

### 方式四：自己编译打包

<details>
<summary>展开查看编译步骤</summary>

如果需要修改源码或自定义构建：

**一体化版本（内置数据库）：**

```bash
git clone https://github.com/oneclickvirt/oneclickvirt.git
cd oneclickvirt
docker build -t oneclickvirt .
docker run -d \
  --name oneclickvirt \
  -p 80:80 \
  -v oneclickvirt-data:/var/lib/mysql \
  -v oneclickvirt-storage:/app/storage \
  --restart unless-stopped \
  oneclickvirt
```

Docker 构建会自动内嵌 `scripts/install_agent.sh`。如果你还希望控制端镜像直接提供本地 Agent 发布包，而不是在下载时 302 跳转到 GitHub Releases，请在执行 `docker build` 前把下面这些文件放到 `server/assets/agent/`：

```text
install_agent.sh
oneclickvirt-agent-linux-amd64.tar.gz
oneclickvirt-agent-linux-arm64.tar.gz
```

**独立数据库版本：**

```bash
git clone https://github.com/oneclickvirt/oneclickvirt.git
cd oneclickvirt
docker build -f Dockerfile.no-db -t oneclickvirt:no-db .
docker run -d \
  --name oneclickvirt \
  -p 80:80 \
  -e FRONTEND_URL="https://your-domain.com" \
  -e DB_HOST="your-mysql-host" \
  -e DB_PORT="3306" \
  -e DB_NAME="oneclickvirt" \
  -e DB_USER="root" \
  -e DB_PASSWORD="your-password" \
  -v oneclickvirt-storage:/app/storage \
  --restart unless-stopped \
  oneclickvirt:no-db
```

更新或重建 `no-db` 容器时继续挂载同一个 `oneclickvirt-storage` 卷，运行时配置位于卷内的 `/app/storage/config.yaml`，无需重新初始化数据库。

直接执行 Go 源码编译时也是同样逻辑：`server/assets/agent/` 里的本地 Agent 资源是可选的，缺失时会回退到官方 GitHub 安装脚本和 Release 包，不会因此导致控制端构建失败。

</details>

### Proxmox VE 集成检查

`proxmoxve` 集成任务使用 [`oneclickvirt/pve`](https://github.com/oneclickvirt/pve) 的安装脚本，在可销毁的 Worker 上执行分离式安装阶段，等待内核重启以及可能由 `ifupdown2` 引导服务触发的第二次重启，然后校验 PVE 运行时、网桥、NAT 状态和主控侧 Provider 链路。只有在分离任务无法继续观测时才会将失联归类为基础设施跳过，不会把它当作模块测试通过。PVE 仓库本身还会执行脚本语法、网络回归和 ShellCheck 检查。

### 方式五：手动开发部署

<details>
<summary>展开查看开发部署步骤</summary>

#### 环境要求

* Go 1.25.0
* Node.js 22+
* MySQL 5.7+
* npm 或 yarn

#### 环境部署

1. 构建前端
```bash
cd web
npm i
npm run serve
```

2. 构建后端
```bash
cd server
go mod tidy
go run main.go
```

3. 开发模式下不需要反代后端，vite已自带后端代理请求。

5. 在mysql中创建一个空的数据库```oneclickvirt```，记录对应的账户和密码。

6. 访问前端地址，自动跳转到初始化界面，填写数据库信息和相关信息，点击初始化。

7. 完成初始化后会自动跳转到首页，可以开始开发测试了。

#### 本地开发

* 前端：[http://localhost:8080](http://localhost:8080)
* 后端 API：[http://localhost:8888](http://localhost:8888)
* API 文档：[http://localhost:8888/swagger/index.html](http://localhost:8888/swagger/index.html)

</details>

## 初始账户

首次初始化时会根据初始化表单创建管理员账户。快捷填充会每次生成随机强密码，请在提交表单前保存生成的密码。

## 配置文件

主要配置文件位于 `server/config.yaml`

## 赞助方

感谢以下团体或个人赞助 OneClickVirt 项目：

[![Docker Sponsored OSS](https://img.shields.io/badge/Docker-Sponsored%20OSS-2496ED?logo=docker&logoColor=white)](https://hub.docker.com/r/oneclickvirt/oneclickvirt)

<p>
  <a href="https://dartnode.com?aff=bonus">
    <img src="./web/src/assets/images/dartnode.png" alt="DartNode" height="44">
  </a>
  &nbsp;&nbsp;
  <a href="https://console.zmto.com/?affid=1524">
    <img src="https://console.zmto.com/templates/2019/dist/images/logo_dark.svg" alt="zmto" height="44">
  </a>
  &nbsp;&nbsp;
  <a href="https://community.ibm.com/zsystems/form/l1cc-oss-vm-request/">
    <img src="./web/src/assets/images/ibm-linuxone.png" alt="IBM LinuxONE OSS Community Cloud" height="44">
  </a>
  &nbsp;&nbsp;
  <a href="https://fossvps.org/">
    <img src="https://lowendspirit.com/uploads/userpics/793/nHSR7IOVIBO84.png" alt="fossvps" height="44">
  </a>
  &nbsp;&nbsp;
  <a href="https://linux.do/">
    <img src="https://cdn3.ldstatic.com/original/4X/d/1/4/d146c68151340881c884d95e0da4acdf369258c6.png" alt="Linux DO" height="44">
  </a>
  &nbsp;&nbsp;
  <a href="https://www.jtti.cc/zh/activity/special-offer.html?z=oneclickvirt">
    <img src="https://www.jtti.cc/static/images/common/article_logo.png" alt="Jtti.cc" height="44">
  </a>
</p>

## LICENSE

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt.svg?type=large&issueType=license)](https://app.fossa.com/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt?ref=badge_large&issueType=license)

## 演示截图

以下截图从当前响应式前端重新生成，覆盖未登录首页、赞助方区域、移动端布局、管理员页面和用户页面。

**未登录首页**

![](./.back/1.png)

**赞助方**

![](./.back/2.png)

**移动端首页**

![](./.back/3.png)

**管理员仪表盘**

![](./.back/4.png)

**节点管理**

![](./.back/5.png)

**用户仪表盘**

![](./.back/6.png)

**用户实例**

![](./.back/7.png)
