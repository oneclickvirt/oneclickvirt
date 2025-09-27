# OneClickVirt Virtualization Management Platform

An extensible universal virtualization management platform that supports LXD, Incus, Docker, and Proxmox VE.

## Detailed Description

[www.spiritlhl.net](https://www.spiritlhl.net/)

## Development and Testing

### Environment Requirements

* Go 1.24.5
* Node.js 22+
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

## Default Accounts

After system initialization, the following default accounts will be generated:

* Administrator account: `admin / Admin123!@#`
* Regular user: `testuser / TestUser123!@#`

> Tip: Please change the default passwords immediately after first login.

## Configuration File

The main configuration file is located at `server/config.yaml`

## Demo Screenshots

![](./.back/1.png)
![](./.back/2.png)
![](./.back/3.png)
![](./.back/4.png)
![](./.back/5.png)
![](./.back/6.png)
![](./.back/7.png)