<template>
  <div class="user-tasks">
    <div class="page-header">
      <h1>{{ t('user.tasks.title') }}</h1>
      <p>{{ t('user.tasks.subtitle') }}</p>
    </div>

    <!-- 筛选器 -->
    <div class="filter-section">
      <el-form
        :inline="true"
        :model="filterForm"
      >
        <el-form-item>
          <el-select
            v-model="filterForm.providerId"
            :placeholder="t('user.tasks.selectNode')"
            clearable
            style="width: 150px;"
          >
            <el-option
              :label="t('user.tasks.all')"
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
        <el-form-item>
          <el-select
            v-model="filterForm.taskType"
            :placeholder="t('user.tasks.selectTaskType')"
            clearable
            style="width: 150px;"
          >
            <el-option
              :label="t('user.tasks.all')"
              value=""
            />
            <el-option
              :label="t('user.tasks.taskTypeCreate')"
              value="create"
            />
            <el-option
              :label="t('user.tasks.taskTypeStart')"
              value="start"
            />
            <el-option
              :label="t('user.tasks.taskTypeStop')"
              value="stop"
            />
            <el-option
              :label="t('user.tasks.taskTypeRestart')"
              value="restart"
            />
            <el-option
              :label="t('user.tasks.taskTypeReset')"
              value="reset"
            />
            <el-option
              :label="t('user.tasks.taskTypeDelete')"
              value="delete"
            />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-select
            v-model="filterForm.status"
            :placeholder="t('user.tasks.selectStatus')"
            clearable
            style="width: 150px;"
          >
            <el-option
              :label="t('user.tasks.all')"
              value=""
            />
            <el-option
              :label="t('user.tasks.statusPending')"
              value="pending"
            />
            <el-option
              :label="t('user.tasks.statusProcessing')"
              value="processing"
            />
            <el-option
              :label="t('user.tasks.statusRunning')"
              value="running"
            />
            <el-option
              :label="t('user.tasks.statusCompleted')"
              value="completed"
            />
            <el-option
              :label="t('user.tasks.statusFailed')"
              value="failed"
            />
            <el-option
              :label="t('user.tasks.statusCancelled')"
              value="cancelled"
            />
            <el-option
              :label="t('user.tasks.statusCancelling')"
              value="cancelling"
            />
            <el-option
              :label="t('user.tasks.statusTimeout')"
              value="timeout"
            />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            @click="() => loadTasks(true)"
          >
            {{ t('user.tasks.filter') }}
          </el-button>
          <el-button @click="resetFilter">
            {{ t('user.tasks.reset') }}
          </el-button>
          <el-button @click="() => loadTasks(true)">
            <el-icon><Refresh /></el-icon>
            {{ t('user.tasks.refresh') }}
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
              {{ t('user.tasks.executing') }}: {{ serverGroup.currentTasks.length }}{{ t('user.tasks.tasksCount') }}
            </el-tag>
            <el-tag 
              v-else
              type="success"
            >
              {{ t('user.tasks.idle') }}
            </el-tag>
          </div>
        </div>

        <!-- 当前执行中的任务 -->
        <div
          v-if="serverGroup.currentTasks.length > 0"
          class="current-tasks"
        >
          <h3>{{ t('user.tasks.runningTasksTitle') }} ({{ serverGroup.currentTasks.length }})</h3>
          <div 
            v-for="currentTask in serverGroup.currentTasks" 
            :key="currentTask.id"
            class="current-task"
          >
            <el-card class="task-card current">
              <div class="task-header">
                <div class="task-info">
                  <h3>{{ getTaskTypeText(currentTask.taskType) }}</h3>
                  <span class="task-target">{{ currentTask.instanceName || t('user.tasks.newInstance') }}</span>
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
                  <span class="label">{{ t('user.tasks.createdTime') }}:</span>
                  <span class="value">{{ formatDate(currentTask.createdAt) }}</span>
                </div>
                <div class="detail-item">
                  <span class="label">{{ t('user.tasks.estimatedCompletion') }}:</span>
                  <span class="value">{{ getEstimatedTime(currentTask) }}</span>
                </div>
                <!-- running 状态不显示排队位置 -->
                <div
                  v-if="shouldShowInstanceConfig(currentTask)"
                  class="detail-item"
                >
                  <span class="label">{{ t('user.tasks.instanceConfig') }}:</span>
                  <span class="value">
                    <template v-if="currentTask.preallocatedCpu > 0">
                      {{ currentTask.preallocatedCpu }}{{ t('common.core') }} / 
                      {{ (currentTask.preallocatedMemory / 1024).toFixed(1) }}GB / 
                      {{ (currentTask.preallocatedDisk / 1024).toFixed(1) }}GB / 
                      {{ currentTask.preallocatedBandwidth }}Mbps
                    </template>
                    <template v-else>
                      <el-text type="info" size="small">{{ t('user.tasks.configLoading') }}</el-text>
                    </template>
                  </span>
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
          <h3>{{ t('user.tasks.pendingQueueTitle') }} ({{ serverGroup.pendingTasks.length }})</h3>
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
                  {{ task.instanceName || t('user.tasks.newInstance') }}
                </div>
                <div class="task-time">
                  {{ formatDate(task.createdAt) }}
                </div>
                <div
                  v-if="task.queuePosition > 0"
                  class="task-queue-info"
                >
                  <el-text type="info" size="small">
                    {{ t('user.tasks.beforeYouInQueue', { count: task.queuePosition }) }}
                  </el-text>
                </div>
                <div
                  v-if="task.queuePosition === 0"
                  class="task-queue-info"
                >
                  <el-text type="success" size="small">
                    {{ t('user.tasks.nextToExecute') }}
                  </el-text>
                </div>
                <div
                  v-if="task.estimatedWaitTime > 0"
                  class="task-wait-time"
                >
                  {{ t('user.tasks.estimatedWait') }}: {{ formatDurationSeconds(task.estimatedWaitTime) }}
                </div>
                <div
                  v-if="task.taskType === 'create' && task.preallocatedCpu > 0"
                  class="task-config"
                >
                  <el-tag size="small" type="info">
                    {{ task.preallocatedCpu }}{{ t('common.core') }} / 
                    {{ (task.preallocatedMemory / 1024).toFixed(1) }}GB / 
                    {{ (task.preallocatedDisk / 1024).toFixed(1) }}GB / 
                    {{ task.preallocatedBandwidth }}Mbps
                  </el-tag>
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
                  {{ t('user.tasks.cancel') }}
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
              :title="`${t('user.tasks.historyTasksTitle')} (${serverGroup.historyTasks.length})`"
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
                      {{ task.instanceName || t('user.tasks.newInstance') }}
                    </div>
                    <div class="task-time">
                      {{ formatDate(task.createdAt) }}
                    </div>
                    <div
                      v-if="task.completedAt"
                      class="task-duration"
                    >
                      {{ t('user.tasks.duration') }}: {{ calculateDuration(task.createdAt, task.completedAt) }}
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
                      {{ t('user.tasks.cancelReason') }}: {{ task.cancelReason }}
                    </el-text>
                  </div>
                </div>
              </div>
            </el-collapse-item>
          </el-collapse>
        </div>

        <!-- 该节点无任务的空状态：只有在该节点确实没有任何任务时才显示 -->
        <div
          v-if="serverGroup.pendingTasks.length === 0 && serverGroup.historyTasks.length === 0 && serverGroup.currentTasks.length === 0"
          class="empty-provider"
        >
          <el-empty 
            :description="t('user.tasks.noTasksForProvider')"
            :image-size="80"
          />
        </div>
      </div>
    </div>

    <!-- 全局空状态：没有任何任务时显示 -->
    <el-empty 
      v-if="!loading && groupedTasks.length === 0"
      :description="t('user.tasks.noTasksDescription')"
    >
      <el-button
        type="primary"
        @click="$router.push('/user/apply')"
      >
        {{ t('user.tasks.createInstance') }}
      </el-button>
    </el-empty>

    <!-- 分页 - 只在有筛选条件且有数据时显示 -->
    <div
      v-if="(filterForm.providerId || filterForm.taskType || filterForm.status) && total > pagination.pageSize"
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
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { getUserTasks, cancelUserTask, getAvailableProviders } from '@/api/user'

