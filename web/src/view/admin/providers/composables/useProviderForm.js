// Provider 添加/编辑表单状态与逻辑
import { ref, reactive, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createProvider, updateProvider } from '@/api/admin'
import { getCountriesByRegion } from '@/utils/countries'
import { extractEndpointHost } from '@/utils/endpoint'
import { useI18n } from 'vue-i18n'
import { DEFAULT_LEVEL_LIMITS, normalizeLevelLimits, formatLevelLimitsForBackend as formatLevels, getLevelTagType } from '@/utils/levels'
import { isContainerOnlyProvider, isVMOnlyProvider } from '@/utils/providerTypes'

// 解析等级限制配置（后端 kebab-case → 前端 camelCase）
export const parseLevelLimits = (levelLimitsStr) => {
  if (!levelLimitsStr) return normalizeLevelLimits(DEFAULT_LEVEL_LIMITS)
  try {
    const parsed = typeof levelLimitsStr === 'string' ? JSON.parse(levelLimitsStr) : levelLimitsStr
    return normalizeLevelLimits(parsed)
  } catch (e) {
    console.error('解析等级限制配置失败:', e)
    return normalizeLevelLimits(DEFAULT_LEVEL_LIMITS)
  }
}

// 转换等级限制配置为后端格式（前端 camelCase → 后端 kebab-case）
export const formatLevelLimitsForBackend = (levelLimits) => {
  return formatLevels(levelLimits)
}

const TB_TO_MB = 1048576

const buildDefaultForm = () => ({
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
  enableTrafficControl: false,
  enableResourceMonitoring: false,
  maxTraffic: 1048576,
  trafficCountMode: 'both',
  trafficMultiplier: 1.0,
  trafficSyncMethod: 'agent',
  trafficStatsMode: 'light',
  trafficCollectInterval: 60,
  trafficCollectBatchSize: 10,
  trafficLimitCheckInterval: 180,
  trafficLimitCheckBatchSize: 10,
  trafficAutoResetInterval: 1800,
  trafficAutoResetBatchSize: 10,
  trafficOverLimitAction: 'stop',
  trafficSpeedLimitKbps: 1024,
  trafficQuotaVisible: true,
  instanceExpiryAction: 'delete',
  instanceExpiryExtendDays: 1,
  ipv4PortMappingMethod: 'device_proxy',
  ipv6PortMappingMethod: 'device_proxy',
  executionRule: 'auto',
  sshConnectTimeout: 30,
  sshExecuteTimeout: 300,
  containerLimitCpu: false,
  containerLimitMemory: false,
  containerLimitDisk: true,
  vmLimitCpu: true,
  vmLimitMemory: true,
  vmLimitDisk: true,
  containerPrivileged: false,
  containerAllowNesting: true,
  containerEnableLxcfs: true,
  containerCpuAllowance: '100%',
  containerMemorySwap: true,
  containerMaxProcesses: 0,
  containerDiskIoLimit: '',
  containerReadIoLimit: '',
  containerWriteIoLimit: '',
  vmReadIoLimit: '',
  vmWriteIoLimit: '',
  redeemCodeOnly: false,
  discoverMode: false,
  autoImport: true,
  autoAdjustQuota: true,
  importedInstanceOwner: 'admin',
  gpuEnabled: false,
  gpuDeviceIds: '',
  connectionType: 'ssh',
  agentStatus: 'offline',
  agentRuntimeStatus: 'offline',
  agentLastSeen: null,
  agentConnectedAt: null,
  agentRemoteIP: '',
  agentControlLastSeen: null,
  agentExecLastSeen: null,
  levelLimits: normalizeLevelLimits(DEFAULT_LEVEL_LIMITS)
})

const hasAgentMappedNetworking = (formData) => Boolean(formData.portIP)

