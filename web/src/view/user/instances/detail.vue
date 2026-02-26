<template>
  <div class="instance-detail">
    <!-- 页面头部 -->
    <div class="page-header">
      <el-button 
        type="text" 
        class="back-btn"
        @click="$router.back()"
      >
        <el-icon><ArrowLeft /></el-icon>
        {{ t('user.instanceDetail.backToList') }}
      </el-button>
    </div>

    <!-- 实例概览卡片 -->
    <el-card class="overview-card">
      <!-- 关联任务提示 -->
      <el-alert
        v-if="instance.relatedTask"
        :title="getTaskTitle(instance.relatedTask)"
        :type="getTaskAlertType(instance.relatedTask.status)"
        :description="instance.relatedTask.statusMessage || `进度: ${instance.relatedTask.progress}%`"
        :closable="false"
        show-icon
        style="margin-bottom: 20px;"
      >
        <template #default>
          <div style="display: flex; justify-content: space-between; align-items: center;">
            <div>
              <p>{{ instance.relatedTask.statusMessage || `正在${getTaskTypeText(instance.relatedTask.taskType)}...` }}</p>
              <el-progress 
                :percentage="instance.relatedTask.progress" 
                :status="instance.relatedTask.progress === 100 ? 'success' : undefined"
                style="margin-top: 10px;"
              />
            </div>
            <el-button 
              type="primary" 
              size="small"
              @click="viewTaskDetail(instance.relatedTask.id)"
            >
              查看任务详情
            </el-button>
          </div>
        </template>
      </el-alert>
      
      <!-- Provider离线警告 -->
      <el-alert
        v-if="instance.providerStatus && (instance.providerStatus === 'inactive' || instance.providerStatus === 'partial')"
        :title="t('user.instanceDetail.providerOfflineWarning')"
        type="error"
        :description="t('user.instanceDetail.providerOfflineDesc')"
        :closable="false"
        show-icon
        style="margin-bottom: 20px;"
      />
      
      <!-- 实例不可用警告 -->
      <el-alert
        v-if="instance.status === 'unavailable'"
        :title="t('user.instanceDetail.instanceUnavailableWarning')"
        type="warning"
        :description="t('user.instanceDetail.instanceUnavailableDesc')"
        :closable="false"
        show-icon
        style="margin-bottom: 20px;"
      />
      
      <div class="server-overview">
        <!-- 左侧：实例基本信息 -->
        <div class="server-basic-info">
          <div class="server-header">
            <div class="server-name-section">
              <h1 class="server-name">
                {{ instance.name }}
              </h1>
              <div class="server-meta">
                <el-tag
                  :type="instance.instance_type === 'vm' ? 'primary' : 'success'"
                  size="small"
                >
                  {{ instance.instance_type === 'vm' ? t('user.instanceDetail.vm') : t('user.instanceDetail.container') }}
                </el-tag>
                <el-tag 
                  v-if="instance.providerType"
                  :type="getProviderTypeColor(instance.providerType)"
                  size="small"
                  style="margin-left: 8px;"
                >
                  {{ getProviderTypeName(instance.providerType) }}
                </el-tag>
                <span class="server-provider">{{ instance.providerName }}</span>
              </div>
            </div>
            <div class="server-status">
              <el-tag 
                :type="getStatusType(instance.status)"
                effect="dark"
                size="large"
              >
                {{ getStatusText(instance.status) }}
              </el-tag>
            </div>
          </div>
          
          <!-- 实例控制按钮 - 移到名称下方 -->
          <div class="control-actions">
            <el-button 
              v-if="instance.status === 'stopped'"
              type="success" 
              size="small"
              :loading="actionLoading"
              @click="performAction('start')"
            >
              <el-icon><VideoPlay /></el-icon>
              {{ t('user.instanceDetail.start') }}
            </el-button>
            <el-button 
              v-if="instance.status === 'running'"
              type="warning" 
              size="small"
              :loading="actionLoading"
              @click="performAction('stop')"
            >
              <el-icon><VideoPause /></el-icon>
              {{ t('user.instanceDetail.stop') }}
            </el-button>
            <el-button 
              v-if="instance.status === 'running' && instance.canRestart !== false"
              size="small"
              :loading="actionLoading"
              @click="performAction('restart')"
            >
              <el-icon><Refresh /></el-icon>
              {{ t('user.instanceDetail.restart') }}
            </el-button>
            <el-button 
              v-if="instanceTypePermissions.canResetInstance"
              type="info"
              size="small"
              :loading="actionLoading"
              @click="performAction('reset')"
            >
              <el-icon><Refresh /></el-icon>
              {{ t('user.instanceDetail.resetSystem') }}
            </el-button>
            <el-button 
              v-if="instance.status === 'running'"
              type="primary"
              size="small"
              :loading="actionLoading"
              @click="showResetPasswordDialog"
            >
              {{ t('user.instanceDetail.resetPassword') }}
            </el-button>
            <!-- Web SSH按钮 -->
            <el-button 
              v-if="instance.status === 'running' && instance.password"
              type="primary"
              size="small"
              @click="openSSHTerminal"
            >
              <el-icon><Monitor /></el-icon>
              {{ t('user.instanceDetail.webSSH') }}
            </el-button>
            <!-- 删除按钮 - 根据权限显示 -->
            <el-button 
              v-if="instanceTypePermissions.canDeleteInstance"
              type="danger"
              size="small"
              :loading="actionLoading"
              @click="performAction('delete')"
            >
              <el-icon><Delete /></el-icon>
              {{ t('user.instanceDetail.delete') }}
            </el-button>
          </div>
        </div>

        <!-- 右侧：硬件信息 -->
        <div class="server-hardware">
          <h3>{{ t('user.instanceDetail.hardware') }}</h3>
          <div class="hardware-grid">
            <div class="hardware-item">
              <span class="label">{{ t('user.instanceDetail.cpu') }}</span>
              <span class="value">{{ instance.cpu }}{{ t('user.instanceDetail.core') }}</span>
            </div>
            <div class="hardware-item">
              <span class="label">{{ t('user.instanceDetail.memory') }}</span>
              <span class="value">{{ formatMemorySize(instance.memory) }}</span>
            </div>
            <div class="hardware-item">
              <span class="label">{{ t('user.instanceDetail.storage') }}</span>
              <span class="value">{{ formatDiskSize(instance.disk) }}</span>
            </div>
            <div class="hardware-item">
              <span class="label">{{ t('user.instanceDetail.bandwidth') }}</span>
              <span class="value">{{ instance.bandwidth }}Mbps</span>
            </div>
          </div>
        </div>
      </div>
    </el-card>

    <!-- 标签页内容 -->
    <el-card class="tabs-card">
      <el-tabs
        v-model="activeTab"
        type="border-card"
      >
        <!-- 概览标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.overview')"
          name="overview"
        >
          <div class="overview-content">
            <!-- SSH连接信息 -->
            <div class="connection-section">
              <h3>{{ t('user.instanceDetail.sshConnection') }}</h3>
              <div class="connection-grid">
                <div class="connection-item">
                  <span class="label">{{ t('user.instanceDetail.publicIPv4') }}</span>
                  <div class="value-with-action">
                    <span 
                      class="value ip-value" 
                      :title="instance.publicIP || t('user.instanceDetail.none')"
                    >
                      {{ truncateIP(instance.publicIP) || t('user.instanceDetail.none') }}
                    </span>
                    <el-button 
                      v-if="instance.publicIP"
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.publicIP)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div 
                  v-if="instance.privateIP"
                  class="connection-item"
                >
                  <span class="label">{{ t('user.instanceDetail.privateIPv4') }}</span>
                  <div class="value-with-action">
                    <span 
                      class="value ip-value" 
                      :title="instance.privateIP"
                    >
                      {{ truncateIP(instance.privateIP) }}
                    </span>
                    <el-button 
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.privateIP)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div 
                  v-if="instance.ipv6Address"
                  class="connection-item"
                >
                  <span class="label">{{ t('user.instanceDetail.ipv6') }}</span>
                  <div class="value-with-action">
                    <span 
                      class="value ip-value" 
                      :title="instance.ipv6Address"
                    >
                      {{ truncateIP(instance.ipv6Address) }}
                    </span>
                    <el-button 
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.ipv6Address)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div 
                  v-if="instance.publicIPv6"
                  class="connection-item"
                >
                  <span class="label">{{ t('user.instanceDetail.ipv6') }}</span>
                  <div class="value-with-action">
                    <span 
                      class="value ip-value" 
                      :title="instance.publicIPv6"
                    >
                      {{ truncateIP(instance.publicIPv6) }}
                    </span>
                    <el-button 
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.publicIPv6)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div class="connection-item">
                  <span class="label">{{ t('user.instanceDetail.sshPort') }}</span>
                  <div class="value-with-action">
                    <span class="value">{{ instance.sshPort || 22 }}</span>
                    <el-button 
                      v-if="instance.sshPort"
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.sshPort.toString())"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div class="connection-item">
                  <span class="label">{{ t('user.instanceDetail.username') }}</span>
                  <div class="value-with-action">
                    <span class="value">{{ instance.username || 'root' }}</span>
                    <el-button 
                      v-if="instance.username"
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.username)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
                <div
                  v-if="instance.password"
                  class="connection-item"
                >
                  <span class="label">{{ t('user.instanceDetail.password') }}</span>
                  <div class="value-with-action">
                    <span class="value">{{ showPassword ? instance.password : '••••••••' }}</span>
                    <el-button 
                      size="small" 
                      text 
                      @click="togglePassword"
                    >
                      {{ showPassword ? t('user.instanceDetail.hide') : t('user.instanceDetail.show') }}
                    </el-button>
                    <el-button 
                      size="small" 
                      text 
                      @click="copyToClipboard(instance.password)"
                    >
                      {{ t('user.instanceDetail.copy') }}
                    </el-button>
                  </div>
                </div>
              </div>
            </div>

            <!-- 基本信息 -->
            <div class="basic-info-section">
              <h3>{{ t('user.instanceDetail.basicInfo') }}</h3>
              <div class="info-grid">
                <div class="info-item">
                  <span class="label">{{ t('user.instanceDetail.os') }}</span>
                  <span class="value">{{ instance.osType }}</span>
                </div>
                <div class="info-item">
                  <span class="label">{{ t('user.instanceDetail.createdAt') }}</span>
                  <span class="value">{{ formatDate(instance.createdAt) }}</span>
                </div>
                <div class="info-item">
                  <span class="label">{{ t('user.instanceDetail.expiredAt') }}</span>
                  <span class="value">{{ formatDate(instance.expiresAt) }}</span>
                </div>
                <div
                  v-if="instance.networkType || instance.ipv4MappingType"
                  class="info-item"
                >
                  <span class="label">{{ t('user.instanceDetail.networkType') }}</span>
                  <el-tag
                    size="small"
                    :type="getNetworkTypeTagType(instance.networkType || getNetworkTypeFromLegacy(instance.ipv4MappingType, instance.ipv6Address))"
                  >
                    {{ getNetworkTypeDisplayName(instance.networkType || getNetworkTypeFromLegacy(instance.ipv4MappingType, instance.ipv6Address)) }}
                  </el-tag>
                </div>
                <!-- 保留旧字段显示以兼容性 -->
                <div
                  v-if="instance.ipv4MappingType && !instance.networkType"
                  class="info-item"
                  style="display: none"
                >
                  <span class="label">IPv4映射类型（兼容）</span>
                  <el-tag
                    size="small"
                    :type="instance.ipv4MappingType === 'dedicated' ? 'success' : 'primary'"
                  >
                    {{ instance.ipv4MappingType === 'dedicated' ? '独立IPv4地址' : 'NAT共享IP' }}
                  </el-tag>
                </div>
              </div>
            </div>
          </div>
        </el-tab-pane>

        <!-- 端口映射标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.portMapping')"
          name="ports"
        >
          <div class="ports-content">
            <div class="ports-header">
              <div class="ports-summary">
                <div class="summary-item">
                  <span class="label">{{ t('user.instanceDetail.publicIP') }}:</span>
                  <span class="value">{{ instance.publicIP || t('user.instanceDetail.none') }}</span>
                </div>
                <div class="summary-item">
                  <span class="label">{{ t('user.instances.portMapping') }}:</span>
                  <span class="value">{{ portMappings.length }}个</span>
                </div>
              </div>
              <el-button
                type="primary"
                size="small"
                @click="refreshPortMappings"
              >
                <el-icon><Refresh /></el-icon>
                {{ t('user.instances.search') }}
              </el-button>
            </div>
            
            <el-table
              v-if="portMappings && portMappings.length > 0"
              :data="portMappings"
              stripe
              class="ports-table"
            >
              <el-table-column
                prop="portType"
                :label="t('user.instanceDetail.portType')"
                width="110"
              >
                <template #default="{ row }">
                  <el-tag
                    size="small"
                    :type="row.portType === 'manual' ? 'warning' : 'success'"
                  >
                    {{ row.portType === 'manual' ? t('user.instanceDetail.manualAdd') : t('user.instanceDetail.rangeMapping') }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column
                prop="hostPort"
                :label="t('user.instanceDetail.publicPort')"
                width="110"
              />
              <el-table-column
                prop="guestPort"
                :label="t('user.instanceDetail.internalPort')"
                width="110"
              />
              <el-table-column
                prop="protocol"
                :label="t('user.instanceDetail.protocol')"
                width="90"
              >
                <template #default="{ row }">
                  <el-tag
                    size="small"
                    :type="row.protocol === 'tcp' ? 'primary' : row.protocol === 'udp' ? 'success' : 'info'"
                  >
                    {{ row.protocol === 'both' ? 'TCP/UDP' : row.protocol.toUpperCase() }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column
                prop="status"
                :label="t('user.instanceDetail.status')"
                width="100"
              >
                <template #default="{ row }">
                  <el-tag
                    size="small"
                    :type="row.status === 'active' ? 'success' : 'info'"
                  >
                    {{ row.status === 'active' ? t('user.instanceDetail.active') : t('user.instanceDetail.unused') }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column
                :label="t('user.instanceDetail.connectionInfo')"
                min-width="300"
              >
                <template #default="{ row }">
                  <div class="connection-commands">
                    <div
                      v-if="row.isSSH"
                      class="ssh-command"
                    >
                      <span 
                        class="command-text" 
                        :title="`ssh ${instance.username || 'root'}@${instance.publicIP} -p ${row.hostPort}`"
                      >
                        {{ formatSSHCommand(instance.username, instance.publicIP, row.hostPort) }}
                      </span>
                      <el-button 
                        size="small" 
                        text 
                        @click="copyToClipboard(`ssh ${instance.username || 'root'}@${instance.publicIP} -p ${row.hostPort}`)"
                      >
                        {{ t('user.instanceDetail.copy') }}
                      </el-button>
                    </div>
                    <div
                      v-else
                      class="port-access"
                    >
                      <span 
                        class="command-text" 
                        :title="`${instance.publicIP}:${row.hostPort}`"
                      >
                        {{ formatIPPort(instance.publicIP, row.hostPort) }}
                      </span>
                      <el-button 
                        size="small" 
                        text 
                        @click="copyToClipboard(`${instance.publicIP}:${row.hostPort}`)"
                      >
                        {{ t('user.instanceDetail.copy') }}
                      </el-button>
                    </div>
                  </div>
                </template>
              </el-table-column>
            </el-table>
            
            <div 
              v-else
              class="no-ports"
            >
              <p>{{ t('user.instances.portMapping') }}</p>
            </div>
          </div>
        </el-tab-pane>

        <!-- 统计标签页 -->
        <el-tab-pane
          :label="t('user.instanceDetail.statistics')"
          name="stats"
        >
          <div class="stats-content">
            <!-- 流量统计 -->
            <div class="traffic-section">
              <div class="traffic-stats">
                <div class="traffic-usage">
                  <div class="usage-header">
                    <span class="usage-label">{{ t('user.trafficOverview.currentMonthUsage') }}</span>
                    <span class="usage-info">
                      {{ formatTraffic(monitoring.trafficData?.currentMonth || 0) }} / 
                      {{ formatTraffic(monitoring.trafficData?.totalLimit || 102400) }}
                    </span>
                  </div>
                  <el-progress 
                    :percentage="monitoring.trafficData?.usagePercent || 0"
                    :color="getTrafficProgressColor(monitoring.trafficData?.usagePercent || 0)"
                    :show-text="false"
                    :stroke-width="10"
                  />
                  <div class="usage-details">
                    <span :class="{ 'limited-text': monitoring.trafficData?.isLimited }">
                      {{ monitoring.trafficData?.isLimited ? t('user.instanceDetail.trafficOverlimit') : t('user.instanceDetail.normalUsage') }}
                    </span>
                    <span class="reset-info">{{ t('user.trafficOverview.resetOn1st') }}</span>
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
                  <h4>{{ t('user.trafficOverview.historicalStats') }}</h4>
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
              ref="trafficChartRef"
              type="instance"
              :resource-id="route.params.id"
              :title="''"
              :auto-refresh="0"
            >
              <template #extra-actions>
                <el-button
                  size="small"
                  @click="refreshMonitoring"
                >
                  <el-icon><Refresh /></el-icon>
                  {{ t('common.refresh') }}
                </el-button>
                <el-button
                  size="small"
                  type="primary"
                  @click="showTrafficDetail = true"
                >
                  {{ t('user.trafficOverview.viewDetailedStats') }}
                </el-button>
              </template>
            </TrafficHistoryChart>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- PMAcct 流量详情对话框 -->
    <InstanceTrafficDetail
      v-model="showTrafficDetail"
      :instance-id="route.params.id"
      :instance-name="instance.name"
    />
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { formatDiskSize, formatMemorySize } from '@/utils/unit-formatter'
import InstanceTrafficDetail from '@/components/InstanceTrafficDetail.vue'
import TrafficHistoryChart from '@/components/TrafficHistoryChart.vue'
import {
  ArrowLeft,
  VideoPlay,
  VideoPause,
  Refresh,
  Delete,
  Monitor
} from '@element-plus/icons-vue'
import { useInstanceDetail } from './composables/useInstanceDetail'
import { useInstanceActions } from './composables/useInstanceActions'
import { useInstanceFormatters } from './composables/useInstanceFormatters'

const route = useRoute()
const router = useRouter()
const activeTab = ref('overview')

const {
  loading,
  portMappings,
  trafficChartRef,
  instanceTypePermissions,
  instance,
  monitoring,
  updateInstancePermissions,
  loadInstanceDetail,
  refreshPortMappings,
  refreshMonitoring,
  loadInstanceTypePermissions
} = useInstanceDetail()

const {
  actionLoading,
  showPassword,
  showTrafficDetail,
  viewTaskDetail,
  performAction,
  openSSHTerminal,
  showResetPasswordDialog,
  togglePassword,
  truncateIP,
  formatSSHCommand,
  formatIPPort,
  copyToClipboard
} = useInstanceActions(instance, monitoring, loadInstanceDetail)

const {
  getNetworkTypeFromLegacy,
  getNetworkTypeDisplayName,
  getNetworkTypeTagType,
  getProviderTypeName,
  getProviderTypeColor,
  getTaskTitle,
  getTaskTypeText,
  getTaskAlertType,
  getStatusType,
  getStatusText,
  getTrafficProgressColor,
  formatTraffic,
  formatDate,
  getTrafficLimitTitle: _getTrafficLimitTitle,
  getTrafficLimitType: _getTrafficLimitType
} = useInstanceFormatters()

// wrap monitoring-dependent helpers
const getTrafficLimitTitle = () => _getTrafficLimitTitle(monitoring)
const getTrafficLimitType = () => _getTrafficLimitType(monitoring)

// 标志位，防止 watch 循环触发
let isUpdatingFromRoute = false

watch(() => route.params.id, async (newId, oldId) => {
  if (newId && newId !== oldId && newId !== 'undefined') {
    try {
      const [detailSuccess, permissionsSuccess] = await Promise.all([
        loadInstanceDetail(true),
        loadInstanceTypePermissions()
      ])
      if (detailSuccess && permissionsSuccess) {
        updateInstancePermissions()
        refreshMonitoring()
        refreshPortMappings()
      }
    } catch (error) {
      console.error('路由切换时加载数据失败:', error)
    }
  }
})

watch(() => route.query.tab, (newTab, oldTab) => {
  if (newTab === oldTab) return
  if (newTab && ['overview', 'ports', 'stats'].includes(newTab)) {
    if (activeTab.value === newTab) return
    isUpdatingFromRoute = true
    activeTab.value = newTab
    nextTick(() => { isUpdatingFromRoute = false })
  } else {
    if (activeTab.value !== 'overview') {
      isUpdatingFromRoute = true
      activeTab.value = 'overview'
      nextTick(() => { isUpdatingFromRoute = false })
    }
  }
}, { immediate: true })

watch(activeTab, (newTab, oldTab) => {
  if (newTab === oldTab || isUpdatingFromRoute) return
  if (newTab && route.query.tab !== newTab) {
    router.replace({ query: { ...route.query, tab: newTab } })
  }
})

let monitoringTimer = null

onMounted(async () => {
  await nextTick()
  try {
    const [detailSuccess, permissionsSuccess] = await Promise.all([
      loadInstanceDetail(true),
      loadInstanceTypePermissions()
    ])
    if (detailSuccess && permissionsSuccess) {
      updateInstancePermissions()
      refreshMonitoring()
      refreshPortMappings()
      monitoringTimer = setInterval(refreshMonitoring, 30000)
    }
  } catch (error) {
    console.error('页面初始化失败:', error)
  }
})

onUnmounted(() => {
  if (monitoringTimer) {
    clearInterval(monitoringTimer)
    monitoringTimer = null
  }
})
</script>

<style scoped>
.instance-detail {
  padding: 24px;
  max-width: 1200px;
  margin: 0 auto;
}

.page-header {
  margin-bottom: 24px;
}

.back-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-color-secondary);
  font-size: 14px;
}

/* 概览卡片样式 */
.overview-card {
  margin-bottom: 24px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.server-overview {
  display: flex;
  gap: 40px;
  align-items: flex-start;
}

.server-basic-info {
  flex: 1;
}

.server-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 20px;
}

.server-name-section {
  flex: 1;
}

.server-name {
  margin: 0 0 8px 0;
  font-size: 28px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.server-meta {
  display: flex;
  align-items: center;
  gap: 12px;
}

.server-provider {
  color: var(--text-color-secondary);
  font-size: 14px;
}

.server-status {
  flex-shrink: 0;
}

.control-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 16px;
}

.server-hardware {
  flex-shrink: 0;
  min-width: 250px;
}

.server-hardware h3 {
  margin: 0 0 16px 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.hardware-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.hardware-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 12px;
  background: var(--neutral-bg);
  border-radius: 6px;
}

.hardware-item .label {
  color: var(--text-color-secondary);
  font-size: 14px;
}

.hardware-item .value {
  color: var(--text-color-primary);
  font-weight: 600;
  font-size: 14px;
}

/* 标签页样式 */
.tabs-card {
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.tabs-card :deep(.el-tabs__header) {
  margin: 0;
}

.tabs-card :deep(.el-tabs__content) {
  padding: 24px;
}

/* 标签页切换 - 使用 GPU 加速 */
.tabs-card :deep(.el-tab-pane) {
  will-change: auto;
  transform: translateZ(0);
}

/* 概览标签页内容 */
.overview-content {
  display: grid;
  gap: 32px;
}

.connection-section h3,
.basic-info-section h3 {
  margin: 0 0 20px 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.connection-grid,
.info-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 16px;
}

.connection-item,
.info-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px;
  background: var(--neutral-bg);
  border-radius: 8px;
  border: 1px solid var(--border-color);
}

.connection-item .label,
.info-item .label {
  color: var(--text-color-secondary);
  font-weight: 500;
  font-size: 14px;
}

.value-with-action {
  display: flex;
  align-items: center;
  gap: 8px;
}

.connection-item .value,
.info-item .value {
  color: var(--text-color-primary);
  font-weight: 500;
  font-size: 14px;
}

.ip-value {
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: help;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
}

/* 端口映射标签页 */
.ports-content {
  min-height: 400px;
}

.ports-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
  padding: 16px;
  background: var(--neutral-bg);
  border-radius: 8px;
}

