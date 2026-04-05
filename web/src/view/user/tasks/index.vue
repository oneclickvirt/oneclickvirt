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
import { onMounted, onUnmounted, onActivated } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { useUserTaskManagement } from './composables/useUserTaskManagement'

const {
  loading, tasks, providers, total, expandedHistory,
  filterForm, pagination, groupedTasks,
  loadTasks, loadProviders, resetFilter, cancelTask,
  getTaskTypeText, formatDurationSeconds, getTaskStatusType,
  shouldShowInstanceConfig, getTaskStatusText, getDefaultStatusMessage,
  formatDate, getEstimatedTime, calculateDuration,
  startAutoRefresh, stopAutoRefresh,
  handleRouterNavigation, handleForceRefresh,
  t
} = useUserTaskManagement()

onMounted(async () => {
  window.addEventListener('router-navigation', handleRouterNavigation)
  window.addEventListener('force-page-refresh', handleForceRefresh)
  await Promise.allSettled([loadTasks(), loadProviders()])
  startAutoRefresh()
})

onActivated(async () => {
  await Promise.allSettled([loadTasks(), loadProviders()])
  startAutoRefresh()
})

onUnmounted(() => {
  stopAutoRefresh()
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
  color: var(--text-color-primary);
}

.page-header p {
  margin: 0;
  color: var(--text-color-secondary);
}

.filter-section {
  background: var(--card-bg-solid);
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
  background: var(--card-bg-solid);
  border-radius: 12px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
  overflow: hidden;
}

.server-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 20px;
  background: var(--neutral-bg);
  border-bottom: 1px solid var(--border-color);
}

.server-header h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.current-tasks {
  padding: 20px;
}

.current-tasks h3 {
  margin: 0 0 16px 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.current-task {
  margin-bottom: 16px;
}

.current-task:last-child {
  margin-bottom: 0;
}

.task-card.current {
  border-left: 4px solid #f59e0b;
  background: var(--warning-bg);
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
  color: var(--text-color-primary);
}

.task-target {
  font-size: 14px;
  color: var(--text-color-secondary);
}

.task-progress {
  margin-bottom: 16px;
}

.progress-text {
  margin-top: 8px;
  font-size: 14px;
  color: var(--text-color-secondary);
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
  color: var(--text-color-secondary);
}

.detail-item .value {
  color: var(--text-color-primary);
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
  color: var(--text-color-primary);
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
  background: var(--info-bg);
  border-color: #0ea5e9;
}

.task-item.completed {
  background: var(--success-bg);
  border-color: #10b981;
}

.task-item.failed {
  background: var(--error-bg);
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
  color: var(--text-color-primary);
  margin-bottom: 2px;
}

.task-target {
  font-size: 14px;
  color: var(--text-color-secondary);
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
