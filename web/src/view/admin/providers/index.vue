<template>
  <div class="providers-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.providers.title') }}</span>
          <div class="header-actions">
            <!-- 批量操作按钮组 - 仅在选中时显示 -->
            <template v-if="selectedProviders.length > 0">
              <el-button
                type="danger"
                :icon="Delete"
                @click="handleBatchDelete"
              >
                {{ $t('admin.providers.batchDelete') }} ({{ selectedProviders.length }})
              </el-button>
              <el-button
                type="warning"
                :icon="Lock"
                @click="handleBatchFreeze"
              >
                {{ $t('admin.providers.batchFreeze') }} ({{ selectedProviders.length }})
              </el-button>
            </template>
            <!-- 添加服务器按钮 -->
            <el-button
              type="primary"
              @click="handleAddProvider"
            >
              {{ $t('admin.providers.addProvider') }}
            </el-button>
          </div>
        </div>
      </template>
      
      <!-- 搜索过滤 -->
      <SearchFilter 
        :search-form="searchForm"
        @search="handleSearch"
        @reset="handleReset"
      />
      
      <!-- Provider列表表格 -->
      <ProviderTable
        :loading="loading"
        :providers="providers"
        :current-page="currentPage"
        :page-size="pageSize"
        :total="total"
        @selection-change="handleSelectionChange"
        @edit="editProvider"
        @auto-configure="autoConfigureAPI"
        @traffic-monitor="handleEnableTrafficMonitor"
        @health-check="checkHealth"
        @set-expiry="handleSetProviderExpiry"
        @freeze="freezeServer"
        @unfreeze="unfreezeServer"
        @delete="handleDeleteProvider"
        @size-change="handleSizeChange"
        @page-change="handleCurrentChange"
      />
    </el-card>

    <!-- 添加/编辑服务器对话框 -->
    <ProviderFormDialog
      v-model:visible="showAddDialog"
      :is-editing="isEditing"
      :provider-data="addProviderForm"
      :grouped-countries="groupedCountries"
      :loading="addProviderLoading"
      @submit="submitAddServer"
      @cancel="cancelAddServer"
      @reset-level-limits="resetLevelLimitsToDefault"
    />

    <!-- 自动配置结果对话框 -->
    <ConfigDialog
      v-model:visible="configDialog.visible"
      :provider="configDialog.provider"
      :show-history="configDialog.showHistory"
      :running-task="configDialog.runningTask"
      :history-tasks="configDialog.historyTasks"
      @close="configDialog.visible = false"
      @view-task-log="viewTaskLog"
      @view-running-task="viewRunningTask"
      @rerun-configuration="rerunConfiguration"
    />

    <!-- 任务日志查看对话框 -->
    <TaskLogDialog
      v-model:visible="taskLogDialog.visible"
      :loading="taskLogDialog.loading"
      :error="taskLogDialog.error"
      :task="taskLogDialog.task"
      @close="taskLogDialog.visible = false"
    />

    <!-- 流量监控任务对话框 -->
    <TrafficMonitorTaskDialog
      v-model:visible="trafficMonitorDialog.visible"
      :provider="trafficMonitorDialog.provider"
      :show-history="trafficMonitorDialog.showHistory"
      :task="trafficMonitorDialog.task"
      :running-task="trafficMonitorDialog.runningTask"
      :history-tasks="trafficMonitorDialog.historyTasks"
      :loading="trafficMonitorDialog.loading"
      :pagination="trafficMonitorDialog.pagination"
      @close="trafficMonitorDialog.visible = false"
      @refresh="refreshTrafficMonitorTask"
      @view-task-log="viewTrafficMonitorTaskLog"
      @view-running-task="viewRunningTrafficMonitorTask"
      @execute-operation="executeTrafficMonitorOperation"
      @page-change="handleTrafficMonitorPageChange"
      @page-size-change="handleTrafficMonitorPageSizeChange"
    />
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, watch, nextTick } from 'vue'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import { Search, Delete, Lock } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { getProviderList, createProvider, updateProvider, deleteProvider, freezeProvider, unfreezeProvider, setProviderExpiry, checkProviderHealth, autoConfigureProvider, getConfigurationTaskDetail, trafficMonitorOperation, getLatestTrafficMonitorTask, getTrafficMonitorTasks, getTrafficMonitorTaskDetail } from '@/api/admin'
import { countries, getCountryByName, getCountriesByRegion } from '@/utils/countries'
import { useUserStore } from '@/pinia/modules/user'
// 导入拆分的组件
import SearchFilter from './components/SearchFilter.vue'
import ConfigDialog from './components/ConfigDialog.vue'
import TaskLogDialog from './components/TaskLogDialog.vue'
import TrafficMonitorTaskDialog from './components/TrafficMonitorTaskDialog.vue'
import ProviderTable from './components/ProviderTable.vue'
import ProviderFormDialog from './components/ProviderFormDialog.vue'

const { t } = useI18n()

const providers = ref([])
const selectedProviders = ref([]) // 批量选中的节点
const loading = ref(false)
const showAddDialog = ref(false)
const addProviderLoading = ref(false)
const isEditing = ref(false)

// 搜索表单
const searchForm = reactive({
  name: '',
  type: '',
  status: ''
})

// 分页
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 服务器表单
const addProviderForm = reactive({
  id: null,
  name: '',
  type: '',
  host: '',
  portIP: '', // 端口映射使用的公网IP
  port: 22,
  username: '',
  password: '',
  sshKey: '',
  authMethod: 'password', // 认证方式：'password' 或 'sshKey'
  description: '',
  region: '',
  country: '',
  countryCode: '',
  city: '',
  containerEnabled: true,
  vmEnabled: false,
  architecture: 'amd64', // 架构字段，默认amd64
  status: 'active',
  expiresAt: '', // 过期时间
  maxContainerInstances: 0, // 最大容器数，0表示无限制
  maxVMInstances: 0, // 最大虚拟机数，0表示无限制
  allowConcurrentTasks: false, // 是否允许并发任务，默认false
  maxConcurrentTasks: 1, // 最大并发任务数，默认1
  taskPollInterval: 60, // 任务轮询间隔（秒），默认60秒
  enableTaskPolling: true, // 是否启用任务轮询，默认true
  // 存储配置（ProxmoxVE专用）
  storagePool: 'local', // 存储池名称，默认local
  // 端口映射配置
  defaultPortCount: 10, // 每个实例默认端口数量
  portRangeStart: 10000, // 端口范围起始
  portRangeEnd: 65535, // 端口范围结束
  networkType: 'nat_ipv4', // 网络配置类型，默认NAT IPv4
  // 带宽配置
  defaultInboundBandwidth: 300, // 默认入站带宽限制（Mbps）
  defaultOutboundBandwidth: 300, // 默认出站带宽限制（Mbps）
  maxInboundBandwidth: 1000, // 最大入站带宽限制（Mbps）
  maxOutboundBandwidth: 1000, // 最大出站带宽限制（Mbps）
  // 流量配置
  enableTrafficControl: false, // 是否启用流量统计和限制，默认不启用
  maxTraffic: 1048576, // 最大流量限制（MB），默认1TB
  trafficCountMode: 'both', // 流量统计模式：both(双向), out(仅出向), in(仅入向)
  trafficMultiplier: 1.0, // 流量计费倍率，默认1.0
  // 流量统计配置
  trafficStatsMode: 'light', // 流量统计模式：high, standard, light, minimal, custom
  trafficCollectInterval: 60, // 流量采集间隔（秒）
  trafficCollectBatchSize: 10, // 流量采集批量大小
  trafficLimitCheckInterval: 180, // 流量限制检查间隔（秒）
  trafficLimitCheckBatchSize: 10, // 流量限制检查批量大小
  trafficAutoResetInterval: 1800, // 流量自动重置检查间隔（秒）
  trafficAutoResetBatchSize: 10, // 流量自动重置批量大小
  ipv4PortMappingMethod: 'device_proxy', // IPv4端口映射方式：device_proxy, iptables, native
  ipv6PortMappingMethod: 'device_proxy',  // IPv6端口映射方式：device_proxy, iptables, native
  executionRule: 'auto', // 操作轮转规则：auto(自动切换), api_only(仅API), ssh_only(仅SSH)
  sshConnectTimeout: 30, // SSH连接超时（秒），默认30秒
  sshExecuteTimeout: 300, // SSH执行超时（秒），默认300秒
  // 容器资源限制配置
  containerLimitCpu: false, // 容器是否限制CPU，默认不限制
  containerLimitMemory: false, // 容器是否限制内存，默认不限制
  containerLimitDisk: true, // 容器是否限制硬盘，默认限制
  // 虚拟机资源限制配置
  vmLimitCpu: true, // 虚拟机是否限制CPU，默认限制
  vmLimitMemory: true, // 虚拟机是否限制内存，默认限制
  vmLimitDisk: true, // 虚拟机是否限制硬盘，默认限制
  // 硬件配置（容器特殊配置）
  containerPrivileged: false, // 特权模式，默认关闭
  containerAllowNesting: true, // 允许嵌套，默认开启
  containerEnableLxcfs: true, // LXCFS资源视图，默认开启
  containerCpuAllowance: '100%', // CPU限制百分比，默认100%（不限制）
  containerMemorySwap: true, // 内存交换，默认开启
  containerMaxProcesses: 0, // 最大进程数，0表示不限制
  containerDiskIoLimit: '', // 磁盘IO限制，空表示不限制
  // 节点级别等级限制配置
  levelLimits: {
    1: { maxInstances: 1, maxResources: { cpu: 1, memory: 350, disk: 1025, bandwidth: 100 }, maxTraffic: 102400 },
    2: { maxInstances: 2, maxResources: { cpu: 2, memory: 512, disk: 2048, bandwidth: 200 }, maxTraffic: 102400 },
    3: { maxInstances: 3, maxResources: { cpu: 3, memory: 1024, disk: 4096, bandwidth: 500 }, maxTraffic: 204800 },
    4: { maxInstances: 4, maxResources: { cpu: 4, memory: 4096, disk: 8192, bandwidth: 1000 }, maxTraffic: 409600 },
    5: { maxInstances: 5, maxResources: { cpu: 5, memory: 8192, disk: 16384, bandwidth: 2000 }, maxTraffic: 512000 }
  }
})

