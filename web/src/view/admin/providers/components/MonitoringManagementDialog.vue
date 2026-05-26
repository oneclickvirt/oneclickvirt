<template>
  <el-dialog
    :model-value="visible"
    :title="$t('admin.providers.monitoringManagement')"
    width="960px"
    :close-on-click-modal="false"
    @close="handleClose"
  >
    <div
      v-if="provider"
      v-loading="configLoading"
    >
      <el-tabs
        v-model="activeTab"
        type="border-card"
      >
        <!-- Agent 监控（推荐） -->
        <el-tab-pane name="agent">
          <template #label>
            <span>{{ $t('admin.providers.agentMonitoring') }}</span>
          </template>
          <AgentMonitoringTab
            :config="config"
            :edit-config="editConfig"
            :show-config-editor="showConfigEditor"
            :show-token="showToken"
            :deploy-loading="deployLoading"
            :uninstall-loading="uninstallLoading"
            :status-loading="statusLoading"
            :sync-loading="syncLoading"
            :clear-monitors-loading="clearMonitorsLoading"
            :save-config-loading="saveConfigLoading"
            :list-agent-loading="listAgentLoading"
            :monitors-loading="monitorsLoading"
            :deploy-output="deployOutput"
            :monitors="monitors"
            :agent-is-online="agentIsOnline"
            :show-agent-monitors="showAgentMonitors"
            :agent-monitors="agentMonitors"
            :monitors-pagination="monitorsPagination"
            :agent-monitors-pagination="agentMonitorsPagination"
            :agent-swagger-url="agentSwaggerUrl"
            :agent-status-type="agentStatusType"
            :agent-status-text="agentStatusText"
            :is-agent-provider="isAgentProvider"
            @toggle-show-token="showToken = !showToken"
            @copy-token="handleCopyToken"
            @copy-url="handleCopyUrl"
            @deploy-agent="handleDeployAgent"
            @uninstall-agent="handleUninstallAgent"
            @check-status="handleCheckStatus"
            @sync-monitors="handleSyncMonitors"
            @clear-monitors="handleClearMonitors"
            @list-agent-monitors="handleListAgentMonitors"
            @save-config="handleSaveConfig"
            @toggle-config-editor="showConfigEditor = !showConfigEditor"
            @load-monitors="loadMonitors"
            @update:showAgentMonitors="showAgentMonitors = $event"
          />
        </el-tab-pane>

        <!-- PMAcct 流量监控（旧模式，不推荐） -->
        <el-tab-pane name="pmacct">
          <template #label>
            <span>{{ $t('admin.providers.pmacctMonitoring') }}</span>
          </template>
          <PmacctMonitoringTab
            :show-history="showHistory"
            :task="task"
            :running-task="runningTask"
            :history-tasks="historyTasks"
            :loading="loading"
            :pagination="pagination"
            @refresh="$emit('refresh')"
            @view-task-log="$emit('viewTaskLog', $event)"
            @view-running-task="$emit('viewRunningTask')"
            @execute-operation="$emit('executeOperation', $event)"
            @page-change="$emit('pageChange', $event)"
            @page-size-change="$emit('pageSizeChange', $event)"
          />
        </el-tab-pane>
      </el-tabs>
    </div>

    <template #footer>
      <el-button @click="handleClose">
        {{ $t('common.close') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import AgentMonitoringTab from './AgentMonitoringTab.vue'
import PmacctMonitoringTab from './PmacctMonitoringTab.vue'
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
  agentIsOnline, showToken, showAgentMonitors, agentMonitors,
  monitorsPagination, agentMonitorsPagination, config, editConfig,
  agentSwaggerUrl, agentStatusType, agentStatusText,
  isAgentProvider,
  loadMonitors, handleCopyToken, handleCopyUrl,
  handleDeployAgent, handleUninstallAgent, handleCheckStatus,
  handleSyncMonitors, handleClearMonitors, handleListAgentMonitors,
  handleSaveConfig, handleClose
} = useMonitoringManagement(props, emit)
</script>
