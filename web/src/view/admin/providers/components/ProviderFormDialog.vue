<template>
  <el-dialog 
    v-model="dialogVisible" 
    :title="isEditing ? $t('admin.providers.editServer') : $t('admin.providers.addServer')" 
    width="1000px"
    :close-on-click-modal="false"
    :before-close="handleBeforeClose"
  >
    <!-- 配置分类标签页 -->
    <el-tabs
      v-model="activeTab"
      type="border-card"
      class="server-config-tabs"
      :lazy="false"
    >
      <!-- 基本信息 -->
      <el-tab-pane
        :label="$t('admin.providers.basicInfo')"
        name="basic"
      >
        <BasicInfoTab
          ref="basicInfoTabRef"
          v-model="formData"
          :rules="rules"
        />
      </el-tab-pane>

      <!-- 连接配置 -->
      <el-tab-pane
        :label="$t('admin.providers.connectionConfig')"
        name="connection"
      >
        <ConnectionTab
          v-model="formData"
          :is-editing="isEditing"
          :testing-connection="testingConnection"
          :connection-test-result="connectionTestResult"
          :generating-secret="generatingSecret"
          :agent-connect-cmd="agentConnectCmd"
          :agent-connect-cmd-github="agentConnectCmdGithub"
          :exec-loading="execLoading"
          :exec-result="execResult"
          :checking-agent-status="checkingAgentStatus"
          @test-connection="handleTestConnection"
          @apply-timeout="handleApplyTimeout"
          @auth-method-change="handleAuthMethodChange"
          @generate-agent-secret="handleGenerateAgentSecret"
          @check-agent-status="handleCheckAgentStatus"
          @exec-command="handleExecCommand"
          @clear-exec-result="execResult = null"
        />
      </el-tab-pane>

      <!-- 地理位置 -->
      <el-tab-pane
        :label="$t('admin.providers.location')"
        name="location"
      >
        <LocationTab
          v-model="formData"
          :grouped-countries="groupedCountries"
        />
      </el-tab-pane>

      <!-- 虚拟化配置 -->
      <el-tab-pane
        :label="$t('admin.providers.virtualizationConfig')"
        name="virtualization"
      >
        <VirtualizationTab
          v-model="formData"
        />
      </el-tab-pane>

      <!-- IP映射配置 -->
      <el-tab-pane
        :label="$t('admin.providers.ipMappingConfig')"
        name="mapping"
      >
        <MappingTab
          v-model="formData"
        />
      </el-tab-pane>

      <!-- 带宽配置 -->
      <el-tab-pane
        :label="$t('admin.providers.bandwidthConfig')"
        name="bandwidth"
      >
        <BandwidthTab
          v-model="formData"
        />
      </el-tab-pane>

      <!-- 等级限制配置 -->
      <el-tab-pane
        :label="$t('admin.providers.levelLimits')"
        name="levelLimits"
      >
        <LevelLimitsTab
          v-model="formData"
          @reset-defaults="handleResetLevelLimits"
        />
      </el-tab-pane>

      <!-- 高级设置 -->
      <el-tab-pane
        :label="$t('admin.providers.advancedSettings')"
        name="advanced"
      >
        <AdvancedTab
          v-model="formData"
        />
      </el-tab-pane>

      <!-- 硬件配置（LXD/Incus 容器和虚拟机） -->
      <el-tab-pane
        v-if="showHardwareConfigTab"
        :label="$t('admin.providers.hardwareConfig')"
        name="hardwareConfig"
      >
        <HardwareConfigTab
          v-model="formData"
        />
      </el-tab-pane>

      <!-- 签到续期配置 -->
      <el-tab-pane
        v-if="isEditing && formData.id"
        :label="$t('admin.providers.checkinConfig')"
        name="checkin"
      >
        <CheckinConfigTab
          :provider-id="formData.id"
        />
      </el-tab-pane>
    </el-tabs>
    
    <template #footer>
      <span class="dialog-footer">
        <el-button @click="handleClose">{{ $t('common.cancel') }}</el-button>
        <el-button
          type="primary"
          :loading="loading"
          @click="handleSubmit"
        >{{ $t('common.save') }}</el-button>
      </span>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, computed, watch, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getCountriesByRegion, getCountryByName, getLocalizedRegion } from '@/utils/countries'
