package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"oneclickvirt/global"
	firewallModel "oneclickvirt/model/firewall"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/agent"

	"go.uber.org/zap"
)

type effectiveApplication struct {
	ApplicationID uint
	Scope         string
	TargetID      uint
	IPVersion     string
	Strings       string
}

type providerRuleSet struct {
	strings        []string
	ipVersion      string
	applicationIDs []uint
	stringSet      map[string]struct{}
	applicationSet map[uint]struct{}
}

type providerAgentTarget struct {
	ProviderID     uint
	Endpoint       string
	PortIP         string
	ConnectionType string
	AgentToken     string
	AgentPort      int
	AgentInstalled bool
	MonitoringMode string
}

func (s *Service) resyncProviders(ctx context.Context, providerIDs []uint) {
	providerIDs = uniqueIDs(providerIDs)
	if len(providerIDs) == 0 {
		return
	}

	ruleSets, err := s.loadProviderRuleSets(ctx, providerIDs)
	if err != nil {
		s.logSyncError("加载节点有效屏蔽规则失败", 0, err)
		return
	}
	targets, err := s.loadProviderAgentTargets(ctx, providerIDs)
	if err != nil {
		s.logSyncError("加载节点Agent配置失败", 0, err)
		return
	}

	failedApps := make(map[uint]struct{})
	appliedApps := make(map[uint]struct{})
	for _, providerID := range providerIDs {
		select {
		case <-ctx.Done():
			s.logSyncError("同步节点屏蔽规则已取消", providerID, ctx.Err())
			return
		default:
		}

		ruleSet := ruleSets[providerID]
		for _, appID := range ruleSet.applicationIDs {
			appliedApps[appID] = struct{}{}
		}
		if err := s.syncProviderRuleSet(targets[providerID], providerID, ruleSet); err != nil {
			for _, appID := range ruleSet.applicationIDs {
				failedApps[appID] = struct{}{}
			}
			s.logSyncError("同步节点屏蔽规则失败", providerID, err)
		}
	}

	for appID := range failedApps {
		delete(appliedApps, appID)
	}
	if err := updateApplicationStatuses(appliedApps, failedApps); err != nil {
		s.logSyncError("更新屏蔽规则应用状态失败", 0, err)
	}
}

func (s *Service) loadProviderRuleSets(ctx context.Context, providerIDs []uint) (map[uint]*providerRuleSet, error) {
	db := global.APP_DB.WithContext(ctx)
	instanceIDs := db.Model(&providerModel.Instance{}).Select("id").Where("provider_id IN ?", providerIDs)
	userIDs := db.Model(&providerModel.Instance{}).Select("DISTINCT user_id").Where("provider_id IN ?", providerIDs)

	var apps []effectiveApplication
	err := db.Table("block_rule_applications AS applications").
		Select("applications.id AS application_id, applications.scope, applications.target_id, applications.ip_version, rules.strings").
		Joins("JOIN block_rules AS rules ON rules.id = applications.rule_id AND rules.deleted_at IS NULL AND rules.enabled = ?", true).
		Where("applications.deleted_at IS NULL").
		Where(`(applications.scope = 'global' AND applications.target_id = 0)
			OR (applications.scope = 'provider' AND applications.target_id IN ?)
			OR (applications.scope = 'instance' AND applications.target_id IN (?))
			OR (applications.scope = 'user' AND applications.target_id IN (?))`, providerIDs, instanceIDs, userIDs).
		Find(&apps).Error
	if err != nil {
		return nil, err
	}

	instanceTargets := make([]uint, 0)
	userTargets := make([]uint, 0)
	for _, app := range apps {
		switch app.Scope {
		case "instance":
			instanceTargets = append(instanceTargets, app.TargetID)
		case "user":
			userTargets = append(userTargets, app.TargetID)
		}
	}
	instanceTargets = uniqueIDs(instanceTargets)
	userTargets = uniqueIDs(userTargets)

	instanceProviders := make(map[uint]uint, len(instanceTargets))
	if len(instanceTargets) > 0 {
		var rows []struct {
			InstanceID uint
			ProviderID uint
		}
		if err := db.Unscoped().Table("instances").
			Select("id AS instance_id, provider_id").
			Where("id IN ?", instanceTargets).
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			instanceProviders[row.InstanceID] = row.ProviderID
		}
	}

	userProviders := make(map[uint][]uint, len(userTargets))
	if len(userTargets) > 0 {
		var rows []struct {
			UserID     uint
			ProviderID uint
		}
		if err := db.Model(&providerModel.Instance{}).
			Select("DISTINCT user_id, provider_id").
			Where("user_id IN ? AND provider_id IN ?", userTargets, providerIDs).
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			userProviders[row.UserID] = append(userProviders[row.UserID], row.ProviderID)
		}
	}

	sets := make(map[uint]*providerRuleSet, len(providerIDs))
	providerAllowed := make(map[uint]struct{}, len(providerIDs))
	for _, providerID := range providerIDs {
		providerAllowed[providerID] = struct{}{}
		sets[providerID] = newProviderRuleSet()
	}
	for _, app := range apps {
		var targets []uint
		switch app.Scope {
		case "global":
			targets = providerIDs
		case "provider":
			targets = []uint{app.TargetID}
		case "instance":
			targets = []uint{instanceProviders[app.TargetID]}
		case "user":
			targets = userProviders[app.TargetID]
		}
		for _, providerID := range targets {
			if _, exists := providerAllowed[providerID]; !exists {
				continue
			}
			if err := sets[providerID].add(app); err != nil {
				return nil, err
			}
		}
	}
	for _, set := range sets {
		sort.Strings(set.strings)
		sort.Slice(set.applicationIDs, func(i, j int) bool { return set.applicationIDs[i] < set.applicationIDs[j] })
	}
	return sets, nil
}

