<template>
  <div class="performance-monitor">
    <el-card
      class="header-card"
      shadow="never"
    >
      <div class="header-content">
        <div class="title-section">
          <h2>
            <el-icon><Monitor /></el-icon>
            {{ $t('admin.performance.title') }}
          </h2>
          <p class="subtitle">
            {{ $t('admin.performance.subtitle') }}
          </p>
        </div>
      </div>
    </el-card>

    <!-- 关键指标卡片 -->
    <el-row
      :gutter="20"
      class="metrics-cards"
    >
      <el-col
        :xs="24"
        :sm="12"
        :md="6"
      >
        <el-card
          shadow="hover"
          class="metric-card"
        >
          <div class="metric-icon goroutine">
            <el-icon><Connection /></el-icon>
          </div>
          <div class="metric-content">
            <div class="metric-label">
              {{ $t('admin.performance.goroutineCount') }}
            </div>
            <div class="metric-value">
              {{ metrics.goroutine_count || 0 }}
            </div>
            <div :class="['metric-status', getGoroutineStatus()]">
              {{ getGoroutineStatusText() }}
            </div>
          </div>
        </el-card>
      </el-col>

      <el-col
        :xs="24"
        :sm="12"
        :md="6"
      >
        <el-card
          shadow="hover"
          class="metric-card"
        >
          <div class="metric-icon memory">
            <el-icon><Memo /></el-icon>
          </div>
          <div class="metric-content">
            <div class="metric-label">
              {{ $t('admin.performance.memoryUsage') }}
            </div>
            <div class="metric-value">
              {{ metrics.memory_alloc || 0 }} MB
            </div>
            <div :class="['metric-status', getMemoryStatus()]">
              {{ getMemoryStatusText() }}
            </div>
          </div>
        </el-card>
      </el-col>

      <el-col
        :xs="24"
        :sm="12"
        :md="6"
      >
        <el-card
          shadow="hover"
          class="metric-card"
        >
          <div class="metric-icon gc">
            <el-icon><DeleteFilled /></el-icon>
          </div>
          <div class="metric-content">
            <div class="metric-label">
              {{ $t('admin.performance.gcCount') }}
            </div>
            <div class="metric-value">
              {{ metrics.gc_count || 0 }}
            </div>
            <div class="metric-status normal">
              {{ $t('admin.performance.averagePause') }}: {{ formatDuration(metrics.gc_pause_avg) }}
            </div>
          </div>
        </el-card>
      </el-col>

      <el-col
        :xs="24"
        :sm="12"
        :md="6"
      >
        <el-card
          shadow="hover"
          class="metric-card"
        >
          <div class="metric-icon database">
            <el-icon><Coin /></el-icon>
          </div>
          <div class="metric-content">
            <div class="metric-label">
              {{ $t('admin.performance.databaseConnections') }}
            </div>
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
    <el-row
      :gutter="20"
      class="detail-section"
    >
      <!-- 内存详情 -->
      <el-col
        :xs="24"
        :md="12"
      >
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>{{ $t('admin.performance.memoryDetails') }}</span>
              <el-tag
                type="info"
                size="small"
              >
                {{ $t('admin.performance.unit') }}: MB
              </el-tag>
            </div>
          </template>
          <el-descriptions
            :column="2"
            border
          >
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
          <div
            ref="memoryChartRef"
            class="chart-container"
          />
        </el-card>
      </el-col>

      <!-- GC 详情 -->
      <el-col
        :xs="24"
        :md="12"
      >
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>{{ $t('admin.performance.gcDetails') }}</span>
            </div>
          </template>
          <el-descriptions
            :column="2"
            border
          >
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
          <div
            ref="gcChartRef"
            class="chart-container"
          />
        </el-card>
      </el-col>
    </el-row>

    <!-- 数据库和连接池 -->
    <el-row
      :gutter="20"
      class="detail-section"
    >
      <el-col
        :xs="24"
        :md="12"
      >
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>{{ $t('admin.performance.databasePool') }}</span>
              <el-tag
                v-if="dbManagerStats && dbManagerStats.connected" 
                :type="dbManagerStats.reconnecting ? 'warning' : 'success'" 
                size="small"
              >
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
            style="margin-bottom: 16px"
          >
            <template #default>
              <div style="font-size: 12px; line-height: 1.8;">
                <div>{{ $t('admin.performance.heartbeatActive') }}: {{ dbManagerStats.heartbeat_active ? $t('common.yes') : $t('common.no') }}</div>
                <div>{{ $t('admin.performance.maxReconnectRetry') }}: {{ dbManagerStats.max_reconnect_retry }}</div>
                <div>{{ $t('admin.performance.reconnectInterval') }}: {{ dbManagerStats.reconnect_interval }}</div>
              </div>
            </template>
          </el-alert>

          <el-descriptions
            v-if="dbStats"
            :column="2"
            border
          >
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
          <el-empty
            v-else
            :description="$t('admin.performance.noData')"
          />
        </el-card>
      </el-col>

      <el-col
        :xs="24"
        :md="12"
      >
        <el-card shadow="hover">
          <template #header>
            <div class="card-header">
              <span>{{ $t('admin.performance.sshPool') }}</span>
              <el-tag
                v-if="sshPoolStats && sshPoolStats.utilization !== undefined" 
                :type="getSSHPoolUtilizationType(sshPoolStats.utilization)" 
                size="small"
              >
                {{ $t('admin.performance.utilization') }}: {{ sshPoolStats.utilization?.toFixed(1) || 0 }}%
              </el-tag>
            </div>
          </template>
          <el-descriptions
            v-if="sshPoolStats && sshPoolStats.total_connections !== undefined"
            :column="2"
            border
          >
            <el-descriptions-item :label="$t('admin.performance.totalConnections')">
              {{ sshPoolStats.total_connections || 0 }} / {{ sshPoolStats.max_connections || 0 }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.healthyConnections')">
              <el-tag
                :type="sshPoolStats.healthy_connections === sshPoolStats.total_connections ? 'success' : 'warning'"
                size="small"
              >
                {{ sshPoolStats.healthy_connections || 0 }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.unhealthyConnections')">
              <el-tag
                :type="sshPoolStats.unhealthy_connections > 0 ? 'danger' : 'info'"
                size="small"
              >
                {{ sshPoolStats.unhealthy_connections || 0 }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.activeConnections')">
              <el-tag
                type="success"
                size="small"
              >
                {{ sshPoolStats.active_connections || 0 }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('admin.performance.idleConnections')">
              <el-tag
                type="info"
                size="small"
              >
                {{ sshPoolStats.idle_connections || 0 }}
              </el-tag>
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
          <el-empty
            v-else
            :description="$t('admin.performance.noSSHData')"
          />
        </el-card>
      </el-col>
    </el-row>

    <!-- Goroutine 趋势图 -->
    <el-card
      shadow="hover"
      class="chart-card"
    >
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.performance.goroutineTrend') }}</span>
          <el-radio-group
            v-model="timeRange"
            size="small"
            @change="fetchHistory"
          >
            <el-radio-button label="5m">
              {{ $t('admin.performance.timeRange.5m') }}
            </el-radio-button>
            <el-radio-button label="15m">
              {{ $t('admin.performance.timeRange.15m') }}
            </el-radio-button>
            <el-radio-button label="1h">
              {{ $t('admin.performance.timeRange.1h') }}
            </el-radio-button>
            <el-radio-button label="6h">
              {{ $t('admin.performance.timeRange.6h') }}
            </el-radio-button>
          </el-radio-group>
        </div>
      </template>
      <div
        ref="goroutineChartRef"
        class="chart-container-large"
      />
    </el-card>
  </div>
</template>

<script setup>
import { 
  Monitor, 
  Refresh, 
  Connection, 
  Memo, 
  DeleteFilled,
  Coin 
} from '@element-plus/icons-vue'
import { usePerformanceMetrics } from './composables/usePerformanceMetrics'

const {
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
} = usePerformanceMetrics()
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
          color: var(--text-color-secondary);
          font-size: 14px;
        }
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
        
        &.goroutine { background: linear-gradient(135deg, #16a34a 0%, #22c55e 100%); }
        &.memory { background: linear-gradient(135deg, #059669 0%, #10b981 100%); }
        &.gc { background: linear-gradient(135deg, #0f766e 0%, #14b8a6 100%); }
        &.database { background: linear-gradient(135deg, #65a30d 0%, #84cc16 100%); }
      }
      
      .metric-content {
        flex: 1;
        
        .metric-label {
          font-size: 14px;
          color: var(--text-color-secondary);
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
            background: var(--success-bg);
            color: #16a34a;
          }
          
          &.warning {
            background: var(--warning-bg);
            color: #e6a23c;
          }
          
          &.critical {
            background: var(--error-bg);
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
