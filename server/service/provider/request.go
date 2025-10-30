package provider

import (
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type RequestProcessService struct{}

// ProcessUserListRequest 处理用户列表请求参数，设置默认值和处理size参数
func (s *RequestProcessService) ProcessUserListRequest(c *gin.Context, req *admin.UserListRequest) error {
	if err := c.ShouldBindQuery(req); err != nil {
		return err
	}

	// 手动处理 size 参数，兼容前端发送的 size 参数
	if req.PageSize == 0 {
		if sizeStr := c.Query("size"); sizeStr != "" {
			if size, err := strconv.Atoi(sizeStr); err == nil {
				req.PageSize = size
			}
		}
	}

	// 设置默认值
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 10
	}

	return nil
}

// ProcessInstanceListRequest 处理实例列表请求参数
func (s *RequestProcessService) ProcessInstanceListRequest(c *gin.Context, req *admin.InstanceListRequest) error {
	// 设置默认值
	req.Page = 1
	req.PageSize = 10

	// 尝试绑定参数，如果失败也不返回错误，使用默认值
	if err := c.ShouldBindQuery(req); err != nil {
		global.APP_LOG.Warn("实例列表查询参数绑定失败，使用默认值", zap.Error(err))
	}

	// 确保页码和页大小的合理性
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 10
	}

	return nil
}
