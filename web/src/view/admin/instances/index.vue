<template>
  <div class="instances-container">
    <el-card>
      <template #header>
        <div class="header-row">
          <span>{{ $t('admin.instances.title') }}</span>
          <div class="header-actions">
            <el-button
              v-if="selectedInstances.length > 0"
              type="success"
              @click="batchStartInstances"
            >
              {{ $t('admin.instances.batchStart') }} ({{ selectedInstances.length }})
            </el-button>
            <el-button
              v-if="selectedInstances.length > 0"
              type="warning"
              @click="batchStopInstances"
            >
              {{ $t('admin.instances.batchStop') }} ({{ selectedInstances.length }})
            </el-button>
            <el-button
              v-if="selectedInstances.length > 0"
              type="danger"
              @click="batchDeleteInstances"
            >
              {{ $t('admin.instances.batchDelete') }} ({{ selectedInstances.length }})
            </el-button>
            <el-button
              type="primary"
              :loading="loading"
              @click="loadInstances"
            >
              {{ $t('common.refresh') }}
            </el-button>
          </div>
        </div>
      </template>

      <!-- 筛选条件 -->
      <div class="filter-row">
        <el-input
          v-model="filters.instanceName"
          :placeholder="$t('admin.instances.searchByInstanceName')"
          style="width: 200px; margin-right: 10px;"
          clearable
        />
        <el-input
          v-model="filters.providerName"
          :placeholder="$t('admin.instances.searchByProviderName')"
          style="width: 200px; margin-right: 10px;"
          clearable
        />
        <el-input
          v-model="filters.ownerName"
          :placeholder="$t('admin.instances.searchByOwner')"
          style="width: 200px; margin-right: 10px;"
          clearable
        />
        <el-select
          v-model="filters.status"
          :placeholder="$t('admin.instances.filterByStatus')"
          style="width: 120px; margin-right: 10px;"
          clearable
        >
          <el-option
            :label="$t('admin.instances.statusRunning')"
            value="running"
          />
          <el-option
            :label="$t('admin.instances.statusStopped')"
            value="stopped"
          />
          <el-option
            :label="$t('admin.instances.statusCreating')"
            value="creating"
          />
          <el-option
            :label="$t('admin.instances.statusStarting')"
            value="starting"
          />
          <el-option
            :label="$t('admin.instances.statusStopping')"
            value="stopping"
          />
          <el-option
            :label="$t('admin.instances.statusRestarting')"
            value="restarting"
          />
          <el-option
            :label="$t('admin.instances.statusResetting')"
            value="resetting"
          />
          <el-option
            :label="$t('admin.instances.statusError')"
            value="error"
          />
        </el-select>
        <el-select
          v-model="filters.instanceType"
          :placeholder="$t('admin.instances.filterByType')"
          style="width: 120px; margin-right: 10px;"
          clearable
        >
          <el-option
            :label="$t('admin.instances.typeContainer')"
            value="container"
          />
          <el-option
            :label="$t('admin.instances.typeVM')"
            value="vm"
          />
        </el-select>
        <el-button
          type="primary"
          @click="handleSearch"
        >
          {{ $t('common.search') }}
        </el-button>
        <el-button
          @click="handleReset"
        >
          {{ $t('common.reset') }}
        </el-button>
      </div>

      <el-table
        ref="tableRef"
        v-loading="loading"
        :data="instances"
        style="width: 100%"
        row-key="id"
        @selection-change="handleSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
        />
        <el-table-column
          prop="name"
          :label="$t('admin.instances.instanceName')"
          min-width="140"
          show-overflow-tooltip
          fixed="left"
        />
        <el-table-column
          prop="userName"
          :label="$t('admin.instances.owner')"
          width="100"
        />
        <el-table-column
          prop="providerName"
          :label="$t('admin.instances.provider')"
          width="120"
          show-overflow-tooltip
        />
        <el-table-column
          prop="instance_type"
          :label="$t('admin.instances.instanceType')"
          width="80"
        >
          <template #default="scope">
            <el-tag
              :type="scope.row.instance_type === 'container' ? 'primary' : 'success'"
              size="small"
            >
              {{ scope.row.instance_type === 'container' ? $t('admin.instances.typeContainer') : $t('admin.instances.typeVM') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.instances.acceleratorConfig')"
          width="130"
        >
          <template #default="scope">
            <template v-if="scope.row.gpuEnabled || scope.row.npuEnabled">
              <el-tag
                v-if="scope.row.gpuEnabled"
                type="warning"
                size="small"
                style="margin-right: 4px;"
              >
                {{ scope.row.gpuDeviceIds ? 'GPU:' + scope.row.gpuDeviceIds : $t('admin.instances.gpuEnabled') }}
              </el-tag>
              <el-tag
                v-if="scope.row.npuEnabled"
                type="danger"
                size="small"
              >
                {{ scope.row.npuDeviceIds ? 'NPU:' + scope.row.npuDeviceIds : $t('admin.instances.gpuEnabled') }}
              </el-tag>
            </template>
            <span v-else style="color: #c0c4cc; font-size: 12px;">—</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="sshPort"
          :label="$t('admin.instances.sshPort')"
          width="80"
        />
        <el-table-column
          prop="osType"
          :label="$t('admin.instances.system')"
          width="80"
        />
        <el-table-column
          :label="$t('admin.instances.instanceStatus')"
          width="100"
        >
          <template #default="scope">
            <el-tag
              :type="getStatusType(scope.row.status)"
              size="small"
            >
              {{ getStatusText(scope.row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.instances.trafficStatus')"
          width="100"
        >
          <template #default="scope">
            <el-tag
              v-if="scope.row.trafficLimited"
              type="danger"
              size="small"
            >
              {{ $t('admin.instances.limited') }}
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
          prop="createdAt"
          :label="$t('common.createTime')"
          width="140"
        >
          <template #default="scope">
            {{ formatDate(scope.row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          prop="expiresAt"
          :label="$t('admin.instances.expiryTime')"
          width="140"
        >
          <template #default="scope">
            <span :class="{ 'expired': isExpired(scope.row.expiresAt), 'expiring-soon': isExpiringSoon(scope.row.expiresAt) }">
              {{ formatDate(scope.row.expiresAt) }}
            </span>
            <div v-if="scope.row.isManualExpiry" style="margin-top: 4px;">
              <el-tag size="small" type="info">{{ $t('admin.instances.manualExpiry') }}</el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="isFrozen"
          :label="$t('admin.instances.freezeStatus')"
          width="100"
          align="center"
        >
          <template #default="scope">
            <el-tag :type="scope.row.isFrozen ? 'danger' : 'success'" size="small">
              {{ scope.row.isFrozen ? $t('admin.instances.frozen') : $t('admin.instances.normal') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="360"
          fixed="right"
        >
          <template #default="scope">
            <div class="action-buttons">
              <el-button
                size="small"
                type="info"
                @click="viewInstanceDetail(scope.row)"
              >
                {{ $t('admin.instances.viewDetail') }}
              </el-button>
              <el-button
                size="small"
                type="primary"
                @click="showActionDialog(scope.row)"
              >
                {{ $t('admin.instances.actions') }}
              </el-button>
              <el-button
                size="small"
                type="success"
                :disabled="scope.row.status !== 'running' || !scope.row.password || (!scope.row.hasSshMapping && scope.row.networkType === 'no_port_mapping')"
                :title="(!scope.row.hasSshMapping && scope.row.networkType === 'no_port_mapping') ? $t('admin.instances.sshNoPortMapping') : ''"
                @click="openSSHTerminal(scope.row)"
              >
                {{ $t('admin.instances.connect') }}
              </el-button>
              <el-button
                size="small"
                type="warning"
                @click="showTransferDialog(scope.row)"
              >
                {{ $t('admin.instances.transfer') }}
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="pagination-row">
        <el-pagination
          v-model:current-page="pagination.page"
          v-model:page-size="pagination.pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="pagination.total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </el-card>

    <!-- 实例详情对话框 -->
    <el-dialog
      v-model="detailDialogVisible"
      :title="$t('admin.instances.instanceDetails')"
      width="60%"
    >
      <div
        v-if="selectedInstance"
        class="instance-detail"
      >
        <el-descriptions
          :column="2"
          border
        >
          <el-descriptions-item :label="$t('admin.instances.instanceName')">
            {{ selectedInstance.name }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.uuid')">
            {{ selectedInstance.uuid }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.owner')">
            {{ selectedInstance.userName }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.provider')">
            {{ selectedInstance.providerName }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.instanceType')">
            <el-tag :type="selectedInstance.instance_type === 'container' ? 'primary' : 'success'">
              {{ selectedInstance.instance_type === 'container' ? $t('admin.instances.typeContainer') : $t('admin.instances.typeVM') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.instanceStatus')">
            <el-tag :type="getStatusType(selectedInstance.status)">
              {{ getStatusText(selectedInstance.status) }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.image')">
            {{ selectedInstance.image }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.operatingSystem')">
            {{ selectedInstance.osType }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.cpu')">
            {{ selectedInstance.cpu }}{{ $t('admin.instances.cores') }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.memory')">
            {{ formatMemory(selectedInstance.memory) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.disk')">
            {{ formatDisk(selectedInstance.disk) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.bandwidth')">
            {{ selectedInstance.bandwidth }}Mbps
          </el-descriptions-item>
          <el-descriptions-item
            v-if="selectedInstance.gpuEnabled || selectedInstance.npuEnabled"
            :label="$t('admin.instances.acceleratorConfig')"
          >
            <template v-if="selectedInstance.gpuEnabled">
              <el-tag type="warning" size="small">{{ $t('admin.instances.gpu') }} {{ selectedInstance.gpuDeviceIds || $t('admin.instances.gpuEnabled') }}</el-tag>&nbsp;
            </template>
            <template v-if="selectedInstance.npuEnabled">
              <el-tag type="danger" size="small">{{ $t('admin.instances.npu') }} {{ selectedInstance.npuDeviceIds || $t('admin.instances.gpuEnabled') }}</el-tag>
            </template>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.publicIPv4')">
            {{ selectedInstance.publicIP || $t('admin.instances.unassigned') }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.privateIPv4')">
            {{ selectedInstance.privateIP || $t('admin.instances.unassigned') }}
          </el-descriptions-item>
          <el-descriptions-item
            v-if="selectedInstance.ipv6Address"
            :label="$t('admin.instances.privateIPv6')"
          >
            {{ selectedInstance.ipv6Address }}
          </el-descriptions-item>
          <el-descriptions-item
            v-if="selectedInstance.publicIPv6"
            :label="$t('admin.instances.publicIPv6')"
          >
            {{ selectedInstance.publicIPv6 }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.sshPort')">
            {{ selectedInstance.sshPort }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.username')">
            {{ selectedInstance.username }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.password')">
            <span v-if="showPassword">{{ selectedInstance.password }}</span>
            <span v-else>••••••••</span>
            <el-button
              link
              @click="showPassword = !showPassword"
            >
              {{ showPassword ? $t('admin.instances.hide') : $t('admin.instances.show') }}
            </el-button>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.trafficLimit')">
            <el-tag
              v-if="selectedInstance.trafficLimited"
              type="danger"
            >
              {{ $t('admin.instances.limited') }}
            </el-tag>
            <el-tag
              v-else
              type="success"
            >
              {{ $t('common.normal') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.networkInterfaceV4')">
            {{ selectedInstance.pmacctInterfaceV4 || $t('admin.instances.notSet') }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.networkInterfaceV6')">
            {{ selectedInstance.pmacctInterfaceV6 || $t('admin.instances.notSet') }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('common.createTime')">
            {{ formatDate(selectedInstance.createdAt) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('common.updatedAt')">
            {{ formatDate(selectedInstance.updatedAt) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.expiryTime')">
            <span :class="{ 'expired': isExpired(selectedInstance.expiresAt), 'expiring-soon': isExpiringSoon(selectedInstance.expiresAt) }">
              {{ formatDate(selectedInstance.expiresAt) }}
            </span>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.healthStatus')">
            <el-tag :type="selectedInstance.healthStatus === 'healthy' ? 'success' : 'danger'">
              {{ selectedInstance.healthStatus === 'healthy' ? $t('admin.instances.healthy') : $t('admin.instances.unhealthy') }}
            </el-tag>
          </el-descriptions-item>
        </el-descriptions>

        <div
          class="traffic-info"
          style="margin-top: 20px;"
        >
          <h4>{{ $t('admin.instances.historicalTraffic') }}</h4>
          <el-descriptions
            :column="2"
            border
          >
            <el-descriptions-item :label="$t('admin.instances.inboundTraffic')">
              {{ formatTraffic(selectedInstance.usedTrafficIn) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.instances.outboundTraffic')">
              {{ formatTraffic(selectedInstance.usedTrafficOut) }}
            </el-descriptions-item>
          </el-descriptions>
        </div>
      </div>
    </el-dialog>

    <!-- 实例操作对话框 -->
    <el-dialog
      v-model="actionDialogVisible"
      :title="$t('admin.instances.instanceActions')"
      width="400px"
    >
      <div
        v-if="actionInstance"
        class="action-dialog-content"
      >
        <el-button
          type="success"
          :disabled="actionInstance.status === 'running' || actionInstance.status === 'starting'"
          :loading="actionLoading"
          style="width: 100%; margin-bottom: 10px;"
          @click="performAction('start')"
        >
          <el-icon><VideoPlay /></el-icon>
          {{ $t('common.start') }}
        </el-button>
        <el-button
          type="warning"
          :disabled="actionInstance.status === 'stopped' || actionInstance.status === 'stopping'"
          :loading="actionLoading"
          style="width: 100%; margin-bottom: 10px;"
          @click="performAction('stop')"
        >
          <el-icon><VideoPause /></el-icon>
          {{ $t('common.stop') }}
        </el-button>
        <el-button
          type="primary"
          :disabled="actionInstance.status !== 'running'"
          :loading="actionLoading"
          style="width: 100%; margin-bottom: 10px;"
          @click="performAction('restart')"
        >
          <el-icon><Refresh /></el-icon>
          {{ $t('common.restart') }}
        </el-button>
        <el-button
          type="info"
          :disabled="actionInstance.status !== 'running'"
          :loading="actionLoading"
          style="width: 100%; margin-bottom: 10px;"
          @click="performAction('resetPassword')"
        >
          <el-icon><Lock /></el-icon>
          {{ $t('admin.instances.resetPassword') }}
        </el-button>
        <el-button
          type="warning"
          :disabled="actionInstance.status !== 'running'"
          :loading="actionLoading"
          style="width: 100%; margin-bottom: 10px;"
          @click="performAction('reset')"
        >
          <el-icon><RefreshRight /></el-icon>
          {{ $t('admin.instances.resetSystem') }}
        </el-button>
        <el-divider />
        <el-button
          type="info"
          style="width: 100%; margin-bottom: 10px;"
          @click="performAction('setExpiry')"
        >
          {{ $t('admin.instances.setExpiry') }}
        </el-button>
        <el-button
          v-if="!actionInstance.isFrozen"
          type="warning"
          style="width: 100%; margin-bottom: 10px;"
          @click="performAction('freeze')"
        >
          {{ $t('admin.instances.freeze') }}
        </el-button>
        <el-button
          v-else
          type="success"
          style="width: 100%; margin-bottom: 10px;"
          @click="performAction('unfreeze')"
        >
          {{ $t('admin.instances.unfreeze') }}
        </el-button>
        <el-divider />
        <el-button
          type="danger"
          :loading="actionLoading"
          style="width: 100%;"
          @click="performAction('delete')"
        >
          <el-icon><Delete /></el-icon>
          {{ $t('common.delete') }}
        </el-button>
      </div>
    </el-dialog>

    <!-- 转移实例对话框 -->
    <el-dialog
      v-model="transferDialogVisible"
      :title="$t('admin.instances.transferInstance')"
      width="400px"
    >
      <el-form label-width="100px">
        <el-form-item :label="$t('admin.instances.instanceName')">
          <el-input :model-value="transferForm.instanceName" disabled />
        </el-form-item>
        <el-form-item :label="$t('admin.instances.targetUserId')">
          <el-select
            v-model="transferForm.targetUserId"
            filterable
            remote
            :remote-method="searchUsers"
            :loading="searchingUsers"
            :placeholder="$t('admin.instances.searchUserPlaceholder')"
            style="width: 100%"
          >
            <el-option
              v-for="user in userOptions"
              :key="user.id"
              :label="`${user.nickname || user.username} (ID: ${user.id})`"
              :value="user.id"
            />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="transferDialogVisible = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="transferLoading" @click="confirmTransfer">{{ $t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'
import { 
  VideoPlay, 
  VideoPause, 
  Refresh, 
  RefreshRight, 
  Lock, 
  Delete 
} from '@element-plus/icons-vue'
import { useInstanceManagement } from './composables/useInstanceManagement'

const {
  instances, loading, detailDialogVisible, actionDialogVisible,
  selectedInstance, actionInstance, actionLoading, showPassword,
  selectedInstances, transferDialogVisible, transferLoading, transferForm, tableRef,
  filters, pagination,
  loadInstances, handleSearch, handleReset, handleSizeChange, handleCurrentChange,
  viewInstanceDetail, showActionDialog, performAction,
  getStatusType, getStatusText, formatDate, formatMemory, formatDisk, formatTraffic,
  isExpired, isExpiringSoon, openSSHTerminal,
  handleSelectionChange, batchDeleteInstances, batchStartInstances, batchStopInstances,
  showTransferDialog, confirmTransfer, handleWindowResize,
  searchUsers, searchingUsers, userOptions,
  t
} = useInstanceManagement()

onMounted(() => {
  loadInstances()
  window.addEventListener('resize', handleWindowResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', handleWindowResize)
})
</script>

<style scoped>
.instances-container {
  width: 100%;
  height: 100%;
}

.header-row {
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

.filter-row {
  margin-bottom: 20px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.action-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.action-buttons .el-button {
  margin: 0;
}

.pagination-row {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.instance-detail {
  max-height: 70vh;
  overflow-y: auto;
}

.traffic-info {
  border-top: 1px solid #ebeef5;
  padding-top: 20px;
}

.expired {
  color: #f56c6c;
  font-weight: bold;
}

.expiring-soon {
  color: #e6a23c;
  font-weight: bold;
}

.action-dialog-content {
  padding: 10px 0;
}

.action-dialog-content .el-button {
  margin: 0;
}

/* 修复表格在窗口调整时的显示问题 */
:deep(.el-table) {
  overflow: visible !important;
}

:deep(.el-table__body-wrapper) {
  overflow-x: auto;
}

/* 确保fixed列正确渲染 */
:deep(.el-table__fixed),
:deep(.el-table__fixed-right) {
  height: auto !important;
}

:deep(.el-table__fixed-body-wrapper),
:deep(.el-table__fixed-right .el-table__fixed-body-wrapper) {
  height: auto !important;
}

/* 响应式设计 */
@media (max-width: 1200px) {
  .action-buttons {
    flex-direction: column;
  }
  
  .action-buttons .el-button {
    width: 100%;
    margin-bottom: 2px;
  }
}

@media (max-width: 768px) {
  .filter-row {
    flex-direction: column;
    align-items: stretch;
  }
  
  .filter-row > * {
    width: 100% !important;
    margin-bottom: 10px;
  }
  
  .header-actions {
    flex-wrap: wrap;
  }
}
</style>