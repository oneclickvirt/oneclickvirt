package snapshot

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/taskgate"
	"oneclickvirt/service/userquota"

	"gorm.io/gorm"
)

const maxBatchSnapshotInstances = 100

type BatchSnapshotRequest struct {
	InstanceIDs []uint `json:"instanceIds" binding:"required,min=1"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type BatchSnapshotItemResult struct {
	InstanceID   uint                            `json:"instanceId"`
	InstanceName string                          `json:"instanceName"`
	Task         *adminModel.Task                `json:"task,omitempty"`
	SnapshotTask *providerModel.SnapshotTask     `json:"snapshotTask,omitempty"`
	Snapshot     *providerModel.InstanceSnapshot `json:"snapshot,omitempty"`
	Error        string                          `json:"error,omitempty"`
}

type BatchSnapshotResponse struct {
	Total   int                       `json:"total"`
	Success int                       `json:"success"`
	Failed  int                       `json:"failed"`
	Results []BatchSnapshotItemResult `json:"results"`
}

type SnapshotDownloadManifest struct {
	ID           uint      `json:"id"`
	UUID         string    `json:"uuid"`
	ProviderID   uint      `json:"providerId"`
	InstanceID   uint      `json:"instanceId"`
	UserID       uint      `json:"userId"`
	ProviderType string    `json:"providerType"`
	InstanceType string    `json:"instanceType"`
	InstanceName string    `json:"instanceName"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Status       string    `json:"status"`
	Source       string    `json:"source"`
	SizeBytes    int64     `json:"sizeBytes"`
	Metadata     string    `json:"metadata"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Note         string    `json:"note"`
}

func (s *Service) startCreateSnapshotTaskForLoaded(inst *providerModel.Instance, req SnapshotRequest, createdBy uint, source string, schedule *providerModel.SnapshotSchedule, providerTypeOverride string) (*SnapshotTaskResponse, error) {
	if inst == nil {
		return nil, fmt.Errorf("实例不存在")
	}
	quota, err := maxSnapshotsForUser(inst.UserID)
	if err != nil {
		return nil, err
	}
	if quota <= 0 {
		return nil, fmt.Errorf("当前用户等级未开放快照配额")
	}
	var count int64
	if err := global.APP_DB.Model(&providerModel.InstanceSnapshot{}).
		Where("instance_id = ? AND status IN ?", inst.ID, []string{StatusCreating, StatusAvailable}).Count(&count).Error; err != nil {
		return nil, err
	}
	if int(count) >= quota {
		return nil, fmt.Errorf("快照数量已达用户等级配额上限 %d", quota)
	}
	if providerTypeOverride == "" {
		providerTypeOverride = providerTypeForInstance(*inst)
	}
	return s.startCreateSnapshotTaskPrepared(inst, req, createdBy, source, schedule, providerTypeOverride)
}

func (s *Service) startCreateSnapshotTaskPrepared(inst *providerModel.Instance, req SnapshotRequest, createdBy uint, source string, schedule *providerModel.SnapshotSchedule, providerType string) (*SnapshotTaskResponse, error) {
	if inst == nil {
		return nil, fmt.Errorf("实例不存在")
	}
	snapshotName := sanitizeName(req.Name)
	if snapshotName == "" {
		snapshotName = fmt.Sprintf("snap-%s", time.Now().Format("20060102150405"))
	}
	if providerType == "" {
		providerType = providerTypeForInstance(*inst)
	}
	snapshot := &providerModel.InstanceSnapshot{
		ProviderID:   inst.ProviderID,
		InstanceID:   inst.ID,
		UserID:       inst.UserID,
		ProviderType: providerType,
		InstanceType: strings.ToLower(strings.TrimSpace(inst.InstanceType)),
		InstanceName: inst.Name,
		Name:         snapshotName,
		Description:  req.Description,
		Status:       StatusCreating,
		Source:       source,
		CreatedBy:    createdBy,
	}
	if err := global.APP_DB.Create(snapshot).Error; err != nil {
		return nil, err
	}

	task := &providerModel.SnapshotTask{
		ProviderID:  inst.ProviderID,
		InstanceID:  inst.ID,
		SnapshotID:  snapshot.ID,
		UserID:      inst.UserID,
		Action:      SnapshotTaskActionCreate,
		Status:      SnapshotTaskStatusPending,
		Source:      source,
		Name:        snapshotName,
		Description: req.Description,
		CreatedBy:   createdBy,
	}
	if schedule != nil {
		task.ScheduleID = schedule.ID
	}
	if err := global.APP_DB.Create(task).Error; err != nil {
		markSnapshotFailed(snapshot.ID, err)
		return nil, err
	}
	adminTask, err := s.createUnifiedSnapshotTask(task, snapshot, SnapshotTaskActionCreate, createdBy)
	if err != nil {
		markSnapshotFailed(snapshot.ID, err)
		_ = global.APP_DB.Model(task).Updates(map[string]interface{}{
			"status":        SnapshotTaskStatusFailed,
			"error_message": err.Error(),
		}).Error
		return nil, err
	}

	return &SnapshotTaskResponse{Task: adminTask, SnapshotTask: task, Snapshot: snapshot}, nil
}

func (s *Service) StartCreateSnapshotTaskForUser(instanceID uint, req SnapshotRequest, userID uint) (*SnapshotTaskResponse, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, err
	}
	inst, err := loadUserInstance(instanceID, userID)
	if err != nil {
		return nil, err
	}
	return s.startCreateSnapshotTaskForLoaded(inst, req, userID, "manual", nil, "")
}

func (s *Service) StartCreateSnapshotTaskForAdmin(instanceID uint, req SnapshotRequest, createdBy uint, ownerAdminID uint) (*SnapshotTaskResponse, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, err
	}
	inst, err := loadAdminInstance(instanceID, ownerAdminID)
	if err != nil {
		return nil, err
	}
	return s.startCreateSnapshotTaskForLoaded(inst, req, createdBy, "manual", nil, "")
}

func (s *Service) StartBatchCreateSnapshotTasks(req BatchSnapshotRequest, createdBy uint, ownerAdminID uint) (*BatchSnapshotResponse, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, err
	}
	ids := normalizeBatchIDs(req.InstanceIDs)
	if len(ids) == 0 {
		return nil, fmt.Errorf("请选择实例")
	}
	if len(ids) > maxBatchSnapshotInstances {
		return nil, fmt.Errorf("一次最多选择 %d 个实例", maxBatchSnapshotInstances)
	}

	instances, providerTypes, quotas, counts, err := s.loadBatchSnapshotContext(ids, ownerAdminID)
	if err != nil {
		return nil, err
	}
	response := &BatchSnapshotResponse{Total: len(ids), Results: make([]BatchSnapshotItemResult, 0, len(ids))}
	for _, id := range ids {
		inst, ok := instances[id]
		item := BatchSnapshotItemResult{InstanceID: id}
		if ok {
			item.InstanceName = inst.Name
		}
		if !ok {
			item.Error = "实例不存在或无权限"
			response.Failed++
			response.Results = append(response.Results, item)
			continue
		}
		quota := quotas[inst.UserID]
		if quota <= 0 {
			item.Error = "当前用户等级未开放快照配额"
			response.Failed++
			response.Results = append(response.Results, item)
			continue
		}
		if int(counts[inst.ID]) >= quota {
			item.Error = fmt.Sprintf("快照数量已达用户等级配额上限 %d", quota)
			response.Failed++
			response.Results = append(response.Results, item)
			continue
		}
		result, err := s.startCreateSnapshotTaskPrepared(inst, SnapshotRequest{Name: req.Name, Description: req.Description}, createdBy, "manual", nil, providerTypes[inst.ProviderID])
		if err != nil {
			item.Error = err.Error()
			response.Failed++
		} else {
			item.Task = result.Task
			item.SnapshotTask = result.SnapshotTask
			item.Snapshot = result.Snapshot
			response.Success++
			counts[inst.ID]++
		}
		response.Results = append(response.Results, item)
	}
	return response, nil
}

func (s *Service) ListUserInstanceSnapshots(userID uint, instanceID uint, page int, pageSize int) ([]providerModel.InstanceSnapshot, int64, error) {
	if _, err := loadUserInstance(instanceID, userID); err != nil {
		return nil, 0, err
	}
	return s.ListSnapshots(ListFilter{Page: page, PageSize: pageSize, InstanceID: instanceID, UserID: userID})
}

func (s *Service) ListAdminInstanceSnapshots(instanceID uint, page int, pageSize int, ownerAdminID uint) ([]providerModel.InstanceSnapshot, int64, error) {
	if _, err := loadAdminInstance(instanceID, ownerAdminID); err != nil {
		return nil, 0, err
	}
	return s.ListSnapshots(ListFilter{Page: page, PageSize: pageSize, InstanceID: instanceID})
}

func (s *Service) ListAdminSnapshots(filter ListFilter, ownerAdminID uint) ([]providerModel.InstanceSnapshot, int64, error) {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	db := global.APP_DB.Model(&providerModel.InstanceSnapshot{})
	if ownerAdminID > 0 {
		db = db.Joins("JOIN providers ON providers.id = instance_snapshots.provider_id AND providers.owner_admin_id = ?", ownerAdminID)
	}
	if filter.ProviderID > 0 {
		db = db.Where("instance_snapshots.provider_id = ?", filter.ProviderID)
	}
	if filter.InstanceID > 0 {
		db = db.Where("instance_snapshots.instance_id = ?", filter.InstanceID)
	}
	if filter.UserID > 0 {
		db = db.Where("instance_snapshots.user_id = ?", filter.UserID)
	}
	if filter.ProviderType != "" {
		db = db.Where("instance_snapshots.provider_type = ?", filter.ProviderType)
	}
	if filter.Status != "" {
		db = db.Where("instance_snapshots.status = ?", filter.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var snapshots []providerModel.InstanceSnapshot
	err := db.Order("instance_snapshots.created_at DESC").Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&snapshots).Error
	return snapshots, total, err
}

func (s *Service) StartDeleteSnapshotTaskForUser(snapshotID uint, userID uint) (*SnapshotTaskResponse, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, err
	}
	snapshot, err := loadUserSnapshot(snapshotID, userID)
	if err != nil {
		return nil, err
	}
	return s.startSnapshotOperationTask(snapshot, SnapshotTaskActionDelete, userID)
}

func (s *Service) StartRestoreSnapshotTaskForUser(snapshotID uint, userID uint) (*SnapshotTaskResponse, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, err
	}
	snapshot, err := loadUserSnapshot(snapshotID, userID)
	if err != nil {
		return nil, err
	}
	return s.startSnapshotOperationTask(snapshot, SnapshotTaskActionRestore, userID)
}

func (s *Service) StartDeleteSnapshotTaskForAdmin(snapshotID uint, createdBy uint, ownerAdminID uint) (*SnapshotTaskResponse, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, err
	}
	snapshot, err := loadAdminSnapshot(snapshotID, ownerAdminID)
	if err != nil {
		return nil, err
	}
	return s.startSnapshotOperationTask(snapshot, SnapshotTaskActionDelete, createdBy)
}

func (s *Service) StartRestoreSnapshotTaskForAdmin(snapshotID uint, createdBy uint, ownerAdminID uint) (*SnapshotTaskResponse, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, err
	}
	snapshot, err := loadAdminSnapshot(snapshotID, ownerAdminID)
	if err != nil {
		return nil, err
	}
	return s.startSnapshotOperationTask(snapshot, SnapshotTaskActionRestore, createdBy)
}

func (s *Service) startSnapshotOperationTask(snapshot *providerModel.InstanceSnapshot, action string, createdBy uint) (*SnapshotTaskResponse, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("快照不存在")
	}
	if action == SnapshotTaskActionRestore && snapshot.Status != StatusAvailable {
		return nil, fmt.Errorf("只有可用快照才能恢复")
	}
	task := &providerModel.SnapshotTask{
		ProviderID: snapshot.ProviderID,
		InstanceID: snapshot.InstanceID,
		SnapshotID: snapshot.ID,
		UserID:     snapshot.UserID,
		Action:     action,
		Status:     SnapshotTaskStatusPending,
		Source:     snapshot.Source,
		Name:       snapshot.Name,
		CreatedBy:  createdBy,
	}
	if err := global.APP_DB.Create(task).Error; err != nil {
		return nil, err
	}
	adminTask, err := s.createUnifiedSnapshotTask(task, snapshot, action, createdBy)
	if err != nil {
		_ = global.APP_DB.Model(task).Updates(map[string]interface{}{"status": SnapshotTaskStatusFailed, "error_message": err.Error()}).Error
		return nil, err
	}
	return &SnapshotTaskResponse{Task: adminTask, SnapshotTask: task, Snapshot: snapshot}, nil
}

func (s *Service) BuildSnapshotDownloadManifest(snapshotID uint, userID uint, ownerAdminID uint) ([]byte, string, error) {
	var snapshot *providerModel.InstanceSnapshot
	var err error
	if userID > 0 {
		snapshot, err = loadUserSnapshot(snapshotID, userID)
	} else {
		snapshot, err = loadAdminSnapshot(snapshotID, ownerAdminID)
	}
	if err != nil {
		return nil, "", err
	}
	manifest := SnapshotDownloadManifest{
		ID:           snapshot.ID,
		UUID:         snapshot.UUID,
		ProviderID:   snapshot.ProviderID,
		InstanceID:   snapshot.InstanceID,
		UserID:       snapshot.UserID,
		ProviderType: snapshot.ProviderType,
		InstanceType: snapshot.InstanceType,
		InstanceName: snapshot.InstanceName,
		Name:         snapshot.Name,
		Description:  snapshot.Description,
		Status:       snapshot.Status,
		Source:       snapshot.Source,
		SizeBytes:    snapshot.SizeBytes,
		Metadata:     snapshot.Metadata,
		CreatedAt:    snapshot.CreatedAt,
		UpdatedAt:    snapshot.UpdatedAt,
		Note:         "通用虚拟化快照通常保存在对应 Provider 后端，本文件为可下载的快照元数据清单。",
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, "", err
	}
	filename := fmt.Sprintf("snapshot-%s-%s.json", sanitizeName(snapshot.InstanceName), sanitizeName(snapshot.Name))
	return payload, filename, nil
}

func loadUserInstance(instanceID uint, userID uint) (*providerModel.Instance, error) {
	var inst providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", instanceID, userID).First(&inst).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("实例不存在或无权限")
		}
		return nil, err
	}
	return &inst, nil
}

func loadAdminInstance(instanceID uint, ownerAdminID uint) (*providerModel.Instance, error) {
	var inst providerModel.Instance
	db := global.APP_DB.Model(&providerModel.Instance{})
	if ownerAdminID > 0 {
		db = db.Joins("JOIN providers ON providers.id = instances.provider_id AND providers.owner_admin_id = ?", ownerAdminID)
	}
	if err := db.Where("instances.id = ?", instanceID).First(&inst).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("实例不存在或无权限")
		}
		return nil, err
	}
	return &inst, nil
}

func loadUserSnapshot(snapshotID uint, userID uint) (*providerModel.InstanceSnapshot, error) {
	var snapshot providerModel.InstanceSnapshot
	if err := global.APP_DB.Where("id = ? AND user_id = ?", snapshotID, userID).First(&snapshot).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("快照不存在或无权限")
		}
		return nil, err
	}
	return &snapshot, nil
}

func loadAdminSnapshot(snapshotID uint, ownerAdminID uint) (*providerModel.InstanceSnapshot, error) {
	var snapshot providerModel.InstanceSnapshot
	db := global.APP_DB.Model(&providerModel.InstanceSnapshot{})
	if ownerAdminID > 0 {
		db = db.Joins("JOIN providers ON providers.id = instance_snapshots.provider_id AND providers.owner_admin_id = ?", ownerAdminID)
	}
	if err := db.Where("instance_snapshots.id = ?", snapshotID).First(&snapshot).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("快照不存在或无权限")
		}
		return nil, err
	}
	return &snapshot, nil
}

func normalizeBatchIDs(input []uint) []uint {
	seen := map[uint]struct{}{}
	ids := make([]uint, 0, len(input))
	for _, id := range input {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *Service) loadBatchSnapshotContext(ids []uint, ownerAdminID uint) (map[uint]*providerModel.Instance, map[uint]string, map[uint]int, map[uint]int64, error) {
	instances := []providerModel.Instance{}
	db := global.APP_DB.Model(&providerModel.Instance{})
	if ownerAdminID > 0 {
		db = db.Joins("JOIN providers ON providers.id = instances.provider_id AND providers.owner_admin_id = ?", ownerAdminID)
	}
	if err := db.Where("instances.id IN ?", ids).Find(&instances).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	instanceMap := make(map[uint]*providerModel.Instance, len(instances))
	providerIDs := map[uint]struct{}{}
	userIDs := map[uint]struct{}{}
	for i := range instances {
		inst := &instances[i]
		instanceMap[inst.ID] = inst
		providerIDs[inst.ProviderID] = struct{}{}
		if inst.UserID > 0 {
			userIDs[inst.UserID] = struct{}{}
		}
	}

	providerTypes := make(map[uint]string, len(providerIDs))
	if len(providerIDs) > 0 {
		providerIDList := make([]uint, 0, len(providerIDs))
		for id := range providerIDs {
			providerIDList = append(providerIDList, id)
		}
		var providers []providerModel.Provider
		if err := global.APP_DB.Select("id", "type").Where("id IN ?", providerIDList).Find(&providers).Error; err != nil {
			return nil, nil, nil, nil, err
		}
		for _, provider := range providers {
			providerTypes[provider.ID] = strings.ToLower(strings.TrimSpace(provider.Type))
		}
	}

	quotas := map[uint]int{0: 3}
	if len(userIDs) > 0 {
		userIDList := make([]uint, 0, len(userIDs))
		for id := range userIDs {
			userIDList = append(userIDList, id)
		}
		var users []userModel.User
		if err := global.APP_DB.Select("id", "level").Where("id IN ?", userIDList).Find(&users).Error; err != nil {
			return nil, nil, nil, nil, err
		}
		for _, usr := range users {
			limit, err := userquota.ResolveLevelLimit(usr.Level)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			quotas[usr.ID] = limit.MaxSnapshots
		}
	}

	counts := make(map[uint]int64, len(ids))
	var rows []struct {
		InstanceID uint
		Count      int64
	}
	if err := global.APP_DB.Model(&providerModel.InstanceSnapshot{}).
		Select("instance_id, count(*) as count").
		Where("instance_id IN ? AND status IN ?", ids, []string{StatusCreating, StatusAvailable}).
		Group("instance_id").Scan(&rows).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	for _, row := range rows {
		counts[row.InstanceID] = row.Count
	}
	return instanceMap, providerTypes, quotas, counts, nil
}

func (s *Service) ListSchedulesForAdmin(page int, pageSize int, ownerAdminID uint) ([]providerModel.SnapshotSchedule, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	db := global.APP_DB.Model(&providerModel.SnapshotSchedule{})
	if ownerAdminID > 0 {
		db = db.Joins("JOIN providers ON providers.id = snapshot_schedules.provider_id AND providers.owner_admin_id = ?", ownerAdminID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var schedules []providerModel.SnapshotSchedule
	err := db.Order("snapshot_schedules.created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&schedules).Error
	return schedules, total, err
}

func (s *Service) CreateScheduleForAdmin(req ScheduleRequest, createdBy uint, ownerAdminID uint) (*providerModel.SnapshotSchedule, error) {
	if ownerAdminID > 0 {
		if _, err := loadAdminInstance(req.InstanceID, ownerAdminID); err != nil {
			return nil, err
		}
	}
	return s.CreateSchedule(req, createdBy)
}

func (s *Service) UpdateScheduleForAdmin(id uint, req ScheduleUpdateRequest, ownerAdminID uint) (*providerModel.SnapshotSchedule, error) {
	if _, err := loadAdminSchedule(id, ownerAdminID); err != nil {
		return nil, err
	}
	return s.UpdateSchedule(id, req)
}

func (s *Service) DeleteScheduleForAdmin(id uint, ownerAdminID uint) error {
	if _, err := loadAdminSchedule(id, ownerAdminID); err != nil {
		return err
	}
	return s.DeleteSchedule(id)
}

func loadAdminSchedule(scheduleID uint, ownerAdminID uint) (*providerModel.SnapshotSchedule, error) {
	var schedule providerModel.SnapshotSchedule
	db := global.APP_DB.Model(&providerModel.SnapshotSchedule{})
	if ownerAdminID > 0 {
		db = db.Joins("JOIN providers ON providers.id = snapshot_schedules.provider_id AND providers.owner_admin_id = ?", ownerAdminID)
	}
	if err := db.Where("snapshot_schedules.id = ?", scheduleID).First(&schedule).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("计划快照不存在或无权限")
		}
		return nil, err
	}
	return &schedule, nil
}

func (s *Service) OverviewForAdmin(ownerAdminID uint) (*Overview, error) {
	if ownerAdminID == 0 {
		return s.Overview()
	}
	result := &Overview{ByProviderType: map[string]int64{}, ByInstance: []InstanceSnapshotSum{}}
	base := func() *gorm.DB {
		return global.APP_DB.Model(&providerModel.InstanceSnapshot{}).
			Joins("JOIN providers ON providers.id = instance_snapshots.provider_id AND providers.owner_admin_id = ?", ownerAdminID)
	}
	if err := base().Count(&result.Total).Error; err != nil {
		return nil, err
	}
	if err := base().Where("instance_snapshots.status = ?", StatusAvailable).Count(&result.Available).Error; err != nil {
		return nil, err
	}
	if err := base().Where("instance_snapshots.status = ?", StatusFailed).Count(&result.Failed).Error; err != nil {
		return nil, err
	}
	var providerRows []struct {
		ProviderType string
		Count        int64
	}
	if err := base().Select("instance_snapshots.provider_type, count(*) as count").Group("instance_snapshots.provider_type").Scan(&providerRows).Error; err != nil {
		return nil, err
	}
	for _, row := range providerRows {
		result.ByProviderType[row.ProviderType] = row.Count
	}
	if err := base().
		Select("instance_snapshots.instance_id, instance_snapshots.instance_name, count(*) as count").
		Group("instance_snapshots.instance_id, instance_snapshots.instance_name").
		Order("count DESC").
		Limit(20).
		Scan(&result.ByInstance).Error; err != nil {
		return nil, err
	}
	if err := global.APP_DB.Model(&providerModel.SnapshotSchedule{}).
		Joins("JOIN providers ON providers.id = snapshot_schedules.provider_id AND providers.owner_admin_id = ?", ownerAdminID).
		Count(&result.Schedules).Error; err != nil {
		return nil, err
	}
	return result, nil
}