const { t, locale } = useI18n()
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
    
    // 如果有节点筛选条件（不为空字符串），只显示选中的节点
    if (filterForm.providerId !== '' && String(task.providerId) !== String(filterForm.providerId)) {
      return
    }
    
    if (!groups.has(providerId)) {
      groups.set(providerId, {
        providerId,
        providerName: task.providerName,
        currentTasks: [], // 正在执行的任务（running/processing）
        pendingTasks: [], // 等待中的任务
        historyTasks: [] // 历史任务（completed/failed/cancelled/timeout）
      })
    }
    
    const group = groups.get(providerId)
    
    // 正在执行的任务（running 或 processing 状态）
    if (task.status === 'running' || task.status === 'processing') {
      group.currentTasks.push(task)
    } else if (task.status === 'pending') {
      group.pendingTasks.push(task)
    } else {
      // 其他状态都是历史任务
      group.historyTasks.push(task)
    }
  })
  
  // 对每个分组内的任务进行排序
  groups.forEach(group => {
    // 正在执行的任务按创建时间排序（最早的在前）
    group.currentTasks.sort((a, b) => new Date(a.createdAt) - new Date(b.createdAt))
    // 等待队列按创建时间排序（最早的在前）
    group.pendingTasks.sort((a, b) => new Date(a.createdAt) - new Date(b.createdAt))
    // 历史任务按创建时间倒序（最新的在前）
    group.historyTasks.sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt))
  })
  
  // 将分组转换为数组并排序：优先显示有活跃任务（pending/running）的节点
  const groupArray = Array.from(groups.values())
  groupArray.sort((a, b) => {
    const aHasActive = a.currentTasks.length + a.pendingTasks.length
    const bHasActive = b.currentTasks.length + b.pendingTasks.length
    
    // 有活跃任务的节点优先
    if (aHasActive > 0 && bHasActive === 0) return -1
    if (aHasActive === 0 && bHasActive > 0) return 1
    
    // 都有活跃任务或都没有，按节点名称排序
    return a.providerName.localeCompare(b.providerName)
  })
  
  return groupArray
})