// 流量单位转换：TB 转 MB (1TB = 1024 * 1024 MB = 1048576 MB)
const TB_TO_MB = 1048576

// 计算属性：maxTraffic 的 TB 单位显示
const maxTrafficTB = computed({
  get: () => {
    // 从 MB 转换为 TB
    return Number((addProviderForm.maxTraffic / TB_TO_MB).toFixed(3))
  },
  set: (value) => {
    // 从 TB 转换为 MB
    addProviderForm.maxTraffic = Math.round(value * TB_TO_MB)
  }
})

// 国家列表数据
const countriesData = ref(countries)
const groupedCountries = ref(getCountriesByRegion())

// 获取等级标签类型
const getLevelTagType = (level) => {
  const types = {
    1: 'info',
    2: 'success',
    3: 'warning',
    4: 'danger',
    5: 'primary'
  }
  return types[level] || 'info'
}

// 恢复默认等级限制
const resetLevelLimitsToDefault = () => {
  ElMessageBox.confirm(
    '确定要恢复所有等级的默认限制值吗？',
    '确认操作',
    {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(() => {
    // 恢复默认值
    addProviderForm.levelLimits = {
      1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 10240, bandwidth: 100 }, maxTraffic: 102400 },
      2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 20480, bandwidth: 200 }, maxTraffic: 204800 },
      3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 40960, bandwidth: 500 }, maxTraffic: 307200 },
      4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 81920, bandwidth: 1000 }, maxTraffic: 409600 },
      5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 163840, bandwidth: 2000 }, maxTraffic: 512000 }
    }
    ElMessage.success(t('admin.providers.levelLimitsRestored'))
  }).catch(() => {
    // 用户取消操作
  })
}

// 验证至少选择一种虚拟化类型
const validateVirtualizationType = () => {
  if (!addProviderForm.containerEnabled && !addProviderForm.vmEnabled) {
    ElMessage.warning(t('admin.providers.selectVirtualizationType'))
    return false
  }
  return true
}

// 格式化位置信息
const formatLocation = (provider) => {
  const parts = []
  if (provider.city) {
    parts.push(provider.city)
  }
  if (provider.country) {
    parts.push(provider.country)
  } else if (provider.region) {
    parts.push(provider.region)
  }
  return parts.length > 0 ? parts.join(', ') : '-'
}

// 解析等级限制配置（后端 kebab-case -> 前端 camelCase）
const parseLevelLimits = (levelLimitsStr) => {
  const defaultLevelLimits = {
    1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 10240, bandwidth: 100 }, maxTraffic: 102400 },
    2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 20480, bandwidth: 200 }, maxTraffic: 204800 },
    3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 40960, bandwidth: 500 }, maxTraffic: 307200 },
    4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 81920, bandwidth: 1000 }, maxTraffic: 409600 },
    5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 163840, bandwidth: 2000 }, maxTraffic: 512000 }
  }

  if (!levelLimitsStr) {
    return JSON.parse(JSON.stringify(defaultLevelLimits))
  }

  try {
    const parsed = typeof levelLimitsStr === 'string' ? JSON.parse(levelLimitsStr) : levelLimitsStr
    const result = {}
    
    for (let i = 1; i <= 5; i++) {
      if (!parsed[i]) {
        // 如果该等级不存在，使用默认值
        result[i] = JSON.parse(JSON.stringify(defaultLevelLimits[i]))
      } else {
        // 后端返回的字段名可能是 kebab-case (max-instances) 或 camelCase (maxInstances)
        // 需要兼容两种格式
        const levelData = parsed[i]
        const maxInstances = levelData.maxInstances ?? levelData['max-instances']
        const maxTraffic = levelData.maxTraffic ?? levelData['max-traffic']
        const maxResourcesData = levelData.maxResources ?? levelData['max-resources']
        
        result[i] = {
          maxInstances: maxInstances ?? defaultLevelLimits[i].maxInstances,
          maxTraffic: maxTraffic ?? defaultLevelLimits[i].maxTraffic,
          maxResources: {
            cpu: maxResourcesData?.cpu ?? defaultLevelLimits[i].maxResources.cpu,
            memory: maxResourcesData?.memory ?? defaultLevelLimits[i].maxResources.memory,
            disk: maxResourcesData?.disk ?? defaultLevelLimits[i].maxResources.disk,
            bandwidth: maxResourcesData?.bandwidth ?? defaultLevelLimits[i].maxResources.bandwidth
          }
        }
      }
    }
    return result
  } catch (e) {
    console.error('解析等级限制配置失败:', e)
    return JSON.parse(JSON.stringify(defaultLevelLimits))
  }
}

// 转换等级限制配置为后端格式（前端 camelCase -> 后端 kebab-case）
const formatLevelLimitsForBackend = (levelLimits) => {
  const result = {}
  for (let i = 1; i <= 5; i++) {
    if (levelLimits[i]) {
      result[i] = {
        'max-instances': levelLimits[i].maxInstances,
        'max-traffic': levelLimits[i].maxTraffic,
        'max-resources': {
          cpu: levelLimits[i].maxResources.cpu,
          memory: levelLimits[i].maxResources.memory,
          disk: levelLimits[i].maxResources.disk,
          bandwidth: levelLimits[i].maxResources.bandwidth
        }
      }
    }
  }
  return result
}

const loadProviders = async () => {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      pageSize: pageSize.value
    }
    
    // 搜索参数
    if (searchForm.name) {
      params.name = searchForm.name
    }
    if (searchForm.type) {
      params.type = searchForm.type
    }
    if (searchForm.status) {
      params.status = searchForm.status
    }
    
    const response = await getProviderList(params)
    providers.value = response.data.list || []
    total.value = response.data.total || 0
  } catch (error) {
    ElMessage.error(t('admin.providers.loadProvidersFailed'))
  } finally {
    loading.value = false
  }
}

// 搜索处理
const handleSearch = () => {
  currentPage.value = 1
  loadProviders()
}

// 重置搜索
const handleReset = () => {
  searchForm.name = ''
  searchForm.type = ''
  searchForm.status = ''
  currentPage.value = 1
  loadProviders()
}

