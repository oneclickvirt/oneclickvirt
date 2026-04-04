<template>
  <div class="resource-monitor-chart">
    <el-card>
      <template #header>
        <div class="chart-header">
          <span>{{ $t('user.instanceDetail.resourceMonitoring') }}</span>
          <div class="chart-controls">
            <el-button size="small" :loading="loading" @click="loadData">
              <el-icon><Refresh /></el-icon>
              {{ $t('common.refresh') }}
            </el-button>
          </div>
        </div>
      </template>

      <div v-show="loading" v-loading="true" style="height: 300px;" />

      <div v-show="!loading" class="charts-container">
        <!-- CPU 使用率 -->
        <div class="chart-item">
          <h4>{{ $t('user.instanceDetail.cpuUsage') }}</h4>
          <div ref="cpuChartRef" class="chart-canvas" />
        </div>

        <!-- 内存使用率 -->
        <div class="chart-item">
          <h4>{{ $t('user.instanceDetail.memoryUsage') }}</h4>
          <div ref="memChartRef" class="chart-canvas" />
        </div>

        <!-- 磁盘使用率 -->
        <div class="chart-item">
          <h4>{{ $t('user.instanceDetail.diskUsage') }}</h4>
          <div ref="diskChartRef" class="chart-canvas" />
        </div>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Refresh } from '@element-plus/icons-vue'
import * as echarts from 'echarts'
import { getInstanceResourceMonitoring } from '@/api/user'

const { t } = useI18n()

const props = defineProps({
  instanceId: { type: [String, Number], required: true },
  autoRefresh: { type: Number, default: 300000 }
})

const loading = ref(false)
const hasData = ref(false)
const cpuChartRef = ref(null)
const memChartRef = ref(null)
const diskChartRef = ref(null)

let cpuChart = null
let memChart = null
let diskChart = null
let refreshTimer = null

