package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"oneclickvirt/global"
	commonModel "oneclickvirt/model/common"
	firewallModel "oneclickvirt/model/firewall"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/database"
	"oneclickvirt/service/taskgate"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct{}

var allowedBlockRuleCategories = map[string]struct{}{
	string(firewallModel.BlockRuleCategoryMining):    {},
	string(firewallModel.BlockRuleCategoryBT):        {},
	string(firewallModel.BlockRuleCategorySpeedtest): {},
	string(firewallModel.BlockRuleCategoryCustom):    {},
}

func normalizeRuleStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if len(v) > 128 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func validateRuleCategory(category string) bool {
	_, ok := allowedBlockRuleCategories[category]
	return ok
}

// DefaultBlockRules returns the built-in rule templates.
func DefaultBlockRules() []firewallModel.BlockRule {
	miningStrings, _ := json.Marshal([]string{
		"pool.bar", "antpool.com", "antpool.one", "ethermine.org", "ethermine.com",
		"c3pool", "xmrig.com", "blackcat.host", "minexmr.com", "supportxmr.com",
		"monerohash.com", "hashvault.pro", "xmrpool.eu", "minergate.com",
		"webminepool.com", "nanopool.org", "2miners.com", "f2pool.com",
		"sparkpool.com", "nicehash.com", "prohashing.com", "coinhive.com",
		"coinimp.com", "cryptoloot.pro", "xmrig", "xmr-stak", "cpuminer",
		"cgminer", "ethminer", "stratum+tcp", "stratum+ssl", "stratum+http",
		"stratum", "raw.githubusercontent.com/xmrig", "github.com/xmrig",
	})
	btStrings, _ := json.Marshal([]string{
		"BitTorrent", "BitTorrent protocol", ".torrent",
		"d1:ad2:id20", "d1:rd2:id20", "ut_metadata", "ut_pex",
		"lt_metadata", "lt_donthave", "qBittorrent", "Transmission",
		"Deluge", "aria2", "libtorrent", "uTorrent", "BiglyBT",
		"Vuze", "xunlei", "Thunder", "XLLiveUD", "magnet:",
	})
	speedtestStrings, _ := json.Marshal([]string{
		"speedtest", "fast.com", "speedtest.net", "speedtest.com", "speedtest.cn",
		"ookla.com", "speedtestcustom.com", "ovo.speedtestcustom.com",
		"speed.cloudflare.com", "test.ustc.edu.cn", "10000.gd.cn",
		"db.laomoe.com", "jiyou.cloud", "mirrors.ustc.edu.cn",
		"mirrors.tuna.tsinghua.edu.cn", "mirrors.aliyun.com",
		".speed", ".speed.", "/speedtest", "/speed-test",
	})

	return []firewallModel.BlockRule{
		{
			Name:        "block_mining",
			Category:    string(firewallModel.BlockRuleCategoryMining),
			Description: "Block cryptocurrency mining activities",
			Strings:     string(miningStrings),
			IsBuiltin:   true,
			Enabled:     true,
		},
		{
			Name:        "block_bt",
			Category:    string(firewallModel.BlockRuleCategoryBT),
			Description: "Block BitTorrent/P2P activities",
			Strings:     string(btStrings),
			IsBuiltin:   true,
			Enabled:     true,
		},
		{
			Name:        "block_speedtest",
			Category:    string(firewallModel.BlockRuleCategorySpeedtest),
			Description: "Block speed test activities",
			Strings:     string(speedtestStrings),
			IsBuiltin:   true,
			Enabled:     true,
		},
	}
}

