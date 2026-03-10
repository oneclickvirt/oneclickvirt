<template>
  <div class="providers-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.providers.title') }}</span>
          <div class="header-actions">
            <!-- 批量操作按钮组 - 仅在选中时显示 -->
            <template v-if="selectedProviders.length > 0">
              <el-button
                type="danger"
                :icon="Delete"
                @click="handleBatchDelete"
              >
                {{ $t('admin.providers.batchDelete') }} ({{ selectedProviders.length }})
              </el-button>
              <el-button
                type="warning"
                :icon="Lock"
                @click="handleBatchFreeze"
              >
                {{ $t('admin.providers.batchFreeze') }} ({{ selectedProviders.length }})
              </el-button>
            </template>
            <!-- 添加服务器按钮 -->
            <el-button
              type="primary"
              @click="handleAddProvider"
            >
              {{ $t('admin.providers.addProvider') }}
            </el-button>
          </div>
        </div>
      </template>
      
      <!-- 搜索过滤 -->
      <SearchFilter 
        :search-form="searchForm"
        @search="handleSearch"
        @reset="handleReset"
      />
      
      <!-- Provider列表表格 -->
      <ProviderTable
        :loading="loading"
        :providers="providers"
        :current-page="currentPage"
        :page-size="pageSize"
        :total="total"
        @selection-change="handleSelectionChange"
        @edit="editProvider"
        @auto-configure="autoConfigureAPI"
        @traffic-monitor="handleEnableTrafficMonitor"
        @health-check="checkHealth"
        @set-expiry="handleSetProviderExpiry"
        @freeze="freezeServer"
        @unfreeze="unfreezeServer"
        @delete="handleDeleteProvider"
        @size-change="handleSizeChange"
        @page-change="handleCurrentChange"
      />
    </el-card>

    <!-- 添加/编辑服务器对话框 -->
    <ProviderFormDialog
      v-model:visible="showAddDialog"
      :is-editing="isEditing"
      :provider-data="addProviderForm"
      :grouped-countries="groupedCountries"
      :loading="addProviderLoading"
      @submit="submitAddServer"
      @cancel="cancelAddServer"
      @reset-level-limits="resetLevelLimitsToDefault"
    />

    <!-- 自动配置结果对话框 -->
    <ConfigDialog
      v-model:visible="configDialog.visible"
      :provider="configDialog.provider"
      :show-history="configDialog.showHistory"
      :running-task="configDialog.runningTask"
      :history-tasks="configDialog.historyTasks"
      @close="configDialog.visible = false"
      @view-task-log="viewTaskLog"
      @view-running-task="viewRunningTask"
      @rerun-configuration="rerunConfiguration"
    />

    <!-- 任务日志查看对话框 -->
    <TaskLogDialog
      v-model:visible="taskLogDialog.visible"
      :loading="taskLogDialog.loading"
      :error="taskLogDialog.error"
      :task="taskLogDialog.task"
      @close="taskLogDialog.visible = false"
    />

    <!-- 流量监控任务对话框 -->
    <TrafficMonitorTaskDialog
      v-model:visible="trafficMonitorDialog.visible"
      :provider="trafficMonitorDialog.provider"
      :show-history="trafficMonitorDialog.showHistory"
      :task="trafficMonitorDialog.task"
      :running-task="trafficMonitorDialog.runningTask"
      :history-tasks="trafficMonitorDialog.historyTasks"
      :loading="trafficMonitorDialog.loading"
      :pagination="trafficMonitorDialog.pagination"
      @close="trafficMonitorDialog.visible = false"
      @refresh="refreshTrafficMonitorTask"
      @view-task-log="viewTrafficMonitorTaskLog"
      @view-running-task="viewRunningTrafficMonitorTask"
      @execute-operation="executeTrafficMonitorOperation"
      @page-change="handleTrafficMonitorPageChange"
      @page-size-change="handleTrafficMonitorPageSizeChange"
    />
  </div>
</template>

