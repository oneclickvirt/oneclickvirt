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
              <h1 class="server-name">{{ instance.name }}</h1>
              <div class="server-meta">
                <el-tag :type="instance.instance_type === 'vm' ? 'primary' : 'success'" size="small">
                  {{ instance.instance_type === 'vm' ? t('user.instanceDetail.vm') : t('user.instanceDetail.container') }}
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
      <el-tabs v-model="activeTab" type="border-card">
        <!-- 概览标签页 -->
        <el-tab-pane :label="t('user.instanceDetail.overview')" name="overview">
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
                  <span class="label">{{ t('user.instanceDetail.publicIPv4') }}</span>
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
                  <span class="value">{{ formatDate(instance.expiredAt) }}</span>
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
        <el-tab-pane :label="t('user.instanceDetail.portMapping')" name="ports">
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
        <el-tab-pane :label="t('user.instanceDetail.statistics')" name="stats">
          <div class="stats-content">


            <!-- 流量统计 -->
            <div class="traffic-section">
              <div class="section-header">
                <h3>{{ t('user.trafficOverview.trafficStats') }}</h3>
                <div class="section-actions">
                  <el-button
                    size="small"
                    @click="refreshMonitoring"
                  >
                    <el-icon><Refresh /></el-icon>
                    {{ t('user.instances.search') }}
                  </el-button>
                  <el-button
                    size="small"
                    type="primary"
                    @click="showTrafficDetail = true"
                  >
                    {{ t('user.trafficOverview.viewDetailedStats') }}
                  </el-button>
                </div>
              </div>
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
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- SSH终端对话框 -->
    <el-dialog
      v-model="sshDialogVisible"
      :title="t('user.instanceDetail.sshTerminal')"
      width="80%"
      :before-close="handleCloseSSH"
      destroy-on-close
    >
      <div class="ssh-dialog-content">
        <SSHTerminal
          v-if="sshDialogVisible"
          :instance-id="instance.id"
          @close="handleCloseSSH"
          @error="handleSSHError"
        />
      </div>
    </el-dialog>

    <!-- vnStat 流量详情对话框 -->
    <InstanceTrafficDetail
      v-model="showTrafficDetail"
      :instance-id="route.params.id"
      :instance-name="instance.name"
    />
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { 
  ArrowLeft, 
  VideoPlay, 
  VideoPause, 
  Refresh, 
  Delete,
  Monitor
} from '@element-plus/icons-vue'
import { 
  getUserInstanceDetail, 
  performInstanceAction,
  getInstanceMonitoring,
  getUserInstancePorts,
  getUserInstanceTypePermissions,
  resetInstancePassword
} from '@/api/user'
import { formatDiskSize, formatMemorySize } from '@/utils/unit-formatter'
import InstanceTrafficDetail from '@/components/InstanceTrafficDetail.vue'
import SSHTerminal from '@/components/SSHTerminal.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()

const loading = ref(false)
const actionLoading = ref(false)
const showPassword = ref(false)
const showTrafficDetail = ref(false)
const sshDialogVisible = ref(false) // SSH对话框显示状态
const portMappings = ref([])
const activeTab = ref('overview') // 默认显示概览标签页

// 实例类型权限配置
const instanceTypePermissions = ref({
  canCreateContainer: false,
  canCreateVM: false,
  canDeleteInstance: false,
  canResetInstance: false,
  canDeleteContainer: false,
  canDeleteVM: false,
  canResetContainer: false,
  canResetVM: false
})

const instance = ref({
  id: '',
  name: '',
  type: '',
  status: '',
  providerName: '',
  osType: '',
  cpu: 0,
  memory: 0,
  disk: 0,
  bandwidth: 0,
  privateIP: '',
  publicIP: '',
  ipv6Address: '',
  publicIPv6: '',
  sshPort: '',
  username: '',
  password: '',
  createdAt: '',
  expiredAt: '',
  portRangeStart: 0,
  portRangeEnd: 0
})

