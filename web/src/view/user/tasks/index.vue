<template>
  <div class="user-tasks">
    <div class="page-header">
      <h1>任务列表</h1>
      <p>查看您的实例操作任务状态</p>
    </div>

    <!-- 筛选器 -->
    <div class="filter-section">
      <el-form
        :inline="true"
        :model="filterForm"
      >
        <el-form-item label="节点">
          <el-select
            v-model="filterForm.providerId"
            placeholder="选择节点"
            clearable
          >
            <el-option
              label="全部"
              value=""
            />
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
          >
            <el-option
              label="全部"
              value=""
            />
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
          >
            <el-option
              label="全部"
              value=""
            />
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
        <el-form-item>
          <el-button
            type="primary"
            @click="() => loadTasks(true)"
          >
            筛选
          </el-button>
          <el-button @click="resetFilter">
            重置
          </el-button>
          <el-button @click="() => loadTasks(true)">
            <el-icon><Refresh /></el-icon>
            刷新
          </el-button>
        </el-form-item>
      </el-form>
    </div>

    <!-- 服务器任务分组 -->
    <div class="server-tasks">
      <div 
        v-for="serverGroup in groupedTasks" 
        :key="serverGroup.providerId"
        class="server-group"
      >
        <div class="server-header">
          <h2>{{ serverGroup.providerName }}</h2>
          <div class="server-status">
            <el-tag 
              v-if="serverGroup.currentTasks.length > 0"
              type="warning"
              effect="dark"
            >
              执行中: {{ serverGroup.currentTasks.length }}个任务
            </el-tag>
            <el-tag 
              v-else
              type="success"
            >
              空闲
            </el-tag>
          </div>
        </div>

        <!-- 当前执行中的任务 -->
        <div
          v-if="serverGroup.currentTasks.length > 0"
          class="current-tasks"
        >
          <h3>执行中的任务 ({{ serverGroup.currentTasks.length }})</h3>
          <div 
            v-for="currentTask in serverGroup.currentTasks" 
            :key="currentTask.id"
            class="current-task"
          >
            <el-card class="task-card current">
              <div class="task-header">
                <div class="task-info">
                  <h3>{{ getTaskTypeText(currentTask.taskType) }}</h3>
                  <span class="task-target">{{ currentTask.instanceName || '新实例' }}</span>
                </div>
                <div class="task-status">
                  <el-tag
                    :type="getTaskStatusType(currentTask.status)"
                    effect="dark"
                  >
                    {{ getTaskStatusText(currentTask.status) }}
                  </el-tag>
                </div>
              </div>
              <div class="task-progress">
                <el-progress 
                  v-if="currentTask.status === 'running' || currentTask.status === 'processing'"
                  :percentage="currentTask.progress || 0"
                  :status="currentTask.status === 'failed' ? 'exception' : undefined"
                />
                <div class="progress-text">
                  {{ currentTask.statusMessage || getDefaultStatusMessage(currentTask.status) }}
                </div>
              </div>
              <div class="task-details">
                <div class="detail-item">
                  <span class="label">创建时间:</span>
                  <span class="value">{{ formatDate(currentTask.createdAt) }}</span>
                </div>
                <div class="detail-item">
                  <span class="label">预计完成:</span>
                  <span class="value">{{ getEstimatedTime(currentTask) }}</span>
                </div>
              </div>
            </el-card>
          </div>
        </div>

        <!-- 等待队列 -->
        <div
          v-if="serverGroup.pendingTasks.length > 0"
          class="pending-tasks"
        >
          <h3>等待队列 ({{ serverGroup.pendingTasks.length }})</h3>
          <div class="tasks-list">
            <div 
              v-for="(task, index) in serverGroup.pendingTasks" 
              :key="task.id"
              class="task-item pending"
            >
              <div class="task-order">
                {{ index + 1 }}
              </div>
              <div class="task-content">
                <div class="task-name">
                  {{ getTaskTypeText(task.taskType) }}
                </div>
                <div class="task-target">
                  {{ task.instanceName || '新实例' }}
                </div>
                <div class="task-time">
                  {{ formatDate(task.createdAt) }}
                </div>
              </div>
              <div class="task-actions">
                <el-button 
                  size="small" 
                  type="danger" 
                  text
                  :disabled="!task.canCancel"
                  @click="cancelTask(task)"
                >
                  取消
                </el-button>
              </div>
            </div>
          </div>
        </div>

        <!-- 历史任务 -->
        <div
          v-if="serverGroup.historyTasks.length > 0"
          class="history-tasks"
        >
          <el-collapse v-model="expandedHistory">
            <el-collapse-item 
              :title="`历史任务 (${serverGroup.historyTasks.length})`"
              :name="serverGroup.providerId"
            >
              <div class="tasks-list">
                <div 
                  v-for="task in serverGroup.historyTasks" 
                  :key="task.id"
                  class="task-item history"
                  :class="task.status"
                >
                  <div class="task-content">
                    <div class="task-name">
                      {{ getTaskTypeText(task.taskType) }}
                    </div>
                    <div class="task-target">
                      {{ task.instanceName || '新实例' }}
                    </div>
                    <div class="task-time">
                      {{ formatDate(task.createdAt) }}
                    </div>
                    <div
                      v-if="task.completedAt"
                      class="task-duration"
                    >
                      耗时: {{ calculateDuration(task.createdAt, task.completedAt) }}
                    </div>
                  </div>
                  <div class="task-status">
                    <el-tag 
                      :type="getTaskStatusType(task.status)"
                      size="small"
                    >
                      {{ getTaskStatusText(task.status) }}
                    </el-tag>
                  </div>
                  <div
                    v-if="task.errorMessage"
                    class="task-error"
                  >
                    <el-text
                      type="danger"
                      size="small"
                    >
                      {{ task.errorMessage }}
                    </el-text>
                  </div>
                  <div
                    v-if="task.cancelReason"
                    class="task-cancel-reason"
                  >
                    <el-text
                      type="warning"
                      size="small"
                    >
                      取消原因: {{ task.cancelReason }}
                    </el-text>
                  </div>
                </div>
              </div>
            </el-collapse-item>
          </el-collapse>
        </div>

        <!-- 空状态 -->
        <el-empty 
          v-if="serverGroup.pendingTasks.length === 0 && serverGroup.historyTasks.length === 0 && serverGroup.currentTasks.length === 0"
          description="该服务器暂无任务"
        />
      </div>
    </div>

    <!-- 全局空状态 -->
    <el-empty 
      v-if="tasks.length === 0 && !loading"
      description="暂无任务记录"
    >
      <el-button
        type="primary"
        @click="$router.push('/user/apply')"
      >
        创建实例
      </el-button>
    </el-empty>

    <!-- 分页 -->
    <div
      v-if="total > 0"
      class="pagination"
    >
      <el-pagination
        v-model:current-page="pagination.page"
        v-model:page-size="pagination.pageSize"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next, jumper"
        @size-change="loadTasks"
        @current-change="loadTasks"
      />
    </div>

    <!-- 加载状态 -->
    <div
      v-if="loading"
      class="loading-container"
    >
      <el-skeleton
        :rows="5"
        animated
      />
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted, watch, onActivated } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { getUserTasks, cancelUserTask, getAvailableProviders } from '@/api/user'