const cancelAddServer = () => {
  showAddDialog.value = false
  isEditing.value = false
  Object.assign(addProviderForm, {
    id: null,
    name: '',
    type: '',
    host: '',
    portIP: '',
    port: 22,
    username: '',
    password: '',
    sshKey: '',
    authMethod: 'password',
    description: '',
    region: '',
    country: '',
    countryCode: '',
    city: '',
    containerEnabled: true,
    vmEnabled: false,
    architecture: 'amd64',
    status: 'active',
    expiresAt: '',
    maxContainerInstances: 0,
    maxVMInstances: 0,
    allowConcurrentTasks: false,
    maxConcurrentTasks: 1,
    taskPollInterval: 60,
    enableTaskPolling: true,
    storagePool: 'local',
    defaultPortCount: 10,
    portRangeStart: 10000,
    portRangeEnd: 65535,
    networkType: 'nat_ipv4',
    defaultInboundBandwidth: 300,
    defaultOutboundBandwidth: 300,
    maxInboundBandwidth: 1000,
    maxOutboundBandwidth: 1000,
    maxTraffic: 1048576,
    trafficCountMode: 'both',
    trafficMultiplier: 1.0,
    executionRule: 'auto',
    ipv4PortMappingMethod: 'device_proxy',
    ipv6PortMappingMethod: 'device_proxy',
    sshConnectTimeout: 30,
    sshExecuteTimeout: 300,
    containerLimitCpu: false,
    containerLimitMemory: false,
    containerLimitDisk: true,
    vmLimitCpu: true,
    vmLimitMemory: true,
    vmLimitDisk: true,
    levelLimits: {
      1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 10240, bandwidth: 100 }, maxTraffic: 102400 },
      2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 20480, bandwidth: 200 }, maxTraffic: 204800 },
      3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 40960, bandwidth: 500 }, maxTraffic: 307200 },
      4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 81920, bandwidth: 1000 }, maxTraffic: 409600 },
      5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 163840, bandwidth: 2000 }, maxTraffic: 512000 }
    }
  })
}

const submitAddServer = async (formData) => {
  try {
    // 验证虚拟化类型
    if (!formData.containerEnabled && !formData.vmEnabled) {
      ElMessage.warning(t('admin.providers.selectVirtualizationType'))
      return
    }
    
    // 验证SSH认证方式（创建模式）
    if (!isEditing.value) {
      if (formData.authMethod === 'password' && !formData.password) {
        ElMessage.error(t('admin.providers.passwordRequired'))
        return
      }
      if (formData.authMethod === 'sshKey' && !formData.sshKey) {
        ElMessage.error(t('admin.providers.sshKeyRequired'))
        return
      }
    }
    
    addProviderLoading.value = true

    const serverData = {
      name: formData.name,
      type: formData.type,
      endpoint: formData.host,
      portIP: formData.portIP,
      sshPort: formData.port,
      username: formData.username,
      config: '',
      region: formData.region,
      country: formData.country,
      countryCode: formData.countryCode,
      city: formData.city,
      container_enabled: formData.containerEnabled,  // 后端使用 snake_case
      vm_enabled: formData.vmEnabled,                // 后端使用 snake_case
      architecture: formData.architecture,
      totalQuota: 0,
      allowClaim: true,
      status: formData.status,
      expiresAt: formData.expiresAt || '',
      maxContainerInstances: formData.maxContainerInstances || 0,
      maxVMInstances: formData.maxVMInstances || 0,
      allowConcurrentTasks: formData.allowConcurrentTasks,
      maxConcurrentTasks: formData.maxConcurrentTasks || 1,
      taskPollInterval: formData.taskPollInterval || 60,
      enableTaskPolling: formData.enableTaskPolling !== undefined ? formData.enableTaskPolling : true,
      storagePool: formData.storagePool || 'local',
      defaultPortCount: formData.defaultPortCount || 10,
      portRangeStart: formData.portRangeStart || 10000,
      portRangeEnd: formData.portRangeEnd || 65535,
      networkType: formData.networkType || 'nat_ipv4',
      defaultInboundBandwidth: formData.defaultInboundBandwidth || 300,
      defaultOutboundBandwidth: formData.defaultOutboundBandwidth || 300,
      maxInboundBandwidth: formData.maxInboundBandwidth || 1000,
      maxOutboundBandwidth: formData.maxOutboundBandwidth || 1000,
      enableTrafficControl: formData.enableTrafficControl !== undefined ? formData.enableTrafficControl : false,
      maxTraffic: formData.maxTraffic || 1048576,
      trafficCountMode: formData.trafficCountMode || 'both',
      trafficMultiplier: formData.trafficMultiplier !== undefined && formData.trafficMultiplier !== null ? formData.trafficMultiplier : 1.0,
      trafficStatsMode: formData.trafficStatsMode || 'light',
      trafficCollectInterval: formData.trafficCollectInterval || 60,
      trafficCollectBatchSize: formData.trafficCollectBatchSize || 10,
      trafficLimitCheckInterval: formData.trafficLimitCheckInterval || 180,
      trafficLimitCheckBatchSize: formData.trafficLimitCheckBatchSize || 10,
      trafficAutoResetInterval: formData.trafficAutoResetInterval || 1800,
      trafficAutoResetBatchSize: formData.trafficAutoResetBatchSize || 10,
      executionRule: formData.executionRule || 'auto',
      sshConnectTimeout: formData.sshConnectTimeout || 30,
      sshExecuteTimeout: formData.sshExecuteTimeout || 300,
      containerLimitCpu: formData.containerLimitCpu !== undefined ? formData.containerLimitCpu : false,
      containerLimitMemory: formData.containerLimitMemory !== undefined ? formData.containerLimitMemory : false,
      containerLimitDisk: formData.containerLimitDisk !== undefined ? formData.containerLimitDisk : true,
      vmLimitCpu: formData.vmLimitCpu !== undefined ? formData.vmLimitCpu : true,
      vmLimitMemory: formData.vmLimitMemory !== undefined ? formData.vmLimitMemory : true,
      vmLimitDisk: formData.vmLimitDisk !== undefined ? formData.vmLimitDisk : true,
      // 节点等级限制配置 - 转换为后端 kebab-case 格式（直接发送对象，不转换为字符串）
      levelLimits: formatLevelLimitsForBackend(formData.levelLimits || {}),
      // 容器特殊配置（LXD/Incus）
      containerPrivileged: formData.containerPrivileged || false,
      containerAllowNesting: formData.containerAllowNesting || false,
      containerEnableLxcfs: formData.containerEnableLxcfs !== undefined ? formData.containerEnableLxcfs : true,
      containerCpuAllowance: formData.containerCpuAllowance || '100%',
      containerMemorySwap: formData.containerMemorySwap !== undefined ? formData.containerMemorySwap : true,
      containerMaxProcesses: formData.containerMaxProcesses || 0,
      containerDiskIoLimit: formData.containerDiskIoLimit || ''
    }

    // 根据Provider类型设置端口映射方式
    // Docker 类型固定使用 native，不可选择
    if (formData.type === 'docker') {
      serverData.ipv4PortMappingMethod = 'native'
      serverData.ipv6PortMappingMethod = 'native'
    } else if (formData.type === 'proxmox') {
      // Proxmox IPv4: NAT情况下默认iptables，独立IP情况下可选
      if (formData.networkType === 'nat_ipv4' || formData.networkType === 'nat_ipv4_ipv6') {
        serverData.ipv4PortMappingMethod = formData.ipv4PortMappingMethod || 'iptables'
      } else {
        serverData.ipv4PortMappingMethod = formData.ipv4PortMappingMethod || 'native'
      }
      // Proxmox IPv6: 默认native，可选iptables
      serverData.ipv6PortMappingMethod = formData.ipv6PortMappingMethod || 'native'
    } else if (['lxd', 'incus'].includes(formData.type)) {
      // LXD/Incus IPv4默认device_proxy
      serverData.ipv4PortMappingMethod = formData.ipv4PortMappingMethod || 'device_proxy'
      // LXD/Incus IPv6默认device_proxy，可选iptables
      serverData.ipv6PortMappingMethod = formData.ipv6PortMappingMethod || 'device_proxy'
    }

    // 密码和SSH密钥的处理逻辑
    if (isEditing.value) {
      // 编辑模式：根据当前选择的认证方式和是否填写了新凭证来决定
      const originalAuthMethod = formData.authMethod // 当前选择的认证方式
      
      if (originalAuthMethod === 'password') {
        // 当前选择密码认证
        if (formData.password) {
          // 填写了新密码，更新密码
          serverData.password = formData.password
        }
        // 如果原来是SSH密钥认证，但现在切换到密码，需要确保填写了密码
        // 不主动清空SSH密钥，让后端根据优先级处理
      } else if (originalAuthMethod === 'sshKey') {
        // 当前选择SSH密钥认证
        if (formData.sshKey) {
          // 填写了新SSH密钥，更新SSH密钥
          serverData.sshKey = formData.sshKey
        }
        // 如果原来是密码认证，但现在切换到SSH密钥，需要确保填写了SSH密钥
        // 不主动清空密码，让后端根据优先级处理（SSH密钥优先于密码）
      }
    } else {
      // 创建模式：根据认证方式发送对应字段，确保只发送一种
      if (formData.authMethod === 'password') {
        serverData.password = formData.password
        // 创建时不发送sshKey，避免发送空字符串
      } else if (formData.authMethod === 'sshKey') {
        serverData.sshKey = formData.sshKey
        // 创建时不发送password，避免发送空字符串
      }
    }

    if (isEditing.value) {
      await updateProvider(formData.id, serverData)
      ElMessage.success(t('admin.providers.serverUpdateSuccess'))
    } else {
      // 服务器
      await createProvider(serverData)
      ElMessage.success(t('admin.providers.serverAddSuccess'))
    }
    
    cancelAddServer()
    await loadProviders()
  } catch (error) {
    console.error('Provider操作失败:', error)
    const errorMsg = error.response?.data?.msg || error.message || (isEditing.value ? t('admin.providers.serverUpdateFailed') : t('admin.providers.serverAddFailed'))
    ElMessage.error(errorMsg)
  } finally {
    addProviderLoading.value = false
  }
}