import { testSSHConnection as testSSHConnectionAPI, generateAgentSecret as generateAgentSecretAPI, execOnProvider as execOnProviderAPI, getProviderDetail, checkProviderHealth as checkProviderHealthAPI } from '@/api/admin'
// 导入子标签页组件
import BasicInfoTab from './formTabs/BasicInfoTab.vue'
import ConnectionTab from './formTabs/ConnectionTab.vue'
import LocationTab from './formTabs/LocationTab.vue'
import VirtualizationTab from './formTabs/VirtualizationTab.vue'
import MappingTab from './formTabs/MappingTab.vue'
import BandwidthTab from './formTabs/BandwidthTab.vue'
import LevelLimitsTab from './formTabs/LevelLimitsTab.vue'
import AdvancedTab from './formTabs/AdvancedTab.vue'
import HardwareConfigTab from './formTabs/HardwareConfigTab.vue'
import CheckinConfigTab from './formTabs/CheckinConfigTab.vue'

const { t, locale } = useI18n()

// 表单初始快照（用于脖测未保存更改）
const formSnapshot = ref(null)

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  isEditing: {
    type: Boolean,
    default: false
  },
  providerData: {
    type: Object,
    default: () => ({})
  },
  groupedCountries: {
    type: Object,
    default: () => ({})
  },
  loading: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:visible', 'submit', 'cancel', 'reset-level-limits'])

// 对话框显示状态
const dialogVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

// 当前激活的标签页
const activeTab = ref('basic')

// BasicInfoTab 组件引用（用于获取表单引用）
const basicInfoTabRef = ref()

// 连接测试状态
const testingConnection = ref(false)
const connectionTestResult = ref(null)

// Agent 密钥生成状态
const generatingSecret = ref(false)
const checkingAgentStatus = ref(false)
const agentConnectCmd = ref('')
const agentConnectCmdGithub = ref('')

const isAgentMode = computed(() => formData.value.connectionType === 'agent')

const hasAgentMappedNetworking = computed(() => Boolean(formData.value.host && formData.value.portIP))

// 国家列表数据 - 使用 computed 从 props 获取，如果没有则使用本地获取
const groupedCountries = computed(() => {
  if (props.groupedCountries && Object.keys(props.groupedCountries).length > 0) {
    return props.groupedCountries
  }
  return getCountriesByRegion(locale.value === 'en' ? 'en' : 'zh')
})

// 是否显示硬件配置标签页（LXD/Incus 的容器或虚拟机都可以配置硬件参数）
const showHardwareConfigTab = computed(() => {
  const type = formData.value.type
  return type === 'lxd' || type === 'incus'
})

// 表单数据
const formData = ref({
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
  redeemCodeOnly: false,
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
  },
  // 容器特殊配置选项（仅 LXD/Incus 容器）
  containerPrivileged: false,
  containerAllowNesting: false,
  containerEnableLxcfs: true,
  containerCpuAllowance: '100%',
  containerMemorySwap: true,
  containerMaxProcesses: 0,
  containerDiskIoLimit: '',
  // GPU 直通配置（仅 LXD/Incus）
  gpuEnabled: false,
  gpuDeviceIds: '',
  // 连接方式（内网穿透）
  connectionType: 'ssh',
  agentStatus: 'offline',
  agentRuntimeStatus: 'offline',
  agentLastSeen: null,
  agentConnectedAt: null,
  agentRemoteIP: '',
  agentControlLastSeen: null,
  agentExecLastSeen: null,
  // Proxmox 网桥配置
  nodeInstallType: 'script',
  bridgeNAT: '',
  bridgeDedicatedV4: '',
  bridgeDedicatedV6: '',
  natSubnet: ''
})

// 异步验证器：检查Provider名称是否已存在
const validateProviderName = async (rule, value, callback) => {
  if (!value) {
    callback()
    return
  }
  
  try {
    const { checkProviderNameExists } = await import('@/api/admin')
    const excludeId = props.isEditing ? formData.value.id : null
    const response = await checkProviderNameExists(value, excludeId)
    
    if (response.data.exists) {
      callback(new Error(t('admin.providers.validation.nameAlreadyExists')))
    } else {
      callback()
    }
  } catch (error) {
    // 网络错误时不阻止提交，只在后端再次验证
    console.warn('检查Provider名称失败:', error)
    callback()
  }
}