const route = useRoute()

const loading = ref(false)
const tasks = ref([])
const providers = ref([])
const total = ref(0)
const expandedHistory = ref([])

const filterForm = reactive({
  status: '',
  taskType: '',
  providerId: '',
  search: ''
})

const pagination = reactive({
  page: 1,
  pageSize: 10
})

// 按服务器分组任务
const groupedTasks = computed(() => {
  const groups = new Map()
  
  tasks.value.forEach(task => {
    const providerId = task.providerId
    if (!groups.has(providerId)) {
      groups.set(providerId, {
        providerId,
        providerName: task.providerName,
        currentTasks: [], // 改为数组，支持多个正在执行的任务
        pendingTasks: [],
        historyTasks: []
      })
    }
    
    const group = groups.get(providerId)
    
    // 正在执行的任务（running 或 processing 状态）
    if (task.status === 'running' || task.status === 'processing') {
      group.currentTasks.push(task) // 添加到数组中而不是覆盖
    } else if (task.status === 'pending') {
      group.pendingTasks.push(task)
    } else {
      group.historyTasks.push(task)
    }
  })
  
  // 对等待队列按创建时间排序
  groups.forEach(group => {
    // 对正在执行的任务按创建时间排序（最早的在前）
    group.currentTasks.sort((a, b) => new Date(a.createdAt) - new Date(b.createdAt))
    group.pendingTasks.sort((a, b) => new Date(a.createdAt) - new Date(b.createdAt))
    group.historyTasks.sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt))
  })
  
  return Array.from(groups.values())
})