// 处理添加新节点
const handleAddProvider = () => {
  // 重置为新增模式
  isEditing.value = false
  // 重置表单到初始状态
  cancelAddServer()
  // 打开对话框
  showAddDialog.value = true
}

const editProvider = (provider) => {
  // 获取主机地址，如果endpoint包含端口则分离，否则使用完整地址
  let host = provider.endpoint
  if (provider.endpoint && provider.endpoint.includes(':')) {
    host = provider.endpoint.split(':')[0]
  }
  
  // 使用数据库中的sshPort字段，如果没有则默认为22
  const port = provider.sshPort || 22
  
  // 解析等级限制配置
  const parsedLevelLimits = parseLevelLimits(provider.levelLimits)
  
  // 直接替换整个 levelLimits 对象
  addProviderForm.levelLimits = parsedLevelLimits
  
  // 填充其他表单数据
  addProviderForm.id = provider.id
  addProviderForm.name = provider.name
  addProviderForm.type = provider.type
  addProviderForm.host = host
  addProviderForm.portIP = provider.portIP || ''
  addProviderForm.port = parseInt(port) || 22
  addProviderForm.username = provider.username || ''
  addProviderForm.password = ''
  addProviderForm.sshKey = ''
  addProviderForm.authMethod = provider.authMethod || 'password'
  addProviderForm.description = provider.description || ''
  addProviderForm.region = provider.region || ''
  addProviderForm.country = provider.country || ''
  addProviderForm.countryCode = provider.countryCode || ''
  addProviderForm.city = provider.city || ''
  addProviderForm.containerEnabled = Boolean(provider.container_enabled)
  addProviderForm.vmEnabled = Boolean(provider.vm_enabled)
  addProviderForm.architecture = provider.architecture || 'amd64'
  addProviderForm.status = provider.status || 'active'
  addProviderForm.expiresAt = provider.expiresAt || ''
  addProviderForm.maxContainerInstances = provider.maxContainerInstances || 0
  addProviderForm.maxVMInstances = provider.maxVMInstances || 0
  addProviderForm.allowConcurrentTasks = provider.allowConcurrentTasks || false
  addProviderForm.maxConcurrentTasks = provider.maxConcurrentTasks || 1
  addProviderForm.taskPollInterval = provider.taskPollInterval || 60
  addProviderForm.enableTaskPolling = provider.enableTaskPolling !== undefined ? provider.enableTaskPolling : true
  addProviderForm.storagePool = provider.storagePool || 'local'
  addProviderForm.defaultPortCount = provider.defaultPortCount || 10
  addProviderForm.enableIPv6 = provider.enableIPv6 || false
  addProviderForm.portRangeStart = provider.portRangeStart || 10000
  addProviderForm.portRangeEnd = provider.portRangeEnd || 65535
  addProviderForm.networkType = provider.networkType || 'nat_ipv4'
  addProviderForm.defaultInboundBandwidth = provider.defaultInboundBandwidth || 300
  addProviderForm.defaultOutboundBandwidth = provider.defaultOutboundBandwidth || 300
  addProviderForm.maxInboundBandwidth = provider.maxInboundBandwidth || 1000
  addProviderForm.maxOutboundBandwidth = provider.maxOutboundBandwidth || 1000
  addProviderForm.enableTrafficControl = provider.enableTrafficControl !== undefined ? provider.enableTrafficControl : false
  addProviderForm.maxTraffic = provider.maxTraffic || 1048576
  addProviderForm.trafficCountMode = provider.trafficCountMode || 'both'
  addProviderForm.trafficMultiplier = provider.trafficMultiplier || 1.0
  addProviderForm.trafficStatsMode = provider.trafficStatsMode || 'light'
  addProviderForm.trafficCollectInterval = provider.trafficCollectInterval || 60
  addProviderForm.trafficCollectBatchSize = provider.trafficCollectBatchSize || 10
  addProviderForm.trafficLimitCheckInterval = provider.trafficLimitCheckInterval || 180
  addProviderForm.trafficLimitCheckBatchSize = provider.trafficLimitCheckBatchSize || 10
  addProviderForm.trafficAutoResetInterval = provider.trafficAutoResetInterval || 1800
  addProviderForm.trafficAutoResetBatchSize = provider.trafficAutoResetBatchSize || 10
  addProviderForm.executionRule = provider.executionRule || 'auto'
  addProviderForm.sshConnectTimeout = provider.sshConnectTimeout || 30
  addProviderForm.sshExecuteTimeout = provider.sshExecuteTimeout || 300
  addProviderForm.containerLimitCpu = provider.containerLimitCpu !== undefined ? provider.containerLimitCpu : false
  addProviderForm.containerLimitMemory = provider.containerLimitMemory !== undefined ? provider.containerLimitMemory : false
  addProviderForm.containerLimitDisk = provider.containerLimitDisk !== undefined ? provider.containerLimitDisk : true
  addProviderForm.vmLimitCpu = provider.vmLimitCpu !== undefined ? provider.vmLimitCpu : true
  addProviderForm.vmLimitMemory = provider.vmLimitMemory !== undefined ? provider.vmLimitMemory : true
  addProviderForm.vmLimitDisk = provider.vmLimitDisk !== undefined ? provider.vmLimitDisk : true

  // 硬件配置（容器特殊配置）
  addProviderForm.containerPrivileged = provider.containerPrivileged !== undefined ? provider.containerPrivileged : false
  addProviderForm.containerAllowNesting = provider.containerAllowNesting !== undefined ? provider.containerAllowNesting : false
  addProviderForm.containerEnableLxcfs = provider.containerEnableLxcfs !== undefined ? provider.containerEnableLxcfs : true
  addProviderForm.containerCpuAllowance = provider.containerCpuAllowance || '100%'
  addProviderForm.containerMemorySwap = provider.containerMemorySwap !== undefined ? provider.containerMemorySwap : true
  addProviderForm.containerMaxProcesses = provider.containerMaxProcesses || 0
  addProviderForm.containerDiskIoLimit = provider.containerDiskIoLimit || ''

  // 根据Provider类型设置端口映射方式，优先使用数据库中保存的值，没有时使用类型默认值
  // Docker 类型固定为 native
  if (provider.type === 'docker') {
    addProviderForm.ipv4PortMappingMethod = 'native'
    addProviderForm.ipv6PortMappingMethod = 'native'
  } else if (provider.type === 'proxmox') {
    addProviderForm.ipv4PortMappingMethod = provider.ipv4PortMappingMethod || 'iptables'
    addProviderForm.ipv6PortMappingMethod = provider.ipv6PortMappingMethod || 'native'
  } else if (['lxd', 'incus'].includes(provider.type)) {
    addProviderForm.ipv4PortMappingMethod = provider.ipv4PortMappingMethod || 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = provider.ipv6PortMappingMethod || 'device_proxy'
  } else {
    addProviderForm.ipv4PortMappingMethod = provider.ipv4PortMappingMethod || 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = provider.ipv6PortMappingMethod || 'device_proxy'
  }
  
  isEditing.value = true
  showAddDialog.value = true
}