// EnsureDefaultRules creates built-in rules if they don't exist.
func (s *Service) EnsureDefaultRules() error {
	dbService := database.GetDatabaseService()
	defaults := DefaultBlockRules()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		for _, rule := range defaults {
			var existing firewallModel.BlockRule
			if err := tx.Where("name = ?", rule.Name).First(&existing).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					if err := tx.Create(&rule).Error; err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// ListRules returns all block rules.
func (s *Service) ListRules() ([]firewallModel.BlockRule, error) {
	var rules []firewallModel.BlockRule
	if err := global.APP_DB.Order("category, name").Find(&rules).Error; err != nil {
		return nil, err
	}
	return rules, nil
}

// GetRule returns a single rule by ID.
func (s *Service) GetRule(id uint) (*firewallModel.BlockRule, error) {
	var rule firewallModel.BlockRule
	if err := global.APP_DB.First(&rule, id).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

// CreateRule creates a new block rule.
func (s *Service) CreateRule(req *firewallModel.CreateBlockRuleRequest) (*firewallModel.BlockRule, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, commonModel.NewError(commonModel.CodeValidationError, "规则名称不能为空")
	}
	if len(name) > 64 {
		return nil, commonModel.NewError(commonModel.CodeValidationError, "规则名称长度不能超过64")
	}
	category := strings.TrimSpace(req.Category)
	if !validateRuleCategory(category) {
		return nil, commonModel.NewError(commonModel.CodeValidationError, "无效的规则分类")
	}
	normalizedStrings := normalizeRuleStrings(req.Strings)
	if len(normalizedStrings) == 0 {
		return nil, commonModel.NewError(commonModel.CodeValidationError, "规则内容不能为空")
	}

	var existing firewallModel.BlockRule
	if err := global.APP_DB.Where("name = ?", name).First(&existing).Error; err == nil {
		return nil, commonModel.NewError(commonModel.CodeConflict, "规则名称已存在")
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	description := strings.TrimSpace(req.Description)
	if len(description) > 512 {
		return nil, commonModel.NewError(commonModel.CodeValidationError, "规则描述长度不能超过512")
	}

	stringsJSON, err := json.Marshal(normalizedStrings)
	if err != nil {
		return nil, fmt.Errorf("marshal strings: %w", err)
	}
	rule := &firewallModel.BlockRule{
		Name:        name,
		Category:    category,
		Description: description,
		Strings:     string(stringsJSON),
		Enabled:     req.Enabled,
	}
	if err := global.APP_DB.Create(rule).Error; err != nil {
		return nil, err
	}
	return rule, nil
}

// UpdateRule updates an existing block rule.
func (s *Service) UpdateRule(id uint, req *firewallModel.UpdateBlockRuleRequest) (*firewallModel.BlockRule, error) {
	var rule firewallModel.BlockRule
	if err := global.APP_DB.First(&rule, id).Error; err != nil {
		return nil, err
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, commonModel.NewError(commonModel.CodeValidationError, "规则名称不能为空")
		}
		if len(name) > 64 {
			return nil, commonModel.NewError(commonModel.CodeValidationError, "规则名称长度不能超过64")
		}
		var dupCount int64
		if err := global.APP_DB.Model(&firewallModel.BlockRule{}).
			Where("name = ? AND id <> ?", name, id).
			Count(&dupCount).Error; err != nil {
			return nil, err
		}
		if dupCount > 0 {
			return nil, commonModel.NewError(commonModel.CodeConflict, "规则名称已存在")
		}
		rule.Name = name
	}
	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		if len(description) > 512 {
			return nil, commonModel.NewError(commonModel.CodeValidationError, "规则描述长度不能超过512")
		}
		rule.Description = description
	}
	if req.Strings != nil {
		normalizedStrings := normalizeRuleStrings(req.Strings)
		if len(normalizedStrings) == 0 {
			return nil, commonModel.NewError(commonModel.CodeValidationError, "规则内容不能为空")
		}
		stringsJSON, err := json.Marshal(normalizedStrings)
		if err != nil {
			return nil, fmt.Errorf("marshal strings: %w", err)
		}
		rule.Strings = string(stringsJSON)
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if err := global.APP_DB.Save(&rule).Error; err != nil {
		return nil, err
	}
	var applications []firewallModel.BlockRuleApplication
	if err := global.APP_DB.Where("rule_id = ?", rule.ID).Find(&applications).Error; err != nil {
		return nil, err
	}
	providerIDs, err := s.resolveApplicationProviders(applications)
	if err != nil {
		return nil, err
	}
	if len(providerIDs) > 0 {
		go s.resyncProviders(context.Background(), providerIDs)
	}
	return &rule, nil
}

// DeleteRule deletes a block rule and all its applications.
func (s *Service) DeleteRule(id uint) error {
	var applications []firewallModel.BlockRuleApplication
	if err := global.APP_DB.Where("rule_id = ?", id).Find(&applications).Error; err != nil {
		return err
	}
	providerIDs, err := s.resolveApplicationProviders(applications)
	if err != nil {
		return err
	}
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("rule_id = ?", id).Delete(&firewallModel.BlockRuleApplication{}).Error; err != nil {
			return err
		}
		return tx.Delete(&firewallModel.BlockRule{}, id).Error
	}); err != nil {
		return err
	}
	if len(providerIDs) > 0 {
		go s.resyncProviders(context.Background(), providerIDs)
	}
	return nil
}