<script setup>
import { onMounted, watch } from 'vue'
import { Search, Delete, Lock } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import SearchFilter from './components/SearchFilter.vue'
import ConfigDialog from './components/ConfigDialog.vue'
import TaskLogDialog from './components/TaskLogDialog.vue'
import TrafficMonitorTaskDialog from './components/TrafficMonitorTaskDialog.vue'
import ProviderTable from './components/ProviderTable.vue'
import ProviderFormDialog from './components/ProviderFormDialog.vue'
import { useProviderCRUD } from './composables/useProviderCRUD'
import { useProviderForm } from './composables/useProviderForm'
import { useProviderDialogs } from './composables/useProviderDialogs'

const { t } = useI18n()

const {
  providers, selectedProviders, loading,
  currentPage, pageSize, total, searchForm,
  loadProviders, handleSearch, handleReset,
  handleSizeChange, handleCurrentChange, handleSelectionChange,
  handleDeleteProvider, handleBatchDelete, handleBatchFreeze,
  handleSetProviderExpiry, freezeServer, unfreezeServer, checkHealth
} = useProviderCRUD()

const {
  showAddDialog, addProviderLoading, isEditing, addProviderForm,
  maxTrafficTB, groupedCountries, getLevelTagType,
  resetLevelLimitsToDefault, cancelAddServer,
  handleAddProvider, editProvider, submitAddServer
} = useProviderForm(loadProviders)

const {
  configDialog, taskLogDialog, trafficMonitorDialog,
  viewTaskLog, copyTaskLog, autoConfigureAPI,
  startNewConfiguration, rerunConfiguration, viewRunningTask,
  handleEnableTrafficMonitor, loadTrafficMonitorHistory,
  openTrafficMonitorDialog, handleTrafficMonitorPageChange,
  handleTrafficMonitorPageSizeChange, executeTrafficMonitorOperation,
  viewTrafficMonitorTaskLog, viewRunningTrafficMonitorTask,
  refreshTrafficMonitorTask, debugAuthStatus
} = useProviderDialogs(loadProviders)


// 监听provider类型变化，自动设置虚拟化类型支持和端口映射方式
watch(() => addProviderForm.type, (newType) => {
  // 编辑模式下不自动修改虚拟化类型设置，保持用户已保存的配置
  if (isEditing.value) {
    return
  }
  if (['docker', 'podman', 'containerd'].includes(newType)) {
    // Docker/Podman/Containerd只支持容器，使用原生端口映射
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = false
    addProviderForm.ipv4PortMappingMethod = 'native'
    addProviderForm.ipv6PortMappingMethod = 'native'
  } else if (newType === 'proxmox') {
    // Proxmox支持容器和虚拟机
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = true
    // IPv4: NAT模式下默认iptables，独立IP模式下默认native
    const isNATMode = addProviderForm.networkType === 'nat_ipv4' || addProviderForm.networkType === 'nat_ipv4_ipv6'
    addProviderForm.ipv4PortMappingMethod = isNATMode ? 'iptables' : 'native'
    // IPv6: 默认native
    addProviderForm.ipv6PortMappingMethod = 'native'
  } else if (['lxd', 'incus'].includes(newType)) {
    // LXD/Incus支持容器和虚拟机，默认使用device_proxy
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = true
    addProviderForm.ipv4PortMappingMethod = 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = 'device_proxy'
  } else {
    // 其他类型保持默认设置
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = false
    addProviderForm.ipv4PortMappingMethod = 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = 'device_proxy'
  }
})

// 监听网络类型变化，当Proxmox从NAT改为独立IP时，自动调整端口映射方法
watch(() => [addProviderForm.type, addProviderForm.networkType], ([type, networkType]) => {
  // 编辑模式下不自动修改虚拟化类型设置，但仍需处理端口映射方式的联动
  // 端口映射方式的联动由 MappingTab.vue 组件处理
  if (isEditing.value) {
    return
  }
  
  if (type === 'proxmox') {
    const isNATMode = networkType === 'nat_ipv4' || networkType === 'nat_ipv4_ipv6'
    if (isNATMode) {
      // NAT模式只能使用iptables
      addProviderForm.ipv4PortMappingMethod = 'iptables'
    } else {
      // 独立IP模式默认使用native，但也可以选择iptables
      if (addProviderForm.ipv4PortMappingMethod === 'iptables') {
        // 如果当前是iptables，保持不变
      } else {
        addProviderForm.ipv4PortMappingMethod = 'native'
      }
    }
  }
})