func newProviderRuleSet() *providerRuleSet {
	return &providerRuleSet{
		stringSet:      make(map[string]struct{}),
		applicationSet: make(map[uint]struct{}),
	}
}

func (set *providerRuleSet) add(app effectiveApplication) error {
	var values []string
	if err := json.Unmarshal([]byte(app.Strings), &values); err != nil {
		return fmt.Errorf("解析规则应用 %d 的匹配内容失败: %w", app.ApplicationID, err)
	}
	for _, value := range values {
		if _, exists := set.stringSet[value]; exists {
			continue
		}
		set.stringSet[value] = struct{}{}
		set.strings = append(set.strings, value)
	}
	if _, exists := set.applicationSet[app.ApplicationID]; !exists {
		set.applicationSet[app.ApplicationID] = struct{}{}
		set.applicationIDs = append(set.applicationIDs, app.ApplicationID)
	}
	set.ipVersion = mergeIPVersion(set.ipVersion, app.IPVersion)
	return nil
}

func mergeIPVersion(current, next string) string {
	normalize := func(value string) string {
		switch value {
		case "ipv4", "ipv6":
			return value
		default:
			return "both"
		}
	}
	if current == "" {
		return normalize(next)
	}
	current = normalize(current)
	next = normalize(next)
	if current == next {
		return current
	}
	return "both"
}

func (s *Service) loadProviderAgentTargets(ctx context.Context, providerIDs []uint) (map[uint]providerAgentTarget, error) {
	var rows []providerAgentTarget
	err := global.APP_DB.WithContext(ctx).Table("providers AS providers").
		Select(`providers.id AS provider_id, providers.endpoint, providers.port_ip, providers.connection_type,
			configs.agent_token, configs.agent_port, configs.agent_installed, configs.monitoring_mode`).
		Joins("LEFT JOIN monitoring_configs AS configs ON configs.provider_id = providers.id AND configs.deleted_at IS NULL").
		Where("providers.id IN ?", providerIDs).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	targets := make(map[uint]providerAgentTarget, len(rows))
	for _, row := range rows {
		targets[row.ProviderID] = row
	}
	return targets, nil
}

