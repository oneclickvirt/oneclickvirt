<template>
  <el-dialog
    :model-value="visible"
    :title="$t('admin.providers.monitoringManagement')"
    width="960px"
    :close-on-click-modal="false"
    @close="handleClose"
  >
    <div v-if="provider">
      <!-- 监控模式选择 -->
      <el-tabs v-model="activeTab" type="border-card">
        <!-- Agent 监控 -->
        <el-tab-pane :label="$t('admin.providers.agentMonitoring')" name="agent">
          <!-- Agent 状态 -->
          <div class="agent-status-section">
            <el-descriptions :column="2" border size="small">
              <el-descriptions-item :label="$t('admin.providers.monitoringMode')">
                <el-tag :type="config.monitoringMode === 'agent' ? 'success' : 'info'" size="small">
                  {{ config.monitoringMode === 'agent' ? 'Agent' : 'PMAcct' }}
                </el-tag>
              </el-descriptions-item>
              <el-descriptions-item :label="$t('admin.providers.agentStatus')">
                <el-tag :type="agentStatusType" size="small">
                  {{ agentStatusText }}
                </el-tag>
              </el-descriptions-item>
              <el-descriptions-item v-if="config.agentVersion" :label="$t('admin.providers.agentVersion')">
                {{ config.agentVersion }}
              </el-descriptions-item>
              <el-descriptions-item :label="$t('admin.providers.agentPort')">
                {{ config.agentPort || 23782 }}
              </el-descriptions-item>
              <el-descriptions-item :label="$t('admin.providers.collectInterval')">
                {{ config.collectInterval || 60 }}s
              </el-descriptions-item>
              <el-descriptions-item :label="$t('admin.providers.resourceCollectInterval')">
                {{ config.resourceCollectInterval || 300 }}s
              </el-descriptions-item>
            </el-descriptions>

            <!-- 操作按钮 -->
            <div class="action-buttons">
              <el-button
                type="success"
                :loading="deployLoading"
                @click="handleDeployAgent"
              >
                {{ config.agentInstalled ? $t('admin.providers.redeployAgent') : $t('admin.providers.deployAgent') }}
              </el-button>
              <el-button
                v-if="config.agentInstalled"
                type="danger"
                :loading="uninstallLoading"
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
                  <el-select v-model="editConfig.monitoringMode" style="width: 160px;">
                    <el-option label="Agent" value="agent" />
                    <el-option label="PMAcct" value="pmacct" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="$t('admin.providers.agentPort')">
                  <el-input-number v-model="editConfig.agentPort" :min="1024" :max="65535" />
                </el-form-item>
                <el-form-item :label="$t('admin.providers.collectInterval')">
                  <el-input-number v-model="editConfig.collectInterval" :min="10" :max="300" />
                  <span style="margin-left: 8px; color: #909399;">s</span>
                </el-form-item>
                <el-form-item :label="$t('admin.providers.resourceCollectInterval')">
                  <el-input-number v-model="editConfig.resourceCollectInterval" :min="60" :max="3600" />
                  <span style="margin-left: 8px; color: #909399;">s</span>
                </el-form-item>
                <el-form-item :label="$t('admin.providers.extraExcludeCIDRs')">
                  <el-input
                    v-model="editConfig.extraExcludeCidrs"
                    type="textarea"
                    :rows="3"
                    :placeholder="$t('admin.providers.extraExcludeCIDRsPlaceholder')"
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
          <div v-if="monitors.length > 0" style="margin-top: 20px;">
            <h4>{{ $t('admin.providers.instanceMonitors') }}</h4>
            <el-table :data="monitors" size="small" max-height="300">
              <el-table-column prop="instanceName" :label="$t('admin.providers.instanceName')" width="150" />
              <el-table-column prop="interfaces" :label="$t('admin.providers.interfaces')" show-overflow-tooltip>
                <template #default="{ row }">
                  {{ row.interfaces || '-' }}
                </template>
              </el-table-column>
              <el-table-column :label="$t('admin.providers.status')" width="80">
                <template #default="{ row }">
                  <el-tag :type="row.isEnabled ? 'success' : 'info'" size="small">
                    {{ row.isEnabled ? $t('common.enabled') : $t('common.disabled') }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column :label="$t('admin.providers.lastSync')" width="160">
                <template #default="{ row }">
                  {{ row.lastSyncAt ? formatDateTime(row.lastSyncAt) : '-' }}
                </template>
              </el-table-column>
            </el-table>
          </div>
        </el-tab-pane>

        <!-- PMAcct 流量监控（旧模式） -->
        <el-tab-pane :label="$t('admin.providers.pmacctMonitoring')" name="pmacct">
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
                      ✅ {{ $t('common.success') }}: {{ row.successCount }}/{{ row.totalCount }}
                    </span>
                    <span v-else-if="row.status === 'failed'" style="color: #F56C6C;">
                      ❌ {{ row.errorMsg || $t('common.failed') }}
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
import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getMonitoringConfig,
  updateMonitoringConfig,
  deployAgent,
  uninstallAgent,
  getAgentStatus,
  getProviderMonitors
} from '@/api/admin'

