<template>
  <div class="instance-traffic-detail">
    <el-dialog
      v-model="visible"
      :title="`${t('user.traffic.detail.title')} - ${displayInstanceName}`"
      width="800px"
      :before-close="handleClose"
    >
      <div
        v-if="loading"
        class="loading-container"
      >
        <el-skeleton
          :rows="5"
          animated
        />
      </div>

      <div
        v-else-if="trafficData"
        class="traffic-detail-content"
      >
        <!-- 实例基本信息 -->
        <div class="instance-info">
          <el-descriptions
            :column="2"
            border
          >
            <el-descriptions-item :label="t('user.traffic.detail.instanceId')">
              {{ trafficData.instance_id }}
            </el-descriptions-item>
            <el-descriptions-item :label="t('user.traffic.detail.dataSource')">
              <el-tag type="success">
                {{ t('user.traffic.detail.realtimeData') }}
              </el-tag>
            </el-descriptions-item>
          </el-descriptions>
        </div>

        <!-- 流量汇总信息 -->
        <div class="traffic-summary">
          <h4>{{ t('user.traffic.detail.trafficSummary') }}</h4>
          
          <!-- 当月流量 -->
          <div class="period-section">
            <h5>{{ t('user.traffic.detail.currentMonth') }}</h5>
            <el-row :gutter="20">
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    {{ t('user.traffic.detail.receivedTraffic') }}
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.formatted?.rx || formatBytes(trafficData.rx_bytes) }}
                  </div>
                </div>
              </el-col>
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    {{ t('user.traffic.detail.sentTraffic') }}
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.formatted?.tx || formatBytes(trafficData.tx_bytes) }}
                  </div>
                </div>
              </el-col>
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    {{ t('user.traffic.detail.totalTraffic') }}
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.formatted?.current_usage || formatBytes(trafficData.total_bytes) }}
                  </div>
                </div>
              </el-col>
            </el-row>
          </div>

        </div>

        <!-- 监控配置信息 -->
        <div
          v-if="trafficData.traffic_control_enabled"
          class="config-section"
        >
          <h4>{{ t('user.traffic.detail.monitoringConfig') }}</h4>
          <el-descriptions :column="2" border>
            <el-descriptions-item :label="t('user.traffic.detail.mappedIP')">
              {{ trafficData.mapped_ip || '-' }}
            </el-descriptions-item>
            <el-descriptions-item :label="t('user.traffic.detail.mappedIPv6')">
              {{ trafficData.mapped_ipv6 || '-' }}
            </el-descriptions-item>
            <el-descriptions-item :label="t('user.traffic.detail.monitoringStatus')">
              <el-tag :type="trafficData.is_enabled ? 'success' : 'info'">
                {{ trafficData.is_enabled ? t('user.traffic.detail.enabled') : t('user.traffic.detail.disabled') }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="t('user.traffic.detail.lastSync')">
              {{ trafficData.last_sync ? new Date(trafficData.last_sync).toLocaleString(locale === 'zh-CN' ? 'zh-CN' : 'en-US') : '-' }}
            </el-descriptions-item>
            <el-descriptions-item :label="t('user.traffic.detail.billingMode')">
              {{ getTrafficCountModeText(trafficData.traffic_count_mode) }}
            </el-descriptions-item>
            <el-descriptions-item :label="t('user.traffic.detail.trafficMultiplier')">
              {{ trafficData.traffic_multiplier || 1 }}x
            </el-descriptions-item>
          </el-descriptions>
        </div>



        <!-- 网络接口信息 -->
        <div
          v-if="trafficData.interfaces && trafficData.interfaces.length > 0"
          class="interfaces-section"
        >
          <h4>{{ t('user.traffic.detail.networkInterfaces') }}</h4>
          <el-table
            :data="trafficData.interfaces"
            border
            stripe
          >
            <el-table-column
              prop="name"
              :label="t('user.traffic.detail.interfaceName')"
              width="120"
            />
            <el-table-column
              prop="alias"
              :label="t('user.traffic.detail.alias')"
              width="150"
            />
            <el-table-column
              prop="total_rx"
              :label="t('user.traffic.detail.totalReceived')"
              :formatter="formatBytesColumn"
            />
            <el-table-column
              prop="total_tx"
              :label="t('user.traffic.detail.totalSent')"
              :formatter="formatBytesColumn"
            />
            <el-table-column
              prop="total_bytes"
              :label="t('user.traffic.detail.totalTraffic')"
              :formatter="formatBytesColumn"
            />
            <el-table-column
              prop="active"
              :label="t('user.traffic.detail.status')"
              width="80"
            >
              <template #default="{ row }">
                <el-tag :type="row.active ? 'success' : 'info'">
                  {{ row.active ? t('user.traffic.detail.active') : t('user.traffic.detail.inactive') }}
                </el-tag>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </div>

      <div
        v-else
        class="error-state"
      >
        <el-empty :description="t('user.traffic.detail.noData')" />
      </div>

      <template #footer>
        <span class="dialog-footer">
          <el-button @click="handleClose">{{ t('user.traffic.detail.close') }}</el-button>
          <el-button
            type="primary"
            @click="loadTrafficDetail"
          >
            <el-icon><Refresh /></el-icon>
            {{ t('user.traffic.detail.refresh') }}
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, watch, computed } from 'vue'
import { getInstanceTrafficDetail } from '@/api/user'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

