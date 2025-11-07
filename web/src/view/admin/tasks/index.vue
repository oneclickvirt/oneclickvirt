<template>
  <div class="admin-tasks">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.tasks.title') }}</span>
          <p class="header-subtitle">
            {{ $t('admin.tasks.subtitle') }}
          </p>
        </div>
      </template>

      <!-- 统计卡片 -->
      <div class="stats-cards">
        <el-row :gutter="20">
          <el-col :span="4">
            <el-card class="stats-card">
              <div class="stat-item">
                <div class="stat-number">
                  {{ stats.totalTasks }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.tasks.totalTasks') }}
                </div>
              </div>
            </el-card>
          </el-col>
          <el-col :span="4">
            <el-card class="stats-card pending">
              <div class="stat-item">
                <div class="stat-number">
                  {{ stats.pendingTasks }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.tasks.pendingTasks') }}
                </div>
              </div>
            </el-card>
          </el-col>
          <el-col :span="4">
            <el-card class="stats-card running">
              <div class="stat-item">
                <div class="stat-number">
                  {{ stats.runningTasks }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.tasks.runningTasks') }}
                </div>
              </div>
            </el-card>
          </el-col>
          <el-col :span="4">
            <el-card class="stats-card completed">
              <div class="stat-item">
                <div class="stat-number">
                  {{ stats.completedTasks }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.tasks.completedTasks') }}
                </div>
              </div>
            </el-card>
          </el-col>
          <el-col :span="4">
            <el-card class="stats-card failed">
              <div class="stat-item">
                <div class="stat-number">
                  {{ stats.failedTasks }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.tasks.failedTasks') }}
                </div>
              </div>
            </el-card>
          </el-col>
          <el-col :span="4">
            <el-card class="stats-card timeout">
              <div class="stat-item">
                <div class="stat-number">
                  {{ stats.timeoutTasks }}
                </div>
                <div class="stat-label">
                  {{ $t('admin.tasks.timeoutTasks') }}
                </div>
              </div>
            </el-card>
          </el-col>
        </el-row>
      </div>

      <!-- 筛选器 -->
      <div class="filter-section">
        <el-form
          :inline="true"
          :model="filterForm"
          class="filter-form"
        >
          <el-form-item>
            <el-input
              v-model="filterForm.username"
              :placeholder="$t('admin.tasks.enterUsername')"
              clearable
              style="width: 120px"
            />
          </el-form-item>
          <el-form-item>
            <el-select
              v-model="filterForm.providerId"
              :placeholder="$t('admin.tasks.selectProvider')"
              clearable
              style="width: 150px"
            >
              <el-option
                v-for="provider in providers"
                :key="provider.id"
                :label="provider.name"
                :value="provider.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item>
            <el-select
              v-model="filterForm.taskType"
              :placeholder="$t('admin.tasks.selectTaskType')"
              clearable
              style="width: 120px"
            >
              <el-option
                :label="$t('admin.tasks.taskTypeCreate')"
                value="create"
              />
              <el-option
                :label="$t('admin.tasks.taskTypeStart')"
                value="start"
              />
              <el-option
                :label="$t('admin.tasks.taskTypeStop')"
                value="stop"
              />
              <el-option
                :label="$t('admin.tasks.taskTypeRestart')"
                value="restart"
              />
              <el-option
                :label="$t('admin.tasks.taskTypeReset')"
                value="reset"
              />
              <el-option
                :label="$t('admin.tasks.taskTypeDelete')"
                value="delete"
              />
              <el-option
                :label="$t('admin.tasks.taskTypeResetPassword')"
                value="reset-password"
              />
            </el-select>
          </el-form-item>
          <el-form-item>
            <el-select
              v-model="filterForm.status"
              :placeholder="$t('admin.tasks.selectStatus')"
              clearable
              style="width: 120px"
            >
              <el-option
                :label="$t('admin.tasks.statusPending')"
                value="pending"
              />
              <el-option
                :label="$t('admin.tasks.statusProcessing')"
                value="processing"
              />
              <el-option
                :label="$t('admin.tasks.statusRunning')"
                value="running"
              />
              <el-option
                :label="$t('admin.tasks.statusCompleted')"
                value="completed"
              />
              <el-option
                :label="$t('admin.tasks.statusFailed')"
                value="failed"
              />
              <el-option
                :label="$t('admin.tasks.statusCancelled')"
                value="cancelled"
              />
              <el-option
                :label="$t('admin.tasks.statusCancelling')"
                value="cancelling"
              />
              <el-option
                :label="$t('admin.tasks.statusTimeout')"
                value="timeout"
              />
            </el-select>
          </el-form-item>
          <el-form-item>
            <el-select
              v-model="filterForm.instanceType"
              :placeholder="$t('admin.tasks.selectInstanceType')"
              clearable
              style="width: 120px"
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
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              @click="loadTasks"
            >
              {{ $t('common.filter') }}
            </el-button>
            <el-button @click="resetFilter">
              {{ $t('common.reset') }}
            </el-button>
            <el-button 
              :loading="loading"
              @click="loadTasks"
            >
              <el-icon><Refresh /></el-icon>
              {{ $t('common.refresh') }}
            </el-button>
          </el-form-item>
        </el-form>
      </div>

      <!-- 任务列表 -->
      <el-card class="tasks-card">
        <el-table
          v-loading="loading"
          :data="tasks"
          class="tasks-table"
          :row-style="{ height: '60px' }"
          :cell-style="{ padding: '12px 0' }"
          :header-cell-style="{ background: '#f5f7fa', padding: '14px 0', fontWeight: '600' }"
          :default-sort="{prop: 'createdAt', order: 'descending'}"
        >
          <el-table-column
            prop="id"
            label="ID"
            width="80"
            align="center"
            sortable
          />
          <el-table-column
            prop="userName"
            :label="$t('common.user')"
            width="140"
            show-overflow-tooltip
          />
          <el-table-column
            prop="taskType"
            :label="$t('admin.tasks.taskType')"
            width="120"
            align="center"
          >
            <template #default="{ row }">
              <el-tag size="small">
                {{ getTaskTypeText(row.taskType) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column
            prop="status"
            :label="$t('common.status')"
            width="110"
            align="center"
          >
            <template #default="{ row }">
              <el-tag
                :type="getTaskStatusType(row.status)"
                size="small"
              >
                {{ getTaskStatusText(row.status) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column
            prop="progress"
            :label="$t('admin.tasks.progress')"
            width="140"
            align="center"
          >
            <template #default="{ row }">
              <el-progress
                v-if="row.status === 'running' || row.status === 'processing'"
                :percentage="row.progress"
                :status="row.status === 'failed' ? 'exception' : (row.status === 'completed' ? 'success' : undefined)"
                size="small"
              />
              <span v-else>-</span>
            </template>
          </el-table-column>
          <el-table-column
            prop="providerName"
            :label="$t('admin.tasks.provider')"
            width="140"
            show-overflow-tooltip
          />
          <el-table-column
            prop="instanceName"
            :label="$t('admin.tasks.instance')"
            min-width="180"
          >
            <template #default="{ row }">
              <div
                v-if="row.instanceName"
                class="instance-info"
              >
                <div class="instance-name">
                  {{ row.instanceName }}
                </div>
                <el-tag
                  v-if="row.instanceType"
                  size="small"
                  :type="row.instanceType === 'vm' ? 'warning' : 'info'"
                >
                  {{ row.instanceType === 'vm' ? $t('admin.instances.typeVM') : $t('admin.instances.typeContainer') }}
                </el-tag>
              </div>
              <span
                v-else
                class="text-gray"
              >-</span>
            </template>
          </el-table-column>
          <el-table-column
            prop="createdAt"
            :label="$t('common.createTime')"
            width="180"
            align="center"
            sortable
          >
            <template #default="{ row }">
              {{ formatDateTime(row.createdAt) }}
            </template>
          </el-table-column>
          <el-table-column
            prop="remainingTime"
            :label="$t('admin.tasks.remainingTime')"
            width="125"
            align="center"
          >
            <template #default="{ row }">
              <span v-if="row.status === 'running' && row.remainingTime > 0">
                {{ formatDuration(row.remainingTime) }}
              </span>
              <span
                v-else
                class="text-gray"
              >-</span>
            </template>
          </el-table-column>
          <el-table-column
            :label="$t('common.actions')"
            width="220"
            fixed="right"
            align="center"
          >
            <template #default="{ row }">
              <div class="action-buttons">
                <el-button
                  v-if="row.canForceStop"
                  type="danger"
                  size="small"
                  @click="showForceStopDialog(row)"
                >
                  {{ $t('admin.tasks.forceStop') }}
                </el-button>
                <el-button
                  v-if="row.status === 'pending'"
                  type="warning"
                  size="small"
                  @click="cancelTask(row)"
                >
                  {{ $t('admin.tasks.cancelTask') }}
                </el-button>
                <el-button
                  size="small"
                  @click="viewTaskDetail(row)"
                >
                  {{ $t('common.details') }}
                </el-button>
              </div>
            </template>
          </el-table-column>
        </el-table>

        <!-- 分页 -->
        <div class="pagination">
          <el-pagination
            v-model:current-page="pagination.page"
            v-model:page-size="pagination.pageSize"
            :total="total"
            :page-sizes="[10, 20, 50, 100]"
            layout="total, sizes, prev, pager, next, jumper"
            @size-change="loadTasks"
            @current-change="loadTasks"
          />
        </div>
      </el-card>

      <!-- 强制停止任务对话框 -->
      <el-dialog
        v-model="forceStopDialog.visible"
        :title="$t('admin.tasks.forceStopTask')"
        width="500px"
      >
        <el-form
          ref="forceStopFormRef"
          :model="forceStopDialog.form"
          :rules="forceStopDialog.rules"
          label-width="80px"
        >
          <el-form-item :label="$t('admin.tasks.taskInfo')">
            <div class="task-info">
              <p><strong>ID:</strong> {{ forceStopDialog.task?.id }}</p>
              <p><strong>{{ $t('admin.tasks.taskType') }}:</strong> {{ getTaskTypeText(forceStopDialog.task?.taskType) }}</p>
              <p><strong>{{ $t('common.user') }}:</strong> {{ forceStopDialog.task?.userName }}</p>
              <p><strong>{{ $t('admin.tasks.instance') }}:</strong> {{ forceStopDialog.task?.instanceName || '-' }}</p>
            </div>
          </el-form-item>
          <el-form-item 
            :label="$t('admin.tasks.stopReason')" 
            prop="reason"
          >
            <el-input
              v-model="forceStopDialog.form.reason"
              type="textarea"
              :rows="3"
              :placeholder="$t('admin.tasks.enterStopReason')"
            />
          </el-form-item>
        </el-form>
        <template #footer>
          <span class="dialog-footer">
            <el-button @click="forceStopDialog.visible = false">
              {{ $t('common.cancel') }}
            </el-button>
            <el-button
              type="danger"
              :loading="forceStopDialog.loading"
              @click="confirmForceStop"
            >
              {{ $t('admin.tasks.forceStop') }}
            </el-button>
          </span>
        </template>
      </el-dialog>

      <!-- 任务详情对话框 -->
      <el-dialog
        v-model="detailDialog.visible"
        :title="$t('admin.tasks.taskDetails')"
        width="600px"
      >
        <div
          v-if="detailDialog.task"
          class="task-detail"
        >
          <el-descriptions
            :column="2"
            border
          >
            <el-descriptions-item :label="$t('admin.tasks.taskId')">
              {{ detailDialog.task.id }}
            </el-descriptions-item>
            <el-descriptions-item label="UUID">
              {{ detailDialog.task.uuid }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.tasks.taskType')">
              {{ getTaskTypeText(detailDialog.task.taskType) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('common.status')">
              <el-tag :type="getTaskStatusType(detailDialog.task.status)">
                {{ getTaskStatusText(detailDialog.task.status) }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('common.user')">
              {{ detailDialog.task.userName }} (ID: {{ detailDialog.task.userId }})
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.tasks.provider')">
              {{ detailDialog.task.providerName }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.tasks.instance')">
              <div v-if="detailDialog.task.instanceName">
                {{ detailDialog.task.instanceName }}
                <el-tag
                  v-if="detailDialog.task.instanceType"
                  size="mini"
                  :type="detailDialog.task.instanceType === 'vm' ? 'warning' : 'info'"
                >
                  {{ detailDialog.task.instanceType === 'vm' ? $t('admin.instances.typeVM') : $t('admin.instances.typeContainer') }}
                </el-tag>
              </div>
              <span v-else>-</span>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.tasks.progress')">
              <el-progress
                v-if="detailDialog.task.status === 'running' || detailDialog.task.status === 'processing'"
                :percentage="detailDialog.task.progress"
                :status="detailDialog.task.status === 'failed' ? 'exception' : (detailDialog.task.status === 'completed' ? 'success' : undefined)"
              />
              <span v-else>-</span>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.tasks.timeoutDuration')">
              {{ formatDuration(detailDialog.task.timeoutDuration) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.tasks.remainingTime')">
              <span v-if="detailDialog.task.status === 'running' && detailDialog.task.remainingTime > 0">
                {{ formatDuration(detailDialog.task.remainingTime) }}
              </span>
              <span v-else>-</span>
            </el-descriptions-item>
            <el-descriptions-item
              :label="$t('common.createTime')"
              :span="2"
            >
              {{ formatDateTime(detailDialog.task.createdAt) }}
            </el-descriptions-item>
            <el-descriptions-item
              :label="$t('admin.tasks.startTime')"
              :span="2"
            >
              {{ detailDialog.task.startedAt ? formatDateTime(detailDialog.task.startedAt) : '-' }}
            </el-descriptions-item>
            <el-descriptions-item
              :label="$t('admin.tasks.completionTime')"
              :span="2"
            >
              {{ detailDialog.task.completedAt ? formatDateTime(detailDialog.task.completedAt) : '-' }}
            </el-descriptions-item>
            <el-descriptions-item
              v-if="detailDialog.task.errorMessage"
              :label="$t('admin.tasks.errorMessage')"
              :span="2"
            >
              <el-text type="danger">
                {{ detailDialog.task.errorMessage }}
              </el-text>
            </el-descriptions-item>
            <el-descriptions-item
              v-if="detailDialog.task.cancelReason"
              :label="$t('admin.tasks.cancelReason')"
              :span="2"
            >
              <el-text type="warning">
                {{ detailDialog.task.cancelReason }}
              </el-text>
            </el-descriptions-item>
            <el-descriptions-item
              v-if="detailDialog.task.preallocatedCpu > 0"
              :label="$t('admin.tasks.preallocatedConfig')"
              :span="2"
            >
              <el-tag size="small" type="info">
                CPU: {{ detailDialog.task.preallocatedCpu }} {{ $t('common.core') }}
              </el-tag>
              <el-tag size="small" type="info" style="margin-left: 8px;">
                {{ $t('admin.tasks.memory') }}: {{ (detailDialog.task.preallocatedMemory / 1024).toFixed(1) }} GB
              </el-tag>
              <el-tag size="small" type="info" style="margin-left: 8px;">
                {{ $t('admin.tasks.disk') }}: {{ (detailDialog.task.preallocatedDisk / 1024).toFixed(1) }} GB
              </el-tag>
              <el-tag size="small" type="info" style="margin-left: 8px;">
                {{ $t('admin.tasks.bandwidth') }}: {{ detailDialog.task.preallocatedBandwidth }} Mbps
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item
              v-if="detailDialog.task.statusMessage"
              :label="$t('admin.tasks.statusMessage')"
              :span="2"
            >
              {{ detailDialog.task.statusMessage }}
            </el-descriptions-item>
          </el-descriptions>
        </div>
      </el-dialog>
    </el-card>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { getAdminTasks, forceStopTask, getTaskStats, getTaskOverallStats, cancelUserTaskByAdmin } from '@/api/admin'
import { getProviderList } from '@/api/admin'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const loading = ref(false)
const tasks = ref([])
const providers = ref([])
const total = ref(0)

const stats = reactive({
  totalTasks: 0,
  pendingTasks: 0,
  runningTasks: 0,
  completedTasks: 0,
  failedTasks: 0,
  timeoutTasks: 0
})

const filterForm = reactive({
  username: '',
  providerId: '',
  taskType: '',
  status: '',
  instanceType: ''
})

const pagination = reactive({
  page: 1,
  pageSize: 10
})

const forceStopDialog = reactive({
  visible: false,
  loading: false,
  task: null,
  form: {
    reason: ''
  },
  rules: {
    reason: [
      { required: true, message: () => t('admin.tasks.enterStopReason'), trigger: 'blur' }
    ]
  }
})

const detailDialog = reactive({
  visible: false,
  task: null
})

const forceStopFormRef = ref()

// 加载任务列表
const loadTasks = async () => {
  try {
    loading.value = true
    const params = {
      page: pagination.page,
      pageSize: pagination.pageSize,
      ...filterForm
    }

    const response = await getAdminTasks(params)
    if (response.code === 0 || response.code === 200) {
      tasks.value = response.data.list || []
      total.value = response.data.total || 0
    } else {
      ElMessage.error(response.message || t('admin.tasks.loadFailed'))
    }
  } catch (error) {
    console.error('获取任务列表失败:', error)
    ElMessage.error(t('admin.tasks.loadFailed'))
  } finally {
    loading.value = false
  }
}

// 加载统计信息
const loadStats = async () => {
  try {
    const response = await getTaskOverallStats()
    if (response.code === 0 || response.code === 200) {
      Object.assign(stats, response.data)
    }
  } catch (error) {
    console.error('获取统计信息失败:', error)
  }
}

// 加载节点列表
const loadProviders = async () => {
  try {
    const response = await getProviderList({ page: 1, pageSize: 1000 })
    if (response.code === 0 || response.code === 200) {
      providers.value = response.data.list || []
    }
  } catch (error) {
    console.error('获取节点列表失败:', error)
  }
}

// 重置筛选
const resetFilter = () => {
  Object.assign(filterForm, {
    username: '',
    providerId: '',
    taskType: '',
    status: '',
    instanceType: ''
  })
  pagination.page = 1
  loadTasks()
}

// 强制停止任务
const showForceStopDialog = (task) => {
  forceStopDialog.task = task
  forceStopDialog.form.reason = ''
  forceStopDialog.visible = true
}

// 确认强制停止
const confirmForceStop = async () => {
  if (!forceStopFormRef.value) return

  await forceStopFormRef.value.validate(async (valid) => {
    if (!valid) return

    try {
      forceStopDialog.loading = true
      const response = await forceStopTask({
        taskId: forceStopDialog.task.id,
        reason: forceStopDialog.form.reason
      })

      if (response.code === 0 || response.code === 200) {
        ElMessage.success(t('admin.tasks.forceStopSuccess'))
        forceStopDialog.visible = false
        loadTasks()
        loadStats()
      } else {
        ElMessage.error(response.message || t('message.operationFailed'))
      }
    } catch (error) {
      console.error('强制停止任务失败:', error)
      ElMessage.error(t('message.operationFailed'))
    } finally {
      forceStopDialog.loading = false
    }
  })
}

// 取消任务
const cancelTask = async (task) => {
  try {
    await ElMessageBox.confirm(
      t('admin.tasks.cancelTaskConfirm', { taskType: getTaskTypeText(task.taskType) }),
      t('admin.tasks.confirmCancel'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )

    const response = await cancelUserTaskByAdmin(task.id)
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(t('admin.tasks.cancelSuccess'))
      loadTasks()
      loadStats()
    } else {
      ElMessage.error(response.message || t('message.operationFailed'))
    }
  } catch (error) {
    if (error !== 'cancel') {
      console.error('取消任务失败:', error)
      ElMessage.error(t('message.operationFailed'))
    }
  }
}

// 查看任务详情
const viewTaskDetail = (task) => {
  detailDialog.task = task
  detailDialog.visible = true
}

// 获取任务类型文本
const getTaskTypeText = (type) => {
  const typeMap = {
    'create': t('admin.tasks.taskTypeCreate'),
    'start': t('admin.tasks.taskTypeStart'),
    'stop': t('admin.tasks.taskTypeStop'),
    'restart': t('admin.tasks.taskTypeRestart'),
    'reset': t('admin.tasks.taskTypeReset'),
    'delete': t('admin.tasks.taskTypeDelete'),
    'reset-password': t('admin.tasks.taskTypeResetPassword'),
    'create-port-mapping': t('admin.tasks.taskTypeCreatePortMapping'),
    'delete-port-mapping': t('admin.tasks.taskTypeDeletePortMapping')
  }
  return typeMap[type] || type
}

// 获取任务状态类型
const getTaskStatusType = (status) => {
  const statusMap = {
    'pending': 'info',
    'processing': 'warning',
    'running': 'warning',
    'completed': 'success',
    'failed': 'danger',
    'cancelled': 'info',
    'cancelling': 'warning',
    'timeout': 'danger'
  }
  return statusMap[status] || 'info'
}

// 获取任务状态文本
const getTaskStatusText = (status) => {
  const statusMap = {
    'pending': t('admin.tasks.statusPending'),
    'processing': t('admin.tasks.statusProcessing'),
    'running': t('admin.tasks.statusRunning'),
    'completed': t('admin.tasks.statusCompleted'),
    'failed': t('admin.tasks.statusFailed'),
    'cancelled': t('admin.tasks.statusCancelled'),
    'cancelling': t('admin.tasks.statusCancelling'),
    'timeout': t('admin.tasks.statusTimeout')
  }
  return statusMap[status] || status
}

// 格式化日期时间
const formatDateTime = (dateTime) => {
  if (!dateTime) return '-'
  return new Date(dateTime).toLocaleString('zh-CN')
}

// 格式化时长
const formatDuration = (seconds) => {
  if (!seconds || seconds <= 0) return '-'
  
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = seconds % 60
  
  if (hours > 0) {
    return `${hours}h ${minutes}m ${secs}s`
  } else if (minutes > 0) {
    return `${minutes}m ${secs}s`
  } else {
    return `${secs}s`
  }
}

// 页面加载时初始化
onMounted(() => {
  loadTasks()
  loadStats()
  loadProviders()
  
  // 设置定时刷新
  setInterval(() => {
    if (!forceStopDialog.visible && !detailDialog.visible) {
      loadStats()
    }
  }, 30000) // 30秒刷新一次统计
})
</script>

<style scoped>
.card-header {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: #303133;
  }
  
  .header-subtitle {
    margin: 8px 0 0 0;
    color: #909399;
    font-size: 14px;
  }
}

.stats-cards {
  margin-bottom: 24px;
}

.stats-card {
  text-align: center;
  cursor: pointer;
  transition: transform 0.2s;
}

.stats-card:hover {
  transform: translateY(-2px);
}

.stats-card.pending {
  border-left: 4px solid #909399;
}

.stats-card.running {
  border-left: 4px solid #e6a23c;
}

.stats-card.completed {
  border-left: 4px solid #67c23a;
}

.stats-card.failed {
  border-left: 4px solid #f56c6c;
}

.stats-card.timeout {
  border-left: 4px solid #f56c6c;
}

.stat-item {
  padding: 10px;
}

.stat-number {
  font-size: 24px;
  font-weight: bold;
  margin-bottom: 5px;
}

.stat-label {
  font-size: 12px;
  color: #666;
}

.filter-section {
  margin-bottom: 20px;
}

.filter-form {
  background: #f5f5f5;
  padding: 20px;
  border-radius: 4px;
}

.tasks-table {
  width: 100%;
  
  .action-buttons {
    display: flex;
    gap: 10px;
    justify-content: center;
    align-items: center;
    flex-wrap: wrap;
    padding: 4px 0;
    
    .el-button {
      margin: 0 !important;
    }
  }
  
  .instance-info {
    display: flex;
    flex-direction: column;
    gap: 4px;
    
    .instance-name {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      font-weight: 500;
    }
  }
  
  :deep(.el-table__cell) {
    .cell {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }
}

.pagination {
  margin-top: 20px;
  text-align: center;
}

.task-info {
  background: #f5f5f5;
  padding: 15px;
  border-radius: 4px;
}

.task-info p {
  margin: 5px 0;
  font-size: 14px;
}

.task-detail {
  max-height: 500px;
  overflow-y: auto;
}

.text-gray {
  color: #999;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
