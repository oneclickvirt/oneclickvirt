// apply/index.vue - 节点选择与供应商数据管理
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import {
  getAvailableProviders,
  getProviderCapabilities,
  getUserInstanceTypePermissions
} from '@/api/user'
import { getCountryByName, getLocalizedName } from '@/utils/countries'

export function useApplyProviders() {
  const { t, locale } = useI18n()

  const loading = ref(false)
  const refreshing = ref(false)
  const providers = ref([])
  const selectedProvider = ref(null)
  const providerCapabilities = ref({})
  const instanceTypePermissions = ref({
    canCreateContainer: false,
    canCreateVM: false,
    availableTypes: [],
    quotaInfo: {
      usedInstances: 0, maxInstances: 0,
      usedCpu: 0, maxCpu: 0,
      usedMemory: 0, maxMemory: 0
    }
  })

  const getProviderStatusType = (status) => {
    switch (status) {
      case 'active':   return 'success'
      case 'offline':
      case 'inactive': return 'danger'
      case 'partial':  return 'warning'
      default:         return 'info'
    }
  }

  const getProviderStatusText = (status) => {
    switch (status) {
      case 'active':   return t('user.apply.statusActive')
      case 'offline':
      case 'inactive': return t('user.apply.statusOffline')
      case 'partial':  return t('user.apply.statusPartial')
      default:         return status
    }
  }

  const formatProviderLocation = (provider) => {
    const parts = []
    const loc = locale.value === 'en' ? 'en' : 'zh'
    if (provider.city) parts.push(provider.city)
    if (provider.country) {
      const countryInfo = getCountryByName(provider.country)
      parts.push(countryInfo ? getLocalizedName(countryInfo, loc) : provider.country)
    } else if (provider.region) {
      parts.push(provider.region)
    }
    return parts.length > 0 ? parts.join(', ') : '-'
  }

  const hasAvailableSlots = (provider, capabilities, instanceType) => {
    const providerSlotKey = instanceType === 'vm' ? 'availableVMSlots' : 'availableContainerSlots'
    const capabilitySlotKey = instanceType === 'vm' ? 'availableVMSlots' : 'availableContainerSlots'
    const slots = capabilities?.[capabilitySlotKey] ?? provider?.[providerSlotKey]
    return slots === undefined || slots === null || slots === -1 || Number(slots) > 0
  }

  const canCreateInstanceType = (instanceType) => {
    if (!selectedProvider.value) return false
    const capabilities = providerCapabilities.value[selectedProvider.value.id]
    if (!capabilities) return false
    if (!capabilities.supportedTypes?.includes(instanceType)) return false
    if (!hasAvailableSlots(selectedProvider.value, capabilities, instanceType)) return false
    switch (instanceType) {
      case 'container': return instanceTypePermissions.value.canCreateContainer
      case 'vm':        return instanceTypePermissions.value.canCreateVM
      default:          return false
    }
  }

  const loadProviders = async (showSuccessMsg = false) => {
    try {
      loading.value = true
      const response = await getAvailableProviders()
      if (response.code === 200) {
        providers.value = response.data || []
        if (providers.value.length === 0) {
          ElMessage.info(t('user.apply.noProvidersRetry'))
        } else if (showSuccessMsg) {
          ElMessage.success(t('user.apply.refreshedProviders', { count: providers.value.length }))
        }
      } else {
        providers.value = []
        if (response.message) ElMessage.warning(response.message)
      }
    } catch (error) {
      console.error('获取提供商列表失败:', error)
      providers.value = []
      ElMessage.error(error?.details || error?.message || t('user.apply.loadProvidersFailed'))
    } finally {
      loading.value = false
    }
  }

  const loadProviderCapabilities = async (providerId) => {
    try {
      const response = await getProviderCapabilities(providerId)
      if (response.code === 200) {
        providerCapabilities.value[providerId] = response.data
      } else {
        console.warn('获取节点支持能力失败:', response.message)
      }
    } catch (error) {
      console.error('获取节点支持能力失败:', error)
    }
  }

  const loadInstanceTypePermissions = async () => {
    try {
      const response = await getUserInstanceTypePermissions()
      if (response.code === 200) {
        Object.assign(instanceTypePermissions.value, response.data)
      } else {
        console.warn('获取实例类型权限配置失败:', response.message)
      }
    } catch (error) {
      console.error('获取实例类型权限配置失败:', error)
    }
  }

  return {
    loading,
    refreshing,
    providers,
    selectedProvider,
    providerCapabilities,
    instanceTypePermissions,
    getProviderStatusType,
    getProviderStatusText,
    formatProviderLocation,
    canCreateInstanceType,
    loadProviders,
    loadProviderCapabilities,
    loadInstanceTypePermissions
  }
}