const monitoring = reactive({
  trafficData: {
    currentMonth: 0,
    totalLimit: 102400,
    usagePercent: 0,
    isLimited: false,
    history: []
  }
})

// 网络类型相关方法
const getNetworkTypeFromLegacy = (ipv4MappingType, hasIPv6) => {
  if (ipv4MappingType === 'nat') {
    return hasIPv6 ? 'nat_ipv4_ipv6' : 'nat_ipv4'
  } else if (ipv4MappingType === 'dedicated') {
    return hasIPv6 ? 'dedicated_ipv4_ipv6' : 'dedicated_ipv4'
  } else if (ipv4MappingType === 'ipv6_only') {
    return 'ipv6_only'
  }
  return 'nat_ipv4'
}

const getNetworkTypeDisplayName = (networkType) => {
  const typeNames = {
    'nat_ipv4': 'NAT IPv4',
    'nat_ipv4_ipv6': `NAT IPv4 + ${t('user.apply.networkConfig.dedicatedIPv6')}`,
    'dedicated_ipv4': t('user.apply.networkConfig.dedicatedIPv4'),
    'dedicated_ipv4_ipv6': `${t('user.apply.networkConfig.dedicatedIPv4')} + ${t('user.apply.networkConfig.dedicatedIPv6')}`,
    'ipv6_only': t('user.apply.networkConfig.ipv6Only')
  }
  return typeNames[networkType] || t('user.instanceDetail.unknownType')
}

const getNetworkTypeTagType = (networkType) => {
  const tagTypes = {
    'nat_ipv4': 'primary',
    'nat_ipv4_ipv6': 'success',
    'dedicated_ipv4': 'warning',
    'dedicated_ipv4_ipv6': 'success',
    'ipv6_only': 'info'
  }
  return tagTypes[networkType] || 'default'
}

// 获取实例详情
const loadInstanceDetail = async () => {
  // 检查实例ID是否有效
  if (!route.params.id || route.params.id === 'undefined') {
    console.error('实例ID无效，返回实例列表')
    ElMessage.error(t('user.instances.instanceInvalid'))
    router.push('/user/instances')
    return false
  }

  try {
    loading.value = true
    const response = await getUserInstanceDetail(route.params.id)
    if (response.code === 0 || response.code === 200) {
      Object.assign(instance.value, response.data)
      // 实例详情加载完成后，更新权限
      updateInstancePermissions()
      return true
    }
    return false
  } catch (error) {
    console.error('获取实例详情失败:', error)
    ElMessage.error(t('user.instanceDetail.getDetailFailed'))
    router.back()
    return false
  } finally {
    loading.value = false
  }
}

// 获取端口映射数据
const refreshPortMappings = async () => {
  if (!route.params.id) {
    return
  }
  
  try {
    const response = await getUserInstancePorts(route.params.id)
    if (response.code === 0 || response.code === 200) {
      portMappings.value = response.data.list || []
      // 更新实例的公网IP信息（从端口映射API获取更准确的数据）
      if (response.data.publicIP) {
        instance.value.publicIP = response.data.publicIP
      }
      if (response.data.instance) {
        instance.value.username = response.data.instance.username || instance.value.username
      }
    }
  } catch (error) {
    console.error('获取端口映射失败:', error)
    // 不显示错误信息，因为某些实例可能没有端口映射
  }
}

// 获取监控数据
const refreshMonitoring = async () => {
  // 检查实例ID是否有效
  if (!route.params.id || route.params.id === 'undefined') {
    console.warn('实例ID无效，跳过监控数据获取')
    return
  }

  try {
    const response = await getInstanceMonitoring(route.params.id)
    if (response.code === 0 || response.code === 200) {
      Object.assign(monitoring, response.data)
      
      // 如果流量已限制，显示警告
      if (monitoring.trafficData?.isLimited) {
        ElMessage.warning(t('user.instanceDetail.trafficLimitWarning'))
      }
    }
  } catch (error) {
    console.error('获取监控数据失败:', error)
    // 如果监控API失败，使用默认值
    monitoring.trafficData = {
      currentMonth: 0,
      totalLimit: 102400,
      usagePercent: 0,
      isLimited: false,
      history: []
    }
    ElMessage.error(t('user.instanceDetail.getMonitoringFailed'))
  }
}

