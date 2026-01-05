<template>
  <div>
    <el-table
      v-loading="loading"
      :data="providers"
      style="width: 100%"
      :row-style="{ height: '90px' }"
      @selection-change="handleSelectionChange"
    >
      <el-table-column
        type="selection"
        width="55"
        fixed="left"
      />
      <el-table-column
        prop="name"
        :label="$t('common.name')"
        width="100"
        fixed="left"
      />
      <el-table-column
        prop="type"
        :label="$t('admin.providers.providerType')"
        width="100"
      />
      <el-table-column
        prop="version"
        :label="$t('admin.providers.version')"
        width="100"
      >
        <template #default="scope">
          <el-tag
            v-if="scope.row.version && scope.row.version !== ''"
            size="small"
            type="info"
          >
            {{ scope.row.version }}
          </el-tag>
          <el-text
            v-else
            size="small"
            type="info"
          >
            -
          </el-text>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.location')"
        width="100"
      >
        <template #default="scope">
          <div class="location-cell-vertical">
            <div
              v-if="scope.row.countryCode"
              class="location-flag"
            >
              {{ getFlagEmoji(scope.row.countryCode) }}
            </div>
            <div
              v-if="scope.row.country"
              class="location-country"
            >
              {{ scope.row.country }}
            </div>
            <div
              v-if="scope.row.city"
              class="location-city"
            >
              {{ scope.row.city }}
            </div>
            <div
              v-if="!scope.row.country && !scope.row.city"
              class="location-empty"
            >
              -
            </div>
          </div>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.apiEndpoint')"
        width="140"
      >
        <template #default="scope">
          {{ scope.row.endpoint ? scope.row.endpoint.split(':')[0] : '-' }}
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.sshPort')"
        width="80"
      >
        <template #default="scope">
          {{ scope.row.sshPort || 22 }}
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.supportTypes')"
        width="120"
      >
        <template #default="scope">
          <div class="support-types">
            <el-tag
              v-if="scope.row.container_enabled"
              size="small"
              type="primary"
            >
              {{ $t('admin.providers.container') }}
            </el-tag>
            <el-tag
              v-if="scope.row.vm_enabled"
              size="small"
              type="success"
            >
              {{ $t('admin.providers.vm') }}
            </el-tag>
          </div>
        </template>
      </el-table-column>
      <el-table-column
        prop="architecture"
        :label="$t('admin.providers.architecture')"
        width="110"
      >
        <template #default="scope">
          <el-tag
            size="small"
            type="info"
          >
            {{ scope.row.architecture || 'amd64' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.storagePool')"
        width="110"
      >
        <template #default="scope">
          <el-tag
            v-if="scope.row.type === 'proxmox' && scope.row.storagePool"
            size="small"
            type="warning"
          >
            <el-icon style="margin-right: 4px;">
              <FolderOpened />
            </el-icon>
            {{ scope.row.storagePool }}
          </el-tag>
          <el-text
            v-else-if="scope.row.type === 'proxmox'"
            size="small"
            type="info"
          >
            {{ $t('admin.providers.notConfigured') }}
          </el-text>
          <el-text
            v-else
            size="small"
            type="info"
          >
            -
          </el-text>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.connectionStatus')"
        width="100"
      >
        <template #default="scope">
          <div class="connection-status">
            <div style="margin-bottom: 4px;">
              <el-tag 
                size="small" 
                :type="getStatusType(scope.row.apiStatus)"
              >
                API: {{ getStatusText(scope.row.apiStatus) }}
              </el-tag>
            </div>
            <div>
              <el-tag 
                size="small" 
                :type="getStatusType(scope.row.sshStatus)"
              >
                SSH: {{ getStatusText(scope.row.sshStatus) }}
              </el-tag>
            </div>
          </div>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.cpuResource')"
        width="140"
      >
        <template #default="scope">
          <div 
            v-if="scope.row.resourceSynced"
            class="resource-info"
          >
            <div class="resource-usage">
              <span>{{ scope.row.allocatedCpuCores || 0 }}</span>
              <span class="separator">/</span>
              <span>{{ scope.row.nodeCpuCores || 0 }} {{ $t('admin.providers.cores') }}</span>
            </div>
            <div class="resource-progress">
              <el-progress
                :percentage="getResourcePercentage(scope.row.allocatedCpuCores, scope.row.nodeCpuCores)"
                :status="getResourceProgressStatus(scope.row.allocatedCpuCores, scope.row.nodeCpuCores)"
                :stroke-width="6"
                :show-text="false"
              />
            </div>
          </div>
          <div
            v-else
            class="resource-placeholder"
          >
            <el-text
              size="small"
              type="info"
            >
              <el-icon><Loading /></el-icon>
              {{ $t('admin.providers.notSynced') }}
            </el-text>
          </div>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.memoryResource')"
        width="140"
      >
        <template #default="scope">
          <div 
            v-if="scope.row.resourceSynced"
            class="resource-info"
          >
            <div class="resource-usage">
              <span>{{ formatMemorySize(scope.row.allocatedMemory) }}</span>
              <span class="separator">/</span>
              <span>{{ formatMemorySize(scope.row.nodeMemoryTotal) }}</span>
            </div>
            <div class="resource-progress">
              <el-progress
                :percentage="getResourcePercentage(scope.row.allocatedMemory, scope.row.nodeMemoryTotal)"
                :status="getResourceProgressStatus(scope.row.allocatedMemory, scope.row.nodeMemoryTotal)"
                :stroke-width="6"
                :show-text="false"
              />
            </div>
          </div>
          <div
            v-else
            class="resource-placeholder"
          >
            <el-text
              size="small"
              type="info"
            >
              <el-icon><Loading /></el-icon>
              {{ $t('admin.providers.notSynced') }}
            </el-text>
          </div>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.diskResource')"
        width="140"
      >
        <template #default="scope">
          <div 
            v-if="scope.row.resourceSynced"
            class="resource-info"
          >
            <div class="resource-usage">
              <span>{{ formatDiskSize(scope.row.allocatedDisk) }}</span>
              <span class="separator">/</span>
              <span>{{ formatDiskSize(scope.row.nodeDiskTotal) }}</span>
            </div>
            <div class="resource-progress">
              <el-progress
                :percentage="getResourcePercentage(scope.row.allocatedDisk, scope.row.nodeDiskTotal)"
                :status="getResourceProgressStatus(scope.row.allocatedDisk, scope.row.nodeDiskTotal)"
                :stroke-width="6"
                :show-text="false"
              />
            </div>
          </div>
          <div
            v-else
            class="resource-placeholder"
          >
            <el-text
              size="small"
              type="info"
            >
              <el-icon><Loading /></el-icon>
              {{ $t('admin.providers.notSynced') }}
            </el-text>
          </div>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.trafficUsage')"
        width="140"
      >
        <template #default="scope">
          <div 
            v-if="scope.row.enableTrafficControl"
            class="traffic-info"
          >
            <div class="traffic-usage">
              <span>{{ formatTraffic(scope.row.usedTraffic) }}</span>
              <span class="separator">/</span>
              <span>{{ formatTraffic(scope.row.maxTraffic) }}</span>
            </div>
            <div class="traffic-progress">
              <el-progress
                :percentage="getTrafficPercentage(scope.row.usedTraffic, scope.row.maxTraffic)"
                :status="scope.row.trafficLimited ? 'exception' : getTrafficProgressStatus(scope.row.usedTraffic, scope.row.maxTraffic)"
                :stroke-width="6"
                :show-text="false"
              />
            </div>
            <div
              v-if="scope.row.trafficLimited"
              class="traffic-status"
            >
              <el-tag
                type="danger"
                size="small"
              >
                {{ $t('admin.providers.trafficExceeded') }}
              </el-tag>
            </div>
          </div>
          <div
            v-else
            class="traffic-disabled"
          >
            <el-text
              size="small"
              type="info"
            >
              {{ $t('admin.providers.trafficDisabled') }}
            </el-text>
          </div>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.instanceQuota')"
        width="160"
      >
        <template #default="scope">
          <div class="instance-quota-info">
            <!-- 容器配额 -->
            <div
              v-if="scope.row.container_enabled"
              class="quota-item"
            >
              <el-tag
                size="small"
                type="primary"
              >
                {{ $t('admin.providers.container') }}
              </el-tag>
              <span class="quota-text">
                {{ scope.row.currentContainerCount || 0 }} / {{ scope.row.maxContainerInstances === 0 ? '∞' : scope.row.maxContainerInstances }}
              </span>
              <el-progress
                v-if="scope.row.maxContainerInstances > 0"
                :percentage="getQuotaPercentage(scope.row.currentContainerCount, scope.row.maxContainerInstances)"
                :status="getQuotaProgressStatus(scope.row.currentContainerCount, scope.row.maxContainerInstances)"
                :stroke-width="4"
                :show-text="false"
              />
            </div>
            <!-- 虚拟机配额 -->
            <div
              v-if="scope.row.vm_enabled"
              class="quota-item"
            >
              <el-tag
                size="small"
                type="success"
              >
                {{ $t('admin.providers.vm') }}
              </el-tag>
              <span class="quota-text">
                {{ scope.row.currentVMCount || 0 }} / {{ scope.row.maxVMInstances === 0 ? '∞' : scope.row.maxVMInstances }}
              </span>
              <el-progress
                v-if="scope.row.maxVMInstances > 0"
                :percentage="getQuotaPercentage(scope.row.currentVMCount, scope.row.maxVMInstances)"
                :status="getQuotaProgressStatus(scope.row.currentVMCount, scope.row.maxVMInstances)"
                :stroke-width="4"
                :show-text="false"
              />
            </div>
          </div>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('common.status')"
        width="80"
      >
        <template #default="scope">
          <el-tag
            v-if="scope.row.isFrozen"
            type="danger"
            size="small"
          >
            {{ $t('admin.providers.frozen') }}
          </el-tag>
          <el-tag
            v-else-if="isExpired(scope.row.expiresAt)"
            type="warning"
            size="small"
          >
            {{ $t('admin.providers.expired') }}
          </el-tag>
          <el-tag
            v-else
            type="success"
            size="small"
          >
            {{ $t('common.normal') }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('admin.providers.expiryTime')"
        width="130"
      >
        <template #default="scope">
          <div v-if="scope.row.expiresAt">
            <el-tag 
              :type="isExpired(scope.row.expiresAt) ? 'danger' : isNearExpiry(scope.row.expiresAt) ? 'warning' : 'success'" 
              size="small"
            >
              {{ formatDateTime(scope.row.expiresAt) }}
            </el-tag>
          </div>
          <el-text
            v-else
            size="small"
            type="info"
          >
            {{ $t('admin.providers.neverExpires') }}
          </el-text>
        </template>
      </el-table-column>
      <el-table-column
        :label="$t('common.actions')"
        width="230"
        fixed="right"
      >
        <template #default="scope">
          <div class="action-buttons">
            <el-button
              size="small"
              type="primary"
              @click="$emit('edit', scope.row)"
            >
              {{ $t('common.edit') }}
            </el-button>
            <el-button
              size="small"
              type="primary"
              @click="showActionsDialog(scope.row)"
            >
              {{ $t('common.actions') }}
            </el-button>
            <el-button
              size="small"
              type="danger"
              @click="$emit('delete', scope.row)"
            >
              {{ $t('common.delete') }}
            </el-button>
          </div>
        </template>
      </el-table-column>
    </el-table>

    <!-- 分页 -->
    <div class="pagination-wrapper">
      <el-pagination
        :current-page="currentPage"
        :page-size="pageSize"
        :page-sizes="[10, 20, 50, 100]"
        :total="total"
        layout="total, sizes, prev, pager, next, jumper"
        @size-change="$emit('size-change', $event)"
        @current-change="$emit('page-change', $event)"
      />
    </div>

    <!-- 操作对话框 -->
    <el-dialog
      v-model="actionsDialogVisible"
      :title="$t('common.actions')"
      width="400px"
    >
      <div
        v-if="currentRow"
        class="actions-dialog-content"
      >
        <el-button
          v-if="currentRow.type === 'lxd' || currentRow.type === 'incus' || currentRow.type === 'proxmox'"
          class="action-button"
          type="primary"
          @click="handleAction('auto-configure')"
        >
          {{ $t('admin.providers.autoConfigureAPI') }}
        </el-button>
        
        <el-button
          v-if="currentRow.enableTrafficControl"
          class="action-button"
          type="success"
          @click="handleAction('traffic-monitor')"
        >
          {{ $t('admin.providers.trafficMonitorManagement') }}
        </el-button>

        <el-divider v-if="(currentRow.type === 'lxd' || currentRow.type === 'incus' || currentRow.type === 'proxmox') || currentRow.enableTrafficControl" />
        <el-button
          class="action-button"
          type="primary"
          @click="handleAction('health-check')"
        >
          {{ $t('admin.providers.healthCheck') }}
        </el-button>

        <el-button
          class="action-button"
          type="info"
          @click="handleAction('set-expiry')"
        >
          {{ $t('admin.providers.setExpiry') }}
        </el-button>

        <el-button
          v-if="currentRow.isFrozen"
          class="action-button"
          type="success"
          @click="handleAction('unfreeze')"
        >
          {{ $t('admin.providers.unfreeze') }}
        </el-button>
        <el-button
          v-else
          class="action-button"
          type="warning"
          @click="handleAction('freeze')"
        >
          {{ $t('admin.providers.freeze') }}
        </el-button>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { 
  formatMemorySize, 
  formatDiskSize, 
  formatTraffic,
  getTrafficPercentage,
  getTrafficProgressStatus,
  getResourcePercentage,
  getResourceProgressStatus,
  getQuotaPercentage,
  getQuotaProgressStatus,
  formatDateTime,
  isExpired,
  isNearExpiry,
  getStatusType,
  getStatusText,
  getFlagEmoji
} from '../composables/useProviderUtils'

defineProps({
  loading: {
    type: Boolean,
    default: false
  },
  providers: {
    type: Array,
    default: () => []
  },
  currentPage: {
    type: Number,
    default: 1
  },
  pageSize: {
    type: Number,
    default: 10
  },
  total: {
    type: Number,
    default: 0
  }
})

const emit = defineEmits([
  'selection-change',
  'edit',
  'auto-configure',
  'traffic-monitor',
  'health-check',
  'set-expiry',
  'freeze',
  'unfreeze',
  'delete',
  'size-change',
  'page-change'
])

const handleSelectionChange = (selection) => {
  emit('selection-change', selection)
}

const actionsDialogVisible = ref(false)
const currentRow = ref(null)

const showActionsDialog = (row) => {
  currentRow.value = row
  actionsDialogVisible.value = true
}

const handleAction = (action) => {
  if (!currentRow.value) return
  
  actionsDialogVisible.value = false
  
  switch (action) {
    case 'auto-configure':
      emit('auto-configure', currentRow.value)
      break
    case 'traffic-monitor':
      emit('traffic-monitor', currentRow.value)
      break
    case 'health-check':
      emit('health-check', currentRow.value.id)
      break
    case 'set-expiry':
      emit('set-expiry', currentRow.value)
      break
    case 'freeze':
      emit('freeze', currentRow.value.id)
      break
    case 'unfreeze':
      emit('unfreeze', currentRow.value)
      break
  }
  
  currentRow.value = null
}
</script>

<style scoped>
.location-cell-vertical {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  min-height: 75px;
  justify-content: center;
}

.location-flag {
  font-size: 20px;
}

.location-country,
.location-city {
  font-size: 12px;
  color: #606266;
}

.location-empty {
  color: #c0c4cc;
}

.support-types {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.connection-status {
  display: flex;
  flex-direction: column;
}

.resource-info,
.traffic-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.resource-usage,
.traffic-usage {
  font-size: 12px;
  text-align: center;
}

.separator {
  margin: 0 4px;
  color: #909399;
}

.resource-placeholder {
  text-align: center;
}

.traffic-status {
  text-align: center;
}

.traffic-disabled {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 60px;
  text-align: center;
}

.instance-quota-info {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.quota-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  align-items: flex-start;
}

.quota-text {
  font-size: 12px;
  font-weight: 500;
  color: #606266;
  margin-left: 4px;
}

.table-action-buttons {
  display: flex;
  flex-direction: row;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
  padding: 8px 0;
}

.table-action-link {
  cursor: pointer;
  color: #409eff;
  text-decoration: none;
  font-size: 13px;
}

.table-action-link:hover {
  color: #66b1ff;
}

.table-action-link.success {
  color: #67c23a;
}

.table-action-link.success:hover {
  color: #85ce61;
}

.table-action-link.warning {
  color: #e6a23c;
}

.table-action-link.warning:hover {
  color: #ebb563;
}

.table-action-link.danger {
  color: #f56c6c;
}

.table-action-link.danger:hover {
  color: #f78989;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.action-buttons {
  display: flex;
  gap: 5px;
  flex-wrap: wrap;
  align-items: center;
}

.action-buttons .el-button {
  margin: 0;
}

.actions-dialog-content {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 10px 0;
}

.action-button {
  width: 100%;
  margin: 0 !important;
}

.actions-dialog-content .el-divider {
  margin: 10px 0;
}
</style>
