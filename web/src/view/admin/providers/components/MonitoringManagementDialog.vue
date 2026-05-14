<template>
  <el-dialog
    :model-value="visible"
    :title="$t('admin.providers.monitoringManagement')"
    width="960px"
    :close-on-click-modal="false"
    @close="handleClose"
  >
    <div v-if="provider" v-loading="configLoading">
      <!-- 监控模式选择 -->
      <el-tabs v-model="activeTab" type="border-card">
        <!-- Agent 监控（推荐） -->
        <el-tab-pane name="agent">
          <template #label>
            <span>{{ $t('admin.providers.agentMonitoring') }}</span>
          </template>

          <!-- Agent 说明 -->
          <el-alert
            :title="$t('admin.providers.agentMonitoringDescTitle')"
            type="success"
            :closable="false"
            show-icon
            style="margin-bottom: 16px;"
          >
            <template #default>
              <p style="margin: 4px 0 0;">{{ $t('admin.providers.agentMonitoringDesc') }}</p>
            </template>
          </el-alert>

          <!-- Agent 状态 -->
          <div class="agent-status-section">
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item :label="$t('admin.providers.monitoringMode')">
                <el-tag :type="config.monitoring_mode === 'agent' ? 'success' : 'info'" size="small">
                  {{ config.monitoring_mode === 'agent' ? 'Agent' : 'PMAcct' }}
                </el-tag>
              </el-descriptions-item>
              <el-descriptions-item :label="$t('admin.providers.agentStatus')">
                <el-tag :type="agentStatusType" size="small">
                  {{ agentStatusText }}
                </el-tag>
              </el-descriptions-item>
              <el-descriptions-item v-if="config.agent_version" :label="$t('admin.providers.agentVersion')">
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
            <div v-if="config.agent_token" class="token-section">
              <el-descriptions :column="1" border size="small" style="margin-top: 12px;">
                <el-descriptions-item :label="$t('admin.providers.agentToken')">
                  <div style="display: flex; align-items: center; gap: 8px;">
                    <el-input
                      :model-value="showToken ? config.agent_token : '••••••••••••••••'"
                      readonly
                      size="small"
                      style="flex: 1; max-width: 320px;"
                    />
                    <el-button size="small" @click="showToken = !showToken">
                      {{ showToken ? $t('admin.providers.hideToken') : $t('admin.providers.showToken') }}
                    </el-button>
                    <el-button size="small" type="primary" @click="handleCopyToken">
                      {{ $t('admin.providers.copyToken') }}
                    </el-button>
                  </div>
                </el-descriptions-item>
                <el-descriptions-item :label="$t('admin.providers.agentTestUrl')">
                  <div>
                    <div style="display: flex; align-items: center; gap: 8px;">
                      <el-input
                        :model-value="agentSwaggerUrl"
                        readonly
                        size="small"
                        style="flex: 1; max-width: 400px;"
                      />
                      <el-button size="small" @click="handleCopyUrl(agentSwaggerUrl)">
                        {{ $t('admin.providers.copyUrl') }}
                      </el-button>
                    </div>
                    <div v-if="config.agent_version" style="margin-top: 6px;">
                      <el-text size="small" type="info">
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
                @click="handleDeployAgent"
              >
                {{ config.agent_installed ? $t('admin.providers.redeployAgent') : $t('admin.providers.deployAgent') }}
              </el-button>
              <el-button
                v-if="!isAgentProvider"
                type="danger"
                :loading="uninstallLoading"
                :disabled="!config.agent_installed"
                @click="handleUninstallAgent"
              >
                {{ $t('admin.providers.uninstallAgent') }}
              </el-button>
              <el-button
                type="primary"
                :loading="statusLoading"
                @click="handleCheckStatus"
              >
                {{ $t('admin.providers.checkAgentStatus') }}
              </el-button>
              <el-button
                type="warning"
                :loading="syncLoading"
                :disabled="!config.agent_installed"
                @click="handleSyncMonitors"
              >
                {{ $t('admin.providers.syncMonitors') }}
              </el-button>
              <el-button
                type="danger"
                :loading="clearMonitorsLoading"
                :disabled="!config.agent_installed"
                @click="handleClearMonitors"
              >
                {{ $t('admin.providers.clearMonitors') }}
              </el-button>
              <el-button
                @click="showConfigEditor = !showConfigEditor"
              >
                {{ $t('admin.providers.editConfig') }}
              </el-button>
            </div>

            <!-- 配置编辑器 -->
            <el-card v-if="showConfigEditor" shadow="never" style="margin-top: 16px;">
              <template #header>
                <span>{{ $t('admin.providers.monitoringConfig') }}</span>
              </template>
              <el-form :model="editConfig" label-width="180px" size="small">
                <el-form-item :label="$t('admin.providers.monitoringMode')">
                  <el-select v-model="editConfig.monitoring_mode" style="width: 160px;">
                    <el-option label="Agent" value="agent" />
                    <el-option label="PMAcct" value="pmacct" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="$t('admin.providers.trafficCollectMethod')">
                  <el-select v-model="editConfig.traffic_collect_method" style="width: 160px;">
                    <el-option label="nftables (NFT)" value="nft" />
                    <el-option label="iptables (IPT)" value="ipt" />
                  </el-select>
                  <el-text type="info" size="small" style="margin-left: 8px;">{{ $t('admin.providers.trafficCollectMethodHint') }}</el-text>
                </el-form-item>
                <el-form-item :label="$t('admin.providers.agentPort')">
                  <el-input-number v-model="editConfig.agent_port" :min="1024" :max="65535" />
                </el-form-item>
                <el-form-item :label="$t('admin.providers.collectInterval')">
                  <el-input-number v-model="editConfig.collect_interval" :min="1" :max="300" />
                  <span style="margin-left: 8px; color: #909399;">s</span>
                  <el-text type="info" size="small" style="margin-left: 8px;">{{ $t('admin.providers.collectIntervalHint') }}</el-text>
                </el-form-item>
                <el-form-item :label="$t('admin.providers.resourceCollectInterval')">
                  <el-input-number v-model="editConfig.resource_collect_interval" :min="10" :max="3600" />
                  <span style="margin-left: 8px; color: #909399;">s</span>
                  <el-text type="info" size="small" style="margin-left: 8px;">{{ $t('admin.providers.resourceCollectIntervalHint') }}</el-text>
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
                  <el-button type="primary" :loading="saveConfigLoading" @click="handleSaveConfig">
                    {{ $t('common.save') }}
                  </el-button>
                </el-form-item>
              </el-form>
            </el-card>

            <!-- 部署输出 -->
            <div v-if="deployOutput" class="deploy-output">
              <h4>{{ $t('admin.providers.deployOutput') }}</h4>
              <div class="output-content">
                <pre>{{ deployOutput }}</pre>
              </div>
            </div>
          </div>

          <!-- 监控列表 -->
          <div style="margin-top: 20px;">
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
              <h4 style="margin: 0;">{{ $t('admin.providers.instanceMonitors') }} ({{ monitorsPagination.total }})</h4>
              <el-button
                v-if="config.agent_installed && agentIsOnline"
                size="small"
                @click="handleListAgentMonitors"
                :loading="listAgentLoading"
              >
                {{ $t('admin.providers.viewAgentMonitors') }}
              </el-button>
            </div>
            <el-table :data="monitors" size="small" max-height="300" v-loading="monitorsLoading">
              <el-table-column prop="instance_name" :label="$t('admin.providers.instanceName')" width="150">
                <template #default="{ row }">
                  <span>{{ row.instance_name || '-' }}</span>
                  <el-tag v-if="row.instance_deleted" type="danger" size="small" style="margin-left: 4px;">{{ $t('admin.providers.deleted') }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="interfaces" :label="$t('admin.providers.interfaces')" show-overflow-tooltip>
                <template #default="{ row }">
                  {{ row.interfaces || '-' }}
                </template>
              </el-table-column>
              <el-table-column prop="agent_monitor_id" :label="$t('admin.providers.agentId')" width="90" />
              <el-table-column :label="$t('admin.providers.trafficIn')" width="100">
                <template #default="{ row }">
                  {{ formatBytes(row.last_traffic_bytes_in || 0) }}
                </template>
              </el-table-column>
              <el-table-column :label="$t('admin.providers.trafficOut')" width="100">
                <template #default="{ row }">
                  {{ formatBytes(row.last_traffic_bytes_out || 0) }}
                </template>
              </el-table-column>
              <el-table-column :label="$t('admin.providers.status')" width="80">
                <template #default="{ row }">
                  <el-tag :type="row.is_enabled ? 'success' : 'info'" size="small">
                    {{ row.is_enabled ? $t('common.enabled') : $t('common.disabled') }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column :label="$t('admin.providers.lastSync')" width="160">
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
              @current-change="loadMonitors"
              @size-change="() => { monitorsPagination.page = 1; loadMonitors() }"
            />
          </div>

          <!-- Agent端监控列表弹窗 -->
          <el-dialog
            v-model="showAgentMonitors"
            :title="$t('admin.providers.agentMonitorsList')"
            width="800px"
            append-to-body
          >
            <el-table :data="agentMonitors" size="small" max-height="400">
              <el-table-column prop="id" label="ID" width="70" />
              <el-table-column :label="$t('admin.providers.interfaces')" show-overflow-tooltip>
                <template #default="{ row }">
                  {{ (row.interface || []).join(', ') || '-' }}
                </template>
              </el-table-column>
              <el-table-column prop="instance_name" :label="$t('admin.providers.instanceName')" width="150">
                <template #default="{ row }">
                  <span>{{ row.instance_name || '-' }}</span>
                  <el-tag v-if="row.instance_deleted" type="danger" size="small" style="margin-left: 4px;">{{ $t('admin.providers.deleted') }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="provider_kind" :label="$t('admin.providers.provider')" width="100">
                <template #default="{ row }">
                  {{ row.provider_kind || '-' }}
                </template>
              </el-table-column>
              <el-table-column :label="$t('admin.providers.trafficIn')" width="100">
                <template #default="{ row }">
                  {{ formatBytes(row.total_bytes_in || 0) }}
                </template>
              </el-table-column>
              <el-table-column :label="$t('admin.providers.trafficOut')" width="100">
                <template #default="{ row }">
                  {{ formatBytes(row.total_bytes_out || 0) }}
                </template>
              </el-table-column>
              <el-table-column :label="$t('admin.providers.totalTraffic')" width="100">
                <template #default="{ row }">
                  {{ formatBytes(row.total_bytes || 0) }}
                </template>
              </el-table-column>
            </el-table>
            <template #footer>
              <div style="display: flex; justify-content: space-between; align-items: center;">
                <el-text type="info" size="small">{{ $t('admin.providers.agentMonitorsTotal') }}: {{ agentMonitorsPagination.total }}</el-text>
                <el-pagination
                  v-if="agentMonitorsPagination.total > agentMonitorsPagination.pageSize"
                  v-model:current-page="agentMonitorsPagination.page"
                  v-model:page-size="agentMonitorsPagination.pageSize"
                  :page-sizes="[10, 20, 50]"
                  :total="agentMonitorsPagination.total"
                  layout="total, sizes, prev, pager, next"
                  size="small"
                  @current-change="handleListAgentMonitors"
                  @size-change="() => { agentMonitorsPagination.page = 1; handleListAgentMonitors() }"
                />
              </div>
            </template>
          </el-dialog>
        </el-tab-pane>

        <!-- PMAcct 流量监控（旧模式，不推荐） -->
        <el-tab-pane name="pmacct">
          <template #label>
            <span>{{ $t('admin.providers.pmacctMonitoring') }}</span>
          </template>

          <!-- PMAcct 说明 -->
          <el-alert
            :title="$t('admin.providers.pmacctMonitoringDescTitle')"
            type="warning"
            :closable="false"
            show-icon
            style="margin-bottom: 16px;"
          >
            <template #default>
              <p style="margin: 4px 0 0;">{{ $t('admin.providers.pmacctMonitoringDesc') }}</p>
            </template>
          </el-alert>

          <!-- 历史记录视图 -->
          <div v-if="showHistory">
            <el-alert
              :title="$t('admin.providers.trafficMonitorHistory')"
              type="info"
              :closable="false"
              show-icon
              style="margin-bottom: 20px;"
            >
              <template #default>
                <p>{{ $t('admin.providers.trafficMonitorHistoryMessage') }}</p>
              </template>
            </el-alert>

            <!-- 正在运行的任务 -->
            <div v-if="runningTask" style="margin-bottom: 20px;">
              <el-alert
                :title="$t('admin.providers.runningTrafficMonitorTask')"
                type="warning"
                :closable="false"
                show-icon
              >
                <template #default>
                  <p>{{ $t('admin.providers.taskID') }}: {{ runningTask.id }}</p>
                  <p>{{ $t('admin.providers.taskType') }}: {{ getTaskTypeLabel(runningTask.taskType) }}</p>
                  <p>{{ $t('admin.providers.startTime') }}: {{ formatDateTime(runningTask.startedAt) }}</p>
                  <p>{{ $t('admin.providers.progress') }}: {{ runningTask.progress }}%</p>
                </template>
              </el-alert>
            </div>

            <!-- 历史任务列表 -->
            <div v-if="historyTasks.length > 0">
              <h4>{{ $t('admin.providers.trafficMonitorHistoryRecords') }}</h4>
              <el-table :data="historyTasks" size="small" style="margin-bottom: 20px;">
                <el-table-column prop="id" :label="$t('admin.providers.taskID')" width="70" />
                <el-table-column :label="$t('admin.providers.taskType')" width="120">
                  <template #default="{ row }">
                    <el-tag :type="getTaskTypeTagType(row.taskType)" size="small">
                      {{ getTaskTypeLabel(row.taskType) }}
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column :label="$t('admin.providers.status')" width="80">
                  <template #default="{ row }">
                    <el-tag :type="getTaskStatusTagType(row.status)" size="small">
                      {{ getTaskStatusLabel(row.status) }}
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column :label="$t('admin.providers.executionTime')" width="140">
                  <template #default="{ row }">
                    {{ formatDateTime(row.createdAt) }}
                  </template>
                </el-table-column>
                <el-table-column :label="$t('admin.providers.progress')" width="100">
                  <template #default="{ row }">
                    <el-progress
                      :percentage="row.progress"
                      :status="row.status === 'failed' ? 'exception' : row.status === 'completed' ? 'success' : undefined"
                    />
                  </template>
                </el-table-column>
                <el-table-column :label="$t('admin.providers.result')" show-overflow-tooltip>
                  <template #default="{ row }">
                    <span v-if="row.status === 'completed'" style="color: #67C23A;">
                      {{ $t('common.success') }}: {{ row.successCount }}/{{ row.totalCount }}
                    </span>
                    <span v-else-if="row.status === 'failed'" style="color: #F56C6C;">
                      {{ row.errorMsg || $t('common.failed') }}
                    </span>
                    <span v-else>{{ row.message || '-' }}</span>
                  </template>
                </el-table-column>
                <el-table-column :label="$t('common.actions')" width="100">
                  <template #default="{ row }">
                    <el-button type="primary" size="small" @click="$emit('viewTaskLog', row.id)">
                      {{ $t('admin.providers.viewLog') }}
                    </el-button>
                  </template>
                </el-table-column>
              </el-table>

              <el-pagination
                v-model:current-page="pagination.page"
                v-model:page-size="pagination.pageSize"
                :page-sizes="[5, 10, 20, 50]"
                :small="false"
                :background="true"
                layout="total, sizes, prev, pager, next, jumper"
                :total="pagination.total"
                @size-change="$emit('pageSizeChange', $event)"
                @current-change="$emit('pageChange', $event)"
                style="justify-content: center;"
              />
            </div>

            <!-- PMAcct 操作按钮 -->
            <div style="text-align: center; margin-top: 20px;">
              <el-button
                v-if="runningTask"
                type="primary"
                @click="$emit('viewRunningTask')"
              >
                {{ $t('admin.providers.viewRunningTaskLog') }}
              </el-button>
              <el-button type="success" @click="$emit('executeOperation', 'enable')">
                {{ $t('admin.providers.enableTrafficMonitor') }}
              </el-button>
              <el-button type="warning" @click="$emit('executeOperation', 'disable')">
                {{ $t('admin.providers.disableTrafficMonitor') }}
              </el-button>
              <el-button type="info" @click="$emit('executeOperation', 'detect')">
                {{ $t('admin.providers.detectTrafficMonitor') }}
              </el-button>
            </div>
          </div>

          <!-- 任务执行视图 -->
          <div v-else-if="task" class="task-container">
            <el-descriptions :column="2" border>
              <el-descriptions-item :label="$t('admin.providers.trafficMonitorTaskType')">
                <el-tag :type="getTaskTypeTagType(task.taskType)">
                  {{ getTaskTypeLabel(task.taskType) }}
                </el-tag>
              </el-descriptions-item>
              <el-descriptions-item :label="$t('admin.providers.trafficMonitorTaskStatus')">
                <el-tag :type="getTaskStatusTagType(task.status)">
                  {{ getTaskStatusLabel(task.status) }}
                </el-tag>
              </el-descriptions-item>
              <el-descriptions-item :label="$t('admin.providers.trafficMonitorTaskProgress')" :span="2">
                <div class="progress-container">
                  <el-progress
                    :percentage="task.progress"
                    :status="task.status === 'failed' ? 'exception' : task.status === 'completed' ? 'success' : undefined"
                  />
                  <div class="progress-details">
                    <span>{{ $t('common.total') }}: {{ task.totalCount }}</span>
                    <span class="success-count">{{ $t('common.success') }}: {{ task.successCount }}</span>
                    <span class="failed-count">{{ $t('common.failed') }}: {{ task.failedCount }}</span>
                  </div>
                </div>
              </el-descriptions-item>
            </el-descriptions>

            <div class="output-section">
              <div class="section-header">
                <h4>{{ $t('admin.providers.trafficMonitorTaskOutput') }}</h4>
                <el-button
                  v-if="task.status === 'running'"
                  type="primary"
                  size="small"
                  :loading="loading"
                  @click="$emit('refresh')"
                >
                  {{ $t('common.refresh') }}
                </el-button>
              </div>
              <div class="output-content">
                <pre v-if="task.output">{{ task.output }}</pre>
                <el-empty
                  v-else
                  :description="task.status === 'pending' ? $t('admin.providers.taskExecuting') : $t('common.noData')"
                  :image-size="80"
                />
              </div>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>

    <template #footer>
      <el-button @click="handleClose">{{ $t('common.close') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { useMonitoringManagement } from './composables/useMonitoringManagement'

const props = defineProps({
  visible: { type: Boolean, default: false },
  provider: { type: Object, default: null },
  showHistory: { type: Boolean, default: false },
  task: { type: Object, default: null },
  runningTask: { type: Object, default: null },
  historyTasks: { type: Array, default: () => [] },
  loading: { type: Boolean, default: false },
  pagination: {
    type: Object,
    default: () => ({ page: 1, pageSize: 10, total: 0 })
  }
})

const emit = defineEmits([
  'update:visible', 'close', 'refresh', 'viewTaskLog',
  'viewRunningTask', 'executeOperation', 'pageChange', 'pageSizeChange'
])

const {
  activeTab, showConfigEditor, configLoading, deployLoading, uninstallLoading,
  statusLoading, saveConfigLoading, syncLoading, clearMonitorsLoading,
  monitorsLoading, listAgentLoading, deployOutput, monitors,
  agentOnlineChecked, agentIsOnline, showToken, showAgentMonitors, agentMonitors,
  monitorsPagination, agentMonitorsPagination, config, editConfig,
  agentSwaggerUrl, agentStatusType, agentStatusText,
  isAgentProvider,
  loadConfig, loadMonitors, handleCopyToken, handleCopyUrl,
  handleDeployAgent, handleUninstallAgent, handleCheckStatus,
  handleSyncMonitors, handleClearMonitors, handleListAgentMonitors,
  handleSaveConfig, handleClose,
  formatDateTime, formatBytes,
  getTaskTypeLabel, getTaskTypeTagType, getTaskStatusLabel, getTaskStatusTagType,
  t
} = useMonitoringManagement(props, emit)
</script>

<style scoped>
.agent-status-section {
  padding: 0 4px;
}

.token-section {
  margin-top: 12px;
}

.action-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 16px;
}

.deploy-output {
  margin-top: 20px;
}

.deploy-output h4 {
  margin: 0 0 12px;
  font-size: 14px;
  font-weight: 600;
}

.task-container {
  max-height: 600px;
  overflow-y: auto;
}

.progress-container {
  width: 100%;
}

.progress-details {
  display: flex;
  gap: 20px;
  margin-top: 8px;
  font-size: 13px;
  color: var(--text-color-secondary);
}

.success-count { color: #67c23a; }
.failed-count { color: #f56c6c; }

.output-section {
  margin-top: 20px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.section-header h4 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
}

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

.output-content::-webkit-scrollbar,
.task-container::-webkit-scrollbar {
  width: 8px;
}

.output-content::-webkit-scrollbar-track,
.task-container::-webkit-scrollbar-track {
  background: var(--neutral-bg);
  border-radius: 4px;
}

.output-content::-webkit-scrollbar-thumb,
.task-container::-webkit-scrollbar-thumb {
  background: #c0c4cc;
  border-radius: 4px;
}
</style>