.ports-summary {
  display: flex;
  gap: 32px;
}

.summary-item {
  display: flex;
  align-items: center;
  gap: 8px;
}

.summary-item .label {
  font-size: 14px;
  color: var(--text-color-secondary);
  font-weight: 500;
}

.summary-item .value {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.ports-table {
  width: 100%;
}

.connection-commands {
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
}

.command-text {
  font-size: 12px;
  color: var(--text-color-primary);
  background: #f3f4f6;
  padding: 4px 8px;
  border-radius: 4px;
  margin-right: 8px;
  word-break: break-all;
  max-width: 250px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: inline-block;
  vertical-align: middle;
  cursor: help;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
}

.ssh-command, .port-access {
  display: flex;
  align-items: center;
  margin-bottom: 4px;
}

.no-ports {
  text-align: center;
  padding: 60px 20px;
  color: var(--text-color-secondary);
}

/* 统计标签页 */
.stats-content {
  display: grid;
  gap: 12px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.section-header h3,
.traffic-section h3 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.section-actions {
  display: flex;
  gap: 8px;
}

.monitoring-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 20px;
}

.monitor-item {
  text-align: center;
  padding: 20px;
  background: var(--neutral-bg);
  border-radius: 8px;
}

.monitor-label {
  color: var(--text-color-secondary);
  font-size: 14px;
  margin-bottom: 12px;
  font-weight: 500;
}

.monitor-value {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.traffic-usage {
  padding: 20px;
  background: var(--neutral-bg);
  border-radius: 8px;
  margin-bottom: 0;
}

.usage-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}

.usage-label {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.usage-info {
  font-size: 14px;
  color: var(--text-color-secondary);
}

.usage-details {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 12px;
  font-size: 14px;
}

.limited-text {
  color: #f56c6c !important;
  font-weight: 600;
}

.reset-info {
  color: var(--text-color-secondary);
}

.traffic-breakdown h4 {
  margin: 0 0 16px 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--text-color-primary);
}

.history-list {
  display: grid;
  gap: 8px;
}

.history-item {
  display: grid;
  grid-template-columns: 100px 120px 1fr;
  gap: 16px;
  padding: 12px;
  background: var(--neutral-bg);
  border-radius: 6px;
  font-size: 14px;
}

.history-item .month {
  color: var(--text-color-secondary);
  font-weight: 500;
}

.history-item .traffic {
  color: var(--text-color-primary);
  font-weight: 600;
}

.history-item .breakdown {
  color: var(--text-color-secondary);
}

/* 响应式设计 */
@media (max-width: 768px) {
  .instance-detail {
    padding: 16px;
  }
  
  .server-overview {
    flex-direction: column;
    gap: 24px;
  }
  
  .server-header {
    flex-direction: column;
    gap: 16px;
    align-items: flex-start;
  }
  
  .connection-grid,
  .info-grid {
    grid-template-columns: 1fr;
  }
  
  .ports-header {
    flex-direction: column;
    gap: 16px;
    align-items: flex-start;
  }
  
  .ports-summary {
    flex-direction: column;
    gap: 12px;
  }
  
  .monitoring-grid {
    grid-template-columns: 1fr;
  }
  
  .hardware-grid {
    grid-template-columns: 1fr;
  }
  
  .history-item {
    grid-template-columns: 1fr;
    gap: 8px;
  }

  /* 移动端IP地址显示 */
  .ip-value {
    max-width: 150px;
  }
  
  .command-text {
    max-width: 200px;
  }
  
  .connection-item {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
  
  .value-with-action {
    width: 100%;
    justify-content: space-between;
  }
}

/* SSH终端对话框样式 */
.ssh-terminal-dialog :deep(.el-dialog__header) {
  padding: 0;
  margin: 0;
  border-bottom: 1px solid #e0e0e0;
}

.ssh-dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 20px;
  background-color: var(--card-bg-solid);
}

.ssh-dialog-title {
  color: #000000;
  font-size: 15px;
  font-weight: 600;
}

.ssh-dialog-actions {
  display: flex;
  gap: 10px;
}

.ssh-dialog-actions .el-button {
  background-color: var(--card-bg-solid);
  color: #000000;
  border: 1px solid #d0d0d0;
  font-weight: 500;
}

.ssh-dialog-actions .el-button:hover {
  background-color: #f5f5f5;
  border-color: #b0b0b0;
}

.ssh-dialog-content {
  height: 600px;
  background-color: #1e1e1e;
  border-radius: 4px;
  overflow: hidden;
}

/* 最小化SSH终端样式 - 右下角悬浮（使用Teleport到body） */
.ssh-minimized-container {
  position: fixed;
  bottom: 20px;
  right: 20px;
  z-index: 9999;
  background-color: var(--card-bg-solid);
  border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15);
  cursor: pointer;
  transition: all 0.3s ease;
  border: 1px solid #e0e0e0;
}

.ssh-minimized-container:hover {
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.2);
  transform: translateY(-2px);
}

.ssh-minimized-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  color: #000000;
  font-size: 14px;
  font-weight: 600;
  min-width: 280px;
  background-color: var(--card-bg-solid);
  border-radius: 8px;
}

.ssh-minimized-header span {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-right: 10px;
}

.ssh-minimized-header .close-btn {
  color: #666666;
  padding: 4px;
}

.ssh-minimized-header .close-btn:hover {
  color: #000000;
  background-color: #f0f0f0;
}

:deep(.el-dialog__body) {
  padding: 0;
}

:deep(.el-dialog) {
  border-radius: 8px;
}

</style>
