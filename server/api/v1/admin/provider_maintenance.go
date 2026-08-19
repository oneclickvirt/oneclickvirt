package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

func buildInstanceVNCInfo(instanceID uint, userID uint, admin bool) (gin.H, error) {
	// Keep the legacy /vnc response and eligibility rules unchanged. New
	// multi-protocol capabilities are exposed exclusively through /console.
	if _, _, err := resolveInstanceVNCTarget(instanceID, userID, admin); err != nil {
		return gin.H{"enabled": false, "reason": err.Error()}, nil
	}
	return gin.H{"enabled": true}, nil
}

func resolveInstanceVNCTarget(instanceID uint, userID uint, admin bool) (string, int, error) {
	var inst providerModel.Instance
	query := global.APP_DB.Select("id", "provider_id", "status", "instance_type", "provider_vm_id", "discovered_data", "user_id")
	if admin {
		query = query.Where("id = ?", instanceID)
	} else {
		query = query.Where("id = ? AND user_id = ?", instanceID, userID)
	}
	if err := query.First(&inst).Error; err != nil {
		return "", 0, err
	}
	if constant.IsBusyStatus(inst.Status) {
		return "", 0, fmt.Errorf("实例正在操作进行中（当前状态：%s），请等待当前任务完成", inst.Status)
	}
	if inst.Status != constant.InstanceStatusRunning {
		return "", 0, fmt.Errorf("实例未运行")
	}
	if inst.InstanceType != "vm" {
		return "", 0, fmt.Errorf("当前实例类型不支持WebVNC")
	}
	var p providerModel.Provider
	if err := global.APP_DB.Select("id", "type", "endpoint", "port_ip", "enable_vnc", "vnc_base_port", "vnc_host").First(&p, inst.ProviderID).Error; err != nil {
		return "", 0, err
	}
	if !p.EnableVNC {
		return "", 0, fmt.Errorf("节点未启用WebVNC")
	}
	// Keep the legacy VNC host selection byte-for-byte compatible in behavior.
	// The provider-neutral console resolver has its own normalized transport
	// handling; this legacy endpoint must not inherit that new behavior.
	host := strings.TrimSpace(p.VNCHost)
	if host == "" {
		host = strings.TrimSpace(p.PortIP)
		if host == "" {
			host = strings.TrimSpace(p.Endpoint)
		}
	}
	host = strings.Trim(host, "[]")
	port := parseVNCDiscoveredPort(inst.DiscoveredData)
	if port == 0 {
		base := p.VNCBasePort
		if base == 0 {
			base = 5900
		}
		vmid, _ := strconv.Atoi(inst.ProviderVMID)
		if vmid > 0 {
			port = base + vmid
		} else {
			port = base
		}
	}
	if host == "" || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("VNC目标不可用")
	}
	return host, port, nil
}

func parseVNCDiscoveredPort(raw string) int {
	if raw == "" {
		return 0
	}
	var obj map[string]interface{}
	if json.Unmarshal([]byte(raw), &obj) != nil {
		return 0
	}
	var parse func(value interface{}) int
	parse = func(value interface{}) int {
		switch x := value.(type) {
		case float64:
			return int(x)
		case json.Number:
			n, _ := strconv.Atoi(string(x))
			return n
		case int:
			return x
		case string:
			n, _ := strconv.Atoi(strings.TrimSpace(x))
			return n
		case map[string]interface{}:
			for _, key := range []string{"port", "vncPort", "vnc_port"} {
				if n := parse(x[key]); n > 0 {
					return n
				}
			}
		}
		return 0
	}
	for _, key := range []string{"vncPort", "vnc_port", "vnc"} {
		if n := parse(obj[key]); n > 0 {
			return n
		}
	}
	console, _ := obj["console"].(map[string]interface{})
	if console == nil {
		return 0
	}
	for _, key := range []string{"vncPort", "vnc_port", "vnc"} {
		if n := parse(console[key]); n > 0 {
			return n
		}
	}
	if strings.EqualFold(strings.TrimSpace(fmt.Sprint(console["protocol"])), consoleProtocolVNC) {
		return parse(console["port"])
	}
	return 0
}

var vncUpgrader = websocket.Upgrader{
	ReadBufferSize:  32768,
	WriteBufferSize: 32768,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		appConfig := global.GetAppConfig()
		return utils.OriginAllowedForRequest(r, origin, appConfig.System.FrontendURL, appConfig.Cors.Whitelist)
	},
}

func proxyVNCWebSocket(c *gin.Context, host string, port int) {
	ws, err := vncUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 10*time.Second)
	if err != nil {
		_ = ws.WriteMessage(websocket.TextMessage, []byte("VNC连接失败: "+err.Error()))
		return
	}
	defer conn.Close()
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			mt, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.BinaryMessage || mt == websocket.TextMessage {
				_, _ = conn.Write(msg)
			}
		}
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		buf := make([]byte, 32768)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	<-ctx.Done()
	if global.APP_LOG != nil {
		global.APP_LOG.Debug("WebVNC会话结束", zap.String("host", host), zap.Int("port", port))
	}
}