// 获取任务列表
const loadTasks = async (showSuccessMsg = false) => {
  try {
    loading.value = true
    const params = {
      page: pagination.page,
      pageSize: pagination.pageSize,
      ...filterForm
    }
    
    const response = await getUserTasks(params)
    if (response.code === 0 || response.code === 200) {
      tasks.value = response.data.list || []
      total.value = response.data.total || 0
      console.log('任务数据加载成功:', {
        count: tasks.value.length,
        tasks: tasks.value,
        groupedTasks: groupedTasks.value
      })
      // 只有在明确刷新时才显示成功提示
      if (showSuccessMsg) {
        ElMessage.success(`已刷新，共 ${total.value} 个任务`)
      }
    } else {
      tasks.value = []
      total.value = 0
      console.warn('获取任务列表失败:', response.message)
      if (response.message) {
        ElMessage.warning(response.message)
      }
    }
  } catch (error) {
    console.error('获取任务列表失败:', error)
    tasks.value = []
    total.value = 0
    ElMessage.error('获取任务列表失败，请检查网络连接')
  } finally {
    loading.value = false
  }
}

// 获取提供商列表
const loadProviders = async () => {
  try {
    const response = await getAvailableProviders()
    if (response.code === 0 || response.code === 200) {
      providers.value = response.data || []
    }
  } catch (error) {
    console.error('获取提供商列表失败:', error)
  }
}

// 重置筛选
const resetFilter = () => {
  Object.assign(filterForm, {
    providerId: '',
    taskType: '',
    status: ''
  })
  pagination.page = 1
  loadTasks(true)
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

    const response = await cancelUserTask(task.id)
    if (response.code === 0 || response.code === 200) {
      ElMessage.success('任务已取消')
      loadTasks()
    }
  } catch (error) {
    if (error !== 'cancel') {
      console.error('取消任务失败:', error)
      ElMessage.error('取消任务失败')
    }
  }
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

// 获取默认状态消息
const getDefaultStatusMessage = (status) => {
  const messageMap = {
    'pending': '等待调度中...',
    'processing': '正在准备中...',
    'running': '正在执行中...',
    'cancelling': '正在取消中...'
  }
  return messageMap[status] || '处理中...'
}

// 格式化日期
const formatDate = (dateString) => {
  return new Date(dateString).toLocaleString('zh-CN')
}

// 获取预计完成时间
const getEstimatedTime = (task) => {
  if (!task.estimatedDuration) return '未知'
  
  const startTime = new Date(task.startedAt || task.createdAt)
  const estimatedEnd = new Date(startTime.getTime() + task.estimatedDuration * 1000)
  
  return estimatedEnd.toLocaleTimeString('zh-CN')
}

// 计算任务持续时间
const calculateDuration = (startTime, endTime) => {
  const start = new Date(startTime)
  const end = new Date(endTime)
  const duration = Math.floor((end - start) / 1000)
  
  if (duration < 60) return `${duration}秒`
  if (duration < 3600) return `${Math.floor(duration / 60)}分钟`
  return `${Math.floor(duration / 3600)}小时${Math.floor((duration % 3600) / 60)}分钟`
}

// 设置定时刷新
let refreshTimer = null

const startAutoRefresh = () => {
  refreshTimer = setInterval(() => {
    loadTasks()
  }, 10000) // 每10秒刷新一次
}

const stopAutoRefresh = () => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
}

// 监听路由变化，确保页面切换时重新加载数据
watch(() => route.path, (newPath, oldPath) => {
  if (newPath === '/user/tasks' && oldPath !== newPath) {
    loadTasks()
    loadProviders()
    startAutoRefresh()
  } else if (oldPath === '/user/tasks' && newPath !== oldPath) {
    stopAutoRefresh()
  }
}, { immediate: false })

// 监听自定义导航事件
const handleRouterNavigation = (event) => {
  if (event.detail && event.detail.path === '/user/tasks') {
    loadTasks()
    loadProviders()
    startAutoRefresh()
  }
}

