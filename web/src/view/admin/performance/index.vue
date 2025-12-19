<template>
  <div class="performance-monitor">
    <el-card class="header-card" shadow="never">
      <div class="header-content">
        <div class="title-section">
          <h2>
            <el-icon><Monitor /></el-icon>
            {{ $t('admin.performance.title') }}
          </h2>
          <p class="subtitle">{{ $t('admin.performance.subtitle') }}</p>
        </div>
        <div class="refresh-section">
          <el-switch
            v-model="autoRefresh"
            :active-text="$t('admin.performance.autoRefresh')"
            @change="toggleAutoRefresh"
          />
        </div>
      </div>
    </el-card>

    <!-- 关键指标卡片 -->
    <el-row :gutter="20" class="metrics-cards">
      <el-col :xs="24" :sm="12" :md="6">
        <el-card shadow="hover" class="metric-card">
          <div class="metric-icon goroutine">
            <el-icon><Connection /></el-icon>
          </div>
          <div class="metric-content">
            <div class="metric-label">{{ $t('admin.performance.goroutineCount') }}</div>
            <div class="metric-value">{{ metrics.goroutine_count || 0 }}</div>
            <div :class="['metric-status', getGoroutineStatus()]">
              {{ getGoroutineStatusText() }}
            </div>
          </div>
        </el-card>
      </el-col>

      <el-col :xs="24" :sm="12" :md="6">
        <el-card shadow="hover" class="metric-card">
          <div class="metric-icon memory">
            <el-icon><Memo /></el-icon>
          </div>
          <div class="metric-content">
            <div class="metric-label">{{ $t('admin.performance.memoryUsage') }}</div>
            <div class="metric-value">{{ metrics.memory_alloc || 0 }} MB</div>
            <div :class="['metric-status', getMemoryStatus()]">
              {{ getMemoryStatusText() }}
            </div>
          </div>
        </el-card>
      </el-col>

      <el-col :xs="24" :sm="12" :md="6">
        <el-card shadow="hover" class="metric-card">
          <div class="metric-icon gc">
            <el-icon><DeleteFilled /></el-icon>
          </div>
          <div class="metric-content">
            <div class="metric-label">{{ $t('admin.performance.gcCount') }}</div>
            <div class="metric-value">{{ metrics.gc_count || 0 }}</div>
            <div class="metric-status normal">
              {{ $t('admin.performance.averagePause') }}: {{ formatDuration(metrics.gc_pause_avg) }}
            </div>
          </div>
        </el-card>
      </el-col>

      <el-col :xs="24" :sm="12" :md="6">
        <el-card shadow="hover" class="metric-card">
          <div class="metric-icon database">
            <el-icon><Coin /></el-icon>
          </div>
          <div class="metric-content">
            <div class="metric-label">{{ $t('admin.performance.databaseConnections') }}</div>
            <div class="metric-value">
              {{ dbStats.in_use || 0 }} / {{ dbStats.max_open_connections || 0 }}
            </div>
            <div :class="['metric-status', getDBStatus()]">
              {{ $t('admin.performance.utilization') }}: {{ getDBUtilization() }}%
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 详细信息 -->
    <el-row :gutter="20" class="detail-section">
      <!-- 内存详情 -->
      <el-col :xs="24" :md="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>{{ $t('admin.performance.memoryDetails') }}</span>
              <el-tag type="info" size="small">{{ $t('admin.performance.unit') }}: MB</el-tag>
            </div>
          </template>
          <el-descriptions :column="2" border>
            <el-descriptions-item :label="$t('admin.performance.currentAlloc')">
              {{ metrics.memory_alloc || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.totalAlloc')">
              {{ metrics.memory_total_alloc || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.systemMemory')">
              {{ metrics.memory_sys || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.heapMemory')">
              {{ metrics.memory_heap_alloc || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.heapSystem')">
              {{ metrics.memory_heap_sys || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.stackUsage')">
              {{ metrics.memory_stack_inuse || 0 }}
            </el-descriptions-item>
          </el-descriptions>
          
          <!-- 内存使用趋势图 -->
          <div ref="memoryChartRef" class="chart-container"></div>
        </el-card>
      </el-col>

      <!-- GC 详情 -->
      <el-col :xs="24" :md="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>{{ $t('admin.performance.gcDetails') }}</span>
            </div>
          </template>
          <el-descriptions :column="2" border>
            <el-descriptions-item :label="$t('admin.performance.gcCount')">
              {{ metrics.gc_count || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.totalPauseTime')">
              {{ formatDuration(metrics.gc_pause_total) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.averagePause')">
              {{ formatDuration(metrics.gc_pause_avg) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.lastPause')">
              {{ formatDuration(metrics.gc_last_pause) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.nextGC')">
              {{ metrics.next_gc || 0 }} MB
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.cpuCores')">
              {{ metrics.cpu_count || 0 }}
            </el-descriptions-item>
          </el-descriptions>

          <!-- GC 频率趋势图 -->
          <div ref="gcChartRef" class="chart-container"></div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 数据库和连接池 -->
    <el-row :gutter="20" class="detail-section">
      <el-col :xs="24" :md="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>{{ $t('admin.performance.databasePool') }}</span>
              <el-tag v-if="dbManagerStats && dbManagerStats.connected" 
                      :type="dbManagerStats.reconnecting ? 'warning' : 'success'" 
                      size="small">
                {{ dbManagerStats.reconnecting ? $t('admin.performance.reconnecting') : $t('admin.performance.connected') }}
              </el-tag>
            </div>
          </template>
          
          <!-- 数据库管理器状态 -->
          <el-alert 
            v-if="dbManagerStats && dbManagerStats.heartbeat_active"
            :title="$t('admin.performance.dbManagerStatus')"
            type="success"
            :closable="false"
            show-icon
            style="margin-bottom: 16px">
            <template #default>
              <div style="font-size: 12px; line-height: 1.8;">
                <div>{{ $t('admin.performance.heartbeatActive') }}: {{ dbManagerStats.heartbeat_active ? $t('common.yes') : $t('common.no') }}</div>
                <div>{{ $t('admin.performance.maxReconnectRetry') }}: {{ dbManagerStats.max_reconnect_retry }}</div>
                <div>{{ $t('admin.performance.reconnectInterval') }}: {{ dbManagerStats.reconnect_interval }}</div>
              </div>
            </template>
          </el-alert>

          <el-descriptions :column="2" border v-if="dbStats">
            <el-descriptions-item :label="$t('admin.performance.maxConnections')">
              {{ dbStats.max_open_connections || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.currentConnections')">
              {{ dbStats.open_connections || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.inUse')">
              {{ dbStats.in_use || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.idle')">
              {{ dbStats.idle || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.waitCount')">
              {{ dbStats.wait_count || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.waitDuration')">
              {{ formatDuration(dbStats.wait_duration) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.maxIdleClosed')">
              {{ dbStats.max_idle_closed || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.maxLifetimeClosed')">
              {{ dbStats.max_lifetime_closed || 0 }}
            </el-descriptions-item>
          </el-descriptions>
          <el-empty v-else :description="$t('admin.performance.noData')" />
        </el-card>
      </el-col>

      <el-col :xs="24" :md="12">
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>{{ $t('admin.performance.sshPool') }}</span>
              <el-tag v-if="sshPoolStats && sshPoolStats.utilization !== undefined" 
                      :type="getSSHPoolUtilizationType(sshPoolStats.utilization)" 
                      size="small">
                {{ $t('admin.performance.utilization') }}: {{ sshPoolStats.utilization?.toFixed(1) || 0 }}%
              </el-tag>
            </div>
          </template>
          <el-descriptions :column="2" border v-if="sshPoolStats && sshPoolStats.total_connections !== undefined">
            <el-descriptions-item :label="$t('admin.performance.totalConnections')">
              {{ sshPoolStats.total_connections || 0 }} / {{ sshPoolStats.max_connections || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.healthyConnections')">
              <el-tag :type="sshPoolStats.healthy_connections === sshPoolStats.total_connections ? 'success' : 'warning'" size="small">
                {{ sshPoolStats.healthy_connections || 0 }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.unhealthyConnections')">
              <el-tag :type="sshPoolStats.unhealthy_connections > 0 ? 'danger' : 'info'" size="small">
                {{ sshPoolStats.unhealthy_connections || 0 }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.activeConnections')">
              <el-tag type="success" size="small">{{ sshPoolStats.active_connections || 0 }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.idleConnections')">
              <el-tag type="info" size="small">{{ sshPoolStats.idle_connections || 0 }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.avgConnectionAge')">
              {{ formatDuration(sshPoolStats.avg_connection_age) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.oldestConnectionAge')">
              {{ formatDuration(sshPoolStats.oldest_connection_age) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.maxIdleTime')">
              {{ formatDuration(sshPoolStats.max_idle_time) }}
            </el-descriptions-item>
          </el-descriptions>
          <el-empty v-else :description="$t('admin.performance.noSSHData')" />
        </el-card>
      </el-col>
    </el-row>

    <!-- Goroutine 趋势图 -->
    <el-card shadow="hover" class="chart-card">
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.performance.goroutineTrend') }}</span>
          <el-radio-group v-model="timeRange" size="small" @change="fetchHistory">
            <el-radio-button label="5m">{{ $t('admin.performance.timeRange.5m') }}</el-radio-button>
            <el-radio-button label="15m">{{ $t('admin.performance.timeRange.15m') }}</el-radio-button>
            <el-radio-button label="1h">{{ $t('admin.performance.timeRange.1h') }}</el-radio-button>
            <el-radio-button label="6h">{{ $t('admin.performance.timeRange.6h') }}</el-radio-button>
          </el-radio-group>
        </div>
      </template>
      <div ref="goroutineChartRef" class="chart-container-large"></div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { 
  Monitor, 
  Refresh, 
  Connection, 
  Memo, 
  DeleteFilled,
  Coin 
} from '@element-plus/icons-vue'
import * as echarts from 'echarts'
import request from '@/utils/request'

const { t } = useI18n()

// 响应式数据
const loading = ref(false)
const autoRefresh = ref(true)
const timeRange = ref('1h')
const metrics = reactive({})
const dbStats = reactive({})
const dbManagerStats = reactive({})
const sshPoolStats = reactive({})

// 图表实例
let memoryChart = null
let gcChart = null
let goroutineChart = null
let refreshTimer = null
let resizeHandler = null

// 图表引用
const memoryChartRef = ref(null)
const gcChartRef = ref(null)
const goroutineChartRef = ref(null)

// 获取性能指标
const fetchMetrics = async () => {
  loading.value = true
  try {
    const response = await request.get('/v1/admin/performance/metrics')
    if (response.code === 0 && response.data) {
      Object.assign(metrics, response.data)
      if (response.data.db_stats) {
        Object.assign(dbStats, response.data.db_stats)
      }
      if (response.data.db_manager_stats) {
        Object.assign(dbManagerStats, response.data.db_manager_stats)
      }
      if (response.data.ssh_pool_stats) {
        Object.assign(sshPoolStats, response.data.ssh_pool_stats)
      }
      updateCharts()
    }
  } catch (error) {
    ElMessage.error(t('admin.performance.fetchMetricsError') + ': ' + error.message)
  } finally {
    loading.value = false
  }
}

// 获取历史数据
const fetchHistory = async () => {
  try {
    const response = await request.get('/v1/admin/performance/history', {
      params: { duration: timeRange.value }
    })
    if (response.code === 0 && response.data) {
      updateHistoryCharts(response.data.data_points)
    }
  } catch (error) {
    console.error(t('admin.performance.fetchHistoryError') + ':', error)
  }
}

// 更新图表
const updateCharts = () => {
  updateMemoryChart()
  updateGCChart()
}

// 更新内存图表
const updateMemoryChart = () => {
  if (!memoryChart) return
  
  const option = {
    tooltip: { trigger: 'item' },
    legend: { top: '5%', left: 'center' },
    series: [{
      type: 'pie',
      radius: ['40%', '70%'],
      avoidLabelOverlap: false,
      itemStyle: {
        borderRadius: 10,
        borderColor: '#fff',
        borderWidth: 2
      },
      label: { show: false },
      emphasis: { label: { show: true, fontSize: 14, fontWeight: 'bold' } },
      labelLine: { show: false },
      data: [
        { value: metrics.memory_heap_alloc || 0, name: t('admin.performance.heapAlloc') },
        { value: metrics.memory_stack_inuse || 0, name: t('admin.performance.stackInuse') },
        { value: (metrics.memory_sys || 0) - (metrics.memory_heap_sys || 0), name: t('admin.performance.other') }
      ]
    }]
  }
  memoryChart.setOption(option)
}

// 更新GC图表
const updateGCChart = () => {
  if (!gcChart) return
  
  const option = {
    tooltip: { trigger: 'axis' },
    grid: { left: '3%', right: '4%', bottom: '3%', containLabel: true },
    xAxis: { type: 'category', data: [t('admin.performance.totalPauseLabel'), t('admin.performance.averagePauseLabel'), t('admin.performance.lastPauseLabel')] },
    yAxis: { type: 'value', name: t('admin.performance.nanoseconds') },
    series: [{
      data: [
        metrics.gc_pause_total || 0,
        metrics.gc_pause_avg || 0,
        metrics.gc_last_pause || 0
      ],
      type: 'bar',
      showBackground: true,
      backgroundStyle: { color: 'rgba(180, 180, 180, 0.2)' }
    }]
  }
  gcChart.setOption(option)
}

// 更新历史图表
const updateHistoryCharts = (dataPoints) => {
  if (!goroutineChart || !dataPoints || dataPoints.length === 0) return
  
  const times = dataPoints.map(d => new Date(d.timestamp).toLocaleTimeString())
  const goroutineCounts = dataPoints.map(d => d.goroutine_count)
  
  const option = {
    tooltip: { trigger: 'axis' },
    grid: { left: '3%', right: '4%', bottom: '3%', containLabel: true },
    xAxis: { type: 'category', boundaryGap: false, data: times },
    yAxis: { type: 'value', name: t('admin.performance.count') },
    series: [{
      name: t('admin.performance.goroutineLabel'),
      type: 'line',
      smooth: true,
      areaStyle: {},
      data: goroutineCounts
    }]
  }
  goroutineChart.setOption(option)
}

// 格式化时间
const formatDuration = (ns) => {
  if (!ns) return '0ns'
  if (ns < 1000) return `${ns}ns`
  if (ns < 1000000) return `${(ns / 1000).toFixed(2)}μs`
  if (ns < 1000000000) return `${(ns / 1000000).toFixed(2)}ms`
  return `${(ns / 1000000000).toFixed(2)}s`
}

// 获取Goroutine状态
const getGoroutineStatus = () => {
  const count = metrics.goroutine_count || 0
  if (count >= 5000) return 'critical'
  if (count >= 1000) return 'warning'
  return 'normal'
}

const getGoroutineStatusText = () => {
  const count = metrics.goroutine_count || 0
  if (count >= 5000) return t('admin.performance.status.critical')
  if (count >= 1000) return t('admin.performance.status.warning')
  return t('admin.performance.status.normal')
}

// 获取内存状态
const getMemoryStatus = () => {
  const alloc = metrics.memory_alloc || 0
  if (alloc >= 1000) return 'critical'
  if (alloc >= 500) return 'warning'
  return 'normal'
}

const getMemoryStatusText = () => {
  const alloc = metrics.memory_alloc || 0
  if (alloc >= 1000) return t('admin.performance.status.critical')
  if (alloc >= 500) return t('admin.performance.status.warning')
  return t('admin.performance.status.normal')
}

// 获取数据库状态
const getDBStatus = () => {
  if (!dbStats.max_open_connections) return 'normal'
  const utilization = (dbStats.in_use / dbStats.max_open_connections) * 100
  if (utilization >= 95) return 'critical'
  if (utilization >= 80) return 'warning'
  return 'normal'
}

const getDBUtilization = () => {
  if (!dbStats.max_open_connections) return 0
  return ((dbStats.in_use / dbStats.max_open_connections) * 100).toFixed(1)
}

// 获取SSH连接池利用率类型
const getSSHPoolUtilizationType = (utilization) => {
  if (utilization >= 90) return 'danger'
  if (utilization >= 70) return 'warning'
  return 'success'
}

// 自动刷新
const toggleAutoRefresh = (value) => {
  if (value) {
    startAutoRefresh()
  } else {
    stopAutoRefresh()
  }
}

const startAutoRefresh = () => {
  stopAutoRefresh()
  refreshTimer = setInterval(() => {
    fetchMetrics()
  }, 5000) // 每5秒刷新
}

const stopAutoRefresh = () => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
}

// 初始化图表
const initCharts = () => {
  // 检查DOM元素是否存在
  if (!memoryChartRef.value || !gcChartRef.value || !goroutineChartRef.value) {
    console.warn('图表DOM元素未就绪')
    return
  }
  
  try {
    memoryChart = echarts.init(memoryChartRef.value)
    gcChart = echarts.init(gcChartRef.value)
    goroutineChart = echarts.init(goroutineChartRef.value)
    
    // 保存resize处理函数引用，以便后续移除
    resizeHandler = () => {
      memoryChart?.resize()
      gcChart?.resize()
      goroutineChart?.resize()
    }
    window.addEventListener('resize', resizeHandler)
  } catch (error) {
    console.error('图表初始化失败:', error)
  }
}

// 生命周期
onMounted(async () => {
  fetchMetrics()
  fetchHistory()
  
  // 等待DOM渲染完成后再初始化图表
  await nextTick()
  initCharts()
  
  if (autoRefresh.value) {
    startAutoRefresh()
  }
})

onUnmounted(() => {
  // 停止自动刷新
  stopAutoRefresh()
  
  // 移除resize事件监听器
  if (resizeHandler) {
    window.removeEventListener('resize', resizeHandler)
    resizeHandler = null
  }
  
  // 销毁图表实例
  try {
    memoryChart?.dispose()
    gcChart?.dispose()
    goroutineChart?.dispose()
  } catch (error) {
    console.error('图表销毁失败:', error)
  }
  
  // 清空引用
  memoryChart = null
  gcChart = null
  goroutineChart = null
})
</script>

<script>
export default {
  name: 'PerformanceMonitor'
}
</script>

<style scoped lang="scss">
.performance-monitor {
  padding: 20px;

  .header-card {
    margin-bottom: 20px;
    
    .header-content {
      display: flex;
      justify-content: space-between;
      align-items: center;
      
      .title-section {
        h2 {
          margin: 0;
          font-size: 24px;
          font-weight: 600;
          display: flex;
          align-items: center;
          gap: 10px;
        }
        
        .subtitle {
          margin: 5px 0 0;
          color: #909399;
          font-size: 14px;
        }
      }
      
      .refresh-section {
        display: flex;
        gap: 10px;
        align-items: center;
      }
    }
  }

  .metrics-cards {
    margin-bottom: 20px;
    
    .metric-card {
      margin-bottom: 20px;
      cursor: pointer;
      transition: all 0.3s;
      
      &:hover {
        transform: translateY(-5px);
        box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
      }
      
      :deep(.el-card__body) {
        display: flex;
        align-items: center;
        padding: 20px;
      }
      
      .metric-icon {
        width: 60px;
        height: 60px;
        border-radius: 12px;
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 28px;
        color: white;
        margin-right: 15px;
        
        &.goroutine { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); }
        &.memory { background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%); }
        &.gc { background: linear-gradient(135deg, #4facfe 0%, #00f2fe 100%); }
        &.database { background: linear-gradient(135deg, #43e97b 0%, #38f9d7 100%); }
      }
      
      .metric-content {
        flex: 1;
        
        .metric-label {
          font-size: 14px;
          color: #909399;
          margin-bottom: 5px;
        }
        
        .metric-value {
          font-size: 28px;
          font-weight: 600;
          margin-bottom: 5px;
        }
        
        .metric-status {
          font-size: 12px;
          padding: 2px 8px;
          border-radius: 4px;
          display: inline-block;
          
          &.normal {
            background: #f0f9ff;
            color: #409eff;
          }
          
          &.warning {
            background: #fdf6ec;
            color: #e6a23c;
          }
          
          &.critical {
            background: #fef0f0;
            color: #f56c6c;
          }
        }
      }
    }
  }

  .detail-section {
    margin-bottom: 20px;
  }

  .chart-card {
    margin-bottom: 20px;
  }

  .card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    font-weight: 600;
  }

  .chart-container {
    height: 300px;
    margin-top: 20px;
  }

  .chart-container-large {
    height: 400px;
  }
}
</style>
