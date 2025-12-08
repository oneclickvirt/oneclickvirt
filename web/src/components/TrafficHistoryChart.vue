<template>
  <div class="traffic-history-chart">
    <el-card>
      <template #header>
        <div class="chart-header">
          <span v-if="title">{{ title }}</span>
          <div class="chart-controls">
            <slot name="extra-actions"></slot>
            <span style="margin-right: 8px; font-size: 14px;">{{ $t('user.traffic.historyChart.timeRange') }}:</span>
            <el-select
              v-model="selectedPeriod"
              size="small"
              style="width: 120px; margin-right: 16px;"
              @change="loadData"
            >
              <el-option :label="$t('user.traffic.historyChart.period15m')" value="15m" />
              <el-option :label="$t('user.traffic.historyChart.period30m')" value="30m" />
              <el-option :label="$t('user.traffic.historyChart.period1h')" value="1h" />
              <el-option :label="$t('user.traffic.historyChart.period6h')" value="6h" />
              <el-option :label="$t('user.traffic.historyChart.period12h')" value="12h" />
              <el-option :label="$t('user.traffic.historyChart.period24h')" value="24h" />
            </el-select>
            <span style="margin-right: 8px; font-size: 14px;">{{ $t('user.traffic.historyChart.dataInterval') }}:</span>
            <el-select
              v-model="selectedInterval"
              size="small"
              style="width: 120px;"
              @change="loadData"
            >
              <el-option :label="$t('user.traffic.historyChart.interval5m')" :value="5" />
              <el-option :label="$t('user.traffic.historyChart.interval10m')" :value="10" />
              <el-option :label="$t('user.traffic.historyChart.interval15m')" :value="15" />
              <el-option :label="$t('user.traffic.historyChart.interval30m')" :value="30" />
            </el-select>
          </div>
        </div>
      </template>

      <div
        v-show="loading"
        v-loading="loading"
        class="chart-loading"
        style="height: 400px;"
      />
      
      <div
        v-show="error && !loading"
        class="chart-error"
      >
        <el-empty :description="error" />
      </div>

      <div
        ref="chartRef"
        v-show="!loading && !error"
        class="chart-container"
        style="width: 100%; height: 400px;"
      />
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import * as echarts from 'echarts'
import request from '@/utils/request'
import { getUserTrafficHistory, getInstanceTrafficHistory } from '@/api/user'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { useUserStore } from '@/pinia/modules/user'

const { t, locale } = useI18n()
const userStore = useUserStore()

const props = defineProps({
  // 'instance', 'provider', 'user'
  type: {
    type: String,
    required: true,
    validator: (value) => ['instance', 'provider', 'user'].includes(value)
  },
  // 资源ID (instance_id, provider_id, user_id)
  resourceId: {
    type: [Number, String],
    default: null
  },
  // 图表标题
  title: {
    type: String,
    default: ''
  },
  // 自动刷新间隔（秒），0表示不自动刷新
  autoRefresh: {
    type: Number,
    default: 0
  }
})

const chartRef = ref(null)
const loading = ref(false)
const error = ref('')
const selectedPeriod = ref('1h')   // 默认1小时
const selectedInterval = ref(5)    // 默认5分钟间隔
const chartInstance = ref(null)
const refreshTimer = ref(null)
const chartData = ref([])          // 存储图表数据，用于语言切换时重新渲染

// 格式化流量单位
const formatTraffic = (bytes) => {
  if (!bytes || bytes === 0) return '0 B'
  
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const k = 1024
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  
  return `${(bytes / Math.pow(k, i)).toFixed(2)} ${units[i]}`
}

// 格式化时间标签（支持分钟级精度）
const formatTimeLabel = (record) => {
  // 优先使用 record_time 字段
  if (record.record_time) {
    const date = new Date(record.record_time)
    const month = String(date.getMonth() + 1).padStart(2, '0')
    const day = String(date.getDate()).padStart(2, '0')
    const hour = String(date.getHours()).padStart(2, '0')
    const minute = String(date.getMinutes()).padStart(2, '0')
    
    // 根据时间范围调整显示格式
    if (selectedPeriod.value === '5m' || selectedPeriod.value === '10m' || selectedPeriod.value === '15m') {
      // 短时间范围：只显示 时:分
      return `${hour}:${minute}`
    } else if (selectedPeriod.value === '30m' || selectedPeriod.value === '45m' || selectedPeriod.value === '1h') {
      // 中等时间范围：显示 月-日 时:分
      return `${month}-${day} ${hour}:${minute}`
    } else {
      // 长时间范围：显示 月-日 时:分
      return `${month}-${day} ${hour}:${minute}`
    }
  }
  
  // 回退到使用分散的字段
  const month = String(record.month).padStart(2, '0')
  const day = String(record.day).padStart(2, '0')
  const hour = String(record.hour).padStart(2, '0')
  const minute = String(record.minute || 0).padStart(2, '0')
  
  // 根据时间范围调整显示格式
  if (selectedPeriod.value === '5m' || selectedPeriod.value === '10m' || selectedPeriod.value === '15m') {
    // 短时间范围：只显示 时:分
    return `${hour}:${minute}`
  } else if (selectedPeriod.value === '30m' || selectedPeriod.value === '45m' || selectedPeriod.value === '1h') {
    // 中等时间范围：显示 月-日 时:分
    return `${month}-${day} ${hour}:${minute}`
  } else {
    // 长时间范围：显示 月-日 时:分
    return `${month}-${day} ${hour}:${minute}`
  }
}

