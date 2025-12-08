<template>
  <el-dialog 
    v-model="dialogVisible" 
    :title="$t('admin.providers.autoConfigAPI')" 
    width="900px"
    :close-on-click-modal="false"
    :close-on-press-escape="false"
    @close="handleClose"
  >
    <div v-if="provider">
      <!-- 历史记录视图 -->
      <div v-if="showHistory">
        <el-alert
          :title="$t('admin.providers.configHistory', { type: provider.type.toUpperCase() })"
          type="info"
          :closable="false"
          show-icon
          style="margin-bottom: 20px;"
        >
          <template #default>
            <p v-if="historyTasks.length > 0 || runningTask">{{ $t('admin.providers.configHistoryMessage') }}</p>
            <p v-else>{{ $t('admin.providers.noConfigHistory') }}</p>
          </template>
        </el-alert>

        <!-- 正在运行的任务 -->
        <div
          v-if="runningTask"
          style="margin-bottom: 20px;"
        >
          <el-alert
            :title="$t('admin.providers.runningConfigTask')"
            type="warning"
            :closable="false"
            show-icon
          >
            <template #default>
              <p>{{ $t('admin.providers.taskID') }}: {{ runningTask.id }}</p>
              <p>{{ $t('admin.providers.startTime') }}: {{ new Date(runningTask.startedAt).toLocaleString() }}</p>
              <p>{{ $t('admin.providers.executor') }}: {{ runningTask.executorName }}</p>
            </template>
          </el-alert>
        </div>

        <!-- 历史任务列表 -->
        <div v-if="historyTasks.length > 0">
          <h4>{{ $t('admin.providers.configHistoryRecords') }}</h4>
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
              :label="$t('admin.providers.status')"
              width="80"
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
              :label="$t('admin.providers.executionTime')"
              width="140"
            >
              <template #default="{ row }">
                {{ new Date(row.createdAt).toLocaleString() }}
              </template>
            </el-table-column>
            <el-table-column
              prop="executorName"
              :label="$t('admin.providers.executor')"
              width="80"
            />
            <el-table-column
              prop="duration"
              :label="$t('admin.providers.duration')"
              width="70"
            />
            <el-table-column
              :label="$t('admin.providers.result')"
              show-overflow-tooltip
            >
              <template #default="{ row }">
                <span
                  v-if="row.success"
                  style="color: #67C23A;"
                >✅ {{ $t('common.success') }}</span>
                <span
                  v-else-if="row.status === 'failed'"
                  style="color: #F56C6C;"
                >❌ {{ row.errorMessage || $t('common.failed') }}</span>
                <span v-else>{{ row.logSummary || '-' }}</span>
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
            type="warning"
            @click="handleRerunConfiguration"
          >
            {{ historyTasks.length > 0 ? $t('admin.providers.rerunConfig') : $t('admin.providers.startConfig') }}
          </el-button>
          <el-button @click="handleClose">
            {{ $t('common.close') }}
          </el-button>
        </div>
      </div>
    </div>
  </el-dialog>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = defineProps({
  visible: {
    type: Boolean,
    required: true
  },
  provider: {
    type: Object,
    default: null
  },
  showHistory: {
    type: Boolean,
    default: false
  },
  runningTask: {
    type: Object,
    default: null
  },
  historyTasks: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['update:visible', 'close', 'viewTaskLog', 'viewRunningTask', 'rerunConfiguration'])

const dialogVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const handleClose = () => {
  emit('close')
}

const handleViewTaskLog = (taskId) => {
  emit('viewTaskLog', taskId)
}

const handleViewRunningTask = () => {
  emit('viewRunningTask')
}

const handleRerunConfiguration = () => {
  emit('rerunConfiguration')
}

const getTaskStatusType = (status) => {
  const statusMap = {
    'pending': 'info',
    'running': 'primary',
    'completed': 'success',
    'failed': 'danger',
    'cancelled': 'warning'
  }
  return statusMap[status] || 'info'
}

const getTaskStatusText = (status) => {
  const statusTextMap = {
    'pending': '等待中',
    'running': '运行中',
    'completed': '已完成',
    'failed': '失败',
    'cancelled': '已取消'
  }
  return statusTextMap[status] || status
}
</script>

<style scoped>
h4 {
  margin: 16px 0 12px 0;
  color: #303133;
  font-size: 16px;
  font-weight: 600;
}
</style>