const handleDeleteProvider = async (provider) => {
  try {
    // 首先尝试普通删除
    const isOffline = provider.status === 'inactive' || 
                      (provider.sshStatus === 'offline' && provider.apiStatus === 'offline')
    
    // 第一次确认
    const firstConfirmMsg = isOffline 
      ? t('admin.providers.deleteOfflineConfirm', { name: provider.name })
      : t('admin.providers.deleteConfirm', { name: provider.name })
    
    await ElMessageBox.confirm(
      firstConfirmMsg,
      t('common.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
        dangerouslyUseHTMLString: true
      }
    )

    // 如果是离线节点，尝试普通删除，失败后提示强制删除
    if (isOffline) {
      try {
        // 先尝试普通删除
        await deleteProvider(provider.id, false)
        ElMessage.success(t('admin.providers.serverDeleteSuccess'))
        await loadProviders()
      } catch (normalError) {
        // 如果是因为有实例而失败，提示强制删除
        const errorMsg = normalError?.response?.data?.msg || ''
        if (errorMsg.includes('运行中的实例') || errorMsg.includes('实例')) {
          // 第二次确认：强制删除
          await ElMessageBox.confirm(
            t('admin.providers.forceDeleteConfirm', { name: provider.name }),
            t('admin.providers.forceDeleteTitle'),
            {
              confirmButtonText: t('admin.providers.forceDeleteButton'),
              cancelButtonText: t('common.cancel'),
              type: 'error',
              dangerouslyUseHTMLString: true,
              distinguishCancelAndClose: true
            }
          )
          
          // 执行强制删除
          await deleteProvider(provider.id, true)
          ElMessage.success(t('admin.providers.serverDeleteSuccess'))
          await loadProviders()
        } else {
          throw normalError
        }
      }
    } else {
      // 在线节点，直接普通删除
      await deleteProvider(provider.id, false)
      ElMessage.success(t('admin.providers.serverDeleteSuccess'))
      await loadProviders()
    }
  } catch (error) {
    if (error !== 'cancel') {
      const errorMsg = error?.response?.data?.msg || error?.message || t('admin.providers.serverDeleteFailed')
      ElMessage.error(errorMsg)
    }
  }
}

// 批量选择变化处理
const handleSelectionChange = (selection) => {
  selectedProviders.value = selection
}

// 批量删除
const handleBatchDelete = async () => {
  if (selectedProviders.value.length === 0) {
    ElMessage.warning(t('admin.providers.pleaseSelectProviders'))
    return
  }

  // 检查是否有离线节点
  const offlineProviders = selectedProviders.value.filter(p => 
    p.status === 'inactive' || (p.sshStatus === 'offline' && p.apiStatus === 'offline')
  )
  const hasOffline = offlineProviders.length > 0

  try {
    // 第一次确认
    const confirmMsg = hasOffline
      ? t('admin.providers.batchDeleteWithOfflineConfirm', { 
          total: selectedProviders.value.length,
          offline: offlineProviders.length 
        })
      : t('admin.providers.batchDeleteConfirm', { count: selectedProviders.value.length })

    await ElMessageBox.confirm(
      confirmMsg,
      t('common.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
        dangerouslyUseHTMLString: true
      }
    )

    const loadingInstance = ElLoading.service({
      lock: true,
      text: t('admin.providers.batchDeleting'),
      background: 'rgba(0, 0, 0, 0.7)'
    })

    let successCount = 0
    let failCount = 0
    const errors = []
    const needsForceDelete = []

    // 逐个删除（纯前端实现）
    for (const provider of selectedProviders.value) {
      try {
        // 先尝试普通删除
        await deleteProvider(provider.id, false)
        successCount++
      } catch (error) {
        const errorMsg = error?.response?.data?.msg || ''
        // 如果是离线节点且因为有实例而失败，记录下来准备强制删除
        const isOffline = provider.status === 'inactive' || 
                         (provider.sshStatus === 'offline' && provider.apiStatus === 'offline')
        if (isOffline && (errorMsg.includes('运行中的实例') || errorMsg.includes('实例'))) {
          needsForceDelete.push(provider)
        } else {
          failCount++
          errors.push(`${provider.name}: ${errorMsg || error?.message || t('common.failed')}`)
        }
      }
    }

    loadingInstance.close()

    // 如果有需要强制删除的节点，再次确认
    if (needsForceDelete.length > 0) {
      try {
        await ElMessageBox.confirm(
          t('admin.providers.batchForceDeleteConfirm', { count: needsForceDelete.length }),
          t('admin.providers.forceDeleteTitle'),
          {
            confirmButtonText: t('admin.providers.forceDeleteButton'),
            cancelButtonText: t('common.cancel'),
            type: 'error',
            dangerouslyUseHTMLString: true
          }
        )

        const forceLoadingInstance = ElLoading.service({
          lock: true,
          text: t('admin.providers.forceDeleting'),
          background: 'rgba(0, 0, 0, 0.7)'
        })

        // 执行强制删除
        for (const provider of needsForceDelete) {
          try {
            await deleteProvider(provider.id, true)
            successCount++
          } catch (error) {
            failCount++
            errors.push(`${provider.name}: ${error?.response?.data?.msg || error?.message || t('common.failed')}`)
          }
        }

        forceLoadingInstance.close()
      } catch (cancelError) {
        // 用户取消强制删除
        if (cancelError !== 'cancel') {
          failCount += needsForceDelete.length
          needsForceDelete.forEach(p => {
            errors.push(`${p.name}: ${t('admin.providers.forceCancelled')}`)
          })
        }
      }
    }

    loadingInstance.close()

    // 显示结果
    if (failCount === 0) {
      ElMessage.success(t('admin.providers.batchDeleteSuccess', { count: successCount }))
    } else {
      ElMessageBox.alert(
        `<div>
          <p>${t('admin.providers.batchOperationResult')}</p>
          <p style="color: #67C23A;">${t('admin.providers.successCount')}: ${successCount}</p>
          <p style="color: #F56C6C;">${t('admin.providers.failCount')}: ${failCount}</p>
          ${errors.length > 0 ? `<div style="margin-top: 10px; max-height: 200px; overflow-y: auto;">
            <p style="font-weight: bold;">${t('admin.providers.errorDetails')}:</p>
            ${errors.map(e => `<p style="color: #F56C6C; font-size: 12px;">• ${e}</p>`).join('')}
          </div>` : ''}
        </div>`,
        t('admin.providers.operationResult'),
        {
          dangerouslyUseHTMLString: true,
          confirmButtonText: t('common.confirm')
        }
      )
    }

    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.providers.batchDeleteFailed'))
    }
  }
}

// 批量冻结
const handleBatchFreeze = async () => {
  if (selectedProviders.value.length === 0) {
    ElMessage.warning(t('admin.providers.pleaseSelectProviders'))
    return
  }

  // 检查是否有已冻结的节点
  const frozenProviders = selectedProviders.value.filter(p => p.isFrozen)
  const activeProviders = selectedProviders.value.filter(p => !p.isFrozen)

  if (frozenProviders.length > 0 && activeProviders.length === 0) {
    ElMessage.warning(t('admin.providers.allSelectedAlreadyFrozen'))
    return
  }

  try {
    const message = frozenProviders.length > 0
      ? t('admin.providers.batchFreezeConfirmMixed', { 
          total: selectedProviders.value.length, 
          frozen: frozenProviders.length,
          active: activeProviders.length 
        })
      : t('admin.providers.batchFreezeConfirm', { count: selectedProviders.value.length })

    await ElMessageBox.confirm(
      message,
      t('admin.providers.confirmFreeze'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
        dangerouslyUseHTMLString: true
      }
    )

    const loadingInstance = ElLoading.service({
      lock: true,
      text: t('admin.providers.batchFreezing'),
      background: 'rgba(0, 0, 0, 0.7)'
    })

    let successCount = 0
    let failCount = 0
    const errors = []

    // 只处理未冻结的节点
    for (const provider of activeProviders) {
      try {
        await freezeProvider(provider.id)
        successCount++
      } catch (error) {
        failCount++
        errors.push(`${provider.name}: ${error?.response?.data?.msg || error?.message || t('common.failed')}`)
      }
    }

    loadingInstance.close()

    // 显示结果
    if (failCount === 0) {
      ElMessage.success(t('admin.providers.batchFreezeSuccess', { count: successCount }))
    } else {
      ElMessageBox.alert(
        `<div>
          <p>${t('admin.providers.batchOperationResult')}</p>
          <p style="color: #67C23A;">${t('admin.providers.successCount')}: ${successCount}</p>
          <p style="color: #F56C6C;">${t('admin.providers.failCount')}: ${failCount}</p>
          ${errors.length > 0 ? `<div style="margin-top: 10px; max-height: 200px; overflow-y: auto;">
            <p style="font-weight: bold;">${t('admin.providers.errorDetails')}:</p>
            ${errors.map(e => `<p style="color: #F56C6C; font-size: 12px;">• ${e}</p>`).join('')}
          </div>` : ''}
        </div>`,
        t('admin.providers.operationResult'),
        {
          dangerouslyUseHTMLString: true,
          confirmButtonText: t('common.confirm')
        }
      )
    }

    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.providers.batchFreezeFailed'))
    }
  }
}



