package pmacct

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// setupSystemdService 使用systemd管理pmacct服务
func (s *Service) setupSystemdService(providerInstance provider.Provider, instanceName, networkInterface, configFile, configDir, serviceContent string, networkInterfaces *NetworkInterfaceInfo) error {
	serviceFile := fmt.Sprintf("/etc/systemd/system/pmacctd-%s.service", instanceName)

	// 步骤1: 使用SFTP上传systemd服务文件
	if err := s.uploadFileViaSFTP(providerInstance, serviceContent, serviceFile, 0644); err != nil {
		return fmt.Errorf("failed to upload systemd service file: %w", err)
	}

	// 步骤2: 生成启动脚本并上传（包含停止旧服务逻辑）
	startScript := fmt.Sprintf(`#!/bin/bash
set -e

# 停止可能存在的旧进程（在脚本内执行，避免SSH会话中断）
systemctl stop pmacctd-%s 2>/dev/null || true
pkill -f "pmacctd.*%s" 2>/dev/null || true
sleep 1

# 重载systemd配置
systemctl daemon-reload

# 启用并启动服务
systemctl enable pmacctd-%s
systemctl start pmacctd-%s

# 验证服务状态
if systemctl is-active --quiet pmacctd-%s; then
    echo "pmacct service started successfully"
    exit 0
else
    echo "Failed to start pmacct service"
    systemctl status pmacctd-%s --no-pager || true
    exit 1
fi
`, instanceName, configFile, instanceName, instanceName, instanceName, instanceName)

	startScriptPath := fmt.Sprintf("/tmp/pmacct_start_%s.sh", instanceName)
	if err := s.uploadFileViaSFTP(providerInstance, startScript, startScriptPath, 0755); err != nil {
		return fmt.Errorf("failed to upload start script: %w", err)
	}

	// 步骤3: 执行启动脚本
	execCtx, execCancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer execCancel()

	output, err := providerInstance.ExecuteSSHCommand(execCtx, startScriptPath)
	if err != nil {
		return fmt.Errorf("failed to start systemd service: %w, output: %s", err, output)
	}

	// 步骤4: 清理临时脚本
	cleanupCtx, cleanupCancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cleanupCancel()
	providerInstance.ExecuteSSHCommand(cleanupCtx, fmt.Sprintf("rm -f %s", startScriptPath))

	global.APP_LOG.Info("pmacct systemd服务配置并启动成功",
		zap.String("instance", instanceName),
		zap.String("serviceFile", serviceFile))

	// 配置成功后，更新实例的网络接口信息到数据库
	s.updateInstanceNetworkInterfaces(instanceName, networkInterfaces.IPv4Interface, networkInterfaces.IPv6Interface)

	return nil
}