// 加载用户权限配置
const loadInstanceTypePermissions = async () => {
  try {
    const response = await getUserInstanceTypePermissions()
    if (response.code === 0 || response.code === 200) {
      const data = response.data || {}
      instanceTypePermissions.value = {
        canCreateContainer: data.canCreateContainer || false,
        canCreateVM: data.canCreateVM || false,
        canDeleteContainer: data.canDeleteContainer || false,
        canDeleteVM: data.canDeleteVM || false,
        canResetContainer: data.canResetContainer || false,
        canResetVM: data.canResetVM || false,
        canDeleteInstance: false, // 初始化，后续根据实例类型动态设置
        canResetInstance: false  // 初始化，后续根据实例类型动态设置
      }
      // 如果实例详情已加载，根据实例类型设置对应权限
      if (instance.value.instance_type) {
        updateInstancePermissions()
      }
    }
  } catch (error) {
    console.error('获取实例类型权限失败:', error)
    instanceTypePermissions.value = {
      canCreateContainer: false,
      canCreateVM: false,
      canDeleteInstance: false,
      canResetInstance: false,
      canDeleteContainer: false,
      canDeleteVM: false,
      canResetContainer: false,
      canResetVM: false
    }
  }
}

// 根据实例类型更新权限
const updateInstancePermissions = () => {
  if (instance.value.instance_type === 'vm') {
    instanceTypePermissions.value.canDeleteInstance = instanceTypePermissions.value.canDeleteVM
    instanceTypePermissions.value.canResetInstance = instanceTypePermissions.value.canResetVM
  } else {
    // container 或其他类型
    instanceTypePermissions.value.canDeleteInstance = instanceTypePermissions.value.canDeleteContainer
    instanceTypePermissions.value.canResetInstance = instanceTypePermissions.value.canResetContainer
  }
}

// 执行实例操作
const performAction = async (action) => {
  const actionText = {
    'start': t('user.instanceDetail.actionStart'),
    'stop': t('user.instanceDetail.actionStop'),
    'restart': t('user.instanceDetail.actionRestart'),
    'reset': t('user.instanceDetail.actionReset'),
    'delete': t('user.instanceDetail.actionDelete')
  }[action]

  const confirmText = action === 'delete' 
    ? `${t('user.instanceDetail.confirm')}${t('user.instanceDetail.delete')}${t('user.instances.title')} "${instance.value.name}" ${t('common.questionMark')}${t('user.profile.deleteConfirmNote')}`
    : `${t('user.instanceDetail.confirm')}${actionText}${t('user.instances.title')} "${instance.value.name}" ${t('common.questionMark')}`

  // 如果是启动操作且流量已限制，特殊提示
  if (action === 'start' && monitoring.trafficData?.isLimited) {
    const trafficLimitConfirm = await ElMessageBox.confirm(
      `${t('user.instances.title')} "${instance.value.name}" ${t('user.instanceDetail.trafficLimitWarning')}${t('common.comma')}${t('user.instances.title')}${t('user.instanceDetail.actionStart')}${t('common.period')}`,
      t('user.instanceDetail.trafficLimitNotice'),
      {
        confirmButtonText: t('user.instanceDetail.gotIt'),
        showCancelButton: false,
        type: 'warning'
      }
    ).catch(() => false)
    
    if (!trafficLimitConfirm) return
    return
  }

  try {
    await ElMessageBox.confirm(
      confirmText,
      t('user.instanceDetail.confirmOperation'),
      {
        confirmButtonText: t('user.instanceDetail.confirm'),
        cancelButtonText: t('user.instanceDetail.cancel'),
        type: action === 'delete' ? 'error' : 'warning'
      }
    )

    actionLoading.value = true
    const response = await performInstanceAction({
      instanceId: instance.value.id,
      action: action
    })

    if (response.code === 0 || response.code === 200) {
      ElMessage.success(`${actionText}${t('user.tasks.request')}${t('user.tasks.submitted')}${t('common.comma')}${t('user.tasks.processing')}${t('common.ellipsis')}`)
      
      if (action === 'delete' || action === 'reset') {
        // 删除和重置系统后返回列表页，避免显示过期数据
        if (action === 'reset') {
          ElMessage.info(t('user.instanceDetail.resetSystemNotice'))
        }
        router.push('/user/instances')
      } else {
        // 其他操作刷新详情
        await loadInstanceDetail()
      }
    }
  } catch (error) {
    if (error !== 'cancel') {
      console.error(`${actionText}实例失败:`, error)
      ElMessage.error(`${actionText}${t('user.instances.title')}${t('common.failed')}`)
    }
  } finally {
    actionLoading.value = false
  }
}