// ApplyRules applies block rules to targets and executes them on the agent.
func (s *Service) ApplyRules(ctx context.Context, req *firewallModel.ApplyBlockRuleRequest) ([]firewallModel.BlockRuleApplication, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, err
	}

	req.RuleIDs = uniqueIDs(req.RuleIDs)
	req.TargetIDs = uniqueIDs(req.TargetIDs)
	if len(req.RuleIDs) == 0 {
		return nil, commonModel.NewError(commonModel.CodeValidationError, "规则ID不能为空")
	}
	if req.Scope != "global" && len(req.TargetIDs) == 0 {
		return nil, commonModel.NewError(commonModel.CodeValidationError, "目标ID不能为空")
	}
	targetCount := len(req.TargetIDs)
	if req.Scope == "global" {
		targetCount = 1
	}
	if len(req.RuleIDs)*targetCount > 5000 {
		return nil, commonModel.NewError(commonModel.CodeTooLarge, "单次最多创建5000条规则应用")
	}

	var rules []firewallModel.BlockRule
	if err := global.APP_DB.Where("id IN ? AND enabled = ?", req.RuleIDs, true).Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}
	if len(rules) != len(req.RuleIDs) {
		return nil, commonModel.NewError(commonModel.CodeValidationError, "部分规则不存在或未启用")
	}

	providerIDs, err := s.resolveTargetProviders(req)
	if err != nil {
		return nil, err
	}

	targetIDs := req.TargetIDs
	if req.Scope == "global" {
		targetIDs = []uint{0}
	}
	targetNameMap, err := s.resolveTargetNames(req.Scope, targetIDs)
	if err != nil {
		return nil, err
	}

	ipVersion := req.IPVersion
	if ipVersion == "" {
		ipVersion = "both"
	}
	applications := make([]firewallModel.BlockRuleApplication, 0, len(rules)*len(targetIDs))
	for _, rule := range rules {
		for _, targetID := range targetIDs {
			applications = append(applications, firewallModel.BlockRuleApplication{
				RuleID:     rule.ID,
				Scope:      req.Scope,
				TargetID:   targetID,
				TargetName: targetNameMap[targetID],
				Status:     "pending",
				IPVersion:  ipVersion,
			})
		}
	}

	db := global.APP_DB.WithContext(ctx)
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "rule_id"}, {Name: "scope"}, {Name: "target_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "ip_version", "target_name"}),
	}).CreateInBatches(&applications, 200).Error; err != nil {
		return nil, err
	}
	if err := db.Where("rule_id IN ? AND scope = ? AND target_id IN ?", req.RuleIDs, req.Scope, targetIDs).
		Order("rule_id, target_id").
		Find(&applications).Error; err != nil {
		return nil, err
	}

	if len(providerIDs) == 0 {
		appIDs := make(map[uint]struct{}, len(applications))
		for _, application := range applications {
			appIDs[application.ID] = struct{}{}
		}
		if err := updateApplicationStatuses(appIDs, nil); err != nil {
			return nil, err
		}
	} else {
		go s.resyncProviders(context.Background(), providerIDs)
	}
	return applications, nil
}

