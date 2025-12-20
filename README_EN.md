# OneClickVirt Virtualization Management Platform

[![Build and Release oneclickvirt](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/build.yml/badge.svg)](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/build.yml)

[![Build and Push Docker Images](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/build_docker.yml/badge.svg)](https://github.com/oneclickvirt/oneclickvirt/actions/workflows/build_docker.yml)

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt.svg?type=shield&issueType=license)](https://app.fossa.com/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt?ref=badge_shield&issueType=license) [![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt.svg?type=shield&issueType=security)](https://app.fossa.com/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt?ref=badge_shield&issueType=security)

An extensible universal virtualization management platform that supports LXD, Incus, Docker, and Proxmox VE.

## **Language**

[中文文档](README.md) | [English Docs](README_EN.md)

## Detailed Description

[www.spiritlhl.net](https://www.spiritlhl.net/en/guide/oneclickvirt/oneclickvirt_precheck.html)

## Quick Deployment

### Method 1: Using Pre-built Images

Use pre-built multi-architecture images that automatically downloads the appropriate version for your system architecture.

**Image Tags:**

| Image Tag | Description | Use Case |
|-----------|-------------|----------|
| `spiritlhl/oneclickvirt:latest` | All-in-one version (built-in database) | Quick deployment |
| `spiritlhl/oneclickvirt:20251220` | All-in-one version with specific date | Fixed version requirement |
| `spiritlhl/oneclickvirt:no-db` | Standalone database version | Without database |
| `spiritlhl/oneclickvirt:no-db-20251220` | Standalone database version with date | Without database |

All images support both `linux/amd64` and `linux/arm64` architectures.

<details>
<summary>View All-in-One Version (Built-in Database)</summary>

**Basic Usage (without domain configuration):**

```bash
docker run -d \
  --name oneclickvirt \
  -p 80:80 \
  -v oneclickvirt-data:/var/lib/mysql \
  -v oneclickvirt-storage:/app/storage \
  --restart unless-stopped \
  spiritlhl/oneclickvirt:latest
```

**Configure Domain Access:**

If you need to configure a domain, set the `FRONTEND_URL` environment variable:

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

Or using GitHub Container Registry:

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
<summary>View Standalone Database Version</summary>

Use external database for smaller image size and faster startup:

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

**Environment Variables:**
- `FRONTEND_URL`: Frontend access URL (required, supports http/https)
- `DB_HOST`: Database host address
- `DB_PORT`: Database port (default 3306)
- `DB_NAME`: Database name
- `DB_USER`: Database username
- `DB_PASSWORD`: Database password

</details>

> **Note**: `FRONTEND_URL` is used to configure the frontend access address, affecting features like CORS and OAuth2 callbacks. The system will automatically detect HTTP/HTTPS protocol and adjust configurations accordingly. The protocol prefix can be either http or https.

### Method 2: Using Docker Compose

<details>
<summary>View Docker Compose Deployment</summary>

Use Docker Compose to deploy the complete development environment with one command, using **multi-container deployment** architecture with separate frontend, backend, and database containers:

```bash
git clone https://github.com/oneclickvirt/oneclickvirt.git
cd oneclickvirt
docker-compose up -d --build || docker compose up -d --build
```

**Default Configuration:**

- Frontend service: `http://localhost:8888`
- Backend API: Accessed via frontend proxy
- MySQL Database: Port 3306, database name `oneclickvirt`, no password
- Data persistence:
  - Database data: `./data/mysql`
  - Application storage: `./data/app/`

**Initialization Configuration:**

When accessing for the first time, you will enter the initialization interface. Please fill in the database configuration as follows:
- Database Host: `mysql` (container name, not 127.0.0.1)
- Database Port: `3306`
- Database Name: `oneclickvirt`
- Database User: `root`
- Database Password: Leave empty (no password)

**Custom Port (Optional):**

To modify the frontend access port, edit the ports configuration in `docker-compose.yaml`:

```yaml
services:
  web:
    ports:
      - "your-port:80"  # e.g., "80:80" or "8080:80"
```

**Stop Services:**

```bash
docker-compose down
```

**View Logs:**

```bash
docker-compose logs -f
```

**Clean Data:**

```bash
docker-compose down
rm -rf ./data
```

</details>

### Method 3: Build from Source

<details>
<summary>View Build Instructions</summary>

If you need to modify the source code or build custom images:

**All-in-One Version (Built-in Database):**

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

**Standalone Database Version:**

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

## Development and Testing

<details>
<summary>View Development Setup</summary>

### Environment Requirements

* Go 1.24.5
* Node.js 22+
* MySQL 5.7+
* npm or yarn

### Environment Deployment

1. Build frontend
```bash
cd web
npm i
npm run serve
```

2. Build backend
```bash
cd server
go mod tidy
go run main.go
```

3. In development mode, there's no need to proxy the backend, as Vite already includes backend proxy requests.

4. Create an empty database named `oneclickvirt` in MySQL, and record the corresponding account and password.

5. Access the frontend address, which will automatically redirect to the initialization interface. Fill in the database information and related details, then click initialize.

6. After completing initialization, it will automatically redirect to the homepage, and you can start development and testing.

### Local Development

* Frontend: [http://localhost:8080](http://localhost:8080)
* Backend API: [http://localhost:8888](http://localhost:8888)
* API Documentation: [http://localhost:8888/swagger/index.html](http://localhost:8888/swagger/index.html)

</details>

## Default Accounts

After system initialization, the following default accounts will be generated:

* Administrator account: `admin / Admin123!@#`

> Tip: Please change the default passwords immediately after first login.

## Configuration File

The main configuration file is located at `server/config.yaml`

## Thanks

Thank the following platforms for providing testing:

<a href="https://console.zmto.com/?affid=1524" target="_blank">
  <img src="https://console.zmto.com/templates/2019/dist/images/logo_dark.svg" alt="zmto" style="height: 50px;">
</a>

<a href="https://fossvps.org/" target="_blank">
  <img src="https://lowendspirit.com/uploads/userpics/793/nHSR7IOVIBO84.png" alt="fossvps" style="height: 50px;">
</a>

<a href="https://community.ibm.com/zsystems/form/l1cc-oss-vm-request/" target="_blank">
  <img src="https://linuxone.cloud.marist.edu/oss/resources/images/linuxonelogo03.png" alt="ibm" style="height: 50px;">
</a>

## LICENSE

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt.svg?type=large&issueType=license)](https://app.fossa.com/projects/git%2Bgithub.com%2Foneclickvirt%2Foneclickvirt?ref=badge_large&issueType=license)

## Demo Screenshots

![](./.back/1.png)
![](./.back/2.png)
![](./.back/3.png)
![](./.back/4.png)
![](./.back/5.png)
![](./.back/6.png)
![](./.back/7.png)