const handleSetProviderExpiry = async (provider) => {
  try {
    const { value: expiresAt } = await ElMessageBox.prompt(
      '请输入过期时间（格式：YYYY-MM-DD HH:MM:SS 或 YYYY-MM-DD），留空则清除过期时间',
      '设置过期时间',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        inputPattern: /^(\d{4}-\d{2}-\d{2}( \d{2}:\d{2}:\d{2})?)?$/,
        inputErrorMessage: t('admin.providers.validation.dateFormatError'),
        inputPlaceholder: provider.expiresAt || '如：2024-12-31 23:59:59 或留空',
        inputValue: provider.expiresAt ? new Date(provider.expiresAt).toISOString().slice(0, 19).replace('T', ' ') : ''
      }
    )

    await setProviderExpiry({
      providerID: provider.id,
      expiresAt: expiresAt ? new Date(expiresAt).toISOString() : null
    })
    ElMessage.success(t('admin.providers.setExpirySuccess'))
    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.providers.setExpiryFailed'))
    }
  }
}

const freezeServer = async (id) => {
  try {
    await ElMessageBox.confirm(
      '此操作将冻结该服务器，冻结后普通用户无法使用该服务器创建实例，是否继续？',
      '确认冻结',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    await freezeProvider(id)
    ElMessage.success(t('admin.providers.serverFrozen'))
    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.providers.serverFreezeFailed'))
    }
  }
}

const unfreezeServer = async (server) => {
  try {
    const { value: expiresAt } = await ElMessageBox.prompt(
      '请输入新的过期时间（格式：YYYY-MM-DD HH:MM:SS 或 YYYY-MM-DD），留空则默认设置为31天后过期',
      '解冻服务器',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        inputPattern: /^(\d{4}-\d{2}-\d{2}( \d{2}:\d{2}:\d{2})?)?$/,
        inputErrorMessage: t('admin.providers.validation.dateFormatError'),
        inputPlaceholder: '如：2024-12-31 23:59:59 或留空'
      }
    )

    await unfreezeProvider(server.id, expiresAt || '')
    ElMessage.success(t('admin.providers.serverUnfrozen'))
    await loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.providers.serverUnfreezeFailed'))
    }
  }
}

// 流量监控操作处理 - 统一入口，先显示历史记录
const handleEnableTrafficMonitor = async (provider) => {
  await openTrafficMonitorDialog(provider)
}

const handleDisableTrafficMonitor = async (provider) => {
  await openTrafficMonitorDialog(provider)
}

const handleDetectTrafficMonitor = async (provider) => {
  await openTrafficMonitorDialog(provider)
}

// 打开流量监控对话框，显示历史记录
const openTrafficMonitorDialog = async (provider) => {
  trafficMonitorDialog.provider = provider
  trafficMonitorDialog.pagination.page = 1
  trafficMonitorDialog.pagination.pageSize = 10
  await loadTrafficMonitorHistory()
  trafficMonitorDialog.showHistory = true
  trafficMonitorDialog.task = null
  trafficMonitorDialog.visible = true
}

// 加载流量监控历史任务
const loadTrafficMonitorHistory = async () => {
  if (!trafficMonitorDialog.provider) return
  
  try {
    const historyResponse = await getTrafficMonitorTasks(trafficMonitorDialog.provider.id, {
      page: trafficMonitorDialog.pagination.page,
      pageSize: trafficMonitorDialog.pagination.pageSize
    })

    trafficMonitorDialog.historyTasks = historyResponse.data?.list || []
    trafficMonitorDialog.pagination.total = historyResponse.data?.total || 0
    
    // 检查是否有正在运行的任务
    const runningTask = trafficMonitorDialog.historyTasks.find(task => task.status === 'running' || task.status === 'pending')
    trafficMonitorDialog.runningTask = runningTask || null
  } catch (error) {
    console.error('Failed to load traffic monitor tasks:', error)
    ElMessage.error(t('admin.providers.loadTasksFailed'))
  }
}

// 处理流量监控任务列表分页变化
const handleTrafficMonitorPageChange = async (page) => {
  trafficMonitorDialog.pagination.page = page
  await loadTrafficMonitorHistory()
}

// 处理流量监控任务列表每页数量变化
const handleTrafficMonitorPageSizeChange = async (pageSize) => {
  trafficMonitorDialog.pagination.pageSize = pageSize
  trafficMonitorDialog.pagination.page = 1
  await loadTrafficMonitorHistory()
}

// 执行流量监控操作
const executeTrafficMonitorOperation = async (operation) => {
  if (!trafficMonitorDialog.provider) return
  
  const confirmMessages = {
    enable: t('admin.providers.enableTrafficMonitorConfirm'),
    disable: t('admin.providers.disableTrafficMonitorConfirm'),
    detect: t('admin.providers.detectTrafficMonitorConfirm')
  }
  
  const confirmTypes = {
    enable: 'info',
    disable: 'warning',
    detect: 'info'
  }
  
  try {
    await ElMessageBox.confirm(
      confirmMessages[operation],
      t('common.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: confirmTypes[operation]
      }
    )

    const response = await trafficMonitorOperation({
      providerId: trafficMonitorDialog.provider.id,
      operation: operation
    })

    if (response.code === 200 || response.code === 0) {
      ElMessage.success(t('admin.providers.trafficMonitorOperationSuccess'))
      
      // 获取完整的任务详情
      if (response.data?.taskId) {
        try {
          const taskResponse = await getTrafficMonitorTaskDetail(response.data.taskId)
          if (taskResponse.code === 200 || taskResponse.code === 0) {
            trafficMonitorDialog.showHistory = false
            trafficMonitorDialog.task = taskResponse.data
          }
        } catch (error) {
          console.error('Failed to load task detail:', error)
        }
      } else {
        // 如果返回的是完整任务对象
        trafficMonitorDialog.showHistory = false
        trafficMonitorDialog.task = response.data
      }
    } else {
      ElMessage.error(response.msg || t('admin.providers.trafficMonitorOperationFailed'))
    }
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(error?.response?.data?.msg || t('admin.providers.trafficMonitorOperationFailed'))
    }
  }
}

// 查看任务日志
const viewTrafficMonitorTaskLog = async (taskId) => {
  try {
    trafficMonitorDialog.loading = true
    const response = await getTrafficMonitorTaskDetail(taskId)
    
    if (response.code === 200 || response.code === 0) {
      trafficMonitorDialog.showHistory = false
      trafficMonitorDialog.task = response.data
    } else {
      ElMessage.error(response.msg || t('admin.providers.loadTaskFailed'))
    }
  } catch (error) {
    console.error('Failed to load task detail:', error)
    ElMessage.error(t('admin.providers.loadTaskFailed'))
  } finally {
    trafficMonitorDialog.loading = false
  }
}

// 查看正在运行的任务
const viewRunningTrafficMonitorTask = () => {
  if (trafficMonitorDialog.runningTask) {
    trafficMonitorDialog.showHistory = false
    trafficMonitorDialog.task = trafficMonitorDialog.runningTask
  }
}

// 刷新流量监控任务
const refreshTrafficMonitorTask = async () => {
  if (!trafficMonitorDialog.task || !trafficMonitorDialog.task.id) return
  
  try {
    trafficMonitorDialog.loading = true
    const response = await getTrafficMonitorTaskDetail(trafficMonitorDialog.task.id)
    
    if (response.code === 200 || response.code === 0) {
      trafficMonitorDialog.task = response.data
      
      // 如果任务已完成，刷新历史列表
      if (response.data.status === 'completed' || response.data.status === 'failed') {
        await loadTrafficMonitorHistory()
      }
    }
  } catch (error) {
    console.error('Failed to refresh task:', error)
  } finally {
    trafficMonitorDialog.loading = false
  }
}