const { t } = useI18n()

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

const activeTab = ref('agent')
const showConfigEditor = ref(false)
const deployLoading = ref(false)
const uninstallLoading = ref(false)
const statusLoading = ref(false)
const saveConfigLoading = ref(false)
const deployOutput = ref('')
const monitors = ref([])

const config = reactive({
  monitoringMode: 'agent',
  agentToken: '',
  agentPort: 23782,
  agentInstalled: false,
  agentVersion: '',
  collectInterval: 60,
  resourceCollectInterval: 300,
  extraExcludeCidrs: ''
})

const editConfig = reactive({
  monitoringMode: 'agent',
  agentPort: 23782,
  collectInterval: 60,
  resourceCollectInterval: 300,
  extraExcludeCidrs: ''
})

const agentStatusType = computed(() => {
  if (config.agentInstalled) return 'success'
  return 'info'
})

const agentStatusText = computed(() => {
  if (config.agentInstalled) return t('admin.providers.agentInstalled')
  return t('admin.providers.agentNotInstalled')
})

watch(() => props.visible, async (val) => {
  if (val && props.provider) {
    await loadConfig()
    await loadMonitors()
  }
})

const loadConfig = async () => {
  if (!props.provider) return
  try {
    const res = await getMonitoringConfig(props.provider.id)
    if (res.code === 0 || res.code === 200) {
      Object.assign(config, res.data)
      Object.assign(editConfig, {
        monitoringMode: config.monitoringMode,
        agentPort: config.agentPort,
        collectInterval: config.collectInterval,
        resourceCollectInterval: config.resourceCollectInterval,
        extraExcludeCidrs: config.extraExcludeCidrs || ''
      })
    }
  } catch (e) {
    console.error('Failed to load monitoring config:', e)
  }
}

const loadMonitors = async () => {
  if (!props.provider) return
  try {
    const res = await getProviderMonitors(props.provider.id)
    if (res.code === 0 || res.code === 200) {
      monitors.value = res.data?.list || res.data || []
    }
  } catch (e) {
    console.error('Failed to load monitors:', e)
  }
}

const handleDeployAgent = async () => {
  if (!props.provider) return
  try {
    await ElMessageBox.confirm(
      t('admin.providers.deployAgentConfirm'),
      t('common.confirm'),
      { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'info' }
    )
    deployLoading.value = true
    deployOutput.value = ''
    const res = await deployAgent({ providerId: props.provider.id })
    if (res.code === 0 || res.code === 200) {
      ElMessage.success(t('admin.providers.deployAgentSuccess'))
      deployOutput.value = res.data?.output || res.data?.message || 'OK'
      await loadConfig()
      await loadMonitors()
    } else {
      ElMessage.error(res.msg || t('admin.providers.deployAgentFailed'))
      deployOutput.value = res.msg || ''
    }
  } catch (e) {
    if (e !== 'cancel') {
      ElMessage.error(e?.response?.data?.msg || t('admin.providers.deployAgentFailed'))
      deployOutput.value = e?.response?.data?.msg || e.message || ''
    }
  } finally {
    deployLoading.value = false
  }
}