// 异步验证器：检查SSH地址和端口是否已存在
const validateEndpoint = async (rule, value, callback) => {
  if (isAgentMode.value) {
    callback()
    return
  }
  if (!formData.value.host || !formData.value.port) {
    callback()
    return
  }
  
  try {
    const { checkProviderEndpointExists } = await import('@/api/admin')
    const excludeId = props.isEditing ? formData.value.id : null
    const response = await checkProviderEndpointExists(
      formData.value.host, 
      formData.value.port, 
      excludeId
    )
    
    if (response.data.exists) {
      callback(new Error(t('admin.providers.validation.endpointAlreadyExists')))
    } else {
      callback()
    }
  } catch (error) {
    // 网络错误时不阻止提交，只在后端再次验证
    console.warn('检查SSH地址失败:', error)
    callback()
  }
}

// 表单验证规则
const rules = {
  name: [
    { required: true, message: () => t('admin.providers.validation.serverNameRequired'), trigger: 'blur' },
    { pattern: /^[a-zA-Z0-9]+$/, message: () => t('admin.providers.validation.serverNamePattern'), trigger: 'blur' },
    { max: 7, message: () => t('admin.providers.validation.serverNameMaxLength'), trigger: 'blur' },
    { validator: validateProviderName, trigger: 'blur' }
  ],
  type: [
    { required: true, message: () => t('admin.providers.validation.serverTypeRequired'), trigger: 'change' }
  ],
  host: [
    {
      validator: (rule, value, callback) => {
        if (isAgentMode.value || value) {
          callback()
          return
        }
        callback(new Error(t('admin.providers.validation.hostRequired')))
      },
      trigger: 'blur'
    },
    { validator: validateEndpoint, trigger: 'blur' }
  ],
  port: [
    {
      validator: (rule, value, callback) => {
        if (isAgentMode.value || value) {
          callback()
          return
        }
        callback(new Error(t('admin.providers.validation.portRequired')))
      },
      trigger: 'blur'
    },
    { validator: validateEndpoint, trigger: 'blur' }
  ],
  username: [
    {
      validator: (rule, value, callback) => {
        if (isAgentMode.value || value) {
          callback()
          return
        }
        callback(new Error(t('admin.providers.validation.usernameRequired')))
      },
      trigger: 'blur'
    }
  ],
  architecture: [
    { required: true, message: () => t('admin.providers.validation.architectureRequired'), trigger: 'change' }
  ],
  status: [
    { required: true, message: () => t('admin.providers.validation.statusRequired'), trigger: 'change' }
  ],
  trafficCollectInterval: [
    { 
      validator: (rule, value, callback) => {
        if (value && (value < 10 || value > 300)) {
          callback(new Error(t('admin.providers.validation.trafficCollectIntervalRange')))
        } else {
          callback()
        }
      }, 
      trigger: 'blur' 
    }
  ]
}

// 监听 providerData 变化，更新表单数据
// 只在对话框首次打开时同步数据，避免用户编辑过程中被覆盖
watch(() => props.visible, (isVisible) => {
  if (isVisible) {
    if (props.providerData && Object.keys(props.providerData).length > 0) {
      // 对话框打开时，同步父组件的数据到表单（使用深拷贝避免引用问题）
      Object.assign(formData.value, JSON.parse(JSON.stringify(props.providerData)))
    }    // 记录开屏快照用于脖测修改
    formSnapshot.value = JSON.stringify(formData.value)    // 清除上次遗留的验证状态
    nextTick(() => {
      basicInfoTabRef.value?.formRef?.clearValidate()
    })
  }
}, { immediate: true })

watch(() => [formData.value.connectionType, formData.value.host, formData.value.portIP], ([connectionType]) => {
  if (connectionType !== 'agent') {
    return
  }
  if (!hasAgentMappedNetworking.value) {
    formData.value.networkType = 'no_port_mapping'
  }
}, { immediate: true })

