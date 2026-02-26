// Provider 添加/编辑表单状态与逻辑
import { ref, reactive, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createProvider, updateProvider } from '@/api/admin'
import { countries, getCountriesByRegion } from '@/utils/countries'
import { useI18n } from 'vue-i18n'

// 默认等级限制
const DEFAULT_LEVEL_LIMITS = {
  1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 10240, bandwidth: 100 }, maxTraffic: 102400 },
  2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 20480, bandwidth: 200 }, maxTraffic: 204800 },
  3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 40960, bandwidth: 500 }, maxTraffic: 307200 },
  4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 81920, bandwidth: 1000 }, maxTraffic: 409600 },
  5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 163840, bandwidth: 2000 }, maxTraffic: 512000 }
}

// 解析等级限制配置（后端 kebab-case → 前端 camelCase）
export const parseLevelLimits = (levelLimitsStr) => {
  if (!levelLimitsStr) return JSON.parse(JSON.stringify(DEFAULT_LEVEL_LIMITS))
  try {
    const parsed = typeof levelLimitsStr === 'string' ? JSON.parse(levelLimitsStr) : levelLimitsStr
    const result = {}
    for (let i = 1; i <= 5; i++) {
      if (!parsed[i]) {
        result[i] = JSON.parse(JSON.stringify(DEFAULT_LEVEL_LIMITS[i]))
      } else {
        const levelData = parsed[i]
        const maxInstances = levelData.maxInstances ?? levelData['max-instances']
        const maxTraffic = levelData.maxTraffic ?? levelData['max-traffic']
        const maxResourcesData = levelData.maxResources ?? levelData['max-resources']
        result[i] = {
          maxInstances: maxInstances ?? DEFAULT_LEVEL_LIMITS[i].maxInstances,
          maxTraffic: maxTraffic ?? DEFAULT_LEVEL_LIMITS[i].maxTraffic,
          maxResources: {
            cpu: maxResourcesData?.cpu ?? DEFAULT_LEVEL_LIMITS[i].maxResources.cpu,
            memory: maxResourcesData?.memory ?? DEFAULT_LEVEL_LIMITS[i].maxResources.memory,
            disk: maxResourcesData?.disk ?? DEFAULT_LEVEL_LIMITS[i].maxResources.disk,
            bandwidth: maxResourcesData?.bandwidth ?? DEFAULT_LEVEL_LIMITS[i].maxResources.bandwidth
          }
        }
      }
    }
    return result
  } catch (e) {
    console.error('解析等级限制配置失败:', e)
    return JSON.parse(JSON.stringify(DEFAULT_LEVEL_LIMITS))
  }
}

// 转换等级限制配置为后端格式（前端 camelCase → 后端 kebab-case）
export const formatLevelLimitsForBackend = (levelLimits) => {
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
  maxTraffic: 1048576,
  trafficCountMode: 'both',
  trafficMultiplier: 1.0,
  trafficStatsMode: 'light',
  trafficCollectInterval: 60,
  trafficCollectBatchSize: 10,
  trafficLimitCheckInterval: 180,
  trafficLimitCheckBatchSize: 10,
  trafficAutoResetInterval: 1800,
  trafficAutoResetBatchSize: 10,
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
  redeemCodeOnly: false,
  discoverMode: false,
  autoImport: true,
  autoAdjustQuota: true,
  importedInstanceOwner: 'admin',
  levelLimits: JSON.parse(JSON.stringify(DEFAULT_LEVEL_LIMITS))
})