// RemoveApplications removes specific rule applications and re-syncs agents.
func (s *Service) RemoveApplications(ctx context.Context, req *firewallModel.RemoveBlockRuleApplicationRequest) error {
	if err := taskgate.EnsureAccepting(); err != nil {
		return err
	}

	var apps []firewallModel.BlockRuleApplication
	applicationIDs := uniqueIDs(req.ApplicationIDs)
	if len(applicationIDs) == 0 {
		return commonModel.NewError(commonModel.CodeValidationError, "应用ID不能为空")
	}
	if err := global.APP_DB.Where("id IN ?", applicationIDs).Find(&apps).Error; err != nil {
		return err
	}
	if len(apps) != len(applicationIDs) {
		return commonModel.NewError(commonModel.CodeNotFound, "部分规则应用不存在")
	}
	providerIDs, err := s.resolveApplicationProviders(apps)
	if err != nil {
		return err
	}
	if err := global.APP_DB.Unscoped().Where("id IN ?", applicationIDs).Delete(&firewallModel.BlockRuleApplication{}).Error; err != nil {
		return err
	}
	if len(providerIDs) > 0 {
		go s.resyncProviders(context.Background(), providerIDs)
	}
	return nil
}

// ListApplications returns all rule applications, optionally filtered by rule ID.
func (s *Service) ListApplications(ruleID, ownerAdminID uint) ([]firewallModel.BlockRuleApplication, error) {
	var apps []firewallModel.BlockRuleApplication
	db := global.APP_DB
	if ownerAdminID > 0 {
		providerIDs := global.APP_DB.Model(&providerModel.Provider{}).Select("id").Where("owner_admin_id = ?", ownerAdminID)
		instanceIDs := global.APP_DB.Model(&providerModel.Instance{}).Select("id").Where("provider_id IN (?)", providerIDs)
		db = db.Where("scope = 'global' OR (scope = 'provider' AND target_id IN (?)) OR (scope = 'instance' AND target_id IN (?))", providerIDs, instanceIDs)
	}
	if ruleID > 0 {
		db = db.Where("rule_id = ?", ruleID)
	}
	if err := db.Order("rule_id, scope, target_id").Find(&apps).Error; err != nil {
		return nil, err
	}
	return apps, nil
}

// GetProviderBlockStatus returns which rules are applied to a specific provider.
func (s *Service) GetProviderBlockStatus(providerID uint) ([]map[string]interface{}, error) {
	var apps []firewallModel.BlockRuleApplication
	if err := global.APP_DB.Where(
		"(scope = 'global' AND target_id = 0) OR (scope = 'provider' AND target_id = ?)",
		providerID,
	).Find(&apps).Error; err != nil {
		return nil, err
	}

	ruleIDs := make([]uint, 0)
	for _, app := range apps {
		ruleIDs = append(ruleIDs, app.RuleID)
	}
	if len(ruleIDs) == 0 {
		return []map[string]interface{}{}, nil
	}

	var rules []firewallModel.BlockRule
	if err := global.APP_DB.Where("id IN ?", ruleIDs).Find(&rules).Error; err != nil {
		return nil, err
	}
	ruleMap := make(map[uint]firewallModel.BlockRule)
	for _, r := range rules {
		ruleMap[r.ID] = r
	}

	result := make([]map[string]interface{}, 0, len(apps))
	for _, app := range apps {
		rule, ok := ruleMap[app.RuleID]
		if !ok {
			continue
		}
		result = append(result, map[string]interface{}{
			"application_id": app.ID,
			"rule_id":        rule.ID,
			"rule_name":      rule.Name,
			"category":       rule.Category,
			"scope":          app.Scope,
			"status":         app.Status,
		})
	}
	return result, nil
}