// 监听 isEditing 变化：agent 模式切到连接配置页，但不自动生成密钥（避免覆盖旧连接）
watch(() => props.isEditing, async (newEditing, oldEditing) => {
  if (newEditing && !oldEditing && props.providerData?.connectionType === 'agent' && props.providerData?.id) {
    Object.assign(formData.value, JSON.parse(JSON.stringify(props.providerData)))
    formSnapshot.value = JSON.stringify(formData.value)
    activeTab.value = 'connection'
  }
})

// 监听国家选择变化，自动填充国家代码和地区
watch(() => formData.value.country, (newCountry, oldCountry) => {
  // 只在国家真正变化时处理
  if (newCountry && newCountry !== oldCountry) {
    const country = getCountryByName(newCountry)
    if (country) {
      // 更新国家代码
      formData.value.countryCode = country.code
      
      // 自动填充地区（如果地区为空，或者地区与旧国家的地区相同）
      const oldCountryInfo = oldCountry ? getCountryByName(oldCountry) : null
      if (!formData.value.region || (oldCountryInfo && (oldCountryInfo.region === formData.value.region || oldCountryInfo.en_region === formData.value.region))) {
        formData.value.region = getLocalizedRegion(country, locale.value)
      }
    }
  }
})

// 监听 Provider 类型变化，自动设置虚拟化类型
watch(() => formData.value.type, (newType) => {
  if (!newType) return
  if (['docker', 'podman', 'containerd'].includes(newType)) {
    // 容器类型：仅支持容器
    formData.value.containerEnabled = true
    formData.value.vmEnabled = false
  } else if (['qemu', 'kubevirt'].includes(newType)) {
    // VM 专用类型：仅支持虚拟机
    formData.value.containerEnabled = false
    formData.value.vmEnabled = true
  }
  // lxd/incus/proxmox: 两者都可选，不自动修改
})

// 监听对话框关闭，重置表单
watch(() => props.visible, (val) => {
  if (!val) {
    activeTab.value = 'basic'
    connectionTestResult.value = null
  }
})

// 测试SSH连接
const handleTestConnection = async () => {
  if (!formData.value.host || !formData.value.username) {
    ElMessage.warning(t('admin.providers.fillHostUserPassword'))
    return
  }

  if (formData.value.authMethod === 'password' && !formData.value.password) {
    ElMessage.warning(t('admin.providers.fillHostUserPassword'))
    return
  }

  if (formData.value.authMethod === 'sshKey' && !formData.value.sshKey) {
    ElMessage.warning(t('admin.providers.fillSSHKey'))
    return
  }

  testingConnection.value = true
  connectionTestResult.value = null

  try {
    const requestData = {
      host: formData.value.host,
      port: formData.value.port || 22,
      username: formData.value.username,
      testCount: 3
    }

    if (formData.value.authMethod === 'password') {
      requestData.password = formData.value.password
    } else if (formData.value.authMethod === 'sshKey') {
      requestData.sshKey = formData.value.sshKey
    }

    const result = await testSSHConnectionAPI(requestData)

    if ((result.code === 200) && result.data.success) {
      connectionTestResult.value = {
        success: true,
        title: t('admin.providers.sshTestSuccess'),
        type: 'success',
        minLatency: result.data.minLatency,
        maxLatency: result.data.maxLatency,
        avgLatency: result.data.avgLatency,
        recommendedTimeout: result.data.recommendedTimeout
      }
      ElMessage.success(t('admin.providers.sshTestSuccess'))
    } else {
      connectionTestResult.value = {
        success: false,
        title: t('admin.providers.sshTestFailed'),
        type: 'error',
        error: result.data.errorMessage || result.msg || t('admin.providers.connectionFailed')
      }
      ElMessage.error(t('admin.providers.sshTestFailed') + ': ' + (result.data.errorMessage || result.msg))
    }
  } catch (error) {
    connectionTestResult.value = {
      success: false,
      title: t('admin.providers.sshTestFailed'),
      type: 'error',
      error: error.message || t('admin.providers.networkRequestFailed')
    }
    ElMessage.error(t('admin.providers.testFailed') + ': ' + error.message)
  } finally {
    testingConnection.value = false
  }
}

// 应用推荐的超时值
const handleApplyTimeout = () => {
  if (connectionTestResult.value && connectionTestResult.value.success) {
    formData.value.sshConnectTimeout = connectionTestResult.value.recommendedTimeout
    formData.value.sshExecuteTimeout = Math.max(300, connectionTestResult.value.recommendedTimeout * 10)
    ElMessage.success(t('admin.providers.timeoutApplied'))
  }
}