export function useProviderForm(loadProviders) {
  const { t } = useI18n()

  const showAddDialog = ref(false)
  const addProviderLoading = ref(false)
  const isEditing = ref(false)

  const addProviderForm = reactive(buildDefaultForm())

  const maxTrafficTB = computed({
    get: () => Number((addProviderForm.maxTraffic / TB_TO_MB).toFixed(3)),
    set: (value) => { addProviderForm.maxTraffic = Math.round(value * TB_TO_MB) }
  })

  const groupedCountries = ref(getCountriesByRegion())

  const getLevelTagType = (level) => {
    const types = { 1: 'info', 2: 'success', 3: 'warning', 4: 'danger', 5: 'primary' }
    return types[level] || 'info'
  }

  const resetLevelLimitsToDefault = () => {
    ElMessageBox.confirm(
      '确定要恢复所有等级的默认限制值吗？',
      '确认操作',
      { confirmButtonText: '确定', cancelButtonText: '取消', type: 'warning' }
    ).then(() => {
      addProviderForm.levelLimits = JSON.parse(JSON.stringify(DEFAULT_LEVEL_LIMITS))
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
    let host = provider.endpoint
    if (provider.endpoint?.includes(':')) {
      host = provider.endpoint.split(':')[0]
    }
    const port = provider.sshPort || 22
    const parsedLevelLimits = parseLevelLimits(provider.levelLimits)

    addProviderForm.levelLimits = parsedLevelLimits
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
    addProviderForm.containerPrivileged = provider.containerPrivileged !== undefined ? provider.containerPrivileged : false
    addProviderForm.containerAllowNesting = provider.containerAllowNesting !== undefined ? provider.containerAllowNesting : false
    addProviderForm.containerEnableLxcfs = provider.containerEnableLxcfs !== undefined ? provider.containerEnableLxcfs : true
    addProviderForm.containerCpuAllowance = provider.containerCpuAllowance || '100%'
    addProviderForm.containerMemorySwap = provider.containerMemorySwap !== undefined ? provider.containerMemorySwap : true
    addProviderForm.containerMaxProcesses = provider.containerMaxProcesses || 0
    addProviderForm.containerDiskIoLimit = provider.containerDiskIoLimit || ''
    addProviderForm.redeemCodeOnly = provider.redeemCodeOnly !== undefined ? provider.redeemCodeOnly : false

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

  const submitAddServer = async (formData) => {
    try {
      if (!formData.containerEnabled && !formData.vmEnabled) {
        ElMessage.warning(t('admin.providers.selectVirtualizationType'))
        return
      }
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
        levelLimits: formatLevelLimitsForBackend(formData.levelLimits || {}),
        containerPrivileged: formData.containerPrivileged || false,
        containerAllowNesting: formData.containerAllowNesting || false,
        containerEnableLxcfs: formData.containerEnableLxcfs !== undefined ? formData.containerEnableLxcfs : true,
        containerCpuAllowance: formData.containerCpuAllowance || '100%',
        discoverMode: formData.discoverMode !== undefined ? formData.discoverMode : false,
        autoImport: formData.discoverMode ? (formData.autoImport !== undefined ? formData.autoImport : true) : false,
        autoAdjustQuota: formData.discoverMode ? (formData.autoAdjustQuota !== undefined ? formData.autoAdjustQuota : true) : false,
        importedInstanceOwner: formData.discoverMode ? (formData.importedInstanceOwner || 'admin') : null,
        containerMemorySwap: formData.containerMemorySwap !== undefined ? formData.containerMemorySwap : true,
        containerMaxProcesses: formData.containerMaxProcesses || 0,
        containerDiskIoLimit: formData.containerDiskIoLimit || '',
        redeemCodeOnly: formData.redeemCodeOnly !== undefined ? formData.redeemCodeOnly : false
      }

      // 根据 Provider 类型设置端口映射方式
      if (formData.type === 'docker') {
        serverData.ipv4PortMappingMethod = 'native'
        serverData.ipv6PortMappingMethod = 'native'
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

      // 认证方式处理
      if (isEditing.value) {
        if (formData.authMethod === 'password' && formData.password) {
          serverData.password = formData.password
        } else if (formData.authMethod === 'sshKey' && formData.sshKey) {
          serverData.sshKey = formData.sshKey
        }
      } else {
        if (formData.authMethod === 'password') {
          serverData.password = formData.password
        } else if (formData.authMethod === 'sshKey') {
          serverData.sshKey = formData.sshKey
        }
      }

      if (isEditing.value) {
        await updateProvider(formData.id, serverData)
        ElMessage.success(t('admin.providers.serverUpdateSuccess'))
      } else {
        await createProvider(serverData)
        ElMessage.success(t('admin.providers.serverAddSuccess'))
      }

      cancelAddServer()
      await loadProviders()
    } catch (error) {
      console.error('Provider操作失败:', error)
      const errorMsg =
        error.response?.data?.msg ||
        error.message ||
        (isEditing.value
          ? t('admin.providers.serverUpdateFailed')
          : t('admin.providers.serverAddFailed'))
      ElMessage.error(errorMsg)
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