const handleUninstallAgent = async () => {
  if (!props.provider) return
  try {
    await ElMessageBox.confirm(
      t('admin.providers.uninstallAgentConfirm'),
      t('common.confirm'),
      { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' }
    )
    uninstallLoading.value = true
    const res = await uninstallAgent(props.provider.id)
    if (res.code === 0 || res.code === 200) {
      ElMessage.success(t('admin.providers.uninstallAgentSuccess'))
      await loadConfig()
      monitors.value = []
    } else {
      ElMessage.error(res.msg || t('admin.providers.uninstallAgentFailed'))
    }
  } catch (e) {
    if (e !== 'cancel') {
      ElMessage.error(e?.response?.data?.msg || t('admin.providers.uninstallAgentFailed'))
    }
  } finally {
    uninstallLoading.value = false
  }
}

const handleCheckStatus = async () => {
  if (!props.provider) return
  statusLoading.value = true
  try {
    const res = await getAgentStatus(props.provider.id)
    if (res.code === 0 || res.code === 200) {
      const data = res.data
      if (data.online) {
        ElMessage.success(t('admin.providers.agentOnline') + (data.version ? ` (v${data.version})` : ''))
      } else {
        ElMessage.warning(t('admin.providers.agentOffline'))
      }
    }
  } catch (e) {
    ElMessage.error(t('admin.providers.checkStatusFailed'))
  } finally {
    statusLoading.value = false
  }
}

const handleSaveConfig = async () => {
  if (!props.provider) return
  saveConfigLoading.value = true
  try {
    const res = await updateMonitoringConfig(props.provider.id, editConfig)
    if (res.code === 0 || res.code === 200) {
      ElMessage.success(t('common.saveSuccess'))
      await loadConfig()
      showConfigEditor.value = false
    } else {
      ElMessage.error(res.msg || t('common.saveFailed'))
    }
  } catch (e) {
    ElMessage.error(e?.response?.data?.msg || t('common.saveFailed'))
  } finally {
    saveConfigLoading.value = false
  }
}

const handleClose = () => {
  emit('update:visible', false)
  emit('close')
}

const formatDateTime = (dateTime) => {
  if (!dateTime) return '-'
  return new Date(dateTime).toLocaleString()
}

const getTaskTypeLabel = (taskType) => {
  const labels = {
    'enable_all': t('admin.providers.trafficMonitorTaskTypeEnableAll'),
    'disable_all': t('admin.providers.trafficMonitorTaskTypeDisableAll'),
    'detect_all': t('admin.providers.trafficMonitorTaskTypeDetectAll')
  }
  return labels[taskType] || taskType
}

const getTaskTypeTagType = (taskType) => {
  const types = { 'enable_all': 'success', 'disable_all': 'danger', 'detect_all': 'info' }
  return types[taskType] || 'info'
}

const getTaskStatusLabel = (status) => {
  const labels = {
    'pending': t('admin.providers.trafficMonitorTaskStatusPending'),
    'running': t('admin.providers.trafficMonitorTaskStatusRunning'),
    'completed': t('admin.providers.trafficMonitorTaskStatusCompleted'),
    'failed': t('admin.providers.trafficMonitorTaskStatusFailed')
  }
  return labels[status] || status
}

const getTaskStatusTagType = (status) => {
  const types = { 'pending': 'info', 'running': 'warning', 'completed': 'success', 'failed': 'danger' }
  return types[status] || 'info'
}
</script>

<style scoped>
.agent-status-section {
  padding: 0 4px;
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