// 打开SSH终端
const openSSHTerminal = () => {
  if (!instance.value.id) {
    ElMessage.error(t('user.instanceDetail.instanceNotFound'))
    return
  }
  
  if (instance.value.status !== 'running') {
    ElMessage.warning(t('user.instanceDetail.instanceNotRunning'))
    return
  }
  
  if (!instance.value.password) {
    ElMessage.warning(t('user.instanceDetail.noPassword'))
    return
  }
  
  sshDialogVisible.value = true
}

// 关闭SSH终端
const handleCloseSSH = () => {
  sshDialogVisible.value = false
}

// 处理SSH错误
const handleSSHError = (error) => {
  console.error('SSH连接错误:', error)
  ElMessage.error(t('user.instanceDetail.sshConnectFailed'))
}

// 显示重置密码对话框
const showResetPasswordDialog = async () => {
  try {
    await ElMessageBox.confirm(
      `${t('user.instanceDetail.confirm')}${t('user.instanceDetail.resetPassword')}${t('user.instances.title')} "${instance.value.name}" ${t('user.instanceDetail.password')}${t('common.questionMark')}\n${t('user.tasks.system')}${t('user.tasks.willCreateTask')}${t('user.instanceDetail.resetPassword')}${t('user.tasks.operation')}${t('common.period')}`,
      t('user.instanceDetail.resetPasswordTitle'),
      {
        confirmButtonText: t('user.instanceDetail.confirm'),
        cancelButtonText: t('user.instanceDetail.cancel'),
        type: 'warning'
      }
    )

    // 显示加载状态
    actionLoading.value = true

    try {
      const response = await resetInstancePassword(instance.value.id)

      if (response.code === 0 || response.code === 200) {
        const taskId = response.data.taskId
        
        ElMessage.success(`${t('user.instanceDetail.resetPassword')}${t('user.tasks.taskCreated')}${t('common.leftParen')}${t('user.tasks.taskID')}: ${taskId}${t('common.rightParen')}${t('common.comma')}${t('user.tasks.checkProgress')}${t('user.tasks.taskList')}${t('common.inLocation')}`)
        
        // 可以选择跳转到任务列表或者在当前页面轮询任务状态
        // 这里简单显示成功消息，让用户去任务列表查看
        
      } else {
        ElMessage.error(response.message || t('user.instanceDetail.resetPasswordFailed'))
      }
    } catch (error) {
      console.error('创建密码重置任务失败:', error)
      ElMessage.error(t('user.instanceDetail.resetPasswordFailed'))
    }
  } catch (error) {
    // 用户取消操作
  } finally {
    actionLoading.value = false
  }
}

// 切换密码显示
const togglePassword = () => {
  showPassword.value = !showPassword.value
}