// 认证方式切换处理
const handleAuthMethodChange = (newMethod) => {
  if (newMethod === 'password') {
    formData.value.sshKey = ''
  } else if (newMethod === 'sshKey') {
    formData.value.password = ''
  }
}

// 生成/查看 Agent 密钥（密钥一旦创建即写死，不支持刷新）
const handleGenerateAgentSecret = async () => {
  if (!formData.value.id) return
  generatingSecret.value = true
  agentConnectCmd.value = ''
  agentConnectCmdGithub.value = ''
  try {
    const res = await generateAgentSecretAPI(formData.value.id)
    if (!res.data?.installCmdController || !res.data?.installCmdGithub) {
      throw new Error(t('common.operationFailed'))
    }
    agentConnectCmd.value = res.data.installCmdController
    agentConnectCmdGithub.value = res.data.installCmdGithub
    if (res.data.isExisting) {
      ElMessage.info(t('admin.providers.agentSecretExists'))
      ElMessageBox.alert(
        t('admin.providers.agentSecretUpdatedHint'),
        t('admin.providers.agentSecretUpdated'),
        { confirmButtonText: t('common.ok'), type: 'info' }
      )
    } else {
      ElMessage.success(t('admin.providers.agentSecretGenerated'))
    }
  } catch (error) {
    ElMessage.error(error.message || t('common.operationFailed'))
  } finally {
    generatingSecret.value = false
  }
}

// Web 终端执行命令
const execLoading = ref(false)
const execResult = ref(null)

const handleExecCommand = async (command) => {
  if (!command || !command.trim() || !formData.value.id) return
  execLoading.value = true
  try {
    const res = await execOnProviderAPI(formData.value.id, command.trim())
    execResult.value = {
      stdout: res.data?.stdout || '',
      stderr: res.data?.stderr || ''
    }
  } catch (error) {
    const rawMessage = error?.message || t('common.operationFailed')
    const looksLikeConnectionError =
      rawMessage.includes('执行命令超时') ||
      rawMessage.includes('Agent 节点未连接') ||
      rawMessage.includes('agent not connected') ||
      rawMessage.includes('connection')

    // 连接类错误时自动刷新一次 Agent 状态，避免“假在线”误导。
    if (formData.value.connectionType === 'agent' && looksLikeConnectionError) {
      try {
        await handleCheckAgentStatus()
      } catch (_) {
        // 忽略状态刷新错误，保留原始执行错误展示
      }
    }

    execResult.value = {
      stdout: '',
      stderr: looksLikeConnectionError
        ? `${rawMessage}\n\n提示：已自动刷新 Agent 状态，请等待节点重连后重试命令。`
        : rawMessage
    }
  } finally {
    execLoading.value = false
  }
}

// 检测 Agent 连接状态
const handleCheckAgentStatus = async () => {
  if (!formData.value.id) return
  checkingAgentStatus.value = true
  try {
    await checkProviderHealthAPI(formData.value.id, { forceRefresh: true })
    const res = await getProviderDetail(formData.value.id)
    if (res.data) {
      Object.assign(formData.value, {
        host: res.data.endpoint || formData.value.host,
        portIP: res.data.portIP || formData.value.portIP,
        port: res.data.sshPort || formData.value.port,
        agentStatus: res.data.agentStatus || 'offline',
        agentRuntimeStatus: res.data.agentRuntimeStatus || res.data.agentStatus || 'offline',
        agentLastSeen: res.data.agentLastSeen || null,
        agentConnectedAt: res.data.agentConnectedAt || null,
        agentRemoteIP: res.data.agentRemoteIP || '',
        agentControlLastSeen: res.data.agentControlLastSeen || null,
        agentExecLastSeen: res.data.agentExecLastSeen || null,
        networkType: res.data.networkType || formData.value.networkType,
        enableTrafficControl: res.data.enableTrafficControl ?? formData.value.enableTrafficControl,
        enableResourceMonitoring: res.data.enableResourceMonitoring ?? formData.value.enableResourceMonitoring
      })
      if ((formData.value.agentRuntimeStatus || formData.value.agentStatus) === 'online') {
        ElMessage.success(t('admin.providers.agentConnected'))
      } else if ((formData.value.agentRuntimeStatus || formData.value.agentStatus) === 'degraded') {
        ElMessage.warning(t('admin.providers.agentDegraded'))
      } else {
        ElMessage.warning(t('admin.providers.agentNotConnected'))
      }
    }
  } catch (error) {
    ElMessage.error(error.message || t('common.operationFailed'))
  } finally {
    checkingAgentStatus.value = false
  }
}