// setupSysVService 使用SysV init管理pmacct服务
func (s *Service) setupSysVService(providerInstance provider.Provider, instanceName, networkInterface, configFile, configDir string, networkInterfaces *NetworkInterfaceInfo) error {
	initScript := fmt.Sprintf("/etc/init.d/pmacctd-%s", instanceName)

	// 步骤1: 生成init脚本内容
	scriptContent := fmt.Sprintf(`#!/bin/bash
### BEGIN INIT INFO
# Provides:          pmacctd-%s
# Required-Start:    $network $local_fs $remote_fs
# Required-Stop:     $network $local_fs $remote_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: pmacct daemon for instance %s
### END INIT INFO

DAEMON=/usr/sbin/pmacctd
CONFIG=%s
PIDFILE=%s/pmacctd.pid
NAME=pmacctd-%s

case "$1" in
  start)
    echo "Starting $NAME..."
    $DAEMON -f $CONFIG
    ;;
  stop)
    echo "Stopping $NAME..."
    if [ -f $PIDFILE ]; then
      kill $(cat $PIDFILE)
    fi
    pkill -f "pmacctd.*$CONFIG"
    ;;
  restart)
    $0 stop
    sleep 2
    $0 start
    ;;
  status)
    if pgrep -f "pmacctd.*$CONFIG" > /dev/null; then
      echo "$NAME is running"
      exit 0
    else
      echo "$NAME is not running"
      exit 1
    fi
    ;;
  *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac

exit 0
`, instanceName, instanceName, configFile, configDir, instanceName)

	// 步骤1: 使用SFTP上传init脚本
	if err := s.uploadFileViaSFTP(providerInstance, scriptContent, initScript, 0755); err != nil {
		return fmt.Errorf("failed to upload init script: %w", err)
	}

	// 步骤2: 生成启用服务的脚本并上传（包含停止逻辑）
	enableScript := fmt.Sprintf(`#!/bin/bash
set -e

# 停止可能存在的旧进程
if [ -f /etc/init.d/pmacctd-%s ]; then
    /etc/init.d/pmacctd-%s stop 2>/dev/null || true
fi
pkill -f "pmacctd.*%s" 2>/dev/null || true
sleep 1

# 启用服务（支持多种init系统）
# Debian/Ubuntu使用update-rc.d
if command -v update-rc.d >/dev/null 2>&1; then
    update-rc.d pmacctd-%s defaults
# RHEL/CentOS使用chkconfig
elif command -v chkconfig >/dev/null 2>&1; then
    chkconfig --add pmacctd-%s
    chkconfig pmacctd-%s on
# Alpine使用rc-update
elif command -v rc-update >/dev/null 2>&1; then
    rc-update add pmacctd-%s default 2>/dev/null || true
fi

# 启动服务
%s start

# 验证服务状态
sleep 2
if pgrep -f "pmacctd.*%s" > /dev/null; then
    echo "pmacct service started successfully"
    exit 0
else
    echo "Failed to start pmacct service"
    exit 1
fi
`, instanceName, instanceName, configFile, instanceName, instanceName, instanceName, instanceName, initScript, configFile)

	enableScriptPath := fmt.Sprintf("/tmp/pmacct_enable_%s.sh", instanceName)
	if err := s.uploadFileViaSFTP(providerInstance, enableScript, enableScriptPath, 0755); err != nil {
		return fmt.Errorf("failed to upload enable script: %w", err)
	}

	// 步骤3: 执行启用脚本
	execCtx, execCancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer execCancel()

	output, err := providerInstance.ExecuteSSHCommand(execCtx, enableScriptPath)
	if err != nil {
		return fmt.Errorf("failed to enable sysv service: %w, output: %s", err, output)
	}

	// 步骤4: 清理临时脚本
	cleanupCtx, cleanupCancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cleanupCancel()
	providerInstance.ExecuteSSHCommand(cleanupCtx, fmt.Sprintf("rm -f %s", enableScriptPath))

	global.APP_LOG.Info("pmacct SysV服务配置并启动成功",
		zap.String("instance", instanceName),
		zap.String("initScript", initScript))

	// 配置成功后，更新实例的网络接口信息到数据库
	s.updateInstanceNetworkInterfaces(instanceName, networkInterfaces.IPv4Interface, networkInterfaces.IPv6Interface)

	return nil
}