// 获取任务列表
const loadTasks = async (showSuccessMsg = false) => {
  try {
    loading.value = true
    const params = {
      ...filterForm
    }
    
    // 判断是否有筛选条件
    const hasFilter = filterForm.providerId || filterForm.taskType || filterForm.status
    
    // 只在有筛选条件时才使用分页
    if (hasFilter) {
      params.page = pagination.page
      params.pageSize = pagination.pageSize
    }
    
    const response = await getUserTasks(params)
    if (response.code === 0 || response.code === 200) {
      tasks.value = response.data.list || []
      total.value = response.data.total || 0
      console.log('任务数据加载成功:', {
        count: tasks.value.length,
        total: total.value,
        hasFilter,
        tasks: tasks.value,
        groupedTasks: groupedTasks.value
      })
      // 只有在明确刷新时才显示成功提示
      if (showSuccessMsg) {
        if (hasFilter) {
          ElMessage.success(t('user.tasks.refreshedTotal', { count: total.value }))
        } else {
          ElMessage.success(t('user.tasks.loadSuccess'))
        }
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
    ElMessage.error(t('user.tasks.loadFailedNetwork'))
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
      `${t('user.tasks.confirmCancel')} "${getTaskTypeText(task.taskType)}"?`,
      t('user.tasks.confirmCancel'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )

    const response = await cancelUserTask(task.id)
    if (response.code === 0 || response.code === 200) {
      ElMessage.success(t('user.tasks.taskCancelled'))
      loadTasks()
    }
  } catch (error) {
    if (error !== 'cancel') {
      console.error('取消任务失败:', error)
      ElMessage.error(t('user.tasks.cancelTaskFailed'))
    }
  }
}

// 获取任务类型文本
const getTaskTypeText = (type) => {
  const typeMap = {
    'create': t('user.tasks.taskTypeCreate'),
    'start': t('user.tasks.taskTypeStart'),
    'stop': t('user.tasks.taskTypeStop'),
    'restart': t('user.tasks.taskTypeRestart'),
    'reset': t('user.tasks.taskTypeReset'),
    'delete': t('user.tasks.taskTypeDelete')
  }
  return typeMap[type] || type
}

// 格式化秒数为可读时间
const formatDurationSeconds = (seconds) => {
  if (!seconds || seconds <= 0) {
    return t('user.tasks.calculating')
  }
  
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = Math.floor(seconds % 60)
  
  const parts = []
  if (hours > 0) {
    parts.push(`${hours}${t('user.tasks.hours')}`)
  }
  if (minutes > 0) {
    parts.push(`${minutes}${t('user.tasks.minutes')}`)
  }
  if (secs > 0 || parts.length === 0) {
    parts.push(`${secs}${t('user.tasks.seconds')}`)
  }
  
  return parts.join(' ')
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

// 判断是否应该显示实例配置
const shouldShowInstanceConfig = (task) => {
  // create 类型的任务总是显示配置区域
  return task.taskType === 'create'
}

// 获取任务状态文本
const getTaskStatusText = (status) => {
  const statusMap = {
    'pending': t('user.tasks.statusPending'),
    'processing': t('user.tasks.statusProcessing'),
    'running': t('user.tasks.statusRunning'),
    'completed': t('user.tasks.statusCompleted'),
    'failed': t('user.tasks.statusFailed'),
    'cancelled': t('user.tasks.statusCancelled'),
    'cancelling': t('user.tasks.statusCancelling'),
    'timeout': t('user.tasks.statusTimeout')
  }
  return statusMap[status] || status
}

// 获取默认状态消息
const getDefaultStatusMessage = (status) => {
  const messageMap = {
    'pending': t('user.tasks.statusMessagePending'),
    'processing': t('user.tasks.statusMessageProcessing'),
    'running': t('user.tasks.statusMessageRunning'),
    'cancelling': t('user.tasks.statusMessageCancelling')
  }
  return messageMap[status] || t('user.tasks.statusMessageDefault')
}

// 格式化日期
const formatDate = (dateString) => {
  const localeCode = locale.value === 'zh-CN' ? 'zh-CN' : 'en-US'
  return new Date(dateString).toLocaleString(localeCode)
}

// 获取预计完成时间
const getEstimatedTime = (task) => {
  if (!task.estimatedDuration) return t('user.tasks.unknown')
  
  const startTime = new Date(task.startedAt || task.createdAt)
  const estimatedEnd = new Date(startTime.getTime() + task.estimatedDuration * 1000)
  
  const localeCode = locale.value === 'zh-CN' ? 'zh-CN' : 'en-US'
  return estimatedEnd.toLocaleTimeString(localeCode)
}

// 计算任务持续时间
const calculateDuration = (startTime, endTime) => {
  const start = new Date(startTime)
  const end = new Date(endTime)
  const duration = Math.floor((end - start) / 1000)
  
  if (duration < 60) return `${duration}${t('user.tasks.seconds')}`
  if (duration < 3600) return `${Math.floor(duration / 60)}${t('user.tasks.minutes')}`
  return `${Math.floor(duration / 3600)}${t('user.tasks.hours')}${Math.floor((duration % 3600) / 60)}${t('user.tasks.minutes')}`
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

// 监听筛选条件变化，重置分页
watch([() => filterForm.providerId, () => filterForm.taskType, () => filterForm.status], () => {
  pagination.page = 1
}, { deep: true })

// 监听自定义导航事件
const handleRouterNavigation = (event) => {
  if (event.detail && event.detail.path === '/user/tasks') {
    loadTasks()
    loadProviders()
    startAutoRefresh()
  }
}

onMounted(async () => {
  // 自定义导航事件监听器
  window.addEventListener('router-navigation', handleRouterNavigation)
  // 强制页面刷新监听器
  window.addEventListener('force-page-refresh', handleForceRefresh)
  
  // 使用Promise.allSettled确保即使某些API失败，页面也能正常显示
  const results = await Promise.allSettled([
    loadTasks(),
    loadProviders()
  ])
  
  results.forEach((result, index) => {
    if (result.status === 'rejected') {
      const apiNames = [t('user.tasks.apiLoadTasks'), t('user.tasks.apiLoadProviders')]
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
.task-duration,
.task-wait-time,
.task-queue-info {
  font-size: 12px;
  color: #9ca3af;
  margin-top: 4px;
}

.task-queue-info {
  margin-top: 6px;
}

.task-config {
  margin-top: 6px;
}

.task-error,
.task-cancel-reason {
  grid-column: 1 / -1;
  margin-top: 8px;
}

.empty-provider {
  padding: 20px;
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
