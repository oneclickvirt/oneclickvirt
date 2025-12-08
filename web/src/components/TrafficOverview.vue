<template>
  <div class="traffic-overview">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ t('user.trafficOverview.title') }}</span>
          <el-button
            size="small"
            :loading="loading"
            @click="loadTrafficData"
          >
            <el-icon><Refresh /></el-icon>
            {{ t('user.trafficOverview.refresh') }}
          </el-button>
        </div>
      </template>

      <div
        v-if="loading"
        class="loading-container"
      >
        <el-skeleton
          :rows="3"
          animated
        />
      </div>

      <div
        v-else-if="trafficData"
        class="traffic-content"
      >
        <!-- 数据源指示 -->
        <div class="data-source-indicator">
          <el-tag 
            :type="trafficData.traffic_control_enabled ? 'success' : 'warning'"
            size="small"
          >
            {{ trafficData.traffic_control_enabled ? t('user.trafficOverview.pmacctRealtime') : t('user.trafficOverview.basicData') }}
          </el-tag>
        </div>

        <!-- 流量使用进度 -->
        <div class="traffic-usage">
          <div class="usage-header">
            <span class="usage-title">{{ t('user.trafficOverview.monthlyUsage') }}</span>
            <span class="usage-values">
              {{ trafficData.formatted?.current_usage || formatTraffic(trafficData.current_month_usage_mb) }} / 
              {{ trafficData.formatted?.total_limit || formatTraffic(trafficData.total_limit_mb) }}
            </span>
          </div>
          <el-progress 
            :percentage="Math.min(trafficData.usage_percent || 0, 100)"
            :color="getProgressColor(trafficData.usage_percent || 0)"
            :stroke-width="12"
            :status="trafficData.is_limited ? 'exception' : undefined"
            :format="(percentage) => `${percentage.toFixed(2)}%`"
          />
          <div class="usage-info">
            <span class="usage-percent">{{ (trafficData.usage_percent || 0).toFixed(2) }}%</span>
            <span 
              v-if="trafficData.is_limited" 
              class="limit-warning"
            >
              <el-icon><Warning /></el-icon>
              {{ t('user.trafficOverview.limitExceeded') }}
            </span>
          </div>
        </div>

        <!-- 重置时间 -->
        <div
          v-if="trafficData.reset_time"
          class="reset-info"
        >
          <el-text
            type="info"
            size="small"
          >
            <el-icon><Clock /></el-icon>
            {{ t('user.trafficOverview.resetTime') }}: {{ formatDate(trafficData.reset_time) }}
          </el-text>
        </div>

        <!-- PMAcct详细数据 -->
        <div
          v-if="trafficData.traffic_control_enabled && showDetails"
          class="pmacct-details"
        >
          <el-divider content-position="left">
            {{ t('user.trafficOverview.detailedStats') }}
          </el-divider>
          <div class="details-grid">
            <div class="detail-item">
              <span class="detail-label">{{ t('user.trafficOverview.rxTraffic') }}</span>
              <span class="detail-value">{{ trafficData.formatted?.rx || '0 B' }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">{{ t('user.trafficOverview.txTraffic') }}</span>
              <span class="detail-value">{{ trafficData.formatted?.tx || '0 B' }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">{{ t('user.trafficOverview.totalTraffic') }}</span>
              <span class="detail-value">{{ trafficData.formatted?.total || '0 B' }}</span>
            </div>
          </div>
        </div>

        <!-- 展开/收起按钮 -->
        <div
          v-if="trafficData.traffic_control_enabled"
          class="toggle-details"
        >
          <el-button
            text
            size="small"
            @click="showDetails = !showDetails"
          >
            {{ showDetails ? t('user.trafficOverview.hideDetails') : t('user.trafficOverview.viewDetails') }}
            <el-icon><ArrowDown v-if="!showDetails" /><ArrowUp v-else /></el-icon>
          </el-button>
        </div>
      </div>

      <div
        v-else
        class="error-state"
      >
        <el-empty :description="t('user.trafficOverview.noData')" />
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { getUserTrafficOverview } from '@/api/user'
import { ElMessage } from 'element-plus'
import { Refresh, Warning, Clock, ArrowDown, ArrowUp } from '@element-plus/icons-vue'

const { t, locale } = useI18n()
const loading = ref(false)
const trafficData = ref(null)
const showDetails = ref(false)

const loadTrafficData = async () => {
  loading.value = true
  try {
    const response = await getUserTrafficOverview()
    if (response.code === 0) {
      trafficData.value = response.data
    } else {
      ElMessage.error(`${t('user.trafficOverview.loadFailed')}: ${response.msg}`)
    }
  } catch (error) {
    console.error('获取流量数据失败:', error)
    ElMessage.error(t('user.trafficOverview.loadFailed'))
  } finally {
    loading.value = false
  }
}

const formatTraffic = (mb) => {
  if (!mb || mb === 0) return '0 B'
  
  const GB_IN_MB = 1024
  const TB_IN_MB = 1024 * 1024
  
  if (mb >= TB_IN_MB) {
    return `${(mb / TB_IN_MB).toFixed(2)} TB`
  } else if (mb >= GB_IN_MB) {
    return `${(mb / GB_IN_MB).toFixed(2)} GB`
  } else if (mb >= 1) {
    return `${mb.toFixed(2)} MB`
  } else if (mb > 0) {
    return `${(mb * 1024).toFixed(2)} KB`
  }
  return '0 B'
}

const getProgressColor = (percentage) => {
  if (percentage < 60) return '#67c23a'
  if (percentage < 80) return '#e6a23c'
  return '#f56c6c'
}

const formatDate = (dateString) => {
  if (!dateString) return t('common.notSet')
  const localeCode = locale.value === 'zh-CN' ? 'zh-CN' : 'en-US'
  return new Date(dateString).toLocaleString(localeCode)
}

onMounted(() => {
  loadTrafficData()
})
</script>

<style scoped>
.traffic-overview {
  margin-bottom: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.loading-container {
  padding: 20px;
}

.traffic-content {
  padding: 10px 0;
}

.data-source-indicator {
  margin-bottom: 15px;
}

.traffic-usage {
  margin-bottom: 15px;
}

.usage-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 10px;
}

.usage-title {
  font-weight: 500;
  color: var(--el-text-color-primary);
}

.usage-values {
  font-family: monospace;
  font-size: 14px;
  color: var(--el-text-color-regular);
}

.usage-info {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 8px;
}

.usage-percent {
  font-weight: 500;
  color: var(--el-text-color-primary);
}

.limit-warning {
  color: var(--el-color-danger);
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
}

.reset-info {
  margin-bottom: 15px;
  text-align: center;
}

.pmacct-details {
  margin-top: 15px;
}

.details-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 15px;
  margin-top: 10px;
}

@media (max-width: 768px) {
  .details-grid {
    grid-template-columns: 1fr;
  }
}

.detail-item {
  text-align: center;
  padding: 15px;
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
  min-height: 80px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
}

.detail-label {
  display: block;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-bottom: 5px;
}

.detail-value {
  display: block;
  font-weight: 600;
  font-size: 16px;
  font-family: monospace;
  color: var(--el-text-color-primary);
}

.toggle-details {
  text-align: center;
  margin-top: 15px;
}

.error-state {
  padding: 20px;
  text-align: center;
}
</style>
