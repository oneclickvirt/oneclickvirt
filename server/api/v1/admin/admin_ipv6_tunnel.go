package admin

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"oneclickvirt/model/common"
	"oneclickvirt/service/ipv6tunnel"

	"github.com/gin-gonic/gin"
)

// GetProviderIPv6Tunnels returns cached tunnel state without contacting the
// node. Use the check endpoint for one batched remote reconciliation.
// @Summary 获取Provider IPv6隧道
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Success 200 {object} common.Response "获取成功"
// @Router /admin/providers/{id}/ipv6-tunnels [get]
func GetProviderIPv6Tunnels(c *gin.Context) {
	providerID, ok := ownedIPv6TunnelProvider(c)
	if !ok {
		return
	}
	tunnels, err := ipv6tunnel.NewService().List(providerID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{"list": tunnels}, "获取成功")
}

// CreateProviderIPv6Tunnel stores the desired configuration and optionally
// enables it on the node.
// @Summary 新增Provider IPv6隧道
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param body body ipv6tunnel.CreateRequest true "IPv6隧道配置"
// @Success 200 {object} common.Response "创建成功"
// @Router /admin/providers/{id}/ipv6-tunnels [post]
func CreateProviderIPv6Tunnel(c *gin.Context) {
	providerID, ok := ownedIPv6TunnelProvider(c)
	if !ok {
		return
	}
	var request ipv6tunnel.CreateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}
	tunnel, err := ipv6tunnel.NewService().Create(c.Request.Context(), providerID, request)
	if err != nil {
		responseIPv6TunnelError(c, err)
		return
	}
	common.ResponseSuccess(c, tunnel, "创建成功")
}

// UpdateProviderIPv6Tunnel updates tunnel parameters. An active tunnel is
// atomically replaced on the node and rolled back if activation fails.
// @Summary 更新Provider IPv6隧道
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param tunnel_id path int true "隧道ID"
// @Param body body ipv6tunnel.Config true "IPv6隧道配置"
// @Success 200 {object} common.Response "更新成功"
// @Router /admin/providers/{id}/ipv6-tunnels/{tunnel_id} [put]
func UpdateProviderIPv6Tunnel(c *gin.Context) {
	providerID, tunnelID, ok := ownedIPv6Tunnel(c)
	if !ok {
		return
	}
	var request ipv6tunnel.Config
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}
	tunnel, err := ipv6tunnel.NewService().Update(c.Request.Context(), providerID, tunnelID, request)
	if err != nil {
		responseIPv6TunnelError(c, err)
		return
	}
	common.ResponseSuccess(c, tunnel, "更新成功")
}

// EnableProviderIPv6Tunnel enables and verifies the persistent node unit.
// @Summary 启用Provider IPv6隧道
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param tunnel_id path int true "隧道ID"
// @Success 200 {object} common.Response "启用成功"
// @Router /admin/providers/{id}/ipv6-tunnels/{tunnel_id}/enable [post]
func EnableProviderIPv6Tunnel(c *gin.Context) {
	setProviderIPv6TunnelEnabled(c, true)
}

// DisableProviderIPv6Tunnel disables the unit and removes the live interface
// while retaining its configuration for later re-enablement.
// @Summary 禁用Provider IPv6隧道
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param tunnel_id path int true "隧道ID"
// @Success 200 {object} common.Response "禁用成功"
// @Router /admin/providers/{id}/ipv6-tunnels/{tunnel_id}/disable [post]
func DisableProviderIPv6Tunnel(c *gin.Context) {
	setProviderIPv6TunnelEnabled(c, false)
}

func setProviderIPv6TunnelEnabled(c *gin.Context, enabled bool) {
	providerID, tunnelID, ok := ownedIPv6Tunnel(c)
	if !ok {
		return
	}
	tunnel, err := ipv6tunnel.NewService().SetEnabled(c.Request.Context(), providerID, tunnelID, enabled)
	if err != nil {
		responseIPv6TunnelError(c, err)
		return
	}
	message := "禁用成功"
	if enabled {
		message = "启用成功"
	}
	common.ResponseSuccess(c, tunnel, message)
}