onMounted(async () => {
  // 添加自定义导航事件监听器
  window.addEventListener('router-navigation', handleRouterNavigation)
  // 添加强制页面刷新监听器
  window.addEventListener('force-page-refresh', handleForceRefresh)
  
  // 使用Promise.allSettled确保即使某些API失败，页面也能正常显示
  const results = await Promise.allSettled([
    loadTasks(),
    loadProviders()
  ])
  
  results.forEach((result, index) => {
    if (result.status === 'rejected') {
      const apiNames = ['获取任务列表', '获取提供商列表']
      console.error(`${apiNames[index]}失败:`, result.reason)
    }
  })
  
  startAutoRefresh()
})

// 使用 onActivated 确保每次页面激活时都重新加载数据
onActivated(async () => {
  await Promise.allSettled([
    loadTasks(),
    loadProviders()
  ])
  startAutoRefresh()
})

// 处理强制刷新事件
const handleForceRefresh = async (event) => {
  if (event.detail && event.detail.path === '/user/tasks') {
    await Promise.allSettled([
      loadTasks(),
      loadProviders()
    ])
  }
}

onUnmounted(() => {
  stopAutoRefresh()
  // 移除事件监听器
  window.removeEventListener('router-navigation', handleRouterNavigation)
  window.removeEventListener('force-page-refresh', handleForceRefresh)
})
</script>

<style scoped>
.user-tasks {
  padding: 24px;
}

.page-header {
  margin-bottom: 24px;
}

.page-header h1 {
  margin: 0 0 8px 0;
  font-size: 24px;
  font-weight: 600;
  color: #1f2937;
}

.page-header p {
  margin: 0;
  color: #6b7280;
}

.filter-section {
  background: white;
  padding: 16px;
  border-radius: 8px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
  margin-bottom: 24px;
}

.server-tasks {
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.server-group {
  background: white;
  border-radius: 12px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
  overflow: hidden;
}

.server-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 20px;
  background: #f8fafc;
  border-bottom: 1px solid #e2e8f0;
}

.server-header h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #1f2937;
}

.current-tasks {
  padding: 20px;
}

.current-tasks h3 {
  margin: 0 0 16px 0;
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
}

.current-task {
  margin-bottom: 16px;
}

.current-task:last-child {
  margin-bottom: 0;
}

.task-card.current {
  border-left: 4px solid #f59e0b;
  background: #fffbeb;
}

.task-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 16px;
}

.task-info h3 {
  margin: 0 0 4px 0;
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
}

.task-target {
  font-size: 14px;
  color: #6b7280;
}

.task-progress {
  margin-bottom: 16px;
}

.progress-text {
  margin-top: 8px;
  font-size: 14px;
  color: #6b7280;
}

.task-details {
  display: flex;
  gap: 24px;
}

.detail-item {
  display: flex;
  gap: 8px;
  font-size: 14px;
}

.detail-item .label {
  color: #6b7280;
}

.detail-item .value {
  color: #1f2937;
  font-weight: 500;
}

.pending-tasks,
.history-tasks {
  padding: 20px;
  border-top: 1px solid #e2e8f0;
}

.pending-tasks h3 {
  margin: 0 0 16px 0;
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
}

.tasks-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.task-item {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 12px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
}

.task-item.pending {
  background: #f0f9ff;
  border-color: #0ea5e9;
}

.task-item.completed {
  background: #f0fdf4;
  border-color: #10b981;
}

.task-item.failed {
  background: #fef2f2;
  border-color: #ef4444;
}

.task-order {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  background: #0ea5e9;
  color: white;
  border-radius: 50%;
  font-size: 12px;
  font-weight: 600;
}

.task-content {
  flex: 1;
}

.task-name {
  font-weight: 600;
  color: #1f2937;
  margin-bottom: 2px;
}

.task-target {
  font-size: 14px;
  color: #6b7280;
  margin-bottom: 2px;
}

.task-time,
.task-duration {
  font-size: 12px;
  color: #9ca3af;
}

.task-error {
  grid-column: 1 / -1;
  margin-top: 8px;
}

.pagination {
  display: flex;
  justify-content: center;
  margin-top: 24px;
}

.loading-container {
  padding: 24px;
}
</style>