onMounted(() => {
  // 在开发环境下输出调试信息
  if (import.meta.env.DEV) {
    debugAuthStatus()
  }
  loadProviders()
})

</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: var(--text-color-primary);
  }
}

.header-actions {
  display: flex;
  gap: 10px;
  align-items: center;
}

.filter-container {
  margin-bottom: 20px;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.support-type-group {
  display: flex;
  gap: 15px;
}

.form-tip {
  margin-top: 5px;
}

/* 服务器配置标签页样式 */
.server-config-tabs {
  margin-bottom: 20px;
}

.server-config-tabs .el-tab-pane {
  padding: 20px 0;
}

.server-form {
  max-height: 400px;
  overflow-y: auto;
  padding-right: 10px;
}

.location-cell {
  display: flex;
  align-items: center;
  gap: 5px;
}

.location-cell-vertical {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  font-size: 12px;
}

.location-flag {
  font-size: 20px;
  line-height: 1;
}

.location-country {
  font-weight: 500;
  color: var(--text-color-primary);
  text-align: center;
}

.location-city {
  font-size: 11px;
  color: var(--text-color-secondary);
  text-align: center;
}

.location-empty {
  color: #c0c4cc;
}

.flag-icon {
  font-size: 16px;
}

.support-types {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.el-select .el-input {
  width: 100%;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.connection-status {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.resource-info {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
}

.resource-usage {
  display: flex;
  align-items: center;
  gap: 2px;
  font-weight: 500;
}

.resource-usage .separator {
  color: #c0c4cc;
  margin: 0 2px;
}

.resource-progress {
  width: 100%;
}

.resource-item {
  display: flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}

.resource-item .el-icon {
  font-size: 14px;
  color: var(--text-color-secondary);
}

.resource-placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 60px;
  color: #c0c4cc;
}

.sync-time {
  margin-top: 2px;
  text-align: center;
}

.traffic-info {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
}

.traffic-usage {
  display: flex;
  align-items: center;
  gap: 2px;
  font-weight: 500;
}

.traffic-usage .separator {
  color: #c0c4cc;
  margin: 0 2px;
}

.traffic-progress {
  width: 100%;
}

.traffic-status {
  text-align: center;
}

/* 资源限制配置样式 */
.resource-limit-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 20px;
  background: var(--neutral-bg);
  border-radius: 8px;
  transition: all 0.3s;
}

.resource-limit-item:hover {
  background: var(--bg-color-hover);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
}

.resource-limit-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.resource-limit-label .el-icon {
  font-size: 18px;
  color: #16a34a;
}

.resource-limit-tip {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--text-color-secondary);
  text-align: center;
}

.resource-limit-tip .el-icon {
  color: #16a34a;
}

/* 等级限制配置样式 */
.level-limits-container {
  padding: 10px;
  max-height: 450px;
  overflow-y: auto;
}

/* 自定义滚动条样式 */
.level-limits-container::-webkit-scrollbar {
  width: 8px;
}

.level-limits-container::-webkit-scrollbar-track {
  background: var(--neutral-bg);
  border-radius: 4px;
}

.level-limits-container::-webkit-scrollbar-thumb {
  background: #c0c4cc;
  border-radius: 4px;
}

.level-limits-container::-webkit-scrollbar-thumb:hover {
  background: #909399;
}

.level-config-card {
  margin-bottom: 16px;
  padding: 16px;
  background: var(--neutral-bg);
  border-radius: 6px;
  border: 1px solid var(--border-color);
  transition: all 0.3s;
}

.level-config-card:hover {
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
  border-color: #c0c4cc;
}

.level-header {
  margin-bottom: 12px;
  padding-bottom: 8px;
  border-bottom: 2px solid #e4e7ed;
}

.level-form {
  margin-top: 8px;
}

.level-form .el-form-item {
  margin-bottom: 12px;
}

.level-form .el-divider {
  margin: 12px 0;
}

.level-form .form-tip {
  margin-top: 2px;
}
</style>