const handleSizeChange = (newSize) => {
  pageSize.value = newSize
  currentPage.value = 1
  loadProviders()
}

const handleCurrentChange = (newPage) => {
  currentPage.value = newPage
  loadProviders()
}

// 监听provider类型变化，自动设置虚拟化类型支持和端口映射方式
watch(() => addProviderForm.type, (newType) => {
  // 编辑模式下不自动修改虚拟化类型设置，保持用户已保存的配置
  if (isEditing.value) {
    return
  }
  if (newType === 'docker') {
    // Docker只支持容器，使用原生端口映射
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = false
    addProviderForm.ipv4PortMappingMethod = 'native' // Docker使用原生实现
    addProviderForm.ipv6PortMappingMethod = 'native'
  } else if (newType === 'proxmox') {
    // Proxmox支持容器和虚拟机
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = true
    // IPv4: NAT模式下默认iptables，独立IP模式下默认native
    const isNATMode = addProviderForm.networkType === 'nat_ipv4' || addProviderForm.networkType === 'nat_ipv4_ipv6'
    addProviderForm.ipv4PortMappingMethod = isNATMode ? 'iptables' : 'native'
    // IPv6: 默认native
    addProviderForm.ipv6PortMappingMethod = 'native'
  } else if (['lxd', 'incus'].includes(newType)) {
    // LXD/Incus支持容器和虚拟机，默认使用device_proxy
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = true
    addProviderForm.ipv4PortMappingMethod = 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = 'device_proxy'
  } else {
    // 其他类型保持默认设置
    addProviderForm.containerEnabled = true
    addProviderForm.vmEnabled = false
    addProviderForm.ipv4PortMappingMethod = 'device_proxy'
    addProviderForm.ipv6PortMappingMethod = 'device_proxy'
  }
})

// 监听网络类型变化，当Proxmox从NAT改为独立IP时，自动调整端口映射方法
// 编辑模式下不应该覆盖用户已保存的配置
watch(() => [addProviderForm.type, addProviderForm.networkType], ([type, networkType]) => {
  // 编辑模式下不自动修改端口映射配置，保持用户已保存的配置
  if (isEditing.value) {
    return
  }
  
  if (type === 'proxmox') {
    const isNATMode = networkType === 'nat_ipv4' || networkType === 'nat_ipv4_ipv6'
    if (isNATMode) {
      // NAT模式只能使用iptables
      addProviderForm.ipv4PortMappingMethod = 'iptables'
    } else {
      // 独立IP模式默认使用native，但也可以选择iptables
      if (addProviderForm.ipv4PortMappingMethod === 'iptables') {
        // 如果当前是iptables，保持不变
      } else {
        addProviderForm.ipv4PortMappingMethod = 'native'
      }
    }
  }
})

// 格式化流量大小
const formatTraffic = (sizeInMB) => {
  if (!sizeInMB || sizeInMB === 0) return '0B'
  
  const units = ['MB', 'GB', 'TB', 'PB']
  let size = sizeInMB
  let unitIndex = 0
  
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex++
  }
  
  return `${size.toFixed(unitIndex === 0 ? 0 : 1)}${units[unitIndex]}`
}

// 计算流量使用百分比
const getTrafficPercentage = (used, max) => {
  if (!max || max === 0) return 0
  return Math.min(Math.round((used / max) * 100), 100)
}

// 获取流量进度条状态
const getTrafficProgressStatus = (used, max) => {
  const percentage = getTrafficPercentage(used, max)
  if (percentage >= 90) return 'exception'
  if (percentage >= 80) return 'warning'
  return 'success'
}

// 计算资源使用百分比（适用于CPU、内存、磁盘）
const getResourcePercentage = (allocated, total) => {
  if (!total || total === 0) return 0
  return Math.min(Math.round((allocated / total) * 100), 100)
}

// 获取资源进度条状态（适用于CPU、内存、磁盘）
const getResourceProgressStatus = (allocated, total) => {
  const percentage = getResourcePercentage(allocated, total)
  if (percentage >= 95) return 'exception'
  if (percentage >= 85) return 'warning'
  return 'success'
}

// 格式化日期时间
const formatDateTime = (dateTimeStr) => {
  if (!dateTimeStr) return '-'
  const date = new Date(dateTimeStr)
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
}

// 检查是否过期
const isExpired = (dateTimeStr) => {
  if (!dateTimeStr) return false
  return new Date(dateTimeStr) < new Date()
}

// 检查是否即将过期（7天内）
const isNearExpiry = (dateTimeStr) => {
  if (!dateTimeStr) return false
  const expiryDate = new Date(dateTimeStr)
  const now = new Date()
  const diffDays = (expiryDate - now) / (1000 * 60 * 60 * 24)
  return diffDays <= 7 && diffDays > 0
}

// 获取状态类型（用于el-tag的type属性）
const getStatusType = (status) => {
  switch (status) {
    case 'online':
      return 'success'
    case 'offline':
      return 'danger'
    case 'unknown':
    default:
      return 'info'
  }
}

// 获取状态文本
const getStatusText = (status) => {
  switch (status) {
    case 'online':
      return '在线'
    case 'offline':
      return '离线'
    case 'unknown':
    default:
      return '未知'
  }
}

// 自动配置API相关状态
const configDialog = reactive({
  visible: false,
  provider: null,
  showHistory: false,
  runningTask: null,
  historyTasks: []
})

// 任务日志查看对话框状态
const taskLogDialog = reactive({
  visible: false,
  loading: false,
  error: null,
  task: null
})

// 流量监控任务对话框状态
const trafficMonitorDialog = reactive({
  visible: false,
  loading: false,
  provider: null,
  task: null,
  showHistory: false,
  runningTask: null,
  historyTasks: [],
  pagination: {
    page: 1,
    pageSize: 10,
    total: 0
  }
})

// 自动配置API - 新版本
const autoConfigureAPI = async (provider, force = false) => {
  try {
    // 先调用检查API
    const checkResponse = await autoConfigureProvider({
      providerId: provider.id,
      showHistory: true
    })

    const result = checkResponse.data

    // 始终显示配置对话框（包括首次配置的情况）
    configDialog.provider = provider
    configDialog.runningTask = result.runningTask
    configDialog.historyTasks = result.historyTasks || []
    configDialog.showHistory = true
    configDialog.visible = true
    
    // 如果有正在运行的任务，直接查看任务日志
    if (result.runningTask) {
      ElMessage.info(t('admin.providers.showTaskLog'))
      await viewTaskLog(result.runningTask.id)
    }

  } catch (error) {
    console.error('检查配置状态失败:', error)
    ElMessage.error(t('admin.providers.checkConfigFailed') + ': ' + (error.message || t('common.unknownError')))
  }
}

// 开始新的配置
const startNewConfiguration = async (provider, force = false) => {
  // 显示加载提示
  const loadingMessage = ElMessage({
    message: t('admin.providers.validation.autoConfiguring'),
    type: 'info',
    duration: 0,
    showClose: false
  })

  try {
    // 开始配置
    const response = await autoConfigureProvider({
      providerId: provider.id,
      force
    })

    const result = response.data

    // 关闭加载提示
    loadingMessage.close()

    // 关闭配置对话框
    configDialog.visible = false

    // 配置完成后直接显示结果
    if (result.taskId) {
      // 直接查看任务日志
      await viewTaskLog(result.taskId)
      // 重新加载服务器列表
      await loadProviders()
    } else {
      ElMessage.success('API 自动配置成功')
      await loadProviders()
    }

  } catch (error) {
    loadingMessage.close()
    console.error('启动配置失败:', error)
    ElMessage.error(t('admin.providers.startConfigFailed') + ': ' + (error.message || t('common.unknownError')))
  }
}

// 重新执行配置
const rerunConfiguration = () => {
  configDialog.visible = false
  startNewConfiguration(configDialog.provider, true)
}

// 查看运行中的任务
const viewRunningTask = () => {
  if (configDialog.runningTask) {
    // 直接查看任务日志（只支持最终日志）
    viewTaskLog(configDialog.runningTask.id)
  }
}

