<template>
  <el-dialog
    :model-value="visible"
    :title="dialogTitle"
    width="900px"
    :close-on-click-modal="false"
    :close-on-press-escape="false"
    @close="handleClose"
  >
    <div v-if="provider">
      <!-- 历史记录视图 -->
      <div v-if="showHistory">
        <el-alert
          :title="$t('admin.providers.trafficMonitorHistory')"
          type="info"
          :closable="false"
          show-icon
          style="margin-bottom: 20px;"
        >
          <template #default>
            <p>{{ $t('admin.providers.trafficMonitorHistoryMessage') }}</p>
          </template>
        </el-alert>

        <!-- 正在运行的任务 -->
        <div
          v-if="runningTask"
          style="margin-bottom: 20px;"
        >
          <el-alert
            :title="$t('admin.providers.runningTrafficMonitorTask')"
            type="warning"
            :closable="false"
            show-icon
          >
            <template #default>
              <p>{{ $t('admin.providers.taskID') }}: {{ runningTask.id }}</p>
              <p>{{ $t('admin.providers.taskType') }}: {{ getTaskTypeLabel(runningTask.taskType) }}</p>
              <p>{{ $t('admin.providers.startTime') }}: {{ formatDateTime(runningTask.startedAt) }}</p>
              <p>{{ $t('admin.providers.progress') }}: {{ runningTask.progress }}%</p>
            </template>
          </el-alert>
        </div>

        <!-- 历史任务列表 -->
        <div v-if="historyTasks.length > 0">
          <h4>{{ $t('admin.providers.trafficMonitorHistoryRecords') }}</h4>
          <el-table
            :data="historyTasks"
            size="small"
            style="margin-bottom: 20px;"
          >
            <el-table-column
              prop="id"
              :label="$t('admin.providers.taskID')"
              width="70"
            />
            <el-table-column
              :label="$t('admin.providers.taskType')"
              width="120"
            >
              <template #default="{ row }">
                <el-tag 
                  :type="getTaskTypeTagType(row.taskType)"
                  size="small"
                >
                  {{ getTaskTypeLabel(row.taskType) }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('admin.providers.status')"
              width="80"
            >
              <template #default="{ row }">
                <el-tag 
                  :type="getTaskStatusTagType(row.status)"
                  size="small"
                >
                  {{ getTaskStatusLabel(row.status) }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('admin.providers.executionTime')"
              width="140"
            >
              <template #default="{ row }">
                {{ formatDateTime(row.createdAt) }}
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('admin.providers.progress')"
              width="100"
            >
              <template #default="{ row }">
                <el-progress
                  :percentage="row.progress"
                  :status="row.status === 'failed' ? 'exception' : row.status === 'completed' ? 'success' : undefined"
                />
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('admin.providers.result')"
              show-overflow-tooltip
            >
              <template #default="{ row }">
                <span v-if="row.status === 'completed'" style="color: #67C23A;">
                  ✅ {{ $t('common.success') }}: {{ row.successCount }}/{{ row.totalCount }}
                </span>
                <span v-else-if="row.status === 'failed'" style="color: #F56C6C;">
                  ❌ {{ row.errorMsg || $t('common.failed') }}
                </span>
                <span v-else>{{ row.message || '-' }}</span>
              </template>
            </el-table-column>
            <el-table-column
              :label="$t('common.actions')"
              width="100"
            >
              <template #default="{ row }">
                <el-button 
                  type="primary" 
                  size="small"
                  @click="handleViewTaskLog(row.id)"
                >
                  {{ $t('admin.providers.viewLog') }}
                </el-button>
              </template>
            </el-table-column>
          </el-table>
          
          <!-- 分页组件 -->
          <el-pagination
            v-model:current-page="pagination.page"
            v-model:page-size="pagination.pageSize"
            :page-sizes="[5, 10, 20, 50]"
            :small="false"
            :background="true"
            layout="total, sizes, prev, pager, next, jumper"
            :total="pagination.total"
            @size-change="handlePageSizeChange"
            @current-change="handlePageChange"
            style="justify-content: center;"
          />
        </div>

        <!-- 操作按钮 -->
        <div style="text-align: center; margin-top: 20px;">
          <el-button 
            v-if="runningTask"
            type="primary"
            @click="handleViewRunningTask"
          >
            {{ $t('admin.providers.viewRunningTaskLog') }}
          </el-button>
          <el-button 
            type="success"
            @click="handleExecuteOperation('enable')"
          >
            {{ $t('admin.providers.enableTrafficMonitor') }}
          </el-button>
          <el-button 
            type="warning"
            @click="handleExecuteOperation('disable')"
          >
            {{ $t('admin.providers.disableTrafficMonitor') }}
          </el-button>
          <el-button 
            type="info"
            @click="handleExecuteOperation('detect')"
          >
            {{ $t('admin.providers.detectTrafficMonitor') }}
          </el-button>
          <el-button @click="handleClose">
            {{ $t('common.close') }}
          </el-button>
        </div>
      </div>

      <!-- 任务执行视图 -->
      <div v-else-if="task" class="task-container">
        <!-- 任务基本信息 -->
        <el-descriptions :column="2" border>
          <el-descriptions-item :label="$t('admin.providers.trafficMonitorTaskType')">
            <el-tag :type="getTaskTypeTagType(task.taskType)">
              {{ getTaskTypeLabel(task.taskType) }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.providers.trafficMonitorTaskStatus')">
            <el-tag :type="getTaskStatusTagType(task.status)">
              {{ getTaskStatusLabel(task.status) }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.providers.trafficMonitorTaskProgress')" :span="2">
            <div class="progress-container">
              <el-progress 
                :percentage="task.progress" 
                :status="task.status === 'failed' ? 'exception' : task.status === 'completed' ? 'success' : undefined"
              />
              <div class="progress-details">
                <span>{{ $t('common.total') }}: {{ task.totalCount }}</span>
                <span class="success-count">{{ $t('common.success') }}: {{ task.successCount }}</span>
                <span class="failed-count">{{ $t('common.failed') }}: {{ task.failedCount }}</span>
              </div>
            </div>
          </el-descriptions-item>
        </el-descriptions>

        <!-- 任务输出日志 -->
        <div class="output-section">
          <div class="section-header">
            <h4>{{ $t('admin.providers.trafficMonitorTaskOutput') }}</h4>
            <el-button
              v-if="task.status === 'running'"
              type="primary"
              size="small"
              :icon="Refresh"
              :loading="loading"
              @click="$emit('refresh')"
            >
              {{ $t('common.refresh') }}
            </el-button>
          </div>
          <div class="output-content">
            <pre v-if="task.output">{{ task.output }}</pre>
            <el-empty
              v-else
              :description="task.status === 'pending' ? $t('admin.providers.taskExecuting') : $t('common.noData')"
              :image-size="80"
            />
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <el-button @click="handleClose">
        {{ $t('common.close') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  provider: {
    type: Object,
    default: null
  },
  showHistory: {
    type: Boolean,
    default: false
  },
  task: {
    type: Object,
    default: null
  },
  runningTask: {
    type: Object,
    default: null
  },
  historyTasks: {
    type: Array,
    default: () => []
  },
  loading: {
    type: Boolean,
    default: false
  },
  pagination: {
    type: Object,
    default: () => ({
      page: 1,
      pageSize: 10,
      total: 0
    })
  }
})

const emit = defineEmits(['update:visible', 'close', 'refresh', 'viewTaskLog', 'viewRunningTask', 'executeOperation', 'pageChange', 'pageSizeChange'])

const dialogTitle = computed(() => {
  if (props.showHistory) {
    return t('admin.providers.trafficMonitorManagement')
  }
  return t('admin.providers.trafficMonitorTaskTitle')
})

const handleClose = () => {
  emit('update:visible', false)
  emit('close')
}

const handleViewTaskLog = (taskId) => {
  emit('viewTaskLog', taskId)
}

const handleViewRunningTask = () => {
  emit('viewRunningTask')
}

const handleExecuteOperation = (operation) => {
  emit('executeOperation', operation)
}

const handlePageChange = (page) => {
  emit('pageChange', page)
}

const handlePageSizeChange = (pageSize) => {
  emit('pageSizeChange', pageSize)
}

const formatDateTime = (dateTime) => {
  if (!dateTime) return '-'
  return new Date(dateTime).toLocaleString()
}

const getTaskTypeLabel = (taskType) => {
  const labels = {
    'enable_all': t('admin.providers.trafficMonitorTaskTypeEnableAll'),
    'disable_all': t('admin.providers.trafficMonitorTaskTypeDisableAll'),
    'detect_all': t('admin.providers.trafficMonitorTaskTypeDetectAll')
  }
  return labels[taskType] || taskType
}

const getTaskTypeTagType = (taskType) => {
  const types = {
    'enable_all': 'success',
    'disable_all': 'danger',
    'detect_all': 'info'
  }
  return types[taskType] || 'info'
}

const getTaskStatusLabel = (status) => {
  const labels = {
    'pending': t('admin.providers.trafficMonitorTaskStatusPending'),
    'running': t('admin.providers.trafficMonitorTaskStatusRunning'),
    'completed': t('admin.providers.trafficMonitorTaskStatusCompleted'),
    'failed': t('admin.providers.trafficMonitorTaskStatusFailed')
  }
  return labels[status] || status
}

const getTaskStatusTagType = (status) => {
  const types = {
    'pending': 'info',
    'running': 'warning',
    'completed': 'success',
    'failed': 'danger'
  }
  return types[status] || 'info'
}
</script>

<style scoped>
.task-container {
  max-height: 600px;
  overflow-y: auto;
}

.progress-container {
  width: 100%;
}

.progress-details {
  display: flex;
  gap: 20px;
  margin-top: 8px;
  font-size: 13px;
  color: #606266;
}

.success-count {
  color: #67c23a;
}

.failed-count {
  color: #f56c6c;
}

.output-section {
  margin-top: 20px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.section-header h4 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}

.output-content {
  background: #f5f7fa;
  border: 1px solid #e4e7ed;
  border-radius: 4px;
  padding: 12px;
  max-height: 400px;
  overflow-y: auto;
}

.output-content pre {
  margin: 0;
  font-family: 'Courier New', Courier, monospace;
  font-size: 12px;
  line-height: 1.6;
  color: #303133;
  white-space: pre-wrap;
  word-wrap: break-word;
}

/* 自定义滚动条 */
.task-container::-webkit-scrollbar,
.output-content::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

.task-container::-webkit-scrollbar-track,
.output-content::-webkit-scrollbar-track {
  background: #f1f1f1;
  border-radius: 4px;
}

.task-container::-webkit-scrollbar-thumb,
.output-content::-webkit-scrollbar-thumb {
  background: #c0c4cc;
  border-radius: 4px;
}

.task-container::-webkit-scrollbar-thumb:hover,
.output-content::-webkit-scrollbar-thumb:hover {
  background: #909399;
}
</style>