// 加载流量历史数据
const loadData = async () => {
  if (loading.value) return
  
  // 检查用户是否已登录
  if (!userStore.isLoggedIn) {
    console.warn('Traffic history: User not logged in, skipping data load')
    error.value = t('user.trafficOverview.noData') || '暂无数据'
    return
  }
  
  loading.value = true
  error.value = ''
  
  try {
    let response
    const params = {
      period: selectedPeriod.value,
      interval: selectedInterval.value
    }
    
    console.log('Loading traffic history:', {
      type: props.type,
      resourceId: props.resourceId,
      params,
      userType: userStore.userType,
      viewMode: userStore.viewMode,
      hasToken: !!userStore.token
    })
    
    switch (props.type) {
      case 'instance':
        if (!props.resourceId) {
          throw new Error('Instance ID is required')
        }
        response = await getInstanceTrafficHistory(props.resourceId, params)
        break
      case 'provider':
        if (!props.resourceId) {
          throw new Error('Provider ID is required')
        }
        // Provider 使用admin API，仍然需要直接调用
        response = await request({
          url: `/v1/admin/providers/${props.resourceId}/traffic/history`,
          method: 'get',
          params
        })
        break
      case 'user':
        response = await getUserTrafficHistory(params)
        break
      default:
        throw new Error('Invalid type')
    }
    
    console.log('Traffic history response:', response)
    
    if (response && response.code === 0) {
      loading.value = false
      // 存储数据供语言切换时使用
      chartData.value = response.data || []
      // 等待DOM更新后再渲染图表
      await nextTick()
      renderChart(chartData.value)
    } else {
      throw new Error(response?.message || response?.msg || 'Failed to load data')
    }
  } catch (err) {
    console.error('Load traffic history failed:', err)
    loading.value = false
    // 如果是401错误，说明认证失败，显示友好提示
    if (err.response?.status === 401 || err.message?.includes('401') || err.message?.includes('未登录') || err.message?.includes('未授权')) {
      error.value = t('user.trafficOverview.noData') || '暂无数据'
      console.warn('Traffic history: Authentication failed, user may need to re-login')
    } else {
      error.value = err.message || t('user.traffic.historyChart.loadFailed')
      ElMessage.error(error.value)
    }
  }
}