// 重置等级限制为默认值
const handleResetLevelLimits = () => {
  ElMessageBox.confirm(
    t('admin.providers.restoreDefaultLimitsConfirm'),
    t('admin.providers.confirmOperation'),
    {
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    }
  ).then(() => {
    formData.value.levelLimits = {
      1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 10240, bandwidth: 100 }, maxTraffic: 102400 },
      2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 20480, bandwidth: 200 }, maxTraffic: 204800 },
      3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 40960, bandwidth: 500 }, maxTraffic: 307200 },
      4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 81920, bandwidth: 1000 }, maxTraffic: 409600 },
      5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 163840, bandwidth: 2000 }, maxTraffic: 512000 }
    }
    ElMessage.success(t('admin.providers.levelLimitsRestored'))
    // 同时通知父组件
    emit('reset-level-limits')
  }).catch(() => {
    // 用户取消操作
  })
}

// 提交表单
const handleSubmit = async () => {
  try {
    // 获取基本信息Tab中的表单引用并验证
    const basicFormRef = basicInfoTabRef.value?.formRef
    if (basicFormRef) {
      await basicFormRef.validate()
    }
    
    // 验证虚拟化类型
    if (!formData.value.containerEnabled && !formData.value.vmEnabled) {
      ElMessage.warning(t('admin.providers.selectVirtualizationType'))
      return
    }

    if (isAgentMode.value && !hasAgentMappedNetworking.value) {
      formData.value.networkType = 'no_port_mapping'
    }
    
    // 验证SSH认证方式（agent模式无需）
    if (!props.isEditing && formData.value.connectionType !== 'agent') {
      if (formData.value.authMethod === 'password' && !formData.value.password) {
        ElMessage.error(t('admin.providers.passwordRequired'))
        return
      }
      if (formData.value.authMethod === 'sshKey' && !formData.value.sshKey) {
        ElMessage.error(t('admin.providers.sshKeyRequired'))
        return
      }
    }
    
    // 验证Proxmox第三方安装网桥配置
    if (formData.value.type === 'proxmox' && formData.value.nodeInstallType === 'third_party') {
      if (!formData.value.bridgeNAT) {
        ElMessage.error(t('admin.providers.bridgeNATRequired'))
        return
      }
      if (!formData.value.bridgeDedicatedV4) {
        ElMessage.error(t('admin.providers.bridgeDedicatedV4Required'))
        return
      }
      if (!formData.value.natSubnet) {
        ElMessage.error(t('admin.providers.natSubnetRequired'))
        return
      }
    }

    emit('submit', formData.value)
  } catch (error) {
    console.error('表单验证失败:', error)
    // 表单验证失败，Element Plus 会自动显示错误信息
    // 这里只需要提示用户检查表单
    if (error && typeof error === 'object') {
      // 验证失败，滚动到第一个错误字段
      ElMessage.error(t('admin.providers.pleaseCheckRequiredFields'))
    }
  }
}

// 关闭对话框（带未保存更改警告）
const handleBeforeClose = (done) => {
  const isDirty = formSnapshot.value !== null && JSON.stringify(formData.value) !== formSnapshot.value
  if (isDirty) {
    ElMessageBox.confirm(
      t('common.unsavedChangesConfirm'),
      t('common.unsavedChanges'),
      {
        confirmButtonText: t('common.discardChanges'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    ).then(() => {
      done()
      emit('cancel')
    }).catch(() => {
      // 用户取消关闭
    })
  } else {
    done()
    emit('cancel')
  }
}

// 关闭对话框
const handleClose = () => {
  emit('cancel')
}
</script>

<style scoped>
.server-config-tabs {
  margin-bottom: 20px;
}

.server-form {
  max-height: 500px;
  overflow-y: auto;
  padding-right: 10px;
}

.form-tip {
  margin-top: 5px;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
