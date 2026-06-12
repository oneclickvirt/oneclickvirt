// Provider表单辅助功能
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { testSSHConnection as testSSHConnectionAPI } from '@/api/admin'
import { useI18n } from 'vue-i18n'
import { DEFAULT_LEVEL_LIMITS, normalizeLevelLimits } from '@/utils/levels'

export function useProviderFormHelpers() {
  const { t } = useI18n()
  
  // SSH连接测试相关
  const testingConnection = ref(false)
  const connectionTestResult = ref(null)

  // 测试SSH连接
  const testSSHConnection = async (formData) => {
    // 根据认证方式进行验证
    if (!formData.host || !formData.username) {
      ElMessage.warning(t('admin.providers.fillHostUserPassword'))
      return
    }

    if (formData.authMethod === 'password' && !formData.password) {
      ElMessage.warning(t('admin.providers.fillHostUserPassword'))
      return
    }

    if (formData.authMethod === 'sshKey' && !formData.sshKey) {
      ElMessage.warning(t('admin.providers.fillSSHKey'))
      return
    }

    testingConnection.value = true
    connectionTestResult.value = null

    try {
      // 根据认证方式构建请求数据
      const requestData = {
        host: formData.host,
        port: formData.port || 22,
        username: formData.username,
        testCount: 3
      }

      // 对应的认证信息
      if (formData.authMethod === 'password') {
        requestData.password = formData.password
      } else if (formData.authMethod === 'sshKey') {
        requestData.sshKey = formData.sshKey
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
  const applyRecommendedTimeout = (formData) => {
    if (connectionTestResult.value && connectionTestResult.value.success) {
      formData.sshConnectTimeout = connectionTestResult.value.recommendedTimeout
      formData.sshExecuteTimeout = Math.max(300, connectionTestResult.value.recommendedTimeout * 10)
      ElMessage.success(t('admin.providers.timeoutApplied'))
    }
  }

  // 认证方式切换处理
  const handleAuthMethodChange = (formData, newMethod) => {
    // 切换认证方式时，清空被隐藏的字段
    if (newMethod === 'password') {
      formData.sshKey = ''
    } else if (newMethod === 'sshKey') {
      formData.password = ''
    }
  }

  // 验证虚拟化类型
  const validateVirtualizationType = (formData) => {
    if (!formData.containerEnabled && !formData.vmEnabled) {
      ElMessage.error(t('admin.providers.atLeastOneVirtType'))
      return false
    }
    return true
  }

  // 获取默认表单数据
  const getDefaultFormData = () => ({
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
    containerReadIoLimit: '',
    containerWriteIoLimit: '',
    vmReadIoLimit: '',
    vmWriteIoLimit: '',
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
    levelLimits: normalizeLevelLimits(DEFAULT_LEVEL_LIMITS)
  })

  return {
    testingConnection,
    connectionTestResult,
    testSSHConnection,
    applyRecommendedTimeout,
    handleAuthMethodChange,
    validateVirtualizationType,
    getDefaultFormData
  }
}
