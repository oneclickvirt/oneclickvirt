package snapshot

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	providerSvc "oneclickvirt/service/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	StatusCreating  = "creating"
	StatusAvailable = "available"
	StatusFailed    = "failed"
)

type Service struct{}

type ListFilter struct {
	Page         int
	PageSize     int
	ProviderID   uint
	InstanceID   uint
	UserID       uint
	ProviderType string
	Status       string
}

type SnapshotRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ScheduleRequest struct {
	InstanceID    uint   `json:"instanceId" binding:"required"`
	Name          string `json:"name" binding:"required"`
	Enabled       *bool  `json:"enabled"`
	IntervalHours int    `json:"intervalHours"`
	RetentionDays int    `json:"retentionDays"`
	MaxSnapshots  int    `json:"maxSnapshots"`
}

type ScheduleUpdateRequest struct {
	Name          string `json:"name"`
	Enabled       *bool  `json:"enabled"`
	IntervalHours int    `json:"intervalHours"`
	RetentionDays int    `json:"retentionDays"`
	MaxSnapshots  int    `json:"maxSnapshots"`
}

type Overview struct {
	Total          int64                 `json:"total"`
	Available      int64                 `json:"available"`
	Failed         int64                 `json:"failed"`
	ByProviderType map[string]int64      `json:"byProviderType"`
	ByInstance     []InstanceSnapshotSum `json:"byInstance"`
	Schedules      int64                 `json:"schedules"`
}

type InstanceSnapshotSum struct {
	InstanceID   uint   `json:"instanceId"`
	InstanceName string `json:"instanceName"`
	Count        int64  `json:"count"`
}

