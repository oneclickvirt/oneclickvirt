# OneClickVirt 虚拟化管理平台

一个可扩展的通用虚拟化管理平台，支持 LXD、Incus、Docker 和 Proxmox VE。

## 详细说明

[www.spiritlhl.net](https://www.spiritlhl.net/)

## 快速部署

### 方式一：使用预构建镜像

使用已构建好的多架构镜像，会自动根据当前系统架构下载对应版本。

**镜像标签说明：**

| 镜像标签 | 说明 | 适用场景 |
|---------|------|---------|
| `spiritlhl/oneclickvirt:latest` | 一体化版本（内置数据库）最新版 | 快速部署 |
| `spiritlhl/oneclickvirt:20251023` | 一体化版本特定日期版本 | 需要固定版本 |
| `spiritlhl/oneclickvirt:no-db` | 独立数据库版本最新版 | 不内置数据库 |
| `spiritlhl/oneclickvirt:no-db-20251023` | 独立数据库版本特定日期 | 不内置数据库 |

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
  spiritlhl/oneclickvirt:latest
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
  spiritlhl/oneclickvirt:latest
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
  spiritlhl/oneclickvirt:no-db
```

**环境变量说明：**
- `FRONTEND_URL`: 前端访问地址（必填，支持 http/https）
- `DB_HOST`: 数据库主机地址
- `DB_PORT`: 数据库端口（默认 3306）
- `DB_NAME`: 数据库名称
- `DB_USER`: 数据库用户名
- `DB_PASSWORD`: 数据库密码

</details>

> **说明**：`FRONTEND_URL` 用于配置前端访问地址，影响 CORS、OAuth2 回调等功能。系统会自动检测 HTTP/HTTPS 协议并调整相应配置，协议头可以是http或https。

### 方式二：使用 Docker Compose（推荐开发和测试）

<details>
<summary>展开查看 Docker Compose 部署</summary>

使用 Docker Compose 可以一键部署完整的开发环境，包括前端、后端和数据库：

```bash
git clone https://github.com/oneclickvirt/oneclickvirt.git
cd oneclickvirt
docker-compose up -d --build
```

**默认配置说明：**

- 前端服务：`http://localhost:8888`
- 后端 API：通过前端代理访问
- MySQL 数据库：端口 3306，数据库名 `oneclickvirt`，无密码
- 数据持久化：
  - 数据库数据：`./data/mysql`
  - 应用存储：`./data/app/`

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

### 方式三：自己编译打包

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

</details>

### 方式四：手动开发部署

<details>
<summary>展开查看开发部署步骤</summary>

#### 环境要求

* Go 1.24.5
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

## 默认账户

系统初始化后会生成以下默认账户：

* 管理员账户：`admin / Admin123!@#`

> 提示：请在首次登录后立即修改默认密码，修改密码应该在用户管理界面点击对应用户进行修改。

## 配置文件

主要配置文件位于 `server/config.yaml`

## 演示截图

![](./.back/1.png)
![](./.back/2.png)
![](./.back/3.png)
![](./.back/4.png)
![](./.back/5.png)
![](./.back/6.png)
![](./.back/7.png)