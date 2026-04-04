package firewall

import (
	"context"
	"encoding/json"
	"fmt"

	"oneclickvirt/global"
	firewallModel "oneclickvirt/model/firewall"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/agent"
	"oneclickvirt/service/database"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Service struct{}

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
		"BitTorrent", "BitTorrent protocol", "BitTorrent protocol\\x13", "magnet:", ".torrent",
		"d1:ad2:id20", "d1:rd2:id20", "ut_metadata", "ut_pex",
		"lt_metadata", "lt_donthave", "qBittorrent", "Transmission",
		"Deluge", "aria2", "libtorrent", "uTorrent", "BiglyBT",
		"Vuze", "xunlei", "Thunder", "XLLiveUD",
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
	stringsJSON, err := json.Marshal(req.Strings)
	if err != nil {
		return nil, fmt.Errorf("marshal strings: %w", err)
	}
	rule := &firewallModel.BlockRule{
		Name:        req.Name,
		Category:    req.Category,
		Description: req.Description,
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
	if req.Name != "" {
		rule.Name = req.Name
	}
	if req.Description != "" {
		rule.Description = req.Description
	}
	if req.Strings != nil {
		stringsJSON, err := json.Marshal(req.Strings)
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
	return &rule, nil
}

// DeleteRule deletes a block rule and all its applications.
func (s *Service) DeleteRule(id uint) error {
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Where("rule_id = ?", id).Delete(&firewallModel.BlockRuleApplication{}).Error; err != nil {
			return err
		}
		return tx.Delete(&firewallModel.BlockRule{}, id).Error
	})
}

