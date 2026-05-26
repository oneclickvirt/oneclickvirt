<template>
  <div class="stats-content">
    <!-- 流量统计 -->
    <div class="traffic-section">
      <div class="traffic-stats">
        <div class="traffic-usage">
          <div class="usage-header">
            <span class="usage-label">{{ $t('user.trafficOverview.currentMonthUsage') }}</span>
            <span class="usage-info">
              {{ formatTraffic(monitoring.trafficData?.currentMonth || 0) }} /
              {{ formatTraffic(monitoring.trafficData?.totalLimit || 102400) }}
            </span>
          </div>
          <div class="usage-details">
            <span :class="{ 'limited-text': monitoring.trafficData?.isLimited }">
              {{ monitoring.trafficData?.isLimited ? $t('user.instanceDetail.trafficOverlimit') : $t('user.instanceDetail.normalUsage') }}
            </span>
            <span class="reset-info">{{ $t('user.trafficOverview.resetOn1st') }}</span>
          </div>
        </div>

        <!-- 流量超限警告 -->
        <el-alert
          v-if="monitoring?.trafficData?.isLimited"
          :title="getTrafficLimitTitle()"
          :description="monitoring.trafficData.limitReason"
          :type="getTrafficLimitType()"
          :closable="false"
          show-icon
          style="margin: 20px 0;"
        />

        <div
          v-if="monitoring.trafficData?.history?.length"
          class="traffic-breakdown"
        >
          <h4>{{ $t('user.trafficOverview.historicalStats') }}</h4>
          <div class="history-list">
            <div
              v-for="item in monitoring.trafficData.history.slice(0, 6)"
              :key="`${item.year}-${item.month}`"
              class="history-item"
            >
              <span class="month">{{ item.year }}-{{ String(item.month).padStart(2, '0') }}</span>
              <span class="traffic">{{ formatTraffic(item.totalUsed) }}</span>
              <span class="breakdown">
                ↑{{ formatTraffic(item.trafficOut) }} ↓{{ formatTraffic(item.trafficIn) }}
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 流量历史趋势图 -->
    <TrafficHistoryChart
      type="instance"
      :resource-id="instanceId"
      :title="''"
      :auto-refresh="0"
    >
      <template #extra-actions>
        <el-button
          size="small"
          @click="$emit('refresh')"
        >
          <el-icon><Refresh /></el-icon>
          {{ $t('common.refresh') }}
        </el-button>
        <el-button
          size="small"
          type="primary"
          @click="$emit('show-traffic-detail')"
        >
          {{ $t('user.trafficOverview.viewDetailedStats') }}
        </el-button>
      </template>
    </TrafficHistoryChart>
  </div>
</template>

<script setup>
import TrafficHistoryChart from '@/components/TrafficHistoryChart.vue'
import { useInstanceFormatters } from '../composables/useInstanceFormatters'

const props = defineProps({
  instance: { type: Object, required: true },
  monitoring: { type: Object, required: true },
  instanceId: { type: String, required: true }
})

defineEmits(['refresh', 'show-traffic-detail'])

const {
  formatTraffic,
  getTrafficLimitTitle: _getTrafficLimitTitle,
  getTrafficLimitType: _getTrafficLimitType
} = useInstanceFormatters()

const getTrafficLimitTitle = () => _getTrafficLimitTitle(props.monitoring)
const getTrafficLimitType = () => _getTrafficLimitType(props.monitoring)
</script>