func (s *Service) ListSnapshots(filter ListFilter) ([]providerModel.InstanceSnapshot, int64, error) {
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	db := global.APP_DB.Model(&providerModel.InstanceSnapshot{})
	if filter.ProviderID > 0 {
		db = db.Where("provider_id = ?", filter.ProviderID)
	}
	if filter.InstanceID > 0 {
		db = db.Where("instance_id = ?", filter.InstanceID)
	}
	if filter.UserID > 0 {
		db = db.Where("user_id = ?", filter.UserID)
	}
	if filter.ProviderType != "" {
		db = db.Where("provider_type = ?", filter.ProviderType)
	}
	if filter.Status != "" {
		db = db.Where("status = ?", filter.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var snapshots []providerModel.InstanceSnapshot
	err := db.Order("created_at DESC").Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&snapshots).Error
	return snapshots, total, err
}

func (s *Service) Overview() (*Overview, error) {
	result := &Overview{ByProviderType: map[string]int64{}, ByInstance: []InstanceSnapshotSum{}}
	if err := global.APP_DB.Model(&providerModel.InstanceSnapshot{}).Count(&result.Total).Error; err != nil {
		return nil, err
	}
	if err := global.APP_DB.Model(&providerModel.InstanceSnapshot{}).Where("status = ?", StatusAvailable).Count(&result.Available).Error; err != nil {
		return nil, err
	}
	if err := global.APP_DB.Model(&providerModel.InstanceSnapshot{}).Where("status = ?", StatusFailed).Count(&result.Failed).Error; err != nil {
		return nil, err
	}
	var providerRows []struct {
		ProviderType string
		Count        int64
	}
	if err := global.APP_DB.Model(&providerModel.InstanceSnapshot{}).Select("provider_type, count(*) as count").Group("provider_type").Scan(&providerRows).Error; err != nil {
		return nil, err
	}
	for _, row := range providerRows {
		result.ByProviderType[row.ProviderType] = row.Count
	}
	if err := global.APP_DB.Model(&providerModel.InstanceSnapshot{}).
		Select("instance_id, instance_name, count(*) as count").
		Group("instance_id, instance_name").
		Order("count DESC").
		Limit(20).
		Scan(&result.ByInstance).Error; err != nil {
		return nil, err
	}
	if err := global.APP_DB.Model(&providerModel.SnapshotSchedule{}).Count(&result.Schedules).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) CreateSnapshot(ctx context.Context, instanceID uint, req SnapshotRequest, createdBy uint, source string) (*providerModel.InstanceSnapshot, error) {
	inst, err := loadInstance(instanceID)
	if err != nil {
		return nil, err
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

	snapshotName := sanitizeName(req.Name)
	if snapshotName == "" {
		snapshotName = fmt.Sprintf("snap-%s", time.Now().Format("20060102150405"))
	}
	providerType := providerTypeForInstance(*inst)
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

	cmd, err := buildSnapshotCommand("create", *inst, *snapshot)
	if err != nil {
		markSnapshotFailed(snapshot.ID, err)
		return snapshot, err
	}
	if err := executeProviderCommand(ctx, inst.ProviderID, cmd); err != nil {
		markSnapshotFailed(snapshot.ID, err)
		return snapshot, err
	}
	if err := global.APP_DB.Model(snapshot).Updates(map[string]interface{}{"status": StatusAvailable, "error_message": ""}).Error; err != nil {
		return snapshot, err
	}
	snapshot.Status = StatusAvailable
	return snapshot, nil
}

func (s *Service) DeleteSnapshot(ctx context.Context, snapshotID uint) error {
	var snapshot providerModel.InstanceSnapshot
	if err := global.APP_DB.First(&snapshot, snapshotID).Error; err != nil {
		return err
	}
	inst, err := loadInstance(snapshot.InstanceID)
	if err != nil {
		return err
	}
	cmd, err := buildSnapshotCommand("delete", *inst, snapshot)
	if err != nil {
		return err
	}
	if err := executeProviderCommand(ctx, inst.ProviderID, cmd); err != nil {
		return err
	}
	return global.APP_DB.Delete(&snapshot).Error
}

func (s *Service) RestoreSnapshot(ctx context.Context, snapshotID uint) error {
	var snapshot providerModel.InstanceSnapshot
	if err := global.APP_DB.First(&snapshot, snapshotID).Error; err != nil {
		return err
	}
	if snapshot.Status != StatusAvailable {
		return fmt.Errorf("只有可用快照才能恢复")
	}
	inst, err := loadInstance(snapshot.InstanceID)
	if err != nil {
		return err
	}
	cmd, err := buildSnapshotCommand("restore", *inst, snapshot)
	if err != nil {
		return err
	}
	return executeProviderCommand(ctx, inst.ProviderID, cmd)
}

func (s *Service) ListSchedules(page, pageSize int) ([]providerModel.SnapshotSchedule, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	var total int64
	db := global.APP_DB.Model(&providerModel.SnapshotSchedule{})
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var schedules []providerModel.SnapshotSchedule
	err := db.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&schedules).Error
	return schedules, total, err
}

func (s *Service) CreateSchedule(req ScheduleRequest, createdBy uint) (*providerModel.SnapshotSchedule, error) {
	inst, err := loadInstance(req.InstanceID)
	if err != nil {
		return nil, err
	}
	interval := req.IntervalHours
	if interval <= 0 {
		interval = 24
	}
	retention := req.RetentionDays
	if retention <= 0 {
		retention = 7
	}
	maxSnapshots := req.MaxSnapshots
	if maxSnapshots <= 0 {
		maxSnapshots = 3
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	nextRun := time.Now().Add(time.Duration(interval) * time.Hour)
	providerType := providerTypeForInstance(*inst)
	schedule := &providerModel.SnapshotSchedule{
		ProviderID:    inst.ProviderID,
		InstanceID:    inst.ID,
		UserID:        inst.UserID,
		ProviderType:  providerType,
		InstanceType:  strings.ToLower(strings.TrimSpace(inst.InstanceType)),
		InstanceName:  inst.Name,
		Name:          req.Name,
		Enabled:       enabled,
		IntervalHours: interval,
		RetentionDays: retention,
		MaxSnapshots:  maxSnapshots,
		NextRunAt:     &nextRun,
		CreatedBy:     createdBy,
	}
	return schedule, global.APP_DB.Create(schedule).Error
}

func (s *Service) UpdateSchedule(id uint, req ScheduleUpdateRequest) (*providerModel.SnapshotSchedule, error) {
	var schedule providerModel.SnapshotSchedule
	if err := global.APP_DB.First(&schedule, id).Error; err != nil {
		return nil, err
	}
	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.IntervalHours > 0 {
		updates["interval_hours"] = req.IntervalHours
		nextRun := time.Now().Add(time.Duration(req.IntervalHours) * time.Hour)
		updates["next_run_at"] = &nextRun
	}
	if req.RetentionDays > 0 {
		updates["retention_days"] = req.RetentionDays
	}
	if req.MaxSnapshots > 0 {
		updates["max_snapshots"] = req.MaxSnapshots
	}
	if len(updates) == 0 {
		return &schedule, nil
	}
	if err := global.APP_DB.Model(&schedule).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &schedule, nil
}

func (s *Service) DeleteSchedule(id uint) error {
	return global.APP_DB.Delete(&providerModel.SnapshotSchedule{}, id).Error
}

func (s *Service) RunDueSchedules(ctx context.Context) {
	now := time.Now()
	var schedules []providerModel.SnapshotSchedule
	if err := global.APP_DB.Where("enabled = ? AND next_run_at IS NOT NULL AND next_run_at <= ?", true, now).Find(&schedules).Error; err != nil {
		global.APP_LOG.Warn("查询计划快照失败", zap.Error(err))
		return
	}
	for _, schedule := range schedules {
		s.runOneSchedule(ctx, schedule)
	}
}

func (s *Service) runOneSchedule(ctx context.Context, schedule providerModel.SnapshotSchedule) {
	name := fmt.Sprintf("%s-%s", sanitizeName(schedule.Name), time.Now().Format("20060102150405"))
	_, err := s.CreateSnapshot(ctx, schedule.InstanceID, SnapshotRequest{Name: name, Description: "scheduled snapshot"}, schedule.CreatedBy, "scheduled")
	updates := map[string]interface{}{}
	now := time.Now()
	updates["last_run_at"] = &now
	interval := schedule.IntervalHours
	if interval <= 0 {
		interval = 24
	}
	nextRun := now.Add(time.Duration(interval) * time.Hour)
	updates["next_run_at"] = &nextRun
	if err != nil {
		updates["last_error"] = err.Error()
	} else {
		updates["last_error"] = ""
		if schedule.RetentionDays > 0 || schedule.MaxSnapshots > 0 {
			s.pruneScheduledSnapshots(ctx, schedule)
		}
	}
	if updateErr := global.APP_DB.Model(&schedule).Updates(updates).Error; updateErr != nil {
		global.APP_LOG.Warn("更新计划快照状态失败", zap.Uint("scheduleID", schedule.ID), zap.Error(updateErr))
	}
}

func (s *Service) pruneScheduledSnapshots(ctx context.Context, schedule providerModel.SnapshotSchedule) {
	query := global.APP_DB.Where("instance_id = ? AND source = ? AND status = ?", schedule.InstanceID, "scheduled", StatusAvailable)
	if schedule.RetentionDays > 0 {
		cutoff := time.Now().Add(-time.Duration(schedule.RetentionDays) * 24 * time.Hour)
		var expired []providerModel.InstanceSnapshot
		if err := query.Where("created_at < ?", cutoff).Find(&expired).Error; err == nil {
			for _, snapshot := range expired {
				_ = s.DeleteSnapshot(ctx, snapshot.ID)
			}
		}
	}
	if schedule.MaxSnapshots > 0 {
		var snapshots []providerModel.InstanceSnapshot
		if err := global.APP_DB.Where("instance_id = ? AND source = ? AND status = ?", schedule.InstanceID, "scheduled", StatusAvailable).Order("created_at DESC").Find(&snapshots).Error; err == nil && len(snapshots) > schedule.MaxSnapshots {
			for _, snapshot := range snapshots[schedule.MaxSnapshots:] {
				_ = s.DeleteSnapshot(ctx, snapshot.ID)
			}
		}
	}
}

func loadInstance(id uint) (*providerModel.Instance, error) {
	var inst providerModel.Instance
	if err := global.APP_DB.First(&inst, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("实例不存在")
		}
		return nil, err
	}
	return &inst, nil
}

func providerTypeForInstance(inst providerModel.Instance) string {
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Select("type").First(&dbProvider, inst.ProviderID).Error; err == nil {
		return strings.ToLower(strings.TrimSpace(dbProvider.Type))
	}
	return strings.ToLower(strings.TrimSpace(inst.Provider))
}

func maxSnapshotsForUser(userID uint) (int, error) {
	if userID == 0 {
		return 3, nil
	}
	var user userModel.User
	if err := global.APP_DB.Select("id", "level").First(&user, userID).Error; err != nil {
		return 0, err
	}
	limits := global.GetAppConfig().Quota.LevelLimits
	limit, ok := limits[user.Level]
	if !ok {
		keys := make([]int, 0, len(limits))
		for key := range limits {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		if len(keys) == 0 {
			return 3, nil
		}
		limit = limits[keys[0]]
	}
	if limit.MaxSnapshots == 0 {
		return 0, nil
	}
	return limit.MaxSnapshots, nil
}

func markSnapshotFailed(id uint, err error) {
	_ = global.APP_DB.Model(&providerModel.InstanceSnapshot{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        StatusFailed,
		"error_message": err.Error(),
	}).Error
}

func executeProviderCommand(ctx context.Context, providerID uint, command string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	providerAPI := &providerSvc.ProviderApiService{}
	prov, _, err := providerAPI.GetProviderByIDForOperation(providerID, "")
	if err != nil {
		return err
	}
	_, err = prov.ExecuteSSHCommand(cmdCtx, command)
	return err
}

func buildSnapshotCommand(action string, inst providerModel.Instance, snap providerModel.InstanceSnapshot) (string, error) {
	providerType := strings.ToLower(strings.TrimSpace(snap.ProviderType))
	if providerType == "" {
		providerType = strings.ToLower(strings.TrimSpace(inst.Provider))
	}
	instanceType := strings.ToLower(strings.TrimSpace(inst.InstanceType))
	target := instanceTargetName(providerType, inst)
	snapName := sanitizeName(snap.Name)
	if snapName == "" {
		return "", fmt.Errorf("快照名称不能为空")
	}
	description := shellQuote(snap.Description)
	switch providerType {
	case "lxd":
		return lxcSnapshotCommand("lxc", action, target, snapName), nil
	case "incus":
		return lxcSnapshotCommand("incus", action, target, snapName), nil
	case "proxmox":
		tool := "pct"
		if instanceType == "vm" {
			tool = "qm"
		}
		return proxmoxSnapshotCommand(tool, action, target, snapName, description), nil
	case "qemu":
		return qemuSnapshotCommand(action, target, snapName, description, instanceType), nil
	case "docker", "podman":
		return ociSnapshotCommand(providerType, action, target, snapName), nil
	case "containerd":
		return "", fmt.Errorf("containerd 快照暂不支持从业务层安全恢复，请使用镜像/卷备份策略")
	case "kubevirt":
		return kubevirtSnapshotCommand(action, target, snapName), nil
	default:
		return "", fmt.Errorf("Provider %s 暂未实现快照命令", providerType)
	}
}

func instanceTargetName(providerType string, inst providerModel.Instance) string {
	if providerType == "proxmox" && strings.TrimSpace(inst.ProviderVMID) != "" {
		return inst.ProviderVMID
	}
	return inst.Name
}

func lxcSnapshotCommand(binary, action, instanceName, snapshotName string) string {
	i := shellQuote(instanceName)
	s := shellQuote(snapshotName)
	switch action {
	case "create":
		return fmt.Sprintf("%s snapshot %s %s", binary, i, s)
	case "delete":
		return fmt.Sprintf("%s delete %s/%s", binary, i, s)
	case "restore":
		return fmt.Sprintf("%s restore %s %s", binary, i, s)
	default:
		return ""
	}
}

func proxmoxSnapshotCommand(tool, action, instanceName, snapshotName, description string) string {
	i := shellQuote(instanceName)
	s := shellQuote(snapshotName)
	switch action {
	case "create":
		if tool == "qm" {
			return fmt.Sprintf("qm snapshot %s %s --description %s", i, s, description)
		}
		return fmt.Sprintf("pct snapshot %s %s --description %s", i, s, description)
	case "delete":
		if tool == "qm" {
			return fmt.Sprintf("qm delsnapshot %s %s --force 1", i, s)
		}
		return fmt.Sprintf("pct delsnapshot %s %s --force 1", i, s)
	case "restore":
		if tool == "qm" {
			return fmt.Sprintf("qm rollback %s %s", i, s)
		}
		return fmt.Sprintf("pct rollback %s %s", i, s)
	default:
		return ""
	}
}

func qemuSnapshotCommand(action, domainName, snapshotName, description, instanceType string) string {
	uri := "qemu:///system"
	if instanceType == "container" {
		uri = "lxc:///"
	}
	u := shellQuote(uri)
	d := shellQuote(domainName)
	s := shellQuote(snapshotName)
	switch action {
	case "create":
		return fmt.Sprintf("virsh -c %s snapshot-create-as --domain %s --name %s --description %s --atomic", u, d, s, description)
	case "delete":
		return fmt.Sprintf("virsh -c %s snapshot-delete --domain %s --snapshotname %s --metadata", u, d, s)
	case "restore":
		return fmt.Sprintf("virsh -c %s snapshot-revert --domain %s --snapshotname %s", u, d, s)
	default:
		return ""
	}
}

func ociSnapshotCommand(binary, action, containerName, snapshotName string) string {
	tag := fmt.Sprintf("oneclickvirt-snapshots/%s:%s", sanitizeName(containerName), snapshotName)
	switch action {
	case "create":
		return fmt.Sprintf("%s commit %s %s", binary, shellQuote(containerName), shellQuote(tag))
	case "delete":
		return fmt.Sprintf("%s image rm -f %s", binary, shellQuote(tag))
	case "restore":
		return fmt.Sprintf("echo %s && exit 1", shellQuote("Docker/Podman 快照已保存为镜像，安全恢复需要原实例创建参数，暂不自动替换正在运行的实例"))
	default:
		return ""
	}
}

func kubevirtSnapshotCommand(action, vmName, snapshotName string) string {
	vm := shellQuote(vmName)
	snap := shellQuote(snapshotName)
	switch action {
	case "create":
		manifest := fmt.Sprintf(`apiVersion: snapshot.kubevirt.io/v1beta1
kind: VirtualMachineSnapshot
metadata:
  name: %s
spec:
  source:
    apiGroup: kubevirt.io
    kind: VirtualMachine
    name: %s
`, sanitizeName(snapshotName), sanitizeName(vmName))
		return fmt.Sprintf("cat <<'EOF' | kubectl apply -f -\n%sEOF", manifest)
	case "delete":
		return fmt.Sprintf("kubectl delete virtualmachinesnapshot %s --ignore-not-found", snap)
	case "restore":
		return fmt.Sprintf("virtctl vmrestore %s %s", vm, snap)
	default:
		return ""
	}
}

var invalidNameRe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func sanitizeName(name string) string {
	clean := strings.Trim(invalidNameRe.ReplaceAllString(name, "-"), "-._")
	if len(clean) > 80 {
		clean = clean[:80]
	}
	return clean
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
