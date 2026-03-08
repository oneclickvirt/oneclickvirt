<template>
  <div class="log-viewer">
    <!-- 页头 -->
    <el-card class="header-card" shadow="never">
      <div class="header-content">
        <div class="title-section">
          <h2>
            <el-icon><Document /></el-icon>
            {{ $t('admin.logs.title') }}
          </h2>
          <p class="subtitle">{{ $t('admin.logs.subtitle') }}</p>
        </div>
      </div>
    </el-card>

    <!-- 工具栏 -->
    <el-card class="toolbar-card" shadow="never">
      <el-row :gutter="16" align="middle">
        <!-- 日期选择 -->
        <el-col :xs="24" :sm="8" :md="6">
          <el-select
            v-model="selectedDate"
            :placeholder="$t('admin.logs.selectDate')"
            style="width: 100%"
            @change="onDateChange"
          >
            <el-option-group v-if="rootFiles.length" :label="$t('admin.logs.rootFiles')">
              <el-option
                v-for="f in rootFiles"
                :key="'root::' + f"
                :label="f"
                :value="'root::' + f"
              />
            </el-option-group>
            <el-option-group v-if="dates.length" :label="$t('admin.logs.dateFolders')">
              <el-option
                v-for="d in dates"
                :key="d.date"
                :label="d.date"
                :value="d.date"
              />
            </el-option-group>
          </el-select>
        </el-col>

        <!-- 日志级别选择（仅当选择了日期目录时显示） -->
        <el-col v-if="isDateSelected" :xs="24" :sm="6" :md="4">
          <el-select
            v-model="selectedType"
            :placeholder="$t('admin.logs.selectLevel')"
            style="width: 100%"
            @change="loadLog"
          >
            <el-option
              v-for="t in currentTypes"
              :key="t"
              :label="t + '.log'"
              :value="t"
            />
          </el-select>
        </el-col>

        <!-- 行数 -->
        <el-col :xs="12" :sm="5" :md="4">
          <el-input-number
            v-model="tailLines"
            :min="50"
            :max="5000"
            :step="100"
            controls-position="right"
            style="width: 100%"
          />
        </el-col>

        <!-- 操作按钮 -->
        <el-col :xs="12" :sm="5" :md="10">
          <div class="btn-group">
            <el-button
              type="primary"
              :icon="Refresh"
              :loading="loading"
              @click="refreshAll"
            >
              {{ $t('admin.logs.refresh') }}
            </el-button>
            <el-button :icon="CopyDocument" @click="copyContent">
              {{ $t('admin.logs.copy') }}
            </el-button>
          </div>
        </el-col>
      </el-row>
    </el-card>

    <!-- 日志内容 -->
    <el-card class="content-card" shadow="never">
      <template #header>
        <div class="content-header">
          <span class="file-label">
            <el-icon><Folder /></el-icon>
            {{ currentFileLabel }}
          </span>
          <span v-if="lineCount > 0" class="line-count">
            {{ lineCount }} {{ $t('admin.logs.lines') }}
          </span>
        </div>
      </template>

      <div class="log-container" ref="logContainerRef">
        <div v-if="loading" class="log-placeholder">
          <el-icon class="is-loading"><Loading /></el-icon>
          <span>{{ $t('admin.logs.loading') }}</span>
        </div>
        <div v-else-if="!hasSelection" class="log-placeholder">
          <el-icon><InfoFilled /></el-icon>
          <span>{{ $t('admin.logs.pleaseSelect') }}</span>
        </div>
        <div v-else-if="!logContent" class="log-placeholder">
          <el-icon><DocumentDelete /></el-icon>
          <span>{{ $t('admin.logs.noContent') }}</span>
        </div>
        <pre v-else class="log-pre" ref="logPreRef">{{ logContent }}</pre>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import {
  Document, Refresh, CopyDocument, Folder, Loading, InfoFilled,
  DocumentDelete
} from '@element-plus/icons-vue'
import { getLogDates, getLogContent } from '@/api/admin'

const { t } = useI18n()

// 状态
const dates = ref([])
const rootFiles = ref([])
const selectedDate = ref('')
const selectedType = ref('')
const tailLines = ref(200)
const logContent = ref('')
const lineCount = ref(0)
const loading = ref(false)
const logContainerRef = ref(null)
const logPreRef = ref(null)

// 是否选择的是日期目录（非根目录文件）
const isDateSelected = computed(() => {
  return selectedDate.value && !selectedDate.value.startsWith('root::')
})

// 当前日期目录下的类型列表
const currentTypes = computed(() => {
  if (!isDateSelected.value) return []
  const found = dates.value.find(d => d.date === selectedDate.value)
  return found ? found.types : []
})

// 是否已做出完整选择
const hasSelection = computed(() => {
  if (!selectedDate.value) return false
  if (selectedDate.value.startsWith('root::')) return true
  return !!selectedType.value
})