// CheckProviderIPv6Tunnels reconciles every tunnel using one remote command.
// @Summary 检查Provider IPv6隧道状态
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Success 200 {object} common.Response "检查完成"
// @Router /admin/providers/{id}/ipv6-tunnels/check [post]
func CheckProviderIPv6Tunnels(c *gin.Context) {
	providerID, ok := ownedIPv6TunnelProvider(c)
	if !ok {
		return
	}
	tunnels, err := ipv6tunnel.NewService().CheckAll(c.Request.Context(), providerID)
	if err != nil {
		responseIPv6TunnelError(c, err)
		return
	}
	common.ResponseSuccess(c, gin.H{"list": tunnels}, "检查完成")
}

// DeleteProviderIPv6Tunnel removes the interface, persistent files and DB row.
// @Summary 删除Provider IPv6隧道
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param tunnel_id path int true "隧道ID"
// @Success 200 {object} common.Response "删除成功"
// @Router /admin/providers/{id}/ipv6-tunnels/{tunnel_id} [delete]
func DeleteProviderIPv6Tunnel(c *gin.Context) {
	providerID, tunnelID, ok := ownedIPv6Tunnel(c)
	if !ok {
		return
	}
	if err := ipv6tunnel.NewService().Delete(c.Request.Context(), providerID, tunnelID); err != nil {
		responseIPv6TunnelError(c, err)
		return
	}
	common.ResponseSuccess(c, nil, "删除成功")
}

// DetectProviderIPv6TunnelLocalIPv4 reads the route-selected IPv4 source from
// the node. NAT-backed nodes can correctly return a private address here.
// @Summary 自动识别IPv6隧道客户端IPv4
// @Description 按隧道服务端IPv4查询节点路由的src字段；未提供服务端时查询默认IPv4出站路由。
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param body body ipv6tunnel.DetectLocalIPv4Request false "可选的隧道服务端IPv4"
// @Success 200 {object} common.Response "识别成功"
// @Router /admin/providers/{id}/ipv6-tunnels/detect-local-ipv4 [post]
func DetectProviderIPv6TunnelLocalIPv4(c *gin.Context) {
	providerID, ok := ownedIPv6TunnelProvider(c)
	if !ok {
		return
	}
	var request ipv6tunnel.DetectLocalIPv4Request
	if err := json.NewDecoder(c.Request.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}
	detection, err := ipv6tunnel.NewService().DetectLocalIPv4(c.Request.Context(), providerID, request.RemoteIPv4)
	if err != nil {
		responseIPv6TunnelError(c, err)
		return
	}
	common.ResponseSuccess(c, detection, "客户端IPv4识别成功")
}

func responseIPv6TunnelError(c *gin.Context, err error) {
	if ipv6tunnel.IsRemoteCommandError(err) {
		// A CDN can replace an origin 502 page with its own generic gateway
		// response, hiding the node diagnostics that an operator needs. 424 is
		// the accurate dependency-failure status and preserves the JSON details.
		common.ResponseWithError(c, common.NewErrorWithMessage(common.CodeFailedDependency, "IPv6隧道节点操作失败", err.Error()))
		return
	}
	common.ResponseWithError(c, common.ClassifyError(err))
}

func ownedIPv6TunnelProvider(c *gin.Context) (uint, bool) {
	providerID, err := parseProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return 0, false
	}
	if err := ensureProviderOwner(c, providerID); err != nil {
		common.ResponseWithError(c, err)
		return 0, false
	}
	return providerID, true
}

func ownedIPv6Tunnel(c *gin.Context) (uint, uint, bool) {
	providerID, ok := ownedIPv6TunnelProvider(c)
	if !ok {
		return 0, 0, false
	}
	tunnelID, err := strconv.ParseUint(c.Param("tunnel_id"), 10, 64)
	if err != nil || tunnelID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的隧道ID"))
		return 0, 0, false
	}
	return providerID, uint(tunnelID), true
}
