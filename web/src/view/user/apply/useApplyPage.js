import { ref, computed, onMounted, watch, onActivated, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { formatMemorySize, formatDiskSize, formatResourceUsage } from '@/utils/unit-formatter'
import { getFlagEmoji } from '@/utils/countries'
import { useI18n } from 'vue-i18n'
import { useApplyProviders } from './composables/useApplyProviders'
import { useApplyForm } from './composables/useApplyForm'
import { getProviderHardwareReport } from '@/api/public'
import { isContainerGPUProvider } from '@/utils/providerTypes'

export default function useApplyPage() {
  const route = useRoute()
  const { t, locale } = useI18n()

  const {
    loading, refreshing, providers, selectedProvider, providerCapabilities,
    instanceTypePermissions,
    getProviderStatusType, getProviderStatusText, formatProviderLocation,
    canCreateInstanceType, loadProviders,
    loadProviderCapabilities, loadInstanceTypePermissions
  } = useApplyProviders()

  const {
    submitting, redeemCodeInput, redeemSubmitting,
    availableImages, instanceConfig, userLimits, configForm, formRef,
    configRules, availableCpuSpecs, selectedImageInfo,
    availableMemorySpecs, availableDiskSpecs, availableBandwidthSpecs,
    canSelectSpec, formatCpuSpecName, formatImageRequirements,
    loadUserLimits, loadInstanceConfig, loadFilteredImages,
    autoSelectFirstAvailableSpecs, onInstanceTypeChange,
    submitApplication, submitRedemption, resetForm,
    // GPU
    gpuLoading, detectedGpus, selectedGpuIndices, gpuInfoMsg,
    loadProviderGPUs, resetGpuState, selectAllGpus, deselectAllGpus
  } = useApplyForm(selectedProvider, providerCapabilities, loadProviderCapabilities, canCreateInstanceType)


  // Provider groups are shown as collapsed panels by default.
  const expandedProviderGroups = ref([])
  const redeemCodeOnlyProviders = computed(() => providers.value.filter(provider => Boolean(provider.redeemCodeOnly)))
  let providerSelectSeq = 0

  // GPU passthrough is native on LXD/Incus and best-effort on Docker-family providers.
  const canConfigureGpuPassthrough = computed(() => {
    if (!selectedProvider.value) return false
    const providerType = selectedProvider.value.type
    const isContainer = configForm.type === 'container'
    return Boolean(selectedProvider.value.gpuEnabled) && isContainerGPUProvider(providerType) && isContainer
  })

  // When GPU enabled checkbox is toggled, load cached GPU info if not already loaded.
  // Only applicable when provider is lxd/incus AND admin has enabled GPU on the node.
  const onGpuEnabledChange = (val) => {
    if (!val) {
      selectedGpuIndices.value = []
      configForm.gpuDeviceIds = ''
      return
    }
    if (!canConfigureGpuPassthrough.value) {
      configForm.gpuEnabled = false
      return
    }
    if (detectedGpus.value.length === 0 && selectedProvider.value) {
      const pt = selectedProvider.value.type
      if ((pt === 'lxd' || pt === 'incus') && selectedProvider.value.gpuEnabled) {
        loadProviderGPUs(selectedProvider.value.id)
      }
    }
  }

  const providerGroups = computed(() => {
    const groupMap = new Map()
    for (const p of providers.value) {
      const key = p.groupId ? `group-${p.groupId}` : 'default-group'
      if (!groupMap.has(key)) {
        groupMap.set(key, {
          id: p.groupId || 0,
          tabName: key,
          name: p.groupName || t('user.apply.defaultGroup'),
          description: p.groupDescriptionHtml || p.groupDescription || '',
          providers: []
        })
      }
      groupMap.get(key).providers.push(p)
    }
    const groups = Array.from(groupMap.values())
    // 默认分组置顶，其余按名称排序。
    groups.sort((a, b) => {
      if (!a.id) return -1
      if (!b.id) return 1
      return (a.name || '').localeCompare(b.name || '')
    })
    return groups
  })

  const formatProviderType = (type) => {
    if (!type) return '-'
    const key = `admin.providers.${type}`
    const label = t(key)
    return label === key ? type : label
  }

  const formatNetworkType = (networkType) => {
    const normalized = networkType || 'nat_ipv4'
    const keyMap = {
      nat_ipv4: 'natIpv4',
      nat_ipv4_ipv6: 'natIpv4Ipv6',
      dedicated_ipv4: 'dedicatedIpv4',
      dedicated_ipv4_ipv6: 'dedicatedIpv4Ipv6',
      ipv6_only: 'ipv6Only',
      no_port_mapping: 'noPortMapping'
    }
    const key = keyMap[normalized]
    if (!key) return normalized
    const labelKey = `user.apply.networkModes.${key}`
    const label = t(labelKey)
    return label === labelKey ? normalized : label
  }

  const viewHardwareReport = async (providerId) => {
    try {
      const res = await getProviderHardwareReport(providerId)
      const pasteUrl = res.data?.pasteUrl
      if (pasteUrl) {
        window.open(pasteUrl, '_blank', 'noopener,noreferrer')
      } else {
        ElMessage.warning(t('user.apply.noHardwareReport'))
      }
    } catch (error) {
      console.error('Failed to load hardware report:', error)
      ElMessage.error(t('user.apply.noHardwareReport'))
    }
  }

  // Bridge: refresh providers + userLimits together
  const refreshData = async () => {
    if (refreshing.value || loading.value) return
    try {
      refreshing.value = true
      await Promise.allSettled([loadProviders(), loadUserLimits()])
      ElMessage.success(t('user.apply.dataRefreshed'))
    } catch (error) {
      console.error('refreshData failed:', error)
      ElMessage.error(t('user.apply.refreshFailed'))
    } finally {
      refreshing.value = false
    }
  }

  // Bridge: select provider (uses both provider and form state)
  const selectProvider = async (provider) => {
    if (provider.status === 'offline' || provider.status === 'inactive') {
      ElMessage.warning(t('user.apply.nodeOffline'))
      return
    }
    const hasAvailableContainer = provider.containerEnabled && (provider.availableContainerSlots === -1 || provider.availableContainerSlots > 0)
    const hasAvailableVM = provider.vmEnabled && (provider.availableVMSlots === -1 || provider.availableVMSlots > 0)
    if (!provider.redeemCodeOnly && !hasAvailableContainer && !hasAvailableVM) {
      ElMessage.warning(t('user.apply.nodeInsufficientResources'))
      return
    }
    const requestSeq = ++providerSelectSeq
    resetGpuState()
    selectedProvider.value = provider
    if (provider.redeemCodeOnly) {
      ElMessage.info(t('user.apply.useGlobalRedeemArea'))
      return
    }
    await loadProviderCapabilities(provider.id)
    if (requestSeq !== providerSelectSeq || selectedProvider.value?.id !== provider.id) return
    await loadInstanceConfig(provider.id)
    if (requestSeq !== providerSelectSeq || selectedProvider.value?.id !== provider.id) return
    if (!canCreateInstanceType(configForm.type)) {
      const capabilities = providerCapabilities.value[provider.id]
      if (capabilities?.supportedTypes?.length > 0) {
        for (const type of ['container', 'vm']) {
          if (capabilities.supportedTypes.includes(type) && canCreateInstanceType(type)) {
            configForm.type = type
            break
          }
        }
      }
    }
    if (!canConfigureGpuPassthrough.value) resetGpuState()
    if (configForm.type) await loadFilteredImages()
    if (requestSeq !== providerSelectSeq || selectedProvider.value?.id !== provider.id) return
    configForm.imageId = ''
    autoSelectFirstAvailableSpecs()
  }

  // 监听路由变化
  watch(() => route.path, (newPath, oldPath) => {
    if (newPath === '/user/apply' && oldPath !== newPath) {
      loadProviders()
      loadUserLimits()
      loadInstanceConfig()
    }
  }, { immediate: false })

  // 监听镜像选择变化
  watch(() => configForm.imageId, (newImageId, oldImageId) => {
    if (newImageId !== oldImageId && newImageId) {
      const selectedImage = availableImages.value.find(img => img.id === newImageId)
      if (selectedImage && selectedImage.minMemoryMB && selectedImage.minDiskMB) {
        const minMemoryMB = selectedImage.minMemoryMB
        const minDiskMB = selectedImage.minDiskMB
        let needAutoSelect = false
        if (configForm.memoryId) {
          const currentMemory = instanceConfig.value.memorySpecs?.find(spec => spec.id === configForm.memoryId)
          if (currentMemory && currentMemory.sizeMB < minMemoryMB) {
            configForm.memoryId = ''
            needAutoSelect = true
            ElMessage.warning(t('user.apply.imageChangeMemoryReset'))
          }
        }
        if (configForm.diskId) {
          const currentDisk = instanceConfig.value.diskSpecs?.find(spec => spec.id === configForm.diskId)
          if (currentDisk && currentDisk.sizeMB < minDiskMB) {
            configForm.diskId = ''
            needAutoSelect = true
            ElMessage.warning(t('user.apply.imageChangeDiskReset'))
          }
        }
        if (needAutoSelect) autoSelectFirstAvailableSpecs()
      }
    }
  })

  // 监听 Provider 选择变化，重新验证规格
  watch(() => selectedProvider.value?.id, (newProviderId, oldProviderId) => {
    if (newProviderId !== oldProviderId && oldProviderId && configForm.imageId) {
      const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
      if (selectedImage && selectedImage.minDiskMB) {
        const currentDisk = instanceConfig.value.diskSpecs?.find(spec => spec.id === configForm.diskId)
        if (currentDisk && currentDisk.sizeMB < selectedImage.minDiskMB) {
          configForm.diskId = ''
          ElMessage.warning(t('user.apply.providerChangeDiskReset'))
          if (availableDiskSpecs.value.length > 0) configForm.diskId = availableDiskSpecs.value[0].id
        }
      }
    }
  })

  const handleRouterNavigation = (event) => {
    if (event.detail && event.detail.path === '/user/apply') {
      loadProviders()
      loadUserLimits()
      loadInstanceTypePermissions()
      loadInstanceConfig()
    }
  }

  const handleForceRefresh = async (event) => {
    if (event.detail && event.detail.path === '/user/apply') {
      try {
        await loadInstanceTypePermissions()
        await loadProviders()
        Promise.allSettled([loadInstanceConfig(), loadUserLimits()])
      } catch (error) {
        console.error('强制刷新时数据加载失败:', error)
      }
    }
  }

  onMounted(async () => {
    window.addEventListener('router-navigation', handleRouterNavigation)
    window.addEventListener('force-page-refresh', handleForceRefresh)
    try {
      await loadInstanceTypePermissions()
      await loadProviders()
      Promise.allSettled([loadInstanceConfig(), loadUserLimits()])
    } catch (error) {
      console.error('页面初始化失败:', error)
      ElMessage.error(t('user.apply.pageLoadFailed'))
    }
  })

  onActivated(async () => {
    try {
      await loadInstanceTypePermissions()
      await loadProviders()
      Promise.allSettled([loadInstanceConfig(), loadUserLimits()])
    } catch (error) {
      console.error('页面激活时数据加载失败:', error)
    }
  })

  onUnmounted(() => {
    window.removeEventListener('router-navigation', handleRouterNavigation)
    window.removeEventListener('force-page-refresh', handleForceRefresh)
  })

  return {
    loading, refreshing, providers, selectedProvider, providerCapabilities,
    instanceTypePermissions,
    getProviderStatusType, getProviderStatusText, formatProviderLocation,
    canCreateInstanceType,
    submitting, redeemCodeInput, redeemSubmitting,
    availableImages, instanceConfig, userLimits, configForm, formRef,
    configRules, availableCpuSpecs, selectedImageInfo,
    availableMemorySpecs, availableDiskSpecs, availableBandwidthSpecs,
    canSelectSpec, formatCpuSpecName, formatImageRequirements,
    onInstanceTypeChange, submitApplication, submitRedemption, resetForm,
    gpuLoading, detectedGpus, selectedGpuIndices, gpuInfoMsg,
    selectAllGpus, deselectAllGpus,
    expandedProviderGroups, redeemCodeOnlyProviders, canConfigureGpuPassthrough,
    onGpuEnabledChange, providerGroups,
    viewHardwareReport, refreshData, selectProvider,
    formatProviderType, formatNetworkType,
    formatMemorySize, formatDiskSize, formatResourceUsage, getFlagEmoji
  }
}
