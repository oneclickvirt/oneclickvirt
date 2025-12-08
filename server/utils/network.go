package utils

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// ParseEndpoint 解析endpoint获取host和port
// 如果endpoint包含端口，提取主机和端口；否则使用默认端口
func ParseEndpoint(endpoint string, defaultPort int) (string, int) {
	host := endpoint
	port := defaultPort

	// 如果endpoint包含端口，提取主机和端口
	if strings.Contains(host, ":") {
		parts := strings.Split(host, ":")
		host = parts[0]
		if len(parts) > 1 {
			if p, err := strconv.Atoi(parts[1]); err == nil {
				port = p
			}
		}
	}

	return host, port
}

// ExtractHost 从endpoint中提取主机地址（全局统一函数）
func ExtractHost(endpoint string) string {
	if strings.Contains(endpoint, "://") {
		parts := strings.Split(endpoint, "://")
		if len(parts) > 1 {
			hostPort := parts[1]
			if strings.Contains(hostPort, ":") {
				hostParts := strings.Split(hostPort, ":")
				return hostParts[0]
			}
			return hostPort
		}
	}

	// 如果没有协议前缀，直接返回主机部分
	if strings.Contains(endpoint, ":") {
		parts := strings.Split(endpoint, ":")
		return parts[0]
	}

	return endpoint
}

// ExtractIPFromEndpoint 从endpoint中提取纯IP地址（移除端口号）（全局统一函数）
// 用于从 "192.168.1.1:22" 或 "192.168.1.1" 格式的endpoint中提取IP地址
func ExtractIPFromEndpoint(endpoint string) string {
	// 移除端口号部分，只保留IP
	if colonIndex := strings.LastIndex(endpoint, ":"); colonIndex > 0 {
		// 检查是否是IPv6地址
		if strings.Count(endpoint, ":") > 1 && !strings.HasPrefix(endpoint, "[") {
			// IPv6地址，返回原样
			return endpoint
		}
		// IPv4地址，移除端口部分
		return endpoint[:colonIndex]
	}
	return endpoint
}

// ValidatePortRange 验证端口范围的合法性（全局统一函数）
func ValidatePortRange(startPort, portCount int) error {
	if startPort < 1 || startPort > 65535 {
		return fmt.Errorf("起始端口必须在 1-65535 之间")
	}

	if portCount < 1 {
		return fmt.Errorf("端口数量必须大于 0")
	}

	endPort := startPort + portCount - 1
	if endPort > 65535 {
		return fmt.Errorf("端口范围超出限制，结束端口不能大于 65535")
	}

	return nil
}

// CheckPortAvailability 检查指定主机的端口是否可用（未被占用）
// 使用TCP连接测试，如果能连接成功说明端口被占用（不可用）
// 返回true表示端口可用（未被占用），false表示端口不可用（已被占用）
func CheckPortAvailability(host string, port int, timeout time.Duration) bool {
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// 尝试建立TCP连接
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		// 连接失败，说明端口未被占用，端口可用
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			global.APP_LOG.Debug("端口检查超时，判定为可用",
				zap.String("address", address))
		}
		return true
	}

	// 连接成功，说明端口已被占用，立即关闭连接
	conn.Close()
	global.APP_LOG.Debug("端口已被占用",
		zap.String("address", address))
	return false
}

// CheckPortOpen 检查指定主机的端口是否开放（可连接）
// 使用TCP连接测试，如果能连接成功说明端口开放
// 返回true表示端口开放，false表示端口关闭或不可达
func CheckPortOpen(host string, port int, timeout time.Duration) bool {
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// 尝试建立TCP连接
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		// 连接失败，端口关闭或不可达
		return false
	}

	// 连接成功，端口开放
	conn.Close()
	return true
}

// ScanPortRange 扫描指定主机的端口范围，返回所有被占用的端口列表
// 使用并发扫描提高效率
func ScanPortRange(host string, startPort, endPort int, timeout time.Duration, concurrency int) []int {
	occupiedPorts := make([]int, 0)
	portChan := make(chan int, concurrency)
	resultChan := make(chan int, endPort-startPort+1)

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), timeout*time.Duration(endPort-startPort+1))
	defer cancel()

	// 启动worker协程
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case port, ok := <-portChan:
					if !ok {
						return
					}
					if !CheckPortAvailability(host, port, timeout) {
						select {
						case resultChan <- port:
						case <-ctx.Done():
							return
						}
					}
				}
			}
		}()
	}

	// 发送端口到channel
	go func() {
		defer close(portChan)
		for port := startPort; port <= endPort; port++ {
			select {
			case <-ctx.Done():
				return
			case portChan <- port:
			}
		}
	}()

	// 等待所有worker完成或超时
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 收集结果
	totalPorts := endPort - startPort + 1
	timer := time.NewTimer(timeout * time.Duration(totalPorts))
	defer timer.Stop()

	for {
		select {
		case port, ok := <-resultChan:
			if !ok {
				// 所有结果已收集
				return occupiedPorts
			}
			occupiedPorts = append(occupiedPorts, port)
		case <-timer.C:
			// 超时，停止等待
			global.APP_LOG.Warn("端口扫描超时",
				zap.String("host", host),
				zap.Int("startPort", startPort),
				zap.Int("endPort", endPort),
				zap.Int("foundPorts", len(occupiedPorts)),
				zap.Int("totalPorts", totalPorts))
			cancel() // 取消所有worker
			return occupiedPorts
		case <-ctx.Done():
			return occupiedPorts
		}
	}
}