// 复制到剪贴板
// 截断长IP地址用于显示
const truncateIP = (ip, maxLength = 25) => {
  if (!ip || ip.length <= maxLength) {
    return ip
  }
  
  // 只在末尾省略，保留前面部分
  return ip.substring(0, maxLength - 3) + '...'
}

// 格式化SSH命令用于显示
const formatSSHCommand = (username, ip, port) => {
  const fullCommand = `ssh ${username || 'root'}@${ip} -p ${port}`
  if (fullCommand.length <= 40) {
    return fullCommand
  }
  
  // 如果命令太长，截断IP地址部分
  const truncatedIP = truncateIP(ip, 20)
  return `ssh ${username || 'root'}@${truncatedIP} -p ${port}`
}

// 格式化IP:端口用于显示
const formatIPPort = (ip, port) => {
  const fullAddress = `${ip}:${port}`
  if (fullAddress.length <= 30) {
    return fullAddress
  }
  
  // 如果地址太长，截断IP地址部分
  const truncatedIP = truncateIP(ip, 20)
  return `${truncatedIP}:${port}`
}

const copyToClipboard = async (text) => {
  if (!text) {
    ElMessage.warning(t('user.instanceDetail.nothingToCopy'))
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      ElMessage.success(t('user.instanceDetail.copiedToClipboard'))
      return
    }
    
    // 降级方案：使用传统的 document.execCommand
    const textArea = document.createElement('textarea')
    textArea.value = text
    textArea.style.position = 'fixed'
    textArea.style.left = '-999999px'
    textArea.style.top = '-999999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()
    
    try {
      // @ts-ignore - execCommand 已废弃但作为降级方案仍需使用
      const successful = document.execCommand('copy')
      if (successful) {
        ElMessage.success(t('user.instanceDetail.copiedToClipboard'))
      } else {
        throw new Error('execCommand failed')
      }
    } finally {
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    ElMessage.error(t('user.profile.copyFailed'))
  }
}

// 获取状态类型
const getStatusType = (status) => {
  const statusMap = {
    'running': 'success',
    'stopped': 'info',
    'paused': 'warning',
    'unavailable': 'danger',
    'error': 'danger'
  }
  return statusMap[status] || 'info'
}

// 获取状态文本
const getStatusText = (status) => {
  const statusMap = {
    'running': t('user.instanceDetail.statusRunning'),
    'stopped': t('user.instanceDetail.statusStopped'),
    'paused': t('user.instanceDetail.statusPaused'),
    'unavailable': t('user.instanceDetail.statusUnavailable'),
    'error': t('user.instanceDetail.statusError')
  }
  return statusMap[status] || status
}

// 获取流量进度条颜色
const getTrafficProgressColor = (percentage) => {
  if (percentage < 70) return '#67c23a'
  if (percentage < 90) return '#e6a23c'
  return '#f56c6c'
}

// 格式化流量
const formatTraffic = (mb) => {
  if (!mb || mb === 0) return '0 MB'
  if (mb < 1024) return `${mb} MB`
  if (mb < 1024 * 1024) return `${(mb / 1024).toFixed(1)} GB`
  return `${(mb / (1024 * 1024)).toFixed(1)} TB`
}

// 格式化日期
const formatDate = (dateString) => {
  if (!dateString) return '暂无'
  return new Date(dateString).toLocaleString('zh-CN')
}

// 获取流量限制标题
const getTrafficLimitTitle = () => {
  const limitType = monitoring?.trafficData?.limitType
  switch (limitType) {
    case 'user':
      return t('user.instanceDetail.userTrafficWarning')
    case 'provider':
      return t('user.instanceDetail.trafficWarning')
    case 'both':
      return t('user.instanceDetail.dualTrafficWarning')
    default:
      return t('user.instanceDetail.trafficWarning')
  }
}