const props = defineProps({
  modelValue: {
    type: Boolean,
    default: false
  },
  instanceId: {
    type: [Number, String],
    required: true
  },
  instanceName: {
    type: String,
    default: null
  }
})

const emit = defineEmits(['update:modelValue'])

const visible = ref(false)
const loading = ref(false)
const trafficData = ref(null)

// 使用 computed 来处理 instanceName，如果没有传递则使用翻译的默认值
const displayInstanceName = computed(() => {
  return props.instanceName || t('user.traffic.detail.unknownInstance')
})

watch(() => props.modelValue, (newVal) => {
  visible.value = newVal
  if (newVal && props.instanceId) {
    loadTrafficDetail()
  }
})

watch(visible, (newVal) => {
  emit('update:modelValue', newVal)
})

const loadTrafficDetail = async () => {
  if (!props.instanceId) return
  
  loading.value = true
  try {
    const response = await getInstanceTrafficDetail(props.instanceId)
    
    if (response.code === 0) {
      trafficData.value = response.data
      ElMessage.success(t('user.traffic.detail.loadSuccess'))
    } else {
      ElMessage.error(`${t('user.traffic.detail.loadFailed')}: ${response.msg}`)
    }
  } catch (error) {
    console.error('获取实例流量详情失败:', error)
    ElMessage.error(error.message || t('user.traffic.detail.loadFailedRetry'))
  } finally {
    loading.value = false
  }
}

const getTrafficCountModeText = (mode) => {
  const modes = {
    'bidirectional': t('user.traffic.detail.bidirectional'),
    'upload_only': t('user.traffic.detail.uploadOnly'),
    'download_only': t('user.traffic.detail.downloadOnly')
  }
  return modes[mode] || mode || '-'
}

const formatBytes = (bytes) => {
  if (!bytes || bytes === 0) return '0 B'
  
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = bytes
  let unitIndex = 0
  
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex++
  }
  
  return `${size.toFixed(2)} ${units[unitIndex]}`
}

const formatBytesColumn = (row, column, cellValue) => {
  return formatBytes(cellValue)
}

const handleClose = () => {
  visible.value = false
  trafficData.value = null
}
</script>

<style scoped>
.loading-container {
  padding: 20px;
}

.traffic-detail-content {
  padding: 10px 0;
}

.instance-info {
  margin-bottom: 20px;
}

.traffic-summary h4,
.interfaces-section h4 {
  margin-bottom: 15px;
  color: var(--el-text-color-primary);
  border-bottom: 2px solid var(--el-border-color-lighter);
  padding-bottom: 8px;
}

.period-section {
  margin-bottom: 20px;
}

.period-section h5 {
  margin-bottom: 10px;
  color: var(--el-text-color-regular);
  font-size: 14px;
}

.history-info {
  margin-bottom: 15px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.traffic-card {
  background: var(--el-fill-color-lighter);
  border-radius: 8px;
  padding: 15px;
  text-align: center;
  border: 1px solid var(--el-border-color-light);
}

.traffic-label {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-bottom: 8px;
}

.traffic-value {
  font-size: 16px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  font-family: monospace;
}

.interfaces-section {
  margin-top: 25px;
}

.error-state {
  padding: 40px;
  text-align: center;
}

.dialog-footer {
  display: flex;
  justify-content: space-between;
  width: 100%;
}
</style>