const formatTime = (ts) => {
  const d = new Date(ts)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

const formatBytes = (bytes) => {
  if (!bytes || bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const k = 1024
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return (bytes / Math.pow(k, i)).toFixed(1) + ' ' + units[i]
}

const buildLineOption = (times, series, yFormatter, yMax) => {
  return {
    tooltip: {
      trigger: 'axis',
      formatter: (params) => {
        let result = params[0].axisValue + '<br/>'
        params.forEach(p => {
          result += `${p.marker} ${p.seriesName}: ${yFormatter(p.value)}<br/>`
        })
        return result
      }
    },
    legend: { data: series.map(s => s.name), bottom: 0 },
    grid: { top: 10, right: 20, bottom: 50, left: 60 },
    xAxis: {
      type: 'category',
      data: times,
      axisLabel: { fontSize: 10 }
    },
    yAxis: {
      type: 'value',
      max: yMax || undefined,
      axisLabel: { formatter: (v) => yFormatter(v) }
    },
    series: series.map(s => ({
      name: s.name,
      type: 'line',
      smooth: true,
      symbol: 'none',
      data: s.data,
      areaStyle: { opacity: 0.15 },
      lineStyle: { width: 2 }
    }))
  }
}

const renderCharts = (data) => {
  const times = data.map(d => formatTime(d.timestamp))
  const cpuData = data.map(d => d.cpu_percent || 0)
  const memUsed = data.map(d => d.memory_used || 0)
  const memTotal = data.map(d => d.memory_total || 0)
  const memPercent = data.map(d => d.memory_total ? ((d.memory_used / d.memory_total) * 100).toFixed(1) : 0)
  const diskUsed = data.map(d => d.disk_used || 0)
  const diskTotal = data.map(d => d.disk_total || 0)
  const diskPercent = data.map(d => d.disk_total ? ((d.disk_used / d.disk_total) * 100).toFixed(1) : 0)

  // CPU chart
  if (cpuChartRef.value) {
    if (!cpuChart || cpuChart.isDisposed()) cpuChart = echarts.init(cpuChartRef.value)
    cpuChart.setOption(buildLineOption(times,
      [{ name: 'CPU %', data: cpuData }],
      (v) => v.toFixed(1) + '%',
      100
    ))
  }

  // Memory chart
  if (memChartRef.value) {
    if (!memChart || memChart.isDisposed()) memChart = echarts.init(memChartRef.value)
    const maxMem = memTotal.length > 0 ? Math.max(...memTotal) : 0
    memChart.setOption(buildLineOption(times,
      [
        { name: t('user.instanceDetail.memUsed'), data: memUsed },
        { name: t('user.instanceDetail.memPercent'), data: memPercent }
      ],
      (v) => {
        if (v > 1000) return formatBytes(v)
        return v + '%'
      }
    ))
  }

  // Disk chart
  if (diskChartRef.value) {
    if (!diskChart || diskChart.isDisposed()) diskChart = echarts.init(diskChartRef.value)
    diskChart.setOption(buildLineOption(times,
      [
        { name: t('user.instanceDetail.diskUsed'), data: diskUsed },
        { name: t('user.instanceDetail.diskPercent'), data: diskPercent }
      ],
      (v) => {
        if (v > 1000) return formatBytes(v)
        return v + '%'
      }
    ))
  }
}

// 生成零填充资源监控数据（当无数据时显示全零曲线图）
const generateZeroFilledResourceData = () => {
  const now = new Date()
  const points = []
  // 生成24小时数据，每30分钟一个点 = 48个点
  for (let i = 48; i >= 0; i--) {
    const time = new Date(now.getTime() - i * 30 * 60 * 1000)
    points.push({
      timestamp: time.toISOString(),
      cpu_percent: 0,
      memory_used: 0,
      memory_total: 0,
      disk_used: 0,
      disk_total: 0
    })
  }
  return points
}

const loadData = async () => {
  if (!props.instanceId) return
  loading.value = true
  try {
    const res = await getInstanceResourceMonitoring(props.instanceId, { hours: 24 })
    if (res.code === 0 || res.code === 200) {
      let data = Array.isArray(res.data) ? res.data : (res.data?.metrics || [])
      if (data.length === 0) {
        data = generateZeroFilledResourceData()
      }
      hasData.value = true
      loading.value = false
      await nextTick()
      renderCharts(data)
    } else {
      hasData.value = true
      loading.value = false
      await nextTick()
      renderCharts(generateZeroFilledResourceData())
    }
  } catch (e) {
    console.error('Failed to load resource monitoring data:', e)
    hasData.value = true
    loading.value = false
    await nextTick()
    renderCharts(generateZeroFilledResourceData())
  }
}

const handleResize = () => {
  cpuChart?.resize()
  memChart?.resize()
  diskChart?.resize()
}

let resizeObserver = null

onMounted(() => {
  loadData()
  window.addEventListener('resize', handleResize)
  if (props.autoRefresh > 0) {
    refreshTimer = setInterval(loadData, props.autoRefresh)
  }
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  if (resizeObserver) resizeObserver.disconnect()
  cpuChart?.dispose()
  memChart?.dispose()
  diskChart?.dispose()
  if (refreshTimer) clearInterval(refreshTimer)
})

watch(() => props.instanceId, () => {
  loadData()
})

// Watch for cpuChartRef to appear in DOM and attach ResizeObserver
// so charts resize correctly when a hidden tab becomes visible
watch(cpuChartRef, (el) => {
  if (el && !resizeObserver) {
    resizeObserver = new ResizeObserver(() => {
      handleResize()
    })
    resizeObserver.observe(el)
  }
})

defineExpose({ refresh: loadData })
</script>

<style scoped>
.chart-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.charts-container {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.chart-item h4 {
  margin: 0 0 8px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.chart-canvas {
  width: 100%;
  height: 240px;
}

.no-data {
  padding: 40px 0;
}
</style>