func (s *Service) syncProviderRuleSet(target providerAgentTarget, providerID uint, set *providerRuleSet) error {
	if target.ProviderID == 0 {
		return fmt.Errorf("节点不存在")
	}
	if !target.AgentInstalled || target.MonitoringMode != "agent" {
		return fmt.Errorf("节点未安装Agent或未启用Agent监控")
	}
	host := target.Endpoint
	if host == "" {
		host = target.PortIP
	}
	if host == "" {
		if target.ConnectionType != "agent" {
			return fmt.Errorf("节点Agent地址为空")
		}
		host = "127.0.0.1"
	}
	port := target.AgentPort
	if port == 0 {
		port = agent.AgentPort
	}
	client := agent.GetClient(providerID, host, port, target.AgentToken)
	if len(set.strings) == 0 {
		return client.RemoveBlockRules()
	}
	return client.ApplyBlockRules(set.strings, set.ipVersion)
}

func updateApplicationStatuses(applied, failed map[uint]struct{}) error {
	toIDs := func(values map[uint]struct{}) []uint {
		ids := make([]uint, 0, len(values))
		for id := range values {
			ids = append(ids, id)
		}
		return ids
	}
	if ids := toIDs(applied); len(ids) > 0 {
		if err := global.APP_DB.Model(&firewallModel.BlockRuleApplication{}).
			Where("id IN ?", ids).
			Update("status", "applied").Error; err != nil {
			return err
		}
	}
	if ids := toIDs(failed); len(ids) > 0 {
		if err := global.APP_DB.Model(&firewallModel.BlockRuleApplication{}).
			Where("id IN ?", ids).
			Update("status", "failed").Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) resolveApplicationProviders(apps []firewallModel.BlockRuleApplication) ([]uint, error) {
	providerIDs := make([]uint, 0)
	instanceIDs := make([]uint, 0)
	userIDs := make([]uint, 0)
	includeGlobal := false
	for _, app := range apps {
		switch app.Scope {
		case "global":
			includeGlobal = true
		case "provider":
			providerIDs = append(providerIDs, app.TargetID)
		case "instance":
			instanceIDs = append(instanceIDs, app.TargetID)
		case "user":
			userIDs = append(userIDs, app.TargetID)
		}
	}
	db := global.APP_DB
	if len(instanceIDs) > 0 {
		var ids []uint
		if err := db.Unscoped().Model(&providerModel.Instance{}).
			Where("id IN ?", uniqueIDs(instanceIDs)).
			Distinct().
			Pluck("provider_id", &ids).Error; err != nil {
			return nil, err
		}
		providerIDs = append(providerIDs, ids...)
	}
	if len(userIDs) > 0 {
		var ids []uint
		if err := db.Model(&providerModel.Instance{}).
			Where("user_id IN ?", uniqueIDs(userIDs)).
			Distinct().
			Pluck("provider_id", &ids).Error; err != nil {
			return nil, err
		}
		providerIDs = append(providerIDs, ids...)
	}
	if includeGlobal {
		var ids []uint
		if err := db.Model(&providerModel.Provider{}).
			Joins("JOIN monitoring_configs ON monitoring_configs.provider_id = providers.id AND monitoring_configs.deleted_at IS NULL").
			Where("monitoring_configs.agent_installed = ? AND monitoring_configs.monitoring_mode = ?", true, "agent").
			Distinct().
			Pluck("providers.id", &ids).Error; err != nil {
			return nil, err
		}
		providerIDs = append(providerIDs, ids...)
	}
	return uniqueIDs(providerIDs), nil
}

func (s *Service) resyncAllProviders(ctx context.Context) {
	var providerIDs []uint
	if err := global.APP_DB.WithContext(ctx).Model(&providerModel.Provider{}).
		Joins("JOIN monitoring_configs ON monitoring_configs.provider_id = providers.id AND monitoring_configs.deleted_at IS NULL").
		Where("monitoring_configs.agent_installed = ? AND monitoring_configs.monitoring_mode = ?", true, "agent").
		Distinct().
		Pluck("providers.id", &providerIDs).Error; err != nil {
		s.logSyncError("加载启用Agent的节点失败", 0, err)
		return
	}
	s.resyncProviders(ctx, providerIDs)
}

func (s *Service) logSyncError(message string, providerID uint, err error) {
	if global.APP_LOG == nil {
		return
	}
	fields := []zap.Field{zap.Error(err)}
	if providerID > 0 {
		fields = append(fields, zap.Uint("providerID", providerID))
	}
	global.APP_LOG.Warn(message, fields...)
}
