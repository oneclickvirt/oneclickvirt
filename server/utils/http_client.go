package utils

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"oneclickvirt/global"
)

var (
	defaultHTTPClient     *http.Client
	defaultHTTPClientOnce sync.Once
)

// GetDefaultHTTPClient 获取默认的HTTP客户端（带连接池）
func GetDefaultHTTPClient() *http.Client {
	defaultHTTPClientOnce.Do(func() {
		defaultHTTPClient = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:          100,              // 最大空闲连接数
				MaxIdleConnsPerHost:   10,               // 每个host的最大空闲连接数
				IdleConnTimeout:       90 * time.Second, // 空闲连接超时
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				ForceAttemptHTTP2:     true,
			},
		}
	})
	return defaultHTTPClient
}

var (
	sharedTransport       *http.Transport
	sharedTransportOnce   sync.Once
	insecureTransport     *http.Transport
	insecureTransportOnce sync.Once
)

// getSharedTransport 获取共享的HTTP Transport（避免频繁创建导致资源泄漏）
func getSharedTransport() *http.Transport {
	sharedTransportOnce.Do(func() {
		sharedTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	})
	return sharedTransport
}

// getInsecureTransport 获取跳过TLS验证的共享Transport
func getInsecureTransport() *http.Transport {
	insecureTransportOnce.Do(func() {
		insecureTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	})
	return insecureTransport
}

// GetHTTPClientWithTimeout 创建带自定义超时的HTTP客户端（复用Transport）
func GetHTTPClientWithTimeout(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: getSharedTransport(),
	}
}

// GetInsecureHTTPClient 获取跳过TLS验证的HTTP客户端（复用Transport）
func GetInsecureHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: getInsecureTransport(),
	}
}

// CleanupHTTPTransports 清理HTTP Transport的空闲连接（在应用关闭时调用）
func CleanupHTTPTransports() {
	if sharedTransport != nil {
		sharedTransport.CloseIdleConnections()
		global.APP_LOG.Info("已清理共享HTTP Transport的空闲连接")
	}
	if insecureTransport != nil {
		insecureTransport.CloseIdleConnections()
		global.APP_LOG.Info("已清理不安全HTTP Transport的空闲连接")
	}
}
