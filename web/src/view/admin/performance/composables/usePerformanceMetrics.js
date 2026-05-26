import { ref, reactive, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import * as echarts from 'echarts'
import request from '@/utils/request'

export function usePerformanceMetrics() {
  const { t } = useI18n()

  // 响应式数据
  const loading = ref(false)
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
      if ((response.code === 200) && response.data) {
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
      if ((response.code === 200) && response.data) {
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

    // 强制启动自动刷新
    startAutoRefresh()
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

  return {
    t,
    loading,
    timeRange,
    metrics,
    dbStats,
    dbManagerStats,
    sshPoolStats,
    memoryChartRef,
    gcChartRef,
    goroutineChartRef,
    fetchMetrics,
    fetchHistory,
    formatDuration,
    getGoroutineStatus,
    getGoroutineStatusText,
    getMemoryStatus,
    getMemoryStatusText,
    getDBStatus,
    getDBUtilization,
    getSSHPoolUtilizationType,
  }
}