// 当前文件标签
const currentFileLabel = computed(() => {
  if (!selectedDate.value) return '—'
  if (selectedDate.value.startsWith('root::')) {
    return selectedDate.value.replace('root::', '')
  }
  if (selectedType.value) {
    return `${selectedDate.value} / ${selectedType.value}.log`
  }
  return selectedDate.value
})

// 加载日期列表
const loadDates = async () => {
  try {
    const res = await getLogDates()
    dates.value = res.data?.dates || []
    rootFiles.value = res.data?.root_files || []
  } catch (e) {
    console.error('获取日志日期失败', e)
    ElMessage.error(t('admin.logs.loadDatesFailed'))
  }
}

// 同时刷新日期列表和日志内容
const refreshAll = async () => {
  await loadDates()
  loadLog()
}

// 当日期变化时，重置类型并尝试加载
const onDateChange = () => {
  selectedType.value = ''
  logContent.value = ''
  lineCount.value = 0

  if (selectedDate.value.startsWith('root::')) {
    // 根目录文件，直接加载
    loadLog()
  } else if (currentTypes.value.length === 1) {
    // 只有一种类型时自动选择
    selectedType.value = currentTypes.value[0]
    loadLog()
  }
}

// 加载日志内容
const loadLog = async () => {
  if (!hasSelection.value) return

  let params
  if (selectedDate.value.startsWith('root::')) {
    params = { file: selectedDate.value.replace('root::', ''), tail: tailLines.value }
  } else {
    params = { date: selectedDate.value, file: selectedType.value, tail: tailLines.value }
  }

  loading.value = true
  try {
    const res = await getLogContent(params)
    logContent.value = res.data?.content || ''
    lineCount.value = res.data?.lines || 0
    // 滚动到底部
    nextTick(() => {
      if (logContainerRef.value) {
        logContainerRef.value.scrollTop = logContainerRef.value.scrollHeight
      }
    })
  } catch (e) {
    logContent.value = ''
    lineCount.value = 0
    if (e?.message?.includes('不存在')) {
      ElMessage.warning(t('admin.logs.fileNotFound'))
    } else {
      ElMessage.error(t('admin.logs.loadFailed'))
    }
  } finally {
    loading.value = false
  }
}

// 复制日志内容
const copyContent = async () => {
  if (!logContent.value) return
  try {
    await navigator.clipboard.writeText(logContent.value)
    ElMessage.success(t('admin.logs.copySuccess'))
  } catch (e) {
    ElMessage.error('复制失败')
  }
}

onMounted(async () => {
  await loadDates()
  // 默认选择最新日期 + 第一种类型
  if (dates.value.length > 0) {
    selectedDate.value = dates.value[0].date
    if (dates.value[0].types.length > 0) {
      selectedType.value = dates.value[0].types[0]
      loadLog()
    }
  } else if (rootFiles.value.length > 0) {
    selectedDate.value = 'root::' + rootFiles.value[0]
    loadLog()
  }
})

</script>

<style lang="scss" scoped>
.log-viewer {
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 16px;

  .header-card {
    .header-content {
      display: flex;
      align-items: center;
      justify-content: space-between;

      .title-section {
        h2 {
          display: flex;
          align-items: center;
          gap: 8px;
          margin: 0 0 4px 0;
          font-size: 20px;
          font-weight: 600;
          color: var(--el-text-color-primary);
        }
        .subtitle {
          margin: 0;
          font-size: 13px;
          color: var(--el-text-color-secondary);
        }
      }
    }
  }

  .toolbar-card {
    :deep(.el-card__body) {
      padding: 16px 20px;
    }

    .btn-group {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
    }

  }

  .content-card {
    flex: 1;

    .content-header {
      display: flex;
      align-items: center;
      justify-content: space-between;

      .file-label {
        display: flex;
        align-items: center;
        gap: 6px;
        font-family: monospace;
        font-size: 13px;
        color: var(--el-text-color-regular);
      }

      .line-count {
        font-size: 12px;
        color: var(--el-text-color-secondary);
        background: var(--el-fill-color-light);
        padding: 2px 8px;
        border-radius: 10px;
      }
    }

    .log-container {
      background: #0d1117;
      border-radius: 6px;
      min-height: 500px;
      max-height: calc(100vh - 380px);
      overflow-y: auto;
      display: flex;
      flex-direction: column;

      .log-placeholder {
        flex: 1;
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
        gap: 12px;
        color: #6e7681;
        min-height: 300px;
        font-size: 14px;

        .el-icon {
          font-size: 32px;
        }
      }

      .log-pre {
        margin: 0;
        padding: 16px;
        font-family: 'JetBrains Mono', 'Fira Code', 'Cascadia Code', Consolas, monospace;
        font-size: 12px;
        line-height: 1.6;
        color: #c9d1d9;
        white-space: pre-wrap;
        word-break: break-all;
        flex: 1;
      }
    }
  }
}


</style>