// setupOpenRCService 使用OpenRC管理pmacct服务（Alpine Linux）
func (s *Service) setupOpenRCService(providerInstance provider.Provider, instanceName, networkInterface, configFile, configDir string, networkInterfaces *NetworkInterfaceInfo) error {
	initScript := fmt.Sprintf("/etc/init.d/pmacctd-%s", instanceName)

	// 步骤1: 生成OpenRC init脚本
	scriptContent := fmt.Sprintf(`#!/sbin/openrc-run
# OpenRC service script for pmacct instance: %s

name="pmacctd-%s"
description="pmacct daemon for instance %s"

command="/usr/sbin/pmacctd"
command_args="-f %s"
pidfile="%s/pmacctd.pid"
command_background=true

depend() {
    need net
    after firewall
}

start_pre() {
    checkpath --directory --mode 0755 %s
}

stop_post() {
    rm -f "$pidfile"
}
`, instanceName, instanceName, instanceName, configFile, configDir, configDir)

	// 步骤2: 使用SFTP上传OpenRC init脚本
	if err := s.uploadFileViaSFTP(providerInstance, scriptContent, initScript, 0755); err != nil {
		return fmt.Errorf("failed to upload openrc script: %w", err)
	}

	// 步骤3: 生成启用服务的脚本并上传（包含停止逻辑）
	enableScript := fmt.Sprintf(`#!/bin/sh
set -e

# 停止可能存在的旧进程
if [ -f /etc/init.d/pmacctd-%s ]; then
    rc-service pmacctd-%s stop 2>/dev/null || true
fi
pkill -f "pmacctd.*%s" 2>/dev/null || true
sleep 1

# 添加到默认运行级别
rc-update add pmacctd-%s default 2>/dev/null || true

# 启动服务
rc-service pmacctd-%s start

# 验证服务状态
sleep 2
if pgrep -f "pmacctd.*%s" > /dev/null; then
    echo "pmacct service started successfully"
    exit 0
else
    echo "Failed to start pmacct service"
    exit 1
fi
`, instanceName, instanceName, configFile, instanceName, instanceName, configFile)

	enableScriptPath := fmt.Sprintf("/tmp/pmacct_enable_%s.sh", instanceName)
	if err := s.uploadFileViaSFTP(providerInstance, enableScript, enableScriptPath, 0755); err != nil {
		return fmt.Errorf("failed to upload enable script: %w", err)
	}

	// 步骤3: 执行启用脚本
	execCtx, execCancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer execCancel()

	output, err := providerInstance.ExecuteSSHCommand(execCtx, enableScriptPath)
	if err != nil {
		return fmt.Errorf("failed to enable openrc service: %w, output: %s", err, output)
	}

	// 步骤4: 清理临时脚本
	cleanupCtx, cleanupCancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cleanupCancel()
	providerInstance.ExecuteSSHCommand(cleanupCtx, fmt.Sprintf("rm -f %s", enableScriptPath))

	global.APP_LOG.Info("pmacct OpenRC服务配置并启动成功",
		zap.String("instance", instanceName),
		zap.String("initScript", initScript))

	// 配置成功后，更新实例的网络接口信息到数据库
	s.updateInstanceNetworkInterfaces(instanceName, networkInterfaces.IPv4Interface, networkInterfaces.IPv6Interface)

	return nil
}

// startWithNohup 使用nohup启动pmacct（降级方案）
func (s *Service) startWithNohup(providerInstance provider.Provider, instanceName, networkInterface, configFile, configDir string, networkInterfaces *NetworkInterfaceInfo) error {
	// 启动pmacct进程（后台运行）
	startCmd := fmt.Sprintf(`
# 停止可能存在的旧进程
if [ -f %s/pmacctd.pid ]; then
    OLD_PID=$(cat %s/pmacctd.pid)
    kill $OLD_PID 2>/dev/null || true
    sleep 1
fi

# 启动新的pmacct进程
nohup pmacctd -f %s > %s/pmacctd.log 2>&1 &
sleep 2

# 验证进程是否启动
if pgrep -f "pmacctd.*%s" > /dev/null; then
    echo "pmacct started successfully"
else
    echo "Failed to start pmacct"
    exit 1
fi
`, configDir, configDir, configFile, configDir, configFile)

	startCtx, startCancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer startCancel()

	output, err := providerInstance.ExecuteSSHCommand(startCtx, startCmd)
	if err != nil {
		return fmt.Errorf("failed to start pmacct: %w, output: %s", err, output)
	}

	global.APP_LOG.Info("pmacct配置并启动成功（nohup方式）",
		zap.String("instance", instanceName),
		zap.String("configFile", configFile))

	// 配置成功后，更新实例的网络接口信息到数据库
	s.updateInstanceNetworkInterfaces(instanceName, networkInterfaces.IPv4Interface, networkInterfaces.IPv6Interface)

	return nil
}
