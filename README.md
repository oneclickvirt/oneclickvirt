# OneClickVirt 虚拟化管理平台

一个可扩展的通用虚拟化管理平台，支持 LXD、Incus、Docker 和 Proxmox VE。

## 详细说明

[www.spiritlhl.net](https://www.spiritlhl.net/)

## 快速开始

### 环境要求

* Go 1.24.5
* Node.js 22+
* npm 或 yarn

### 服务地址

* 前端：[http://localhost:8080](http://localhost:8080)
* 后端 API：[http://localhost:8888](http://localhost:8888)
* API 文档：[http://localhost:8888/swagger/index.html](http://localhost:8888/swagger/index.html)

### 初始化步骤

1. 访问 [http://localhost:8080](http://localhost:8080)，会自动跳转至系统初始化页面。
2. 设置管理员账户信息。
3. 初始化完成后即可正常使用。

## 默认账户

系统初始化后会生成以下默认账户：

* 管理员账户：`admin / Admin123!@#`
* 普通用户：`testuser / TestUser123!@#`

> 提示：请在首次登录后立即修改默认密码。

## 配置文件

主要配置文件位于 `server/config.yaml`

## 技术栈

### 后端

* Gin Web 框架
* GORM ORM
* MySQL 数据库
* JWT 认证

### 前端

* Vue 3
* Vite
* Element Plus
* Pinia
* Vue Router

## 部署说明

### 生产环境部署

1. 构建前端
```bash
cd web
npm run build
```

2. 构建后端
```bash
cd server
go build -o oneclickvirt main.go
```

3. 配置反向代理（推荐使用 Nginx）。
4. 设置环境变量 `GIN_MODE=release`。

## 演示截图

![](./.back/1.png)
![](./.back/2.png)
![](./.back/3.png)
![](./.back/4.png)
![](./.back/5.png)