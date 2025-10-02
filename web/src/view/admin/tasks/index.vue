<template>
  <div class="admin-tasks">
    <div class="page-header">
      <h1>任务管理</h1>
      <p>管理所有用户的任务，可强制停止运行中的任务</p>
    </div>

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
                总任务数
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
                等待中
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
                执行中
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
                已完成
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
                失败
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
                超时
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
        <el-form-item label="用户ID">
          <el-input
            v-model="filterForm.userId"
            placeholder="输入用户ID"
            clearable
            style="width: 120px"
          />
        </el-form-item>
        <el-form-item label="节点">
          <el-select
            v-model="filterForm.providerId"
            placeholder="选择节点"
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
        <el-form-item label="任务类型">
          <el-select
            v-model="filterForm.taskType"
            placeholder="选择任务类型"
            clearable
            style="width: 120px"
          >
            <el-option
              label="创建实例"
              value="create"
            />
            <el-option
              label="启动实例"
              value="start"
            />
            <el-option
              label="停止实例"
              value="stop"
            />
            <el-option
              label="重启实例"
              value="restart"
            />
            <el-option
              label="重置系统"
              value="reset"
            />
            <el-option
              label="删除实例"
              value="delete"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="任务状态">
          <el-select
            v-model="filterForm.status"
            placeholder="选择状态"
            clearable
            style="width: 120px"
          >
            <el-option
              label="等待中"
              value="pending"
            />
            <el-option
              label="处理中"
              value="processing"
            />
            <el-option
              label="执行中"
              value="running"
            />
            <el-option
              label="已完成"
              value="completed"
            />
            <el-option
              label="失败"
              value="failed"
            />
            <el-option
              label="已取消"
              value="cancelled"
            />
            <el-option
              label="取消中"
              value="cancelling"
            />
            <el-option
              label="超时"
              value="timeout"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="实例类型">
          <el-select
            v-model="filterForm.instanceType"
            placeholder="选择实例类型"
            clearable
            style="width: 120px"
          >
            <el-option
              label="容器"
              value="container"
            />
            <el-option
              label="虚拟机"
              value="vm"
            />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            @click="loadTasks"
          >
            筛选
          </el-button>
          <el-button @click="resetFilter">
            重置
          </el-button>
          <el-button 
            :loading="loading"
            @click="loadTasks"
          >
            <el-icon><Refresh /></el-icon>
            刷新
          </el-button>
        </el-form-item>
      </el-form>
    </div>

    <!-- 任务列表 -->
    <el-card>
      <el-table
        v-loading="loading"
        :data="tasks"
        style="width: 100%"
        :default-sort="{prop: 'createdAt', order: 'descending'}"
      >
        <el-table-column
          prop="id"
          label="ID"
          width="80"
          sortable
        />
        <el-table-column
          prop="userName"
          label="用户"
          width="120"
        />
        <el-table-column
          prop="taskType"
          label="任务类型"
          width="100"
        >
          <template #default="{ row }">
            <el-tag size="small">
              {{ getTaskTypeText(row.taskType) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          label="状态"
          width="100"
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
          label="进度"
          width="120"
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
          label="节点"
          width="120"
        />
        <el-table-column
          prop="instanceName"
          label="实例"
          width="150"
        >
          <template #default="{ row }">
            <div v-if="row.instanceName">
              <div>{{ row.instanceName }}</div>
              <el-tag
                v-if="row.instanceType"
                size="mini"
                :type="row.instanceType === 'vm' ? 'warning' : 'info'"
              >
                {{ row.instanceType === 'vm' ? '虚拟机' : '容器' }}
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
          label="创建时间"
          width="160"
          sortable
        >
          <template #default="{ row }">
            {{ formatDateTime(row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          prop="remainingTime"
          label="剩余时间"
          width="100"
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
          label="操作"
          width="180"
          fixed="right"
        >
          <template #default="{ row }">
            <el-button
              v-if="row.canForceStop"
              type="danger"
              size="small"
              @click="showForceStopDialog(row)"
            >
              强制停止
            </el-button>
            <el-button
              v-if="row.status === 'pending'"
              type="warning"
              size="small"
              @click="cancelTask(row)"
            >
              取消任务
            </el-button>
            <el-button
              size="small"
              @click="viewTaskDetail(row)"
            >
              详情
            </el-button>
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
      title="强制停止任务"
      width="500px"
    >
      <el-form
        ref="forceStopFormRef"
        :model="forceStopDialog.form"
        :rules="forceStopDialog.rules"
        label-width="80px"
      >
        <el-form-item label="任务信息">
          <div class="task-info">
            <p><strong>ID:</strong> {{ forceStopDialog.task?.id }}</p>
            <p><strong>类型:</strong> {{ getTaskTypeText(forceStopDialog.task?.taskType) }}</p>
            <p><strong>用户:</strong> {{ forceStopDialog.task?.userName }}</p>
            <p><strong>实例:</strong> {{ forceStopDialog.task?.instanceName || '-' }}</p>
          </div>
        </el-form-item>
        <el-form-item 
          label="停止原因" 
          prop="reason"
        >
          <el-input
            v-model="forceStopDialog.form.reason"
            type="textarea"
            :rows="3"
            placeholder="请输入强制停止的原因"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="forceStopDialog.visible = false">
            取消
          </el-button>
          <el-button
            type="danger"
            :loading="forceStopDialog.loading"
            @click="confirmForceStop"
          >
            强制停止
          </el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 任务详情对话框 -->
    <el-dialog
      v-model="detailDialog.visible"
      title="任务详情"
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
          <el-descriptions-item label="任务ID">
            {{ detailDialog.task.id }}
          </el-descriptions-item>
          <el-descriptions-item label="UUID">
            {{ detailDialog.task.uuid }}
          </el-descriptions-item>
          <el-descriptions-item label="任务类型">
            {{ getTaskTypeText(detailDialog.task.taskType) }}
          </el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="getTaskStatusType(detailDialog.task.status)">
              {{ getTaskStatusText(detailDialog.task.status) }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="用户">
            {{ detailDialog.task.userName }} (ID: {{ detailDialog.task.userId }})
          </el-descriptions-item>
          <el-descriptions-item label="节点">
            {{ detailDialog.task.providerName }}
          </el-descriptions-item>
          <el-descriptions-item label="实例">
            <div v-if="detailDialog.task.instanceName">
              {{ detailDialog.task.instanceName }}
              <el-tag
                v-if="detailDialog.task.instanceType"
                size="mini"
                :type="detailDialog.task.instanceType === 'vm' ? 'warning' : 'info'"
              >
                {{ detailDialog.task.instanceType === 'vm' ? '虚拟机' : '容器' }}
              </el-tag>
            </div>
            <span v-else>-</span>
          </el-descriptions-item>
          <el-descriptions-item label="进度">
            <el-progress
              v-if="detailDialog.task.status === 'running' || detailDialog.task.status === 'processing'"
              :percentage="detailDialog.task.progress"
              :status="detailDialog.task.status === 'failed' ? 'exception' : (detailDialog.task.status === 'completed' ? 'success' : undefined)"
            />
            <span v-else>-</span>
          </el-descriptions-item>
          <el-descriptions-item label="超时时间">
            {{ formatDuration(detailDialog.task.timeoutDuration) }}
          </el-descriptions-item>
          <el-descriptions-item label="剩余时间">
            <span v-if="detailDialog.task.status === 'running' && detailDialog.task.remainingTime > 0">
              {{ formatDuration(detailDialog.task.remainingTime) }}
            </span>
            <span v-else>-</span>
          </el-descriptions-item>
          <el-descriptions-item
            label="创建时间"
            :span="2"
          >
            {{ formatDateTime(detailDialog.task.createdAt) }}
          </el-descriptions-item>
          <el-descriptions-item
            label="开始时间"
            :span="2"
          >
            {{ detailDialog.task.startedAt ? formatDateTime(detailDialog.task.startedAt) : '-' }}
          </el-descriptions-item>
          <el-descriptions-item
            label="完成时间"
            :span="2"
          >
            {{ detailDialog.task.completedAt ? formatDateTime(detailDialog.task.completedAt) : '-' }}
          </el-descriptions-item>
          <el-descriptions-item
            v-if="detailDialog.task.errorMessage"
            label="错误信息"
            :span="2"
          >
            <el-text type="danger">
              {{ detailDialog.task.errorMessage }}
            </el-text>
          </el-descriptions-item>
          <el-descriptions-item
            v-if="detailDialog.task.cancelReason"
            label="取消原因"
            :span="2"
          >
            <el-text type="warning">
              {{ detailDialog.task.cancelReason }}
            </el-text>
          </el-descriptions-item>
          <el-descriptions-item
            v-if="detailDialog.task.statusMessage"
            label="状态信息"
            :span="2"
          >
            {{ detailDialog.task.statusMessage }}
          </el-descriptions-item>
        </el-descriptions>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { getAdminTasks, forceStopTask, getTaskStats, getTaskOverallStats, cancelUserTaskByAdmin } from '@/api/admin'
import { getProviderList } from '@/api/admin'

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
  userId: '',
  providerId: '',
  taskType: '',
  status: '',
  instanceType: ''
})

const pagination = reactive({
  page: 1,
  pageSize: 20
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
      { required: true, message: '请输入停止原因', trigger: 'blur' }
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
      ElMessage.error(response.message || '获取任务列表失败')
    }
  } catch (error) {
    console.error('获取任务列表失败:', error)
    ElMessage.error('获取任务列表失败')
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
    userId: '',
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
        ElMessage.success('任务已强制停止')
        forceStopDialog.visible = false
        loadTasks()
        loadStats()
      } else {
        ElMessage.error(response.message || '操作失败')
      }
    } catch (error) {
      console.error('强制停止任务失败:', error)
      ElMessage.error('操作失败')
    } finally {
      forceStopDialog.loading = false
    }
  })
}

