# OneClickVirt Virtualization Management Platform

A scalable universal virtualization management platform that supports LXD, Incus, Docker, and Proxmox VE.

## Detailed Description

[www.spiritlhl.net](https://www.spiritlhl.net/)

## Quick Start

### Environment Requirements

* Go 1.24.5
* Node.js 22+
* npm or yarn

### Environment Deployment

1. Build frontend
```bash
cd web
npm i
npm run build
```

2. Build backend
```bash
cd server
go mod tidy
go build -o oneclickvirt main.go
```

3. Run the backend binary file ```oneclickvirt```.

4. Configure domain binding to static file folder, no need to reverse proxy the backend, vite already comes with backend proxy requests.

5. After installing mysql, create an empty database ```oneclickvirt```.

6. Visit your domain, it will automatically redirect to the initialization interface, fill in the database information (root user) and related information, click initialize.

7. After completing initialization, it will automatically redirect to the homepage, you can explore and use it on your own.

### Local Development

* Frontend: [http://localhost:8080](http://localhost:8080)
* Backend API: [http://localhost:8888](http://localhost:8888)
* API Documentation: [http://localhost:8888/swagger/index.html](http://localhost:8888/swagger/index.html)

## Default Accounts

After system initialization, the following default accounts will be generated:

* Administrator account: `admin / Admin123!@#`
* Regular user: `testuser / TestUser123!@#`

> Tip: Please change the default password immediately after first login.

## Configuration File

Main configuration file is located at `server/config.yaml`

## Demo Screenshots

![](./.back/1.png)
![](./.back/2.png)
![](./.back/3.png)
![](./.back/4.png)
![](./.back/5.png)
![](./.back/6.png)
![](./.back/7.png)