<template>
  <div class="instance-traffic-detail">
    <el-dialog
      v-model="visible"
      :title="`实例流量详情 - ${instanceName}`"
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
            <el-descriptions-item label="实例ID">
              {{ trafficData.instance_id }}
            </el-descriptions-item>
            <el-descriptions-item label="数据源">
              <el-tag type="success">
                vnStat实时数据
              </el-tag>
            </el-descriptions-item>
          </el-descriptions>
        </div>

        <!-- 流量汇总信息 -->
        <div
          v-if="trafficData.summary"
          class="traffic-summary"
        >
          <h4>流量使用汇总</h4>
          
          <!-- 今日流量 -->
          <div
            v-if="trafficData.summary.today"
            class="period-section"
          >
            <h5>今日流量</h5>
            <el-row :gutter="20">
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    接收流量
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.today_formatted?.rx || formatBytes(trafficData.summary.today.rx_bytes) }}
                  </div>
                </div>
              </el-col>
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    发送流量
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.today_formatted?.tx || formatBytes(trafficData.summary.today.tx_bytes) }}
                  </div>
                </div>
              </el-col>
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    总流量
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.today_formatted?.total || formatBytes(trafficData.summary.today.total_bytes) }}
                  </div>
                </div>
              </el-col>
            </el-row>
          </div>

          <!-- 本月流量 -->
          <div
            v-if="trafficData.summary.thisMonth"
            class="period-section"
          >
            <h5>本月流量</h5>
            <el-row :gutter="20">
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    接收流量
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.month_formatted?.rx || formatBytes(trafficData.summary.thisMonth.rx_bytes) }}
                  </div>
                </div>
              </el-col>
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    发送流量
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.month_formatted?.tx || formatBytes(trafficData.summary.thisMonth.tx_bytes) }}
                  </div>
                </div>
              </el-col>
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    总流量
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.month_formatted?.total || formatBytes(trafficData.summary.thisMonth.total_bytes) }}
                  </div>
                </div>
              </el-col>
            </el-row>
          </div>

          <!-- 历史总流量 -->
          <div
            v-if="trafficData.summary.allTime"
            class="period-section"
          >
            <h5>历史总流量</h5>
            <el-row :gutter="20">
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    接收流量
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.alltime_formatted?.rx || formatBytes(trafficData.summary.allTime.rx_bytes) }}
                  </div>
                </div>
              </el-col>
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    发送流量
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.alltime_formatted?.tx || formatBytes(trafficData.summary.allTime.tx_bytes) }}
                  </div>
                </div>
              </el-col>
              <el-col :span="8">
                <div class="traffic-card">
                  <div class="traffic-label">
                    总流量
                  </div>
                  <div class="traffic-value">
                    {{ trafficData.alltime_formatted?.total || formatBytes(trafficData.summary.allTime.total_bytes) }}
                  </div>
                </div>
              </el-col>
            </el-row>
          </div>
        </div>

        <!-- 网络接口信息 -->
        <div
          v-if="trafficData.interfaces && trafficData.interfaces.length > 0"
          class="interfaces-section"
        >
          <h4>网络接口详情</h4>
          <el-table
            :data="trafficData.interfaces"
            border
            stripe
          >
            <el-table-column
              prop="name"
              label="接口名称"
              width="120"
            />
            <el-table-column
              prop="alias"
              label="别名"
              width="150"
            />
            <el-table-column
              prop="total_rx"
              label="总接收"
              :formatter="formatBytesColumn"
            />
            <el-table-column
              prop="total_tx"
              label="总发送"
              :formatter="formatBytesColumn"
            />
            <el-table-column
              prop="total_bytes"
              label="总流量"
              :formatter="formatBytesColumn"
            />
            <el-table-column
              prop="active"
              label="状态"
              width="80"
            >
              <template #default="{ row }">
                <el-tag :type="row.active ? 'success' : 'info'">
                  {{ row.active ? '活跃' : '非活跃' }}
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
        <el-empty description="暂无流量数据" />
      </div>

      <template #footer>
        <span class="dialog-footer">
          <el-button @click="handleClose">关闭</el-button>
          <el-button
            type="primary"
            @click="loadTrafficDetail"
          >
            <el-icon><Refresh /></el-icon>
            刷新数据
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, watch } from 'vue'
import { getInstanceTrafficDetail } from '@/api/user'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'

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
    default: '未知实例'
  }
})

const emit = defineEmits(['update:modelValue'])

const visible = ref(false)
const loading = ref(false)
const trafficData = ref(null)

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
    } else {
      ElMessage.error(`获取实例流量详情失败: ${response.msg}`)
    }
  } catch (error) {
    console.error('获取实例流量详情失败:', error)
    ElMessage.error('获取实例流量详情失败，请稍后重试')
  } finally {
    loading.value = false
  }
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
