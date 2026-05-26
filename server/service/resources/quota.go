package resources

import (
	"errors"
	"math"
	"strings"
	"time"

	"oneclickvirt/service/database"

	"oneclickvirt/global"

	"gorm.io/gorm"
)

// QuotaService 资源配额验证服务
type QuotaService struct {
	dbService *database.DatabaseService // 数据库服务
}

// NewQuotaService 创建配额服务
func NewQuotaService() *QuotaService {
	return &QuotaService{
		dbService: database.GetDatabaseService(),
	}
}

// ResourceRequest 资源请求
type ResourceRequest struct {
	UserID       uint
	CPU          int
	Memory       int64
	Disk         int64
	Bandwidth    int // 带宽字段
	InstanceType string
	ProviderID   uint //  Provider ID 用于节点级限制检查
}

// QuotaCheckResult 配额检查结果
type QuotaCheckResult struct {
	Allowed           bool
	Reason            string
	CurrentInstances  int
	MaxInstances      int
	CurrentResources  ResourceUsage // 已确认使用的资源（稳定状态）
	PendingResources  ResourceUsage // 待确认的资源（创建中/重置中）
	MaxResources      ResourceUsage
	MaxQuota          ResourceUsage // MaxQuota字段
	RequiredResources ResourceUsage
}

// ResourceUsage 资源使用情况
type ResourceUsage struct {
	CPU       int
	Memory    int64
	Disk      int64
	Bandwidth int // 带宽字段
}

// GetResourceUsage 计算资源使用量（标准化计算方式）
func (r ResourceUsage) GetResourceUsage() int {
	// 统一的资源计算方式：CPU权重4，内存权重2，磁盘权重1
	// 这样可以更合理地反映资源价值
	return r.CPU*4 + int(r.Memory/512)*2 + int(r.Disk/5)*1
}

// ValidateInstanceCreation 验证实例创建请求
func (s *QuotaService) ValidateInstanceCreation(req ResourceRequest) (*QuotaCheckResult, error) {
	// 使用可序列化隔离级别的事务，防止幻读和并发超配
	var result *QuotaCheckResult
	var err error

	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		err = global.APP_DB.Transaction(func(tx *gorm.DB) error {
			var txErr error
			result, txErr = s.validateInTransaction(tx, req)
			if txErr != nil {
				return txErr
			}

			if !result.Allowed {
				return errors.New(result.Reason)
			}

			return nil
		})

		if err == nil {
			break
		}
		// REPEATABLE READ + SELECT FOR UPDATE 在高并发下仍可能产生死锁，自动重试
		errMsg := err.Error()
		if strings.Contains(errMsg, "1213") || strings.Contains(errMsg, "Deadlock") ||
			strings.Contains(errMsg, "1205") || strings.Contains(errMsg, "Lock wait timeout") {
			if attempt < maxRetries-1 {
				time.Sleep(time.Duration(math.Pow(2, float64(attempt))*100) * time.Millisecond)
				continue
			}
		}
		break
	}

	return result, err
}

// ValidateInTransaction 在事务中进行配额验证（公开方法）
func (s *QuotaService) ValidateInTransaction(tx *gorm.DB, req ResourceRequest) (*QuotaCheckResult, error) {
	return s.validateInTransaction(tx, req)
}
