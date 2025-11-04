<template>
  <el-dialog
    v-model="dialogVisible"
    :title="$t('admin.providers.taskLog')"
    width="80%"
    style="max-width: 1000px;"
    :close-on-click-modal="false"
    @close="handleClose"
  >
    <div
      v-if="loading"
      style="text-align: center; padding: 40px;"
    >
      <el-icon
        class="is-loading"
        style="font-size: 32px;"
      >
        <Loading />
      </el-icon>
      <p style="margin-top: 16px;">
        {{ $t('admin.providers.loadingTaskLog') }}
      </p>
    </div>
    <div
      v-else-if="error"
      style="text-align: center; padding: 40px;"
    >
      <el-alert 
        type="error" 
        :title="error" 
        show-icon 
        :closable="false"
      />
    </div>
    <div v-else>
      <!-- 任务基本信息 -->
      <el-card
        v-if="task"
        style="margin-bottom: 20px;"
      >
        <template #header>
          <span>{{ $t('admin.providers.taskInfo') }}</span>
        </template>
        <el-descriptions
          :column="2"
          border
        >
          <el-descriptions-item :label="$t('admin.providers.taskID')">
            {{ task.id }}
          </el-descriptions-item>
          <el-descriptions-item label="Provider">
            {{ task.providerName }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.providers.taskType')">
            {{ task.taskType }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.providers.status')">
            <el-tag :type="getTaskStatusType(task.status)">
              {{ getTaskStatusText(task.status) }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.providers.executor')">
            {{ task.executorName }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.providers.duration')">
            {{ task.duration }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.providers.startTime')">
            {{ task.startedAt ? new Date(task.startedAt).toLocaleString() : '-' }}
          </el-descriptions-item>
          <el-descriptions-item :label="$t('admin.providers.completionTime')">
            {{ task.completedAt ? new Date(task.completedAt).toLocaleString() : '-' }}
          </el-descriptions-item>
        </el-descriptions>
        <div
          v-if="task.errorMessage"
          style="margin-top: 16px;"
        >
          <el-alert 
            type="error" 
            :title="task.errorMessage" 
            show-icon 
            :closable="false"
          />
        </div>
      </el-card>

      <!-- 日志内容 -->
      <el-card>
        <template #header>
          <div style="display: flex; justify-content: space-between; align-items: center;">
            <span>{{ $t('admin.providers.executionLog') }}</span>
            <el-button 
              v-if="task && task.logOutput" 
              size="small"
              @click="handleCopyLog"
            >
              {{ $t('admin.providers.copyLog') }}
            </el-button>
          </div>
        </template>
        <div 
          class="task-log-content"
          :style="{
            height: '400px',
            overflow: 'auto',
            backgroundColor: '#1e1e1e',
            color: '#ffffff',
            padding: '16px',
            fontFamily: 'Monaco, Consolas, monospace',
            fontSize: '13px',
            lineHeight: '1.5',
            borderRadius: '4px'
          }"
        >
          <pre v-if="task && task.logOutput">{{ task.logOutput }}</pre>
          <div
            v-else
            style="color: #999; text-align: center; padding: 40px;"
          >
            暂无日志内容
          </div>
        </div>
      </el-card>
    </div>

    <template #footer>
      <div style="text-align: center;">
        <el-button @click="handleClose">
          关闭
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed } from 'vue'
import { Loading } from '@element-plus/icons-vue'
import { copyToClipboard } from '@/utils/clipboard'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = defineProps({
  visible: {
    type: Boolean,
    required: true
  },
  loading: {
    type: Boolean,
    default: false
  },
  error: {
    type: String,
    default: null
  },
  task: {
    type: Object,
    default: null
  }
})

const emit = defineEmits(['update:visible', 'close'])

const dialogVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const handleClose = () => {
  emit('close')
}

const handleCopyLog = () => {
  if (props.task && props.task.logOutput) {
    copyToClipboard(props.task.logOutput)
    ElMessage.success(t('admin.providers.logCopied'))
  }
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
.task-log-content::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

.task-log-content::-webkit-scrollbar-track {
  background: #2d2d2d;
  border-radius: 4px;
}

.task-log-content::-webkit-scrollbar-thumb {
  background: #555;
  border-radius: 4px;
}

.task-log-content::-webkit-scrollbar-thumb:hover {
  background: #777;
}

.task-log-content pre {
  margin: 0;
  white-space: pre-wrap;
  word-wrap: break-word;
}
</style>