// 渲染图表
const renderChart = (data) => {
  console.log('TrafficHistoryChart - renderChart called with data:', data)
  
  if (!chartRef.value) {
    console.warn('TrafficHistoryChart - chartRef is null')
    return
  }
  
  if (!data || data.length === 0) {
    console.warn('TrafficHistoryChart - No data to render')
    error.value = t('user.traffic.historyChart.noData') || '暂无流量数据'
    // 销毁图表实例
    if (chartInstance.value) {
      chartInstance.value.dispose()
      chartInstance.value = null
    }
    return
  }
  
  console.log('TrafficHistoryChart - Data length:', data.length)
  
  // 清除错误状态
  error.value = ''
  
  // 初始化或重新初始化图表实例
  // 如果图表实例已存在但DOM已销毁，需要重新初始化
  if (!chartInstance.value || chartInstance.value.isDisposed()) {
    console.log('TrafficHistoryChart - Initializing chart instance')
    if (chartInstance.value) {
      chartInstance.value.dispose()
    }
    chartInstance.value = echarts.init(chartRef.value)
  }
  
  // 准备数据
  const timeLabels = data.map(item => formatTimeLabel(item))
  // 优先使用增量字段（traffic_in/traffic_out/total_used），如果不存在则使用累积字段（rx_bytes/tx_bytes/total_bytes）
  const trafficIn = data.map(item => (((item.traffic_in || item.rx_bytes) || 0) / 1024 / 1024).toFixed(2)) // 转换为MB
  const trafficOut = data.map(item => (((item.traffic_out || item.tx_bytes) || 0) / 1024 / 1024).toFixed(2))
  const totalUsed = data.map(item => (((item.total_used || item.total_bytes) || 0) / 1024 / 1024).toFixed(2))
  
  console.log('TrafficHistoryChart - Processed data:', {
    timeLabels,
    trafficIn,
    trafficOut,
    totalUsed
  })
  
  // 配置图表选项
  const option = {
    tooltip: {
      trigger: 'axis',
      axisPointer: {
        type: 'cross',
        label: {
          backgroundColor: '#6a7985'
        }
      },
      formatter: (params) => {
        let result = `${params[0].axisValue}<br/>`
        params.forEach(item => {
          const value = parseFloat(item.value)
          result += `${item.marker} ${item.seriesName}: ${formatTraffic(value * 1024 * 1024)}<br/>`
        })
        return result
      }
    },
    legend: {
      data: [
        t('user.traffic.historyChart.inbound'),
        t('user.traffic.historyChart.outbound'),
        t('user.traffic.historyChart.total')
      ],
      top: 10
    },
    grid: {
      left: '3%',
      right: '4%',
      bottom: '3%',
      containLabel: true
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: timeLabels,
      axisLabel: {
        rotate: 45,
        interval: 'auto'
      }
    },
    yAxis: {
      type: 'value',
      name: 'MB',
      axisLabel: {
        formatter: (value) => {
          if (value >= 1024) {
            return `${(value / 1024).toFixed(1)} GB`
          }
          return `${value} MB`
        }
      }
    },
    series: [
      {
        name: t('user.traffic.historyChart.inbound'),
        type: 'line',
        smooth: true,
        data: trafficIn,
        itemStyle: {
          color: '#67C23A'
        },
        areaStyle: {
          opacity: 0.3,
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: '#67C23A' },
            { offset: 1, color: 'rgba(103, 194, 58, 0.1)' }
          ])
        }
      },
      {
        name: t('user.traffic.historyChart.outbound'),
        type: 'line',
        smooth: true,
        data: trafficOut,
        itemStyle: {
          color: '#E6A23C'
        },
        areaStyle: {
          opacity: 0.3,
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: '#E6A23C' },
            { offset: 1, color: 'rgba(230, 162, 60, 0.1)' }
          ])
        }
      },
      {
        name: t('user.traffic.historyChart.total'),
        type: 'line',
        smooth: true,
        data: totalUsed,
        itemStyle: {
          color: '#409EFF'
        },
        areaStyle: {
          opacity: 0.3,
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: '#409EFF' },
            { offset: 1, color: 'rgba(64, 158, 255, 0.1)' }
          ])
        }
      }
    ]
  }
  
  console.log('TrafficHistoryChart - Setting chart options:', option)
  chartInstance.value.setOption(option)
  console.log('TrafficHistoryChart - Chart rendered successfully')
}

// 窗口大小改变时重新渲染
const handleResize = () => {
  if (chartInstance.value) {
    chartInstance.value.resize()
  }
}

// 设置自动刷新
const setupAutoRefresh = () => {
  if (props.autoRefresh > 0) {
    refreshTimer.value = setInterval(() => {
      loadData()
    }, props.autoRefresh * 1000)
  }
}

// 清除自动刷新
const clearAutoRefresh = () => {
  if (refreshTimer.value) {
    clearInterval(refreshTimer.value)
    refreshTimer.value = null
  }
}

// 监听资源ID变化
watch(() => props.resourceId, () => {
  if (props.resourceId) {
    loadData()
  }
})

// 监听自动刷新配置变化
watch(() => props.autoRefresh, () => {
  clearAutoRefresh()
  setupAutoRefresh()
})

// 监听语言变化，重新渲染图表
watch(() => locale.value, () => {
  // 当语言切换时，如果图表已经有数据，重新渲染
  if (chartData.value && chartData.value.length > 0) {
    renderChart(chartData.value)
  }
})

onMounted(async () => {
  await nextTick()
  window.addEventListener('resize', handleResize)
  if (props.resourceId || props.type === 'user') {
    loadData()
  }
  setupAutoRefresh()
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  clearAutoRefresh()
  if (chartInstance.value) {
    chartInstance.value.dispose()
    chartInstance.value = null
  }
})

defineExpose({
  refresh: loadData
})
</script>

<style scoped lang="scss">
.traffic-history-chart {
  margin-top: 20px;

  .chart-header {
    display: flex;
    justify-content: space-between;
    align-items: center;

    .chart-controls {
      display: flex;
      align-items: center;
      gap: 4px;
    }
  }

  .chart-loading,
  .chart-error {
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .chart-container {
    min-height: 400px;
  }
}
</style>