// 获取任务状态类型
const getTaskStatusType = (status) => {
  switch (status) {
    case 'completed':
      return 'success'
    case 'failed':
      return 'danger'
    case 'running':
      return 'warning'
    case 'cancelled':
      return 'info'
    default:
      return 'info'
  }
}

// 获取任务状态文本
const getTaskStatusText = (status) => {
  switch (status) {
    case 'completed':
      return '已完成'
    case 'failed':
      return '失败'
    case 'running':
      return '运行中'
    case 'cancelled':
      return '已取消'
    case 'pending':
      return '等待中'
    default:
      return '未知'
  }
}

// 调试函数：检查认证状态
const debugAuthStatus = () => {
  const userStore = useUserStore()
  console.log('Debug Auth Status:')
  console.log('- UserStore token:', userStore.token ? 'exists' : 'not found')
  console.log('- SessionStorage token:', sessionStorage.getItem('token') ? 'exists' : 'not found')
  console.log('- User type:', userStore.userType)
  console.log('- Is logged in:', userStore.isLoggedIn)
  console.log('- Is admin:', userStore.isAdmin)
}

// 健康检查
const checkHealth = async (providerId) => {
  const loadingMessage = ElMessage({
    message: t('admin.providers.validation.healthChecking'),
    type: 'info',
    duration: 0, // 不自动关闭
    showClose: false
  })
  
  try {
    console.log('开始健康检查，Provider ID:', providerId)
    const result = await checkProviderHealth(providerId)
    console.log('健康检查API返回结果:', result)
    
    loadingMessage.close() // 关闭加载消息
    
    if (result.code === 200) {
      console.log('健康检查成功，显示成功消息')
      ElMessage.success(t('admin.providers.healthCheckComplete'))
      await loadProviders() // 重新加载提供商列表以更新状态
    } else {
      console.log('健康检查失败，code:', result.code, 'msg:', result.msg)
      ElMessage.error(result.msg || t('admin.providers.healthCheckFailed'))
    }
  } catch (error) {
    loadingMessage.close() // 确保关闭加载消息
    console.error('健康检查异常:', error)
    console.log('异常详情:', {
      message: error.message,
      response: error.response,
      stack: error.stack
    })
    
    // 错误消息显示
    let errorMsg = '健康检查失败'
    if (error.message && error.message.includes('timeout')) {
      errorMsg = '健康检查超时，请检查网络连接或服务器状态'
    } else if (error.message) {
      errorMsg = '健康检查失败: ' + error.message
    }
    
    ElMessage.error(errorMsg)
  }
}

onMounted(() => {
  // 在开发环境下输出调试信息
  if (import.meta.env.DEV) {
    debugAuthStatus()
  }
  loadProviders()
})

// 查看任务日志
const viewTaskLog = async (taskId) => {
  taskLogDialog.visible = true
  taskLogDialog.loading = true
  taskLogDialog.error = null
  taskLogDialog.task = null

  try {
    const response = await getConfigurationTaskDetail(taskId)
    console.log('任务详情API响应:', response) // 调试日志
    if (response.code === 0 || response.code === 200) {
      taskLogDialog.task = response.data
    } else {
      taskLogDialog.error = response.msg || '获取任务详情失败'
    }
  } catch (error) {
    console.error('获取任务日志失败:', error)
    taskLogDialog.error = '获取任务日志失败: ' + (error.message || '未知错误')
  } finally {
    taskLogDialog.loading = false
  }
}

// 复制任务日志
const copyTaskLog = async () => {
  const logOutput = taskLogDialog.task?.logOutput
  if (!logOutput) {
    ElMessage.warning(t('admin.providers.noLogToCopy'))
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(logOutput)
      ElMessage.success(t('admin.providers.logCopied'))
      return
    }
    
    // 降级方案：使用传统的 document.execCommand
    const textArea = document.createElement('textarea')
    textArea.value = logOutput
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
        ElMessage.success(t('admin.providers.logCopied'))
      } else {
        throw new Error('execCommand failed')
      }
    } finally {
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    ElMessage.error(t('admin.providers.copyFailed'))
  }
}

// 格式化相对时间
const formatRelativeTime = (dateTime) => {
  if (!dateTime) return ''
  
  const now = new Date()
  const date = new Date(dateTime)
  const diffInMinutes = Math.floor((now - date) / (1000 * 60))
  
  if (diffInMinutes < 1) return '刚刚'
  if (diffInMinutes < 60) return `${diffInMinutes}分钟前`
  
  const diffInHours = Math.floor(diffInMinutes / 60)
  if (diffInHours < 24) return `${diffInHours}小时前`
  
  const diffInDays = Math.floor(diffInHours / 24)
  if (diffInDays < 7) return `${diffInDays}天前`
  
  return date.toLocaleDateString()
}
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: #303133;
  }
}

.header-actions {
  display: flex;
  gap: 10px;
  align-items: center;
}

.filter-container {
  margin-bottom: 20px;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.support-type-group {
  display: flex;
  gap: 15px;
}

.form-tip {
  margin-top: 5px;
}

/* 服务器配置标签页样式 */
.server-config-tabs {
  margin-bottom: 20px;
}

.server-config-tabs .el-tab-pane {
  padding: 20px 0;
}

.server-form {
  max-height: 400px;
  overflow-y: auto;
  padding-right: 10px;
}

.location-cell {
  display: flex;
  align-items: center;
  gap: 5px;
}

.location-cell-vertical {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  font-size: 12px;
}

.location-flag {
  font-size: 20px;
  line-height: 1;
}

.location-country {
  font-weight: 500;
  color: #303133;
  text-align: center;
}

.location-city {
  font-size: 11px;
  color: #909399;
  text-align: center;
}

.location-empty {
  color: #c0c4cc;
}

.flag-icon {
  font-size: 16px;
}

.support-types {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.el-select .el-input {
  width: 100%;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.connection-status {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.resource-info {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
}

.resource-usage {
  display: flex;
  align-items: center;
  gap: 2px;
  font-weight: 500;
}

.resource-usage .separator {
  color: #c0c4cc;
  margin: 0 2px;
}

.resource-progress {
  width: 100%;
}

.resource-item {
  display: flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}

.resource-item .el-icon {
  font-size: 14px;
  color: #909399;
}

.resource-placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 60px;
  color: #c0c4cc;
}

.sync-time {
  margin-top: 2px;
  text-align: center;
}

.traffic-info {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
}

.traffic-usage {
  display: flex;
  align-items: center;
  gap: 2px;
  font-weight: 500;
}

.traffic-usage .separator {
  color: #c0c4cc;
  margin: 0 2px;
}

.traffic-progress {
  width: 100%;
}

.traffic-status {
  text-align: center;
}

/* 资源限制配置样式 */
.resource-limit-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 20px;
  background: #f5f7fa;
  border-radius: 8px;
  transition: all 0.3s;
}

.resource-limit-item:hover {
  background: #ecf0f3;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
}

.resource-limit-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}

.resource-limit-label .el-icon {
  font-size: 18px;
  color: #409eff;
}

.resource-limit-tip {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: #909399;
  text-align: center;
}

.resource-limit-tip .el-icon {
  color: #409eff;
}

/* 等级限制配置样式 */
.level-limits-container {
  padding: 10px;
  max-height: 450px;
  overflow-y: auto;
}

/* 自定义滚动条样式 */
.level-limits-container::-webkit-scrollbar {
  width: 8px;
}

.level-limits-container::-webkit-scrollbar-track {
  background: #f1f1f1;
  border-radius: 4px;
}

.level-limits-container::-webkit-scrollbar-thumb {
  background: #c0c4cc;
  border-radius: 4px;
}

.level-limits-container::-webkit-scrollbar-thumb:hover {
  background: #909399;
}

.level-config-card {
  margin-bottom: 16px;
  padding: 16px;
  background: #f8f9fa;
  border-radius: 6px;
  border: 1px solid #e4e7ed;
  transition: all 0.3s;
}

.level-config-card:hover {
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
  border-color: #c0c4cc;
}

.level-header {
  margin-bottom: 12px;
  padding-bottom: 8px;
  border-bottom: 2px solid #e4e7ed;
}

.level-form {
  margin-top: 8px;
}

.level-form .el-form-item {
  margin-bottom: 12px;
}

.level-form .el-divider {
  margin: 12px 0;
}

.level-form .form-tip {
  margin-top: 2px;
}
</style>