// 取消任务
const cancelTask = async (task) => {
  try {
    await ElMessageBox.confirm(
      `确定要取消任务 "${getTaskTypeText(task.taskType)}" 吗？`,
      '确认取消',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    const response = await cancelUserTaskByAdmin(task.id)
    if (response.code === 0 || response.code === 200) {
      ElMessage.success('任务已取消')
      loadTasks()
      loadStats()
    } else {
      ElMessage.error(response.message || '操作失败')
    }
  } catch (error) {
    if (error !== 'cancel') {
      console.error('取消任务失败:', error)
      ElMessage.error('操作失败')
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
    'create': '创建实例',
    'start': '启动实例',
    'stop': '停止实例',
    'restart': '重启实例',
    'reset': '重置系统',
    'delete': '删除实例'
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
    'pending': '等待中',
    'processing': '处理中',
    'running': '执行中',
    'completed': '已完成',
    'failed': '失败',
    'cancelled': '已取消',
    'cancelling': '取消中',
    'timeout': '超时'
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
.admin-tasks {
  padding: 20px;
}

.page-header {
  margin-bottom: 20px;
}

.page-header h1 {
  margin: 0 0 8px 0;
  font-size: 24px;
  font-weight: 600;
}

.page-header p {
  margin: 0;
  color: #666;
  font-size: 14px;
}

.stats-cards {
  margin-bottom: 20px;
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
