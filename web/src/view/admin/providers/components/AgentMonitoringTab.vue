<template>
  <!-- Agent 说明 -->
  <el-alert
    :title="$t('admin.providers.agentMonitoringDescTitle')"
    type="success"
    :closable="false"
    show-icon
    style="margin-bottom: 16px;"
  >
    <template #default>
      <p style="margin: 4px 0 0;">
        {{ $t('admin.providers.agentMonitoringDesc') }}
      </p>
    </template>
  </el-alert>

  <!-- Agent 状态 -->
  <div class="agent-status-section">
    <el-descriptions
      :column="2"
      border
      size="small"
    >
      <el-descriptions-item :label="$t('admin.providers.monitoringMode')">
        <el-tag
          :type="config.monitoring_mode === 'agent' ? 'success' : 'info'"
          size="small"
        >
          {{ config.monitoring_mode === 'agent' ? 'Agent' : 'PMAcct' }}
        </el-tag>
      </el-descriptions-item>
      <el-descriptions-item :label="$t('admin.providers.agentStatus')">
        <el-tag
          :type="agentStatusType"
          size="small"
        >
          {{ agentStatusText }}
        </el-tag>
      </el-descriptions-item>
      <el-descriptions-item
        v-if="config.agent_version"
        :label="$t('admin.providers.agentVersion')"
      >
        {{ config.agent_version }}
      </el-descriptions-item>
      <el-descriptions-item :label="$t('admin.providers.agentPort')">
        {{ config.agent_port || 23782 }}
      </el-descriptions-item>
      <el-descriptions-item :label="$t('admin.providers.collectInterval')">
        {{ config.collect_interval || 5 }}s
      </el-descriptions-item>
      <el-descriptions-item :label="$t('admin.providers.resourceCollectInterval')">
        {{ config.resource_collect_interval || 30 }}s
      </el-descriptions-item>
    </el-descriptions>

    <!-- Agent Token 展示与复制 -->
    <div
      v-if="config.agent_token"
      class="token-section"
    >
      <el-descriptions
        :column="1"
        border
        size="small"
        style="margin-top: 12px;"
      >
        <el-descriptions-item :label="$t('admin.providers.agentToken')">
          <div style="display: flex; align-items: center; gap: 8px;">
            <el-input
              :model-value="showToken ? config.agent_token : '••••••••••••••••'"
              readonly
              size="small"
              style="flex: 1; max-width: 320px;"
            />
            <el-button
              size="small"
              @click="$emit('toggle-show-token')"
            >
              {{ showToken ? $t('admin.providers.hideToken') : $t('admin.providers.showToken') }}
            </el-button>
            <el-button
              size="small"
              type="primary"
              @click="$emit('copy-token')"
            >
              {{ $t('admin.providers.copyToken') }}
            </el-button>
          </div>
        </el-descriptions-item>
        <el-descriptions-item
          v-if="!isAgentProvider"
          :label="$t('admin.providers.agentTestUrl')"
        >
          <div>
            <div style="display: flex; align-items: center; gap: 8px;">
              <el-input
                :model-value="agentSwaggerUrl"
                readonly
                size="small"
                style="flex: 1; max-width: 400px;"
              />
              <el-button
                size="small"
                @click="$emit('copy-url', agentSwaggerUrl)"
              >
                {{ $t('admin.providers.copyUrl') }}
              </el-button>
            </div>
            <div
              v-if="config.agent_version"
              style="margin-top: 6px;"
            >
              <el-text
                size="small"
                type="info"
              >
                {{ $t('admin.providers.agentVersion') }}: {{ config.agent_version }}
              </el-text>
            </div>
          </div>
        </el-descriptions-item>
      </el-descriptions>
    </div>

    <!-- 操作按钮 -->
    <div class="action-buttons">
      <el-button
        v-if="!isAgentProvider"
        type="success"
        :loading="deployLoading"
        @click="$emit('deploy-agent')"
      >
        {{ config.agent_installed ? $t('admin.providers.redeployAgent') : $t('admin.providers.deployAgent') }}
      </el-button>
      <el-button
        v-if="!isAgentProvider"
        type="danger"
        :loading="uninstallLoading"
        :disabled="!config.agent_installed"
        @click="$emit('uninstall-agent')"
      >
        {{ $t('admin.providers.uninstallAgent') }}
      </el-button>
      <el-button
        type="primary"
        :loading="statusLoading"
        @click="$emit('check-status')"
      >
        {{ $t('admin.providers.checkAgentStatus') }}
      </el-button>
      <el-button
        type="warning"
        :loading="syncLoading"
        :disabled="!config.agent_installed"
        @click="$emit('sync-monitors')"
      >
        {{ $t('admin.providers.syncMonitors') }}
      </el-button>
      <el-button
        type="danger"
        :loading="clearMonitorsLoading"
        :disabled="!config.agent_installed"
        @click="$emit('clear-monitors')"
      >
        {{ $t('admin.providers.clearMonitors') }}
      </el-button>
      <el-button
        @click="$emit('toggle-config-editor')"
      >
        {{ $t('admin.providers.editConfig') }}
      </el-button>
    </div>

    <!-- 配置编辑器 -->
    <el-card
      v-if="showConfigEditor"
      shadow="never"
      style="margin-top: 16px;"
    >
      <template #header>
        <span>{{ $t('admin.providers.monitoringConfig') }}</span>
      </template>
      <el-form
        :model="editConfig"
        label-width="180px"
        size="small"
      >
        <el-form-item :label="$t('admin.providers.monitoringMode')">
          <el-select
            v-model="editConfig.monitoring_mode"
            style="width: 160px;"
          >
            <el-option
              label="Agent"
              value="agent"
            />
            <el-option
              label="PMAcct"
              value="pmacct"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('admin.providers.trafficCollectMethod')">
          <el-select
            v-model="editConfig.traffic_collect_method"
            style="width: 160px;"
          >
            <el-option
              label="nftables (NFT)"
              value="nft"
            />
            <el-option
              label="iptables (IPT)"
              value="ipt"
            />
          </el-select>
          <el-text
            type="info"
            size="small"
            style="margin-left: 8px;"
          >
            {{ $t('admin.providers.trafficCollectMethodHint') }}
          </el-text>
        </el-form-item>
        <el-form-item :label="$t('admin.providers.agentPort')">
          <el-input-number
            v-model="editConfig.agent_port"
            :min="1024"
            :max="65535"
          />
        </el-form-item>
        <el-form-item :label="$t('admin.providers.collectInterval')">
          <el-input-number
            v-model="editConfig.collect_interval"
            :min="1"
            :max="300"
          />
          <span style="margin-left: 8px; color: #909399;">s</span>
          <el-text
            type="info"
            size="small"
            style="margin-left: 8px;"
          >
            {{ $t('admin.providers.collectIntervalHint') }}
          </el-text>
        </el-form-item>
        <el-form-item :label="$t('admin.providers.resourceCollectInterval')">
          <el-input-number
            v-model="editConfig.resource_collect_interval"
            :min="10"
            :max="3600"
          />
          <span style="margin-left: 8px; color: #909399;">s</span>
          <el-text
            type="info"
            size="small"
            style="margin-left: 8px;"
          >
            {{ $t('admin.providers.resourceCollectIntervalHint') }}
          </el-text>
        </el-form-item>
        <el-form-item :label="$t('admin.providers.extraExcludeCIDRsV4')">
          <el-input
            v-model="editConfig.extra_exclude_cidrs_v4"
            type="textarea"
            :rows="2"
            :placeholder="$t('admin.providers.extraExcludeCIDRsPlaceholder')"
          />
        </el-form-item>
        <el-form-item :label="$t('admin.providers.extraExcludeCIDRsV6')">
          <el-input
            v-model="editConfig.extra_exclude_cidrs_v6"
            type="textarea"
            :rows="2"
            :placeholder="$t('admin.providers.extraExcludeCIDRsV6Placeholder')"
          />
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            :loading="saveConfigLoading"
            @click="$emit('save-config')"
          >
            {{ $t('common.save') }}
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 部署输出 -->
    <div
      v-if="deployOutput"
      class="deploy-output"
    >
      <h4>{{ $t('admin.providers.deployOutput') }}</h4>
      <div class="output-content">
        <pre>{{ deployOutput }}</pre>
      </div>
    </div>
  </div>

  <!-- 监控列表 -->
  <div style="margin-top: 20px;">
    <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
      <h4 style="margin: 0;">
        {{ $t('admin.providers.instanceMonitors') }} ({{ monitorsPagination.total }})
      </h4>
      <el-button
        v-if="config.agent_installed && agentIsOnline"
        size="small"
        :loading="listAgentLoading"
        @click="$emit('list-agent-monitors')"
      >
        {{ $t('admin.providers.viewAgentMonitors') }}
      </el-button>
    </div>
    <el-table
      v-loading="monitorsLoading"
      :data="monitors"
      size="small"
      max-height="300"
    >
      <el-table-column
        prop="instance_name"
        :label="$t('admin.providers.instanceName')"
        width="150"
      >
        <template #default="{ row }">
          <span>{{ row.instance_name || '-' }}</span>
          <el-tag
            v-if="row.instance_deleted"
            type="danger"
            size="small"
            style="margin-left: 4px;"
          >
            {{ $t('admin.providers.deleted') }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column
        prop="interfaces"
        :label="$t('admin.providers.interfaces')"
        show-overflow-tooltip
      >
        <template #default="{ row }">
          {{ row.interfaces || '-' }}
        </template>
      </el-table-column>
      <el-table-column
        prop="agent_monitor_id"
        :label="$t('admin.providers.agentId')"
        width="90"
      />
      <el-table-column
        :label="$t('admin.providers.trafficIn')"
        width="100"
      >
        <template #default="{ row }">
          {{ formatBytes(row.last_traffic_bytes_in || 0) }}
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.trafficOut')"
        width="100"
      >
        <template #default="{ row }">
          {{ formatBytes(row.last_traffic_bytes_out || 0) }}
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.status')"
        width="80"
      >
        <template #default="{ row }">
          <el-tag
            :type="row.is_enabled ? 'success' : 'info'"
            size="small"
          >
            {{ row.is_enabled ? $t('common.enabled') : $t('common.disabled') }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.lastSync')"
        width="160"
      >
        <template #default="{ row }">
          {{ row.last_sync_at ? formatDateTime(row.last_sync_at) : '-' }}
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      v-if="monitorsPagination.total > monitorsPagination.pageSize"
      v-model:current-page="monitorsPagination.page"
      v-model:page-size="monitorsPagination.pageSize"
      :page-sizes="[10, 20, 50]"
      :total="monitorsPagination.total"
      layout="total, sizes, prev, pager, next"
      size="small"
      style="margin-top: 8px; justify-content: center;"
      @current-change="$emit('load-monitors')"
      @size-change="() => { monitorsPagination.page = 1; $emit('load-monitors') }"
    />
  </div>

  <!-- Agent端监控列表弹窗 -->
  <el-dialog
    v-model="localShowAgentMonitors"
    :title="$t('admin.providers.agentMonitorsList')"
    width="800px"
    append-to-body
  >
    <el-table
      :data="agentMonitors"
      size="small"
      max-height="400"
    >
      <el-table-column
        prop="id"
        label="ID"
        width="70"
      />
      <el-table-column
        :label="$t('admin.providers.interfaces')"
        show-overflow-tooltip
      >
        <template #default="{ row }">
          {{ (row.interface || []).join(', ') || '-' }}
        </template>
      </el-table-column>
      <el-table-column
        prop="instance_name"
        :label="$t('admin.providers.instanceName')"
        width="150"
      >
        <template #default="{ row }">
          <span>{{ row.instance_name || '-' }}</span>
          <el-tag
            v-if="row.instance_deleted"
            type="danger"
            size="small"
            style="margin-left: 4px;"
          >
            {{ $t('admin.providers.deleted') }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column
        prop="provider_kind"
        :label="$t('admin.providers.provider')"
        width="100"
      >
        <template #default="{ row }">
          {{ row.provider_kind || '-' }}
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.trafficIn')"
        width="100"
      >
        <template #default="{ row }">
          {{ formatBytes(row.total_bytes_in || 0) }}
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.trafficOut')"
        width="100"
      >
        <template #default="{ row }">
          {{ formatBytes(row.total_bytes_out || 0) }}
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.totalTraffic')"
        width="100"
      >
        <template #default="{ row }">
          {{ formatBytes(row.total_bytes || 0) }}
        </template>
      </el-table-column>
    </el-table>
    <template #footer>
      <div style="display: flex; justify-content: space-between; align-items: center;">
        <el-text
          type="info"
          size="small"
        >
          {{ $t('admin.providers.agentMonitorsTotal') }}: {{ agentMonitorsPagination.total }}
        </el-text>
        <el-pagination
          v-if="agentMonitorsPagination.total > agentMonitorsPagination.pageSize"
          v-model:current-page="agentMonitorsPagination.page"
          v-model:page-size="agentMonitorsPagination.pageSize"
          :page-sizes="[10, 20, 50]"
          :total="agentMonitorsPagination.total"
          layout="total, sizes, prev, pager, next"
          size="small"
          @current-change="$emit('list-agent-monitors')"
          @size-change="() => { agentMonitorsPagination.page = 1; $emit('list-agent-monitors') }"
        />
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
const { t } = useI18n()

const props = defineProps({
  config: { type: Object, required: true },
  editConfig: { type: Object, required: true },
  showConfigEditor: { type: Boolean, default: false },
  showToken: { type: Boolean, default: false },
  deployLoading: { type: Boolean, default: false },
  uninstallLoading: { type: Boolean, default: false },
  statusLoading: { type: Boolean, default: false },
  syncLoading: { type: Boolean, default: false },
  clearMonitorsLoading: { type: Boolean, default: false },
  saveConfigLoading: { type: Boolean, default: false },
  listAgentLoading: { type: Boolean, default: false },
  monitorsLoading: { type: Boolean, default: false },
  deployOutput: { type: String, default: '' },
  monitors: { type: Array, default: () => [] },
  agentIsOnline: { type: Boolean, default: false },
  showAgentMonitors: { type: Boolean, default: false },
  agentMonitors: { type: Array, default: () => [] },
  monitorsPagination: { type: Object, required: true },
  agentMonitorsPagination: { type: Object, required: true },
  agentSwaggerUrl: { type: String, default: '' },
  agentStatusType: { type: String, default: 'info' },
  agentStatusText: { type: String, default: '' },
  isAgentProvider: { type: Boolean, default: false }
})

const emit = defineEmits([
  'toggle-show-token', 'copy-token', 'copy-url',
  'deploy-agent', 'uninstall-agent', 'check-status',
  'sync-monitors', 'clear-monitors', 'list-agent-monitors',
  'save-config', 'toggle-config-editor', 'load-monitors',
  'update:showAgentMonitors'
])

const localShowAgentMonitors = computed({
  get: () => props.showAgentMonitors,
  set: (val) => emit('update:showAgentMonitors', val)
})

function formatDateTime(dateStr) {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

function formatBytes(bytes) {
  if (!bytes || bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}
</script>

<style scoped>
.agent-status-section { padding: 0 4px; }
.token-section { margin-top: 12px; }

.action-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 16px;
}

.deploy-output { margin-top: 20px; }
.deploy-output h4 { margin: 0 0 12px; font-size: 14px; font-weight: 600; }

.output-content {
  background: var(--neutral-bg);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  padding: 12px;
  max-height: 400px;
  overflow-y: auto;
}

.output-content pre {
  margin: 0;
  font-family: 'Courier New', Courier, monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-wrap: break-word;
}
</style>