// ApplyRules applies block rules to targets and executes them on the agent.
func (s *Service) ApplyRules(ctx context.Context, req *firewallModel.ApplyBlockRuleRequest) ([]firewallModel.BlockRuleApplication, error) {
	var rules []firewallModel.BlockRule
	if err := global.APP_DB.Where("id IN ? AND enabled = ?", req.RuleIDs, true).Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("no enabled rules found")
	}

	providerIDs, err := s.resolveTargetProviders(req)
	if err != nil {
		return nil, err
	}

	// Pre-resolve all target names before entering the transaction
	targetIDs := req.TargetIDs
	if req.Scope == "global" {
		targetIDs = []uint{0}
	}
	targetNameMap := make(map[uint]string, len(targetIDs))
	for _, tid := range targetIDs {
		targetNameMap[tid] = s.resolveTargetName(req.Scope, tid)
	}

	dbService := database.GetDatabaseService()
	var applications []firewallModel.BlockRuleApplication
	err = dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		for _, rule := range rules {
			for _, targetID := range targetIDs {
				var existing firewallModel.BlockRuleApplication
				err := tx.Where("rule_id = ? AND scope = ? AND target_id = ?", rule.ID, req.Scope, targetID).
					First(&existing).Error
				if err == nil {
					existing.Status = "pending"
					if err := tx.Save(&existing).Error; err != nil {
						return err
					}
					applications = append(applications, existing)
					continue
				}
				app := firewallModel.BlockRuleApplication{
					RuleID:     rule.ID,
					Scope:      req.Scope,
					TargetID:   targetID,
					TargetName: targetNameMap[targetID],
					Status:     "pending",
				}
				if err := tx.Create(&app).Error; err != nil {
					return err
				}
				applications = append(applications, app)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	go func() {
		appIDs := make([]uint, 0, len(applications))
		for _, a := range applications {
			appIDs = append(appIDs, a.ID)
		}
		s.executeRulesOnProviders(context.Background(), rules, providerIDs, appIDs)
	}()
	return applications, nil
}

// RemoveApplications removes specific rule applications and re-syncs agents.
func (s *Service) RemoveApplications(ctx context.Context, req *firewallModel.RemoveBlockRuleApplicationRequest) error {
	var apps []firewallModel.BlockRuleApplication
	if err := global.APP_DB.Where("id IN ?", req.ApplicationIDs).Find(&apps).Error; err != nil {
		return err
	}
	if err := global.APP_DB.Where("id IN ?", req.ApplicationIDs).Delete(&firewallModel.BlockRuleApplication{}).Error; err != nil {
		return err
	}
	go s.resyncAllProviders(context.Background())
	return nil
}

// ListApplications returns all rule applications, optionally filtered by rule ID.
func (s *Service) ListApplications(ruleID uint) ([]firewallModel.BlockRuleApplication, error) {
	var apps []firewallModel.BlockRuleApplication
	db := global.APP_DB
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

// GetAgentEnabledProviders returns providers with agent monitoring enabled.
func (s *Service) GetAgentEnabledProviders() ([]uint, error) {
	var configs []monitoringModel.MonitoringConfig
	if err := global.APP_DB.Where("agent_installed = ? AND monitoring_mode = ?", true, "agent").
		Select("provider_id").Find(&configs).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(configs))
	for _, c := range configs {
		ids = append(ids, c.ProviderID)
	}
	return ids, nil
}

// resolveTargetProviders determines which provider IDs are affected by the scope.
func (s *Service) resolveTargetProviders(req *firewallModel.ApplyBlockRuleRequest) ([]uint, error) {
	switch req.Scope {
	case "global":
		var configs []monitoringModel.MonitoringConfig
		if err := global.APP_DB.Where("agent_installed = ? AND monitoring_mode = ?", true, "agent").Find(&configs).Error; err != nil {
			return nil, err
		}
		ids := make([]uint, 0, len(configs))
		for _, c := range configs {
			ids = append(ids, c.ProviderID)
		}
		return ids, nil
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

func (s *Service) resolveTargetName(scope string, targetID uint) string {
	switch scope {
	case "global":
		return "All Nodes"
	case "provider":
		var p providerModel.Provider
		if err := global.APP_DB.Select("name").First(&p, targetID).Error; err == nil {
			return p.Name
		}
	case "instance":
		var inst providerModel.Instance
		if err := global.APP_DB.Select("name").First(&inst, targetID).Error; err == nil {
			return inst.Name
		}
	case "user":
		return fmt.Sprintf("User #%d", targetID)
	}
	return ""
}

// executeRulesOnProviders sends block rules to all affected provider agents.
func (s *Service) executeRulesOnProviders(ctx context.Context, rules []firewallModel.BlockRule, providerIDs []uint, appIDs []uint) {
	allStrings := make([]string, 0)
	for _, rule := range rules {
		var strs []string
		if err := json.Unmarshal([]byte(rule.Strings), &strs); err != nil {
			continue
		}
		allStrings = append(allStrings, strs...)
	}
	for _, providerID := range providerIDs {
		s.applyBlockRulesToProvider(ctx, providerID, allStrings, appIDs)
	}
}

// applyBlockRulesToProvider sends the accumulated block strings to a single provider's agent.
func (s *Service) applyBlockRulesToProvider(ctx context.Context, providerID uint, blockStrings []string, appIDs []uint) {
	var config monitoringModel.MonitoringConfig
	if err := global.APP_DB.Where("provider_id = ?", providerID).First(&config).Error; err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("block rules: no monitoring config for provider",
				zap.Uint("provider_id", providerID))
		}
		return
	}
	if !config.AgentInstalled || config.MonitoringMode != "agent" {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("block rules: agent not installed or not in agent mode",
				zap.Uint("provider_id", providerID))
		}
		return
	}

	var p providerModel.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return
	}
	host := p.Endpoint
	if host == "" {
		host = p.PortIP
	}
	if host == "" {
		return
	}
	port := config.AgentPort
	if port == 0 {
		port = agent.AgentPort
	}
	client := agent.GetClient(providerID, host, port, config.AgentToken)
	if err := client.ApplyBlockRules(blockStrings); err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Error("failed to apply block rules to agent",
				zap.Uint("provider_id", providerID),
				zap.Error(err))
		}
		if len(appIDs) > 0 {
			global.APP_DB.Model(&firewallModel.BlockRuleApplication{}).
				Where("id IN ?", appIDs).
				Updates(map[string]interface{}{"status": "failed"})
		}
	} else {
		if global.APP_LOG != nil {
			global.APP_LOG.Info("block rules applied to agent",
				zap.Uint("provider_id", providerID),
				zap.Int("rule_count", len(blockStrings)))
		}
		if len(appIDs) > 0 {
			global.APP_DB.Model(&firewallModel.BlockRuleApplication{}).
				Where("id IN ?", appIDs).
				Updates(map[string]interface{}{"status": "applied"})
		}
	}
}

// resyncAllProviders collects all active rules and re-applies to all providers.
func (s *Service) resyncAllProviders(ctx context.Context) {
	var apps []firewallModel.BlockRuleApplication
	if err := global.APP_DB.Find(&apps).Error; err != nil {
		return
	}
	ruleIDs := make(map[uint]bool)
	for _, app := range apps {
		ruleIDs[app.RuleID] = true
	}
	if len(ruleIDs) == 0 {
		return
	}
	ids := make([]uint, 0, len(ruleIDs))
	for id := range ruleIDs {
		ids = append(ids, id)
	}

	var rules []firewallModel.BlockRule
	if err := global.APP_DB.Where("id IN ? AND enabled = ?", ids, true).Find(&rules).Error; err != nil {
		return
	}
	allStrings := make([]string, 0)
	for _, rule := range rules {
		var strs []string
		if err := json.Unmarshal([]byte(rule.Strings), &strs); err != nil {
			continue
		}
		allStrings = append(allStrings, strs...)
	}

	var configs []monitoringModel.MonitoringConfig
	if err := global.APP_DB.Where("agent_installed = ? AND monitoring_mode = ?", true, "agent").Find(&configs).Error; err != nil {
		return
	}
	for _, config := range configs {
		s.applyBlockRulesToProvider(ctx, config.ProviderID, allStrings, nil)
	}
}