export function useProviderForm(loadProviders) {
  const { t, locale } = useI18n()

  const showAddDialog = ref(false)
  const addProviderLoading = ref(false)
  const isEditing = ref(false)

  const addProviderForm = reactive(buildDefaultForm())

  const maxTrafficTB = computed({
    get: () => Number((addProviderForm.maxTraffic / TB_TO_MB).toFixed(3)),
    set: (value) => { addProviderForm.maxTraffic = Math.round(value * TB_TO_MB) }
  })

  const groupedCountries = computed(() => getCountriesByRegion(locale.value?.startsWith('en') ? 'en' : 'zh'))

  const resetLevelLimitsToDefault = () => {
    ElMessageBox.confirm(
      t('admin.providers.restoreDefaultLimitsConfirm'),
      t('admin.providers.confirmOperation'),
      { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' }
    ).then(() => {
      addProviderForm.levelLimits = normalizeLevelLimits(DEFAULT_LEVEL_LIMITS)
      ElMessage.success(t('admin.providers.levelLimitsRestored'))
    }).catch(() => {})
  }

  const validateVirtualizationType = () => {
    if (!addProviderForm.containerEnabled && !addProviderForm.vmEnabled) {
      ElMessage.warning(t('admin.providers.selectVirtualizationType'))
      return false
    }
    return true
  }

  const cancelAddServer = () => {
    showAddDialog.value = false
    isEditing.value = false
    Object.assign(addProviderForm, buildDefaultForm())
  }

  const handleAddProvider = () => {
    isEditing.value = false
    cancelAddServer()
    showAddDialog.value = true
  }

  const editProvider = (provider) => {
    const parsedLevelLimits = parseLevelLimits(provider.levelLimits)

    addProviderForm.levelLimits = parsedLevelLimits
    addProviderForm.id = provider.id
    addProviderForm.name = provider.name
    const connectionType = provider.connectionType || 'ssh'
    addProviderForm.type = connectionType === 'local' ? 'qemu' : provider.type
    // Agent/本机模式不使用远程 SSH IP/端口，始终清空
    if (connectionType === 'agent' || connectionType === 'local') {
      addProviderForm.host = ''
      addProviderForm.port = 0
    } else {
      addProviderForm.host = extractEndpointHost(provider.endpoint)
      addProviderForm.port = provider.sshPort || 22
    }
    addProviderForm.portIP = provider.portIP || ''
    addProviderForm.username = connectionType === 'local' ? (provider.username || 'root') : (provider.username || '')
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
    // 强制修正：根据类型确保虚拟化类型正确
    if (isContainerOnlyProvider(provider.type)) {
      addProviderForm.containerEnabled = true
      addProviderForm.vmEnabled = false
    } else if (isVMOnlyProvider(provider.type)) {
      addProviderForm.containerEnabled = false
      addProviderForm.vmEnabled = true
    }
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
    addProviderForm.enableResourceMonitoring = provider.enableResourceMonitoring !== undefined ? provider.enableResourceMonitoring : false
    addProviderForm.maxTraffic = provider.maxTraffic || 1048576
    addProviderForm.trafficCountMode = provider.trafficCountMode || 'both'
    addProviderForm.trafficMultiplier = provider.trafficMultiplier || 1.0
    addProviderForm.trafficSyncMethod = provider.trafficSyncMethod || 'pmacct'
    addProviderForm.trafficStatsMode = provider.trafficStatsMode || 'light'
    addProviderForm.trafficCollectInterval = provider.trafficCollectInterval || 60
    addProviderForm.trafficCollectBatchSize = provider.trafficCollectBatchSize || 10
    addProviderForm.trafficLimitCheckInterval = provider.trafficLimitCheckInterval || 180
    addProviderForm.trafficLimitCheckBatchSize = provider.trafficLimitCheckBatchSize || 10
    addProviderForm.trafficAutoResetInterval = provider.trafficAutoResetInterval || 1800
    addProviderForm.trafficAutoResetBatchSize = provider.trafficAutoResetBatchSize || 10
    addProviderForm.trafficOverLimitAction = provider.trafficOverLimitAction || 'stop'
    addProviderForm.trafficSpeedLimitKbps = provider.trafficSpeedLimitKbps || 1024
    addProviderForm.trafficQuotaVisible = provider.trafficQuotaVisible !== undefined ? provider.trafficQuotaVisible : true
    addProviderForm.instanceExpiryAction = provider.instanceExpiryAction || 'delete'
    addProviderForm.instanceExpiryExtendDays = provider.instanceExpiryExtendDays || 1
    addProviderForm.executionRule = provider.executionRule || 'auto'
    addProviderForm.sshConnectTimeout = provider.sshConnectTimeout || 30
    addProviderForm.sshExecuteTimeout = provider.sshExecuteTimeout || 300
    addProviderForm.containerLimitCpu = provider.containerLimitCpu !== undefined ? provider.containerLimitCpu : false
    addProviderForm.containerLimitMemory = provider.containerLimitMemory !== undefined ? provider.containerLimitMemory : false
    addProviderForm.containerLimitDisk = provider.containerLimitDisk !== undefined ? provider.containerLimitDisk : true
    addProviderForm.vmLimitCpu = provider.vmLimitCpu !== undefined ? provider.vmLimitCpu : true
    addProviderForm.vmLimitMemory = provider.vmLimitMemory !== undefined ? provider.vmLimitMemory : true
    addProviderForm.vmLimitDisk = provider.vmLimitDisk !== undefined ? provider.vmLimitDisk : true
    addProviderForm.containerPrivileged = provider.containerPrivileged !== undefined ? provider.containerPrivileged : false
    addProviderForm.containerAllowNesting = provider.containerAllowNesting !== undefined ? provider.containerAllowNesting : false
    addProviderForm.containerEnableLxcfs = provider.containerEnableLxcfs !== undefined ? provider.containerEnableLxcfs : true
    addProviderForm.containerCpuAllowance = provider.containerCpuAllowance || '100%'
    addProviderForm.containerMemorySwap = provider.containerMemorySwap !== undefined ? provider.containerMemorySwap : true
    addProviderForm.containerMaxProcesses = provider.containerMaxProcesses || 0
    addProviderForm.containerDiskIoLimit = provider.containerDiskIoLimit || ''
    addProviderForm.containerReadIoLimit = provider.containerReadIoLimit || ''
    addProviderForm.containerWriteIoLimit = provider.containerWriteIoLimit || ''
    addProviderForm.vmReadIoLimit = provider.vmReadIoLimit || ''
    addProviderForm.vmWriteIoLimit = provider.vmWriteIoLimit || ''
    addProviderForm.redeemCodeOnly = provider.redeemCodeOnly !== undefined ? provider.redeemCodeOnly : false
    addProviderForm.gpuEnabled = provider.gpuEnabled !== undefined ? provider.gpuEnabled : false
    addProviderForm.gpuDeviceIds = provider.gpuDeviceIds || ''
    // Proxmox 网桥配置
    addProviderForm.nodeInstallType = provider.nodeInstallType || 'script'
    addProviderForm.bridgeNAT = provider.bridgeNAT || ''
    addProviderForm.bridgeDedicatedV4 = provider.bridgeDedicatedV4 || ''
    addProviderForm.bridgeDedicatedV6 = provider.bridgeDedicatedV6 || ''
    addProviderForm.natSubnet = provider.natSubnet || ''
    addProviderForm.connectionType = connectionType
    addProviderForm.agentStatus = provider.agentStatus || 'offline'
    addProviderForm.agentRuntimeStatus = provider.agentRuntimeStatus || provider.agentStatus || 'offline'
    addProviderForm.agentLastSeen = provider.agentLastSeen || null
    addProviderForm.agentConnectedAt = provider.agentConnectedAt || null
    addProviderForm.agentRemoteIP = provider.agentRemoteIP || ''
    addProviderForm.agentControlLastSeen = provider.agentControlLastSeen || null
    addProviderForm.agentExecLastSeen = provider.agentExecLastSeen || null
    // 实例发现与导入配置
    addProviderForm.discoverMode = provider.pendingDiscovery !== undefined ? provider.pendingDiscovery : false
    addProviderForm.autoImport = provider.discoveryAutoImport !== undefined ? provider.discoveryAutoImport : true
    addProviderForm.autoAdjustQuota = provider.discoveryAutoAdjust !== undefined ? provider.discoveryAutoAdjust : true
    addProviderForm.importedInstanceOwner = provider.discoveryOwnerName || provider.discoveryOwnerUserId ? 'admin' : ''

    if (isContainerOnlyProvider(provider.type)) {
      addProviderForm.ipv4PortMappingMethod = 'native'
      addProviderForm.ipv6PortMappingMethod = 'native'
    } else if (isVMOnlyProvider(provider.type) || provider.type === 'qemu') {
      addProviderForm.ipv4PortMappingMethod = provider.ipv4PortMappingMethod || 'iptables'
      addProviderForm.ipv6PortMappingMethod = provider.ipv6PortMappingMethod || 'iptables'
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

    if (connectionType === 'local') {
      addProviderForm.type = 'qemu'
      addProviderForm.containerEnabled = provider.container_enabled !== undefined ? Boolean(provider.container_enabled) : true
      addProviderForm.vmEnabled = provider.vm_enabled !== undefined ? Boolean(provider.vm_enabled) : true
      if (!addProviderForm.containerEnabled && !addProviderForm.vmEnabled) {
        addProviderForm.containerEnabled = true
        addProviderForm.vmEnabled = true
      }
      addProviderForm.networkType = provider.networkType || 'nat_ipv4'
      addProviderForm.ipv4PortMappingMethod = provider.ipv4PortMappingMethod || 'iptables'
      addProviderForm.ipv6PortMappingMethod = provider.ipv6PortMappingMethod || 'native'
      addProviderForm.storagePool = provider.storagePool || 'local'
    }

    isEditing.value = true
    showAddDialog.value = true
  }

  const submitAddServer = async (formData) => {
    try {
      if (!formData.containerEnabled && !formData.vmEnabled) {
        ElMessage.warning(t('admin.providers.selectVirtualizationType'))
        return null
      }
      // agent 模式新增：不需要 SSH 凭据
      if (!isEditing.value && formData.connectionType !== 'agent' && formData.connectionType !== 'local') {
        if (formData.authMethod === 'password' && !formData.password) {
          ElMessage.error(t('admin.providers.passwordRequired'))
          return null
        }
        if (formData.authMethod === 'sshKey' && !formData.sshKey) {
          ElMessage.error(t('admin.providers.sshKeyRequired'))
          return null
        }
      }

      const isAgentMode = formData.connectionType === 'agent'
      const isLocalMode = formData.connectionType === 'local'
      const agentCanUseMappedNetworking = hasAgentMappedNetworking(formData)

      addProviderLoading.value = true

      const serverData = {
        name: formData.name,
        type: isLocalMode ? 'qemu' : formData.type,
        endpoint: (isAgentMode || isLocalMode) ? (isLocalMode ? '127.0.0.1' : '') : (formData.host || ''),
        portIP: formData.portIP || '',
        sshPort: (isAgentMode || isLocalMode) ? 0 : (formData.port || 22),
        username: isLocalMode ? (formData.username || 'root') : (isAgentMode ? (formData.username || '') : formData.username),
        config: '',
        region: formData.region,
        country: formData.country,
        countryCode: formData.countryCode,
        city: formData.city,
        container_enabled: formData.containerEnabled,
        vm_enabled: formData.vmEnabled,
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
        networkType: isAgentMode
          ? (agentCanUseMappedNetworking ? (formData.networkType || 'nat_ipv4') : 'no_port_mapping')
          : (formData.networkType || 'nat_ipv4'),
        defaultInboundBandwidth: formData.defaultInboundBandwidth || 300,
        defaultOutboundBandwidth: formData.defaultOutboundBandwidth || 300,
        maxInboundBandwidth: formData.maxInboundBandwidth || 1000,
        maxOutboundBandwidth: formData.maxOutboundBandwidth || 1000,
        containerReadIoLimit: formData.containerReadIoLimit || '',
        containerWriteIoLimit: formData.containerWriteIoLimit || '',
        vmReadIoLimit: formData.vmReadIoLimit || '',
        vmWriteIoLimit: formData.vmWriteIoLimit || '',
        enableTrafficControl: isAgentMode ? true : (formData.enableTrafficControl !== undefined ? formData.enableTrafficControl : false),
        enableResourceMonitoring: isAgentMode ? true : (formData.enableResourceMonitoring !== undefined ? formData.enableResourceMonitoring : false),
        maxTraffic: formData.maxTraffic || 1048576,
        trafficCountMode: formData.trafficCountMode || 'both',
        trafficMultiplier: formData.trafficMultiplier !== undefined && formData.trafficMultiplier !== null ? formData.trafficMultiplier : 1.0,
        trafficSyncMethod: isAgentMode ? 'agent' : (formData.trafficSyncMethod || 'pmacct'),
        trafficStatsMode: formData.trafficStatsMode || 'light',
        trafficCollectInterval: formData.trafficCollectInterval || 60,
        trafficCollectBatchSize: formData.trafficCollectBatchSize || 10,
        trafficLimitCheckInterval: formData.trafficLimitCheckInterval || 180,
        trafficLimitCheckBatchSize: formData.trafficLimitCheckBatchSize || 10,
        trafficAutoResetInterval: formData.trafficAutoResetInterval || 1800,
        trafficAutoResetBatchSize: formData.trafficAutoResetBatchSize || 10,
        trafficOverLimitAction: formData.trafficOverLimitAction || 'stop',
        trafficSpeedLimitKbps: formData.trafficSpeedLimitKbps || 1024,
        trafficQuotaVisible: formData.trafficQuotaVisible !== undefined ? formData.trafficQuotaVisible : true,
        instanceExpiryAction: formData.instanceExpiryAction || 'delete',
        instanceExpiryExtendDays: formData.instanceExpiryExtendDays || 1,
        executionRule: formData.executionRule || 'auto',
        sshConnectTimeout: formData.sshConnectTimeout || 30,
        sshExecuteTimeout: formData.sshExecuteTimeout || 300,
        containerLimitCpu: formData.containerLimitCpu !== undefined ? formData.containerLimitCpu : false,
        containerLimitMemory: formData.containerLimitMemory !== undefined ? formData.containerLimitMemory : false,
        containerLimitDisk: formData.containerLimitDisk !== undefined ? formData.containerLimitDisk : true,
        vmLimitCpu: formData.vmLimitCpu !== undefined ? formData.vmLimitCpu : true,
        vmLimitMemory: formData.vmLimitMemory !== undefined ? formData.vmLimitMemory : true,
        vmLimitDisk: formData.vmLimitDisk !== undefined ? formData.vmLimitDisk : true,
        levelLimits: formatLevelLimitsForBackend(formData.levelLimits || {}),
        containerPrivileged: formData.containerPrivileged !== undefined ? formData.containerPrivileged : false,
        containerAllowNesting: formData.containerAllowNesting !== undefined ? formData.containerAllowNesting : false,
        containerEnableLxcfs: formData.containerEnableLxcfs !== undefined ? formData.containerEnableLxcfs : true,
        containerCpuAllowance: formData.containerCpuAllowance || '100%',
        discoverMode: formData.discoverMode !== undefined ? formData.discoverMode : false,
        autoImport: formData.discoverMode ? (formData.autoImport !== undefined ? formData.autoImport : true) : false,
        autoAdjustQuota: formData.discoverMode ? (formData.autoAdjustQuota !== undefined ? formData.autoAdjustQuota : true) : false,
        importedInstanceOwner: formData.discoverMode ? (formData.importedInstanceOwner || 'admin') : null,
        containerMemorySwap: formData.containerMemorySwap !== undefined ? formData.containerMemorySwap : true,
        containerMaxProcesses: formData.containerMaxProcesses || 0,
        containerDiskIoLimit: formData.containerDiskIoLimit || '',
        redeemCodeOnly: formData.redeemCodeOnly !== undefined ? formData.redeemCodeOnly : false,
        gpuEnabled: formData.gpuEnabled !== undefined ? formData.gpuEnabled : false,
        gpuDeviceIds: formData.gpuDeviceIds || '',
        nodeInstallType: formData.nodeInstallType || 'script',
        bridgeNAT: formData.bridgeNAT || '',
        bridgeDedicatedV4: formData.bridgeDedicatedV4 || '',
        bridgeDedicatedV6: formData.bridgeDedicatedV6 || '',
        natSubnet: formData.natSubnet || '',
        connectionType: formData.connectionType || 'ssh'
      }

      // 根据 Provider 类型设置端口映射方式
      if (isContainerOnlyProvider(formData.type)) {
        serverData.ipv4PortMappingMethod = 'native'
        serverData.ipv6PortMappingMethod = 'native'
      } else if (isVMOnlyProvider(formData.type) || formData.type === 'qemu') {
        serverData.ipv4PortMappingMethod = formData.ipv4PortMappingMethod || 'iptables'
        serverData.ipv6PortMappingMethod = formData.ipv6PortMappingMethod || 'iptables'
      } else if (formData.type === 'proxmox') {
        if (formData.networkType === 'nat_ipv4' || formData.networkType === 'nat_ipv4_ipv6') {
          serverData.ipv4PortMappingMethod = formData.ipv4PortMappingMethod || 'iptables'
        } else {
          serverData.ipv4PortMappingMethod = formData.ipv4PortMappingMethod || 'native'
        }
        serverData.ipv6PortMappingMethod = formData.ipv6PortMappingMethod || 'native'
      } else if (['lxd', 'incus'].includes(formData.type)) {
        serverData.ipv4PortMappingMethod = formData.ipv4PortMappingMethod || 'device_proxy'
        serverData.ipv6PortMappingMethod = formData.ipv6PortMappingMethod || 'device_proxy'
      }

      // 无端口映射能力的 agent 模式：清除端口映射方法
      if (isAgentMode && !agentCanUseMappedNetworking) {
        serverData.ipv4PortMappingMethod = ''
        serverData.ipv6PortMappingMethod = ''
      }
      if (isLocalMode) {
        serverData.ipv4PortMappingMethod = formData.ipv4PortMappingMethod || 'iptables'
        serverData.ipv6PortMappingMethod = formData.ipv6PortMappingMethod || 'native'
      }

      // 认证方式处理（独立于端口映射方法清除，避免被 if-else 误跳过）
      if (isEditing.value) {
        // 编辑模式：仅当用户主动填写了密码/密钥时才发送（空值表示"不修改"）
        if (formData.authMethod === 'password' && formData.password) {
          serverData.password = formData.password
        } else if (formData.authMethod === 'sshKey' && formData.sshKey) {
          serverData.sshKey = formData.sshKey
        }
      } else {
        // 创建模式：密码/密钥必须提供（上方的校验已确保非 agent/local 模式必填）
        if (isLocalMode) {
          // 本机模式不需要 SSH 认证信息
        } else if (formData.authMethod === 'password') {
          serverData.password = formData.password
        } else if (formData.authMethod === 'sshKey') {
          serverData.sshKey = formData.sshKey
        }
      }

      if (isEditing.value) {
        await updateProvider(formData.id, serverData)
        ElMessage.success(t('admin.providers.serverUpdateSuccess'))
        cancelAddServer()
        await loadProviders()
        return { success: true, isEditing: true }
      } else {
        const resp = await createProvider(serverData)
        const newId = resp.data?.id || resp.data?.ID
        // agent 模式：不关闭对话框，留在连接配置页让用户生成安装命令
        if (formData.connectionType === 'agent' && newId) {
          ElMessage.success(t('admin.providers.serverAddSuccess'))
          await loadProviders()
          return { success: true, isEditing: false, agentMode: true, newId }
        }
        ElMessage.success(t('admin.providers.serverAddSuccess'))
        cancelAddServer()
        await loadProviders()
        return { success: true, isEditing: false, agentMode: false }
      }
    } catch (error) {
      console.error('Provider操作失败:', error)
      const errorMsg =
        error.response?.data?.msg ||
        error.message ||
        (isEditing.value
          ? t('admin.providers.serverUpdateFailed')
          : t('admin.providers.serverAddFailed'))
      ElMessage.error(errorMsg)
      return null
    } finally {
      addProviderLoading.value = false
    }
  }

  return {
    showAddDialog,
    addProviderLoading,
    isEditing,
    addProviderForm,
    maxTrafficTB,
    groupedCountries,
    getLevelTagType,
    resetLevelLimitsToDefault,
    validateVirtualizationType,
    cancelAddServer,
    handleAddProvider,
    editProvider,
    submitAddServer
  }
}