// 获取流量限制类型
const getTrafficLimitType = () => {
  const limitType = monitoring?.trafficData?.limitType
  switch (limitType) {
    case 'provider':
    case 'both':
      return 'error'  // Provider流量限制更严重，显示为错误
    case 'user':
      return 'warning'  // 用户流量限制显示为警告
    default:
      return 'warning'
  }
}

// 监听路由参数变化
watch(() => route.params.id, async (newId, oldId) => {
  if (newId && newId !== oldId && newId !== 'undefined') {
    const success = await loadInstanceDetail()
    if (success) {
      refreshMonitoring()
      // 重新加载权限配置以更新按钮显示
      await loadInstanceTypePermissions()
    }
  }
})

// 监听路由hash变化来切换标签页
watch(() => route.hash, (newHash) => {
  if (newHash) {
    const tab = newHash.replace('#', '')
    if (['overview', 'ports', 'stats'].includes(tab)) {
      activeTab.value = tab
    }
  }
}, { immediate: true })

// 切换标签页时更新URL hash
watch(activeTab, (newTab) => {
  if (newTab && route.hash !== `#${newTab}`) {
    router.replace({ ...route, hash: `#${newTab}` })
  }
})

// 定时器引用
let monitoringTimer = null

onMounted(async () => {
  // 等待下一个tick，确保路由参数已经加载
  await nextTick()
  
  // 并行加载实例详情和权限配置
  const [detailSuccess] = await Promise.allSettled([
    loadInstanceDetail(),
    loadInstanceTypePermissions()
  ])
  
  if (detailSuccess.status === 'fulfilled' && detailSuccess.value) {
    refreshMonitoring()
    refreshPortMappings()
    
    // 定时刷新监控数据
    monitoringTimer = setInterval(refreshMonitoring, 30000)
  }
})

// 组件卸载时清除定时器
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
  color: #6b7280;
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
  color: #1f2937;
}

.server-meta {
  display: flex;
  align-items: center;
  gap: 12px;
}

.server-provider {
  color: #6b7280;
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
  color: #1f2937;
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
  background: #f8f9fa;
  border-radius: 6px;
}

.hardware-item .label {
  color: #6b7280;
  font-size: 14px;
}

.hardware-item .value {
  color: #1f2937;
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
  color: #1f2937;
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
  background: #f8f9fa;
  border-radius: 8px;
  border: 1px solid #e5e7eb;
}

.connection-item .label,
.info-item .label {
  color: #6b7280;
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
  color: #1f2937;
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
  background: #f8f9fa;
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
  color: #6b7280;
  font-weight: 500;
}

.summary-item .value {
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
}

.ports-table {
  width: 100%;
}

.connection-commands {
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
}

.command-text {
  font-size: 12px;
  color: #374151;
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
  color: #6b7280;
}

/* 统计标签页 */
.stats-content {
  display: grid;
  gap: 32px;
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
  color: #1f2937;
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
  background: #f8f9fa;
  border-radius: 8px;
}

.monitor-label {
  color: #6b7280;
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
  background: #f8f9fa;
  border-radius: 8px;
  margin-bottom: 20px;
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
  color: #1f2937;
}

.usage-info {
  font-size: 14px;
  color: #6b7280;
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
  color: #909399;
}

.traffic-breakdown h4 {
  margin: 0 0 16px 0;
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
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
  background: #f8f9fa;
  border-radius: 6px;
  font-size: 14px;
}

.history-item .month {
  color: #6b7280;
  font-weight: 500;
}

.history-item .traffic {
  color: #1f2937;
  font-weight: 600;
}

.history-item .breakdown {
  color: #6b7280;
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

  /* 移动端IP地址显示优化 */
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
.ssh-dialog-content {
  height: 600px;
  background-color: #1e1e1e;
  border-radius: 4px;
  overflow: hidden;
}

:deep(.el-dialog__body) {
  padding: 0;
}

:deep(.el-dialog) {
  border-radius: 8px;
}
</style>
