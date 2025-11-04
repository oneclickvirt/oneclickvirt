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
          prop="expiredAt"
          :label="$t('admin.instances.expiryTime')"
          width="140"
        >
          <template #default="scope">
            <span :class="{ 'expired': isExpired(scope.row.expiredAt), 'expiring-soon': isExpiringSoon(scope.row.expiredAt) }">
              {{ formatDate(scope.row.expiredAt) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="220"
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
                :disabled="scope.row.status !== 'running' || !scope.row.password"
                @click="openSSHTerminal(scope.row)"
              >
                {{ $t('admin.instances.connect') }}
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
          <el-descriptions-item :label="$t('common.status')">
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
          <el-descriptions-item :label="$t('admin.instances.vnstatInterface')">
            {{ selectedInstance.vnstatInterface || $t('admin.instances.notSet') }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('common.createTime')">
            {{ formatDate(selectedInstance.createdAt) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('common.updatedAt')">
            {{ formatDate(selectedInstance.updatedAt) }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.instances.expiryTime')">
            <span :class="{ 'expired': isExpired(selectedInstance.expiredAt), 'expiring-soon': isExpiringSoon(selectedInstance.expiredAt) }">
              {{ formatDate(selectedInstance.expiredAt) }}
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
          <h4>{{ $t('admin.instances.trafficUsage') }}</h4>
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
  </div>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { 
  VideoPlay, 
  VideoPause, 
  Refresh, 
  RefreshRight, 
  Lock, 
  Delete 
} from '@element-plus/icons-vue'
import { getAllInstances, deleteInstance as deleteInstanceApi, adminInstanceAction, resetInstancePassword } from '@/api/admin'
import { useI18n } from 'vue-i18n'
import { useSSHStore } from '@/pinia/modules/ssh'

const { t } = useI18n()
const sshStore = useSSHStore()

const instances = ref([])
const loading = ref(false)
const detailDialogVisible = ref(false)
const actionDialogVisible = ref(false)
const selectedInstance = ref(null)
const actionInstance = ref(null)
const actionLoading = ref(false)
const showPassword = ref(false)
const selectedInstances = ref([])

// 筛选条件
const filters = ref({
  instanceName: '',
  providerName: '',
  status: '',
  instanceType: ''
})

// 分页
const pagination = ref({
  page: 1,
  pageSize: 10,
  total: 0
})

const loadInstances = async () => {
  loading.value = true
  try {
    const params = {
      page: pagination.value.page,
      pageSize: pagination.value.pageSize,
      name: filters.value.instanceName || undefined,
      providerName: filters.value.providerName || undefined,
      status: filters.value.status || undefined,
      instance_type: filters.value.instanceType || undefined
    }
    
    // 移除undefined值
    Object.keys(params).forEach(key => {
      if (params[key] === undefined) {
        delete params[key]
      }
    })
    
    const response = await getAllInstances(params)
    instances.value = response.data.list || []
    pagination.value.total = response.data.total || 0
  } catch (error) {
    ElMessage.error(t('admin.instances.loadFailed'))
    console.error('Load instances error:', error)
  } finally {
    loading.value = false
  }
}

const handleSearch = () => {
  pagination.value.page = 1
  loadInstances()
}

const handleReset = () => {
  filters.value.instanceName = ''
  filters.value.providerName = ''
  filters.value.status = ''
  filters.value.instanceType = ''
  pagination.value.page = 1
  loadInstances()
}

const handleSizeChange = (val) => {
  pagination.value.pageSize = val
  pagination.value.page = 1
  loadInstances()
}

const handleCurrentChange = (val) => {
  pagination.value.page = val
  loadInstances()
}

const viewInstanceDetail = (instance) => {
  selectedInstance.value = instance
  showPassword.value = false
  detailDialogVisible.value = true
}

// 显示操作对话框
const showActionDialog = (instance) => {
  actionInstance.value = instance
  actionDialogVisible.value = true
}

// 执行操作
const performAction = async (action) => {
  const actionText = {
    'start': t('common.start'),
    'stop': t('common.stop'),
    'restart': t('common.restart'),
    'reset': t('admin.instances.resetSystem'),
    'resetPassword': t('admin.instances.resetPassword'),
    'delete': t('common.delete')
  }[action]
  
  try {
    await ElMessageBox.confirm(
      t('admin.instances.manageConfirm', { action: actionText, name: actionInstance.value.name }),
      t('admin.instances.manageTitle', { action: actionText }),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    actionLoading.value = true
    
    // 立即更新本地状态
    const instanceId = actionInstance.value.id
    const instanceIndex = instances.value.findIndex(i => i.id === instanceId)
    if (instanceIndex !== -1) {
      const statusMap = {
        'start': 'starting',
        'stop': 'stopping',
        'restart': 'restarting',
        'reset': 'resetting',
        'resetPassword': instances.value[instanceIndex].status, // 重置密码不改变状态
        'delete': 'deleting'
      }
      instances.value[instanceIndex].status = statusMap[action]
    }
    
    // 重置密码使用特殊API
    if (action === 'resetPassword') {
      await resetInstancePassword(instanceId)
    } else {
      await adminInstanceAction(instanceId, action)
    }
    
    ElMessage.success(t('admin.instances.taskCreated', { action: actionText }))
    
    // 关闭对话框
    actionDialogVisible.value = false
    actionInstance.value = null
    
    // 如果是删除操作，延迟刷新以等待删除完成
    if (action === 'delete') {
      setTimeout(() => loadInstances(), 1000)
    } else {
      // 其他操作也刷新，但状态已经立即更新
      setTimeout(() => loadInstances(), 500)
    }
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.instances.actionFailed', { action: actionText }))
      // 操作失败，刷新列表恢复正确状态
      await loadInstances()
    }
  } finally {
    actionLoading.value = false
  }
}

const getStatusType = (status) => {
  const types = {
    running: 'success',
    stopped: 'info',
    error: 'danger',
    failed: 'danger',
    starting: 'warning',
    stopping: 'warning',
    creating: 'warning',
    restarting: 'warning',
    resetting: 'warning',
    deleting: 'danger'
  }
  return types[status] || 'info'
}

const getStatusText = (status) => {
  const texts = {
    running: t('admin.instances.statusRunning'),
    stopped: t('admin.instances.statusStopped'),
    error: t('admin.instances.statusError'),
    failed: t('admin.instances.statusFailed'),
    starting: t('admin.instances.statusStarting'),
    stopping: t('admin.instances.statusStopping'),
    creating: t('admin.instances.statusCreating'),
    restarting: t('admin.instances.statusRestarting'),
    resetting: t('admin.instances.statusResetting'),
    deleting: t('admin.instances.statusDeleting')
  }
  return texts[status] || status
}

const formatDate = (dateString) => {
  if (!dateString) return t('admin.instances.notSet')
  const date = new Date(dateString)
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  })
}

const formatMemory = (memory) => {
  if (!memory) return '0MB'
  if (memory >= 1024) {
    return `${(memory / 1024).toFixed(1)}GB`
  }
  return `${memory}MB`
}

const formatDisk = (disk) => {
  if (!disk) return '0MB'
  if (disk >= 1024 * 1024) {
    return `${(disk / (1024 * 1024)).toFixed(1)}TB`
  } else if (disk >= 1024) {
    return `${(disk / 1024).toFixed(1)}GB`
  }
  return `${disk}MB`
}

const formatTraffic = (traffic) => {
  if (!traffic) return '0MB'
  if (traffic >= 1024 * 1024) {
    return `${(traffic / (1024 * 1024)).toFixed(2)}TB`
  } else if (traffic >= 1024) {
    return `${(traffic / 1024).toFixed(2)}GB`
  }
  return `${traffic}MB`
}

const isExpired = (expiredAt) => {
  if (!expiredAt) return false
  return new Date(expiredAt) < new Date()
}

const isExpiringSoon = (expiredAt) => {
  if (!expiredAt) return false
  const expireDate = new Date(expiredAt)
  const now = new Date()
  const daysDiff = (expireDate - now) / (1000 * 60 * 60 * 24)
  return daysDiff > 0 && daysDiff <= 7 // 7天内到期
}

// 打开SSH终端
const openSSHTerminal = (instance) => {
  if (!instance.id) {
    ElMessage.error(t('admin.instances.instanceNotFound'))
    return
  }
  
  if (instance.status !== 'running') {
    ElMessage.warning(t('admin.instances.instanceNotRunning'))
    return
  }
  
  if (!instance.password) {
    ElMessage.warning(t('admin.instances.noPassword'))
    return
  }
  
  // 创建或显示SSH连接（由全局管理器处理）
  if (!sshStore.hasConnection(instance.id)) {
    sshStore.createConnection(instance.id, instance.name, true) // true表示管理员模式
  } else {
    sshStore.showConnection(instance.id)
  }
}

// 处理表格选择变化
const handleSelectionChange = (selection) => {
  selectedInstances.value = selection
}

// 批量删除实例
const batchDeleteInstances = async () => {
  if (selectedInstances.value.length === 0) {
    ElMessage.warning(t('admin.instances.selectDeleteWarning'))
    return
  }

  try {
    await ElMessageBox.confirm(
      t('admin.instances.batchDeleteConfirm', { count: selectedInstances.value.length }),
      t('admin.instances.batchDeleteTitle'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )

    let successCount = 0
    let failCount = 0
    const errors = []

    // 依次创建删除任务
    for (const instance of selectedInstances.value) {
      try {
        await adminInstanceAction(instance.id, 'delete')
        successCount++
      } catch (error) {
        failCount++
        errors.push(`${instance.name}: ${error.message || t('common.deleteFailed')}`)
      }
    }

    // 显示结果
    if (failCount === 0) {
      ElMessage.success(t('admin.instances.batchDeleteSuccess', { count: successCount }))
    } else if (successCount === 0) {
      ElMessage.error(t('admin.instances.batchDeleteAllFailed'))
    } else {
      ElMessage.warning(t('admin.instances.batchDeletePartialSuccess', { success: successCount, fail: failCount }))
    }

    // 刷新列表
    await loadInstances()
    selectedInstances.value = []
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.instances.batchDeleteFailed'))
    }
  }
}

// 批量启动
const batchStartInstances = async () => {
  if (selectedInstances.value.length === 0) {
    ElMessage.warning(t('admin.instances.selectStartWarning'))
    return
  }

  try {
    let success = 0
    let fail = 0
    
    // 立即更新所有选中实例的状态为 starting
    selectedInstances.value.forEach(inst => {
      const index = instances.value.findIndex(i => i.id === inst.id)
      if (index !== -1) {
        instances.value[index].status = 'starting'
      }
    })
    
    for (const inst of selectedInstances.value) {
      try {
        await adminInstanceAction(inst.id, 'start')
        success++
      } catch (e) {
        fail++
        // 失败的恢复原状态
        const index = instances.value.findIndex(i => i.id === inst.id)
        if (index !== -1) {
          instances.value[index].status = 'stopped'
        }
      }
    }
    
    if (fail === 0) ElMessage.success(t('admin.instances.batchStartSuccess', { count: success }))
    else ElMessage.warning(t('admin.instances.batchStartPartial', { success, fail }))
    
    // 延迟刷新以获取最新状态
    setTimeout(() => loadInstances(), 500)
    selectedInstances.value = []
  } catch (err) {
    ElMessage.error(t('admin.instances.batchStartFailed'))
    await loadInstances()
  }
}

// 批量停止
const batchStopInstances = async () => {
  if (selectedInstances.value.length === 0) {
    ElMessage.warning(t('admin.instances.selectStopWarning'))
    return
  }

  try {
    let success = 0
    let fail = 0
    
    // 立即更新所有选中实例的状态为 stopping
    selectedInstances.value.forEach(inst => {
      const index = instances.value.findIndex(i => i.id === inst.id)
      if (index !== -1) {
        instances.value[index].status = 'stopping'
      }
    })
    
    for (const inst of selectedInstances.value) {
      try {
        await adminInstanceAction(inst.id, 'stop')
        success++
      } catch (e) {
        fail++
        // 失败的恢复原状态
        const index = instances.value.findIndex(i => i.id === inst.id)
        if (index !== -1) {
          instances.value[index].status = 'running'
        }
      }
    }
    
    if (fail === 0) ElMessage.success(t('admin.instances.batchStopSuccess', { count: success }))
    else ElMessage.warning(t('admin.instances.batchStopPartial', { success, fail }))
    
    // 延迟刷新以获取最新状态
    setTimeout(() => loadInstances(), 500)
    selectedInstances.value = []
  } catch (err) {
    ElMessage.error(t('admin.instances.batchStopFailed'))
    await loadInstances()
  }
}

onMounted(() => {
  loadInstances()
})
</script>

<style scoped>
.header-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: #303133;
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
}
</style>