// GetAgentEnabledProviders returns providers with agent monitoring enabled (excluding deleted providers).
func (s *Service) GetAgentEnabledProviders(ownerAdminID uint) ([]map[string]interface{}, error) {
	var configs []monitoringModel.MonitoringConfig
	if err := global.APP_DB.Where("agent_installed = ? AND monitoring_mode = ?", true, "agent").
		Select("provider_id").Find(&configs).Error; err != nil {
		return nil, err
	}
	if len(configs) == 0 {
		return []map[string]interface{}{}, nil
	}
	// Filter out providers that no longer exist and get their names
	candidateIDs := make([]uint, 0, len(configs))
	for _, c := range configs {
		candidateIDs = append(candidateIDs, c.ProviderID)
	}
	var providers []providerModel.Provider
	providerQuery := global.APP_DB.Where("id IN ?", candidateIDs)
	if ownerAdminID > 0 {
		providerQuery = providerQuery.Where("owner_admin_id = ?", ownerAdminID)
	}
	if err := providerQuery.Select("id, name").Find(&providers).Error; err != nil {
		return nil, err
	}
	result := make([]map[string]interface{}, 0, len(providers))
	for _, p := range providers {
		result = append(result, map[string]interface{}{
			"id":   p.ID,
			"name": p.Name,
		})
	}
	return result, nil
}

// resolveTargetProviders determines which provider IDs are affected by the scope.
func (s *Service) resolveTargetProviders(req *firewallModel.ApplyBlockRuleRequest) ([]uint, error) {
	switch req.Scope {
	case "global":
		var providerIDs []uint
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Joins("JOIN monitoring_configs ON monitoring_configs.provider_id = providers.id").
			Where("monitoring_configs.agent_installed = ? AND monitoring_configs.monitoring_mode = ?", true, "agent").
			Distinct().
			Pluck("providers.id", &providerIDs).Error; err != nil {
			return nil, err
		}
		return providerIDs, nil
	case "provider":
		return req.TargetIDs, nil
	case "instance":
		var instances []struct{ ProviderID uint }
		if err := global.APP_DB.Model(&providerModel.Instance{}).
			Select("DISTINCT provider_id").
			Where("id IN ?", req.TargetIDs).
			Scan(&instances).Error; err != nil {
			return nil, err
		}
		ids := make([]uint, 0, len(instances))
		for _, inst := range instances {
			ids = append(ids, inst.ProviderID)
		}
		return ids, nil
	case "user":
		var instances []struct{ ProviderID uint }
		if err := global.APP_DB.Model(&providerModel.Instance{}).
			Select("DISTINCT provider_id").
			Where("user_id IN ?", req.TargetIDs).
			Scan(&instances).Error; err != nil {
			return nil, err
		}
		ids := make([]uint, 0, len(instances))
		for _, inst := range instances {
			ids = append(ids, inst.ProviderID)
		}
		return ids, nil
	}
	return nil, fmt.Errorf("unknown scope: %s", req.Scope)
}

func (s *Service) resolveTargetNames(scope string, targetIDs []uint) (map[uint]string, error) {
	names := make(map[uint]string, len(targetIDs))
	switch scope {
	case "global":
		names[0] = "All Nodes"
	case "provider":
		var providers []providerModel.Provider
		if err := global.APP_DB.Select("id, name").Where("id IN ?", targetIDs).Find(&providers).Error; err != nil {
			return nil, err
		}
		for _, provider := range providers {
			names[provider.ID] = provider.Name
		}
	case "instance":
		var instances []providerModel.Instance
		if err := global.APP_DB.Select("id, name").Where("id IN ?", targetIDs).Find(&instances).Error; err != nil {
			return nil, err
		}
		for _, instance := range instances {
			names[instance.ID] = instance.Name
		}
	case "user":
		var users []userModel.User
		if err := global.APP_DB.Select("id, username").Where("id IN ?", targetIDs).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, user := range users {
			names[user.ID] = user.Username
		}
	default:
		return nil, commonModel.NewError(commonModel.CodeValidationError, "无效的规则范围")
	}
	if len(names) != len(targetIDs) {
		return nil, commonModel.NewError(commonModel.CodeNotFound, "部分规则目标不存在")
	}
	return names, nil
}

func uniqueIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

// --- Exported helper functions for cross-service use ---

// CleanupInstanceApplications removes all block rule applications targeting a specific instance
// and resyncs all provider agents to update actual firewall rules.
func CleanupInstanceApplications(instanceID uint) {
	var applications []firewallModel.BlockRuleApplication
	_ = global.APP_DB.Where("scope = ? AND target_id = ?", "instance", instanceID).Find(&applications).Error
	svc := &Service{}
	providerIDs, _ := svc.resolveApplicationProviders(applications)
	result := global.APP_DB.Unscoped().Where("scope = ? AND target_id = ?", "instance", instanceID).
		Delete(&firewallModel.BlockRuleApplication{})
	if result.Error != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("清理实例封禁规则应用失败",
				zap.Uint("instance_id", instanceID),
				zap.Error(result.Error))
		}
		return
	}
	if result.RowsAffected > 0 {
		if global.APP_LOG != nil {
			global.APP_LOG.Info("已清理实例封禁规则应用",
				zap.Uint("instance_id", instanceID),
				zap.Int64("count", result.RowsAffected))
		}
		go svc.resyncProviders(context.Background(), providerIDs)
	}
}

// CleanupProviderApplications removes all block rule applications for a provider and its instances,
// then resyncs remaining provider agents.
func CleanupProviderApplications(providerID uint, instanceIDs []uint) {
	var totalAffected int64

	result := global.APP_DB.Unscoped().Where("scope = ? AND target_id = ?", "provider", providerID).
		Delete(&firewallModel.BlockRuleApplication{})
	if result.Error == nil {
		totalAffected += result.RowsAffected
	}

	if len(instanceIDs) > 0 {
		result = global.APP_DB.Unscoped().Where("scope = ? AND target_id IN ?", "instance", instanceIDs).
			Delete(&firewallModel.BlockRuleApplication{})
		if result.Error == nil {
			totalAffected += result.RowsAffected
		}
	}

	if totalAffected > 0 {
		if global.APP_LOG != nil {
			global.APP_LOG.Info("已清理Provider封禁规则应用",
				zap.Uint("provider_id", providerID),
				zap.Int64("count", totalAffected))
		}
		svc := &Service{}
		go svc.resyncProviders(context.Background(), []uint{providerID})
	}
}

// MigrateInstanceApplications updates block rule applications from old instance ID to new instance ID
// (used during instance rebuild/reset to maintain rule continuity).
func MigrateInstanceApplications(oldInstanceID, newInstanceID uint) {
	result := global.APP_DB.Model(&firewallModel.BlockRuleApplication{}).
		Where("scope = ? AND target_id = ?", "instance", oldInstanceID).
		Update("target_id", newInstanceID)
	if result.Error != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("迁移实例封禁规则应用失败",
				zap.Uint("old_instance_id", oldInstanceID),
				zap.Uint("new_instance_id", newInstanceID),
				zap.Error(result.Error))
		}
		return
	}
	if result.RowsAffected > 0 && global.APP_LOG != nil {
		global.APP_LOG.Info("已迁移实例封禁规则应用",
			zap.Uint("old_instance_id", oldInstanceID),
			zap.Uint("new_instance_id", newInstanceID),
			zap.Int64("count", result.RowsAffected))
	}
}

// ResyncAllProviders resyncs all provider agents' firewall rules (exported version).
// Call after instance rebuild/reset to ensure rules are correctly applied.
func ResyncAllProviders() {
	svc := &Service{}
	svc.resyncAllProviders(context.Background())
}
