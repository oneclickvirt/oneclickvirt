// apply/index.vue - 申请表单状态、规格数据与提交逻辑
import { ref, reactive, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getUserLimits,
  getFilteredImages,
  getInstanceConfig,
  createInstance,
  redeemCode
} from '@/api/user'

/**
 * @param {import('vue').Ref} selectedProvider - from useApplyProviders
 * @param {import('vue').Ref} providerCapabilities - from useApplyProviders
 * @param {Function} loadProviderCapabilities - from useApplyProviders
 * @param {Function} canCreateInstanceType - from useApplyProviders
 */
export function useApplyForm(selectedProvider, providerCapabilities, loadProviderCapabilities, canCreateInstanceType) {
  const { t } = useI18n()
  const router = useRouter()

  const submitting = ref(false)
  const redeemCodeInput = ref('')
  const redeemSubmitting = ref(false)
  const availableImages = ref([])
  const formRef = ref()

  const instanceConfig = ref({
    cpuSpecs: [],
    memorySpecs: [],
    diskSpecs: [],
    bandwidthSpecs: []
  })

  const userLimits = reactive({
    level: 1,
    maxInstances: 0,
    usedInstances: 0,
    containerCount: 0,
    vmCount: 0,
    maxCpu: 0,
    usedCpu: 0,
    maxMemory: 0,
    usedMemory: 0,
    maxDisk: 0,
    usedDisk: 0,
    maxBandwidth: 0,
    usedBandwidth: 0,
    maxTraffic: 0,
    usedTraffic: 0
  })

  const configForm = reactive({
    type: 'container',
    imageId: '',
    cpuId: '',
    memoryId: '',
    diskId: '',
    bandwidthId: '',
    description: ''
  })

  const configRules = computed(() => ({
    type: [{ required: true, message: t('user.apply.pleaseSelectInstanceType'), trigger: 'change' }],
    imageId: [{ required: true, message: t('user.apply.pleaseSelectSystemImage'), trigger: 'change' }],
    cpuId: [{ required: true, message: t('user.apply.pleaseSelectCpuSpec'), trigger: 'change' }],
    memoryId: [{ required: true, message: t('user.apply.pleaseSelectMemorySpec'), trigger: 'change' }],
    diskId: [{ required: true, message: t('user.apply.pleaseSelectDiskSpec'), trigger: 'change' }],
    bandwidthId: [{ required: true, message: t('user.apply.pleaseSelectBandwidthSpec'), trigger: 'change' }]
  }))

  // ── Formatters ────────────────────────────────────

  // eslint-disable-next-line no-unused-vars
  const canSelectSpec = (_specType, _spec) => true

  const formatCpuSpecName = (spec) => {
    if (spec.name && spec.name.includes('核')) {
      const coreCount = spec.cores || parseInt(spec.name)
      return `${coreCount}${t('user.apply.cores')}`
    }
    return spec.name
  }

  const formatImageRequirements = (image) => {
    if (!image.minMemoryMB || !image.minDiskMB) return ''
    const memoryMB = image.minMemoryMB
    const diskGB = Math.round(image.minDiskMB / 1024 * 10) / 10
    return `≥${memoryMB}MB / ${diskGB}GB`
  }

  // ── Computed specs ────────────────────────────────

  const availableCpuSpecs = computed(() => {
    const specs = instanceConfig.value.cpuSpecs || []
    return specs.map(spec => ({ ...spec, name: formatCpuSpecName(spec) }))
  })

  const selectedImageInfo = computed(() => {
    if (!configForm.imageId) return null
    return availableImages.value.find(img => img.id === configForm.imageId)
  })

  const availableMemorySpecs = computed(() => {
    const allSpecs = instanceConfig.value.memorySpecs || []
    if (configForm.imageId) {
      const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
      if (selectedImage && selectedImage.minMemoryMB) {
        return allSpecs.filter(spec => spec.sizeMB >= selectedImage.minMemoryMB)
      }
    }
    return allSpecs
  })

  const availableDiskSpecs = computed(() => {
    const allSpecs = instanceConfig.value.diskSpecs || []
    if (configForm.imageId) {
      const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
      if (selectedImage && selectedImage.minDiskMB) {
        return allSpecs.filter(spec => spec.sizeMB >= selectedImage.minDiskMB)
      }
    }
    return allSpecs
  })

  const availableBandwidthSpecs = computed(() => instanceConfig.value.bandwidthSpecs || [])

  // ── Data loaders ───────────────────────────────

  const loadUserLimits = async () => {
    try {
      const response = await getUserLimits()
      if (response.code === 0 || response.code === 200) {
        Object.assign(userLimits, response.data)
      } else {
        console.warn('获取用户限制失败:', response.message)
      }
    } catch (error) {
      console.error('获取用户限制失败:', error)
    }
  }

  const loadInstanceConfig = async (providerId = null) => {
    try {
      const response = await getInstanceConfig(providerId)
      if (response.code === 0 || response.code === 200) {
        Object.assign(instanceConfig.value, response.data)
      } else {
        console.warn('获取实例配置失败:', response.message)
      }
    } catch (error) {
      console.error('获取实例配置失败:', error)
    }
  }

  const loadFilteredImages = async () => {
    if (!selectedProvider.value || !configForm.type) {
      availableImages.value = []
      return
    }
    try {
      const capabilities = providerCapabilities.value[selectedProvider.value.id]
      if (!capabilities) {
        await loadProviderCapabilities(selectedProvider.value.id)
      }
      const response = await getFilteredImages({
        provider_id: selectedProvider.value.id,
        instance_type: configForm.type,
        architecture: capabilities?.architecture || 'amd64'
      })
      if (response.code === 0 || response.code === 200) {
        availableImages.value = response.data || []
      } else {
        availableImages.value = []
        console.warn('获取过滤镜像失败:', response.message)
      }
    } catch (error) {
      console.error('获取过滤镜像失败:', error)
      availableImages.value = []
    }
  }

  // ── Spec helpers ───────────────────────────────

  const autoSelectFirstAvailableSpecs = () => {
    if (availableCpuSpecs.value.length > 0 && !configForm.cpuId) {
      configForm.cpuId = availableCpuSpecs.value[0].id
    }
    if (availableMemorySpecs.value.length > 0 && !configForm.memoryId) {
      configForm.memoryId = availableMemorySpecs.value[0].id
    }
    if (availableDiskSpecs.value.length > 0 && !configForm.diskId) {
      configForm.diskId = availableDiskSpecs.value[0].id
    }
    if (availableBandwidthSpecs.value.length > 0 && !configForm.bandwidthId) {
      configForm.bandwidthId = availableBandwidthSpecs.value[0].id
    }
  }

  const onInstanceTypeChange = async () => {
    if (selectedProvider.value && configForm.type) {
      if (configForm.type === 'container') {
        if (!selectedProvider.value.containerEnabled) {
          ElMessage.warning(t('user.apply.nodeNotSupportContainer'))
          configForm.type = 'vm'
          return
        }
        if (selectedProvider.value.availableContainerSlots !== -1 && selectedProvider.value.availableContainerSlots <= 0) {
          ElMessage.warning(t('user.apply.nodeContainerSlotsFull'))
          if (
            selectedProvider.value.vmEnabled &&
            (selectedProvider.value.availableVMSlots === -1 || selectedProvider.value.availableVMSlots > 0)
          ) {
            configForm.type = 'vm'
            ElMessage.info(t('user.apply.autoSwitchToVM'))
          } else {
            selectedProvider.value = null
            ElMessage.warning(t('user.apply.nodeResourceInsufficient'))
            return
          }
        }
      } else if (configForm.type === 'vm') {
        if (!selectedProvider.value.vmEnabled) {
          ElMessage.warning(t('user.apply.nodeNotSupportVM'))
          configForm.type = 'container'
          return
        }
        if (selectedProvider.value.availableVMSlots !== -1 && selectedProvider.value.availableVMSlots <= 0) {
          ElMessage.warning(t('user.apply.nodeVMSlotsFull'))
          if (
            selectedProvider.value.containerEnabled &&
            (selectedProvider.value.availableContainerSlots === -1 || selectedProvider.value.availableContainerSlots > 0)
          ) {
            configForm.type = 'container'
            ElMessage.info(t('user.apply.autoSwitchToContainer'))
          } else {
            selectedProvider.value = null
            ElMessage.warning(t('user.apply.nodeResourceInsufficient'))
            return
          }
        }
      }
      await loadFilteredImages()
    }
    configForm.imageId = ''
    autoSelectFirstAvailableSpecs()
  }

  // ── Submission ─────────────────────────────────

  const submitApplication = async () => {
    if (submitting.value) {
      ElMessage.warning(t('user.apply.submitInProgress'))
      return
    }
    if (!selectedProvider.value) {
      ElMessage.warning(t('user.apply.pleaseSelectProvider'))
      return
    }
    if (!canCreateInstanceType(configForm.type)) {
      ElMessage.error(t('user.apply.instanceTypeNotSupported'))
      return
    }
    if (!configForm.cpuId) { ElMessage.error(t('user.apply.pleaseSelectCpuSpec')); return }
    if (!configForm.memoryId) { ElMessage.error(t('user.apply.pleaseSelectMemorySpec')); return }
    if (!configForm.diskId) { ElMessage.error(t('user.apply.pleaseSelectDiskSpec')); return }
    if (!configForm.bandwidthId) { ElMessage.error(t('user.apply.pleaseSelectBandwidthSpec')); return }

    try {
      await formRef.value.validate()

      const selectedImage = availableImages.value.find(img => img.id === configForm.imageId)
      const selectedCpu = availableCpuSpecs.value.find(spec => spec.id === configForm.cpuId)
      const selectedMemory = availableMemorySpecs.value.find(spec => spec.id === configForm.memoryId)
      const selectedDisk = availableDiskSpecs.value.find(spec => spec.id === configForm.diskId)
      const selectedBandwidth = availableBandwidthSpecs.value.find(spec => spec.id === configForm.bandwidthId)

      const confirmMessage = `
        <div style="text-align: left; line-height: 2;">
          <p style="margin-bottom: 12px; color: #606266;">${t('user.apply.confirmDialogMessage')}</p>
          <div style="padding: 12px; background: var(--neutral-bg); border-radius: 4px;">
            <p><strong>${t('user.apply.confirmProvider')}:</strong> ${selectedProvider.value.name}</p>
            <p><strong>${t('user.apply.confirmInstanceType')}:</strong> ${configForm.type === 'container' ? t('user.apply.container') : t('user.apply.vm')}</p>
            <p><strong>${t('user.apply.confirmImage')}:</strong> ${selectedImage?.name || '-'}</p>
            <p><strong>${t('user.apply.confirmCpu')}:</strong> ${selectedCpu?.name || '-'}</p>
            <p><strong>${t('user.apply.confirmMemory')}:</strong> ${selectedMemory?.name || '-'}</p>
            <p><strong>${t('user.apply.confirmDisk')}:</strong> ${selectedDisk?.name || '-'}</p>
            <p><strong>${t('user.apply.confirmBandwidth')}:</strong> ${selectedBandwidth?.name || '-'}</p>
            ${configForm.description ? `<p><strong>${t('user.apply.confirmDescription')}:</strong> ${configForm.description}</p>` : ''}
          </div>
          <p style="margin-top: 12px; color: #E6A23C; font-size: 13px;">
            <i class="el-icon-warning" style="margin-right: 4px;"></i>${t('user.apply.confirmWarning')}
          </p>
        </div>
      `

      await ElMessageBox.confirm(confirmMessage, t('user.apply.confirmDialogTitle'), {
        confirmButtonText: t('user.apply.confirmSubmit'),
        cancelButtonText: t('user.apply.confirmCancel'),
        type: 'warning',
        dangerouslyUseHTMLString: true,
        distinguishCancelAndClose: true
      })

      submitting.value = true

      const response = await createInstance({
        providerId: selectedProvider.value.id,
        imageId: configForm.imageId,
        cpuId: configForm.cpuId,
        memoryId: configForm.memoryId,
        diskId: configForm.diskId,
        bandwidthId: configForm.bandwidthId,
        description: configForm.description
      })

      if (response.code === 0 || response.code === 200) {
        ElMessage.success(t('user.apply.instanceCreatedSuccess'))
        if (response.data && response.data.taskId) {
          ElMessage.info(t('user.apply.taskIdInfo', { taskId: response.data.taskId }))
        }
        setTimeout(() => { router.push('/user/tasks') }, 3000)
      } else {
        if (response.message && response.message.includes('进行中')) {
          ElMessage.warning(t('user.apply.duplicateTaskWarning'))
          setTimeout(() => { router.push('/user/tasks') }, 3000)
        } else {
          ElMessage.error(response.message || t('user.apply.createInstanceFailed'))
          submitting.value = false
        }
      }
    } catch (error) {
      if (error === 'cancel' || error === 'close') return
      if (error !== false) {
        console.error('提交申请失败:', error)
        if (error.message && error.message.includes('timeout')) {
          ElMessage.error(t('user.apply.requestTimeout'))
          setTimeout(() => { router.push('/user/tasks') }, 3000)
        } else {
          ElMessage.error(t('user.apply.submitFailed'))
          submitting.value = false
        }
      } else {
        submitting.value = false
      }
    }
  }

  const submitRedemption = async () => {
    const code = redeemCodeInput.value.trim()
    if (!code) {
      ElMessage.warning(t('user.apply.redeemCodeRequired'))
      return
    }
    redeemSubmitting.value = true
    try {
      await redeemCode(code)
      ElMessage.success(t('user.apply.redeemCodeSuccess'))
      redeemCodeInput.value = ''
    } catch (e) {
      ElMessage.error(e?.response?.data?.msg || e.message)
    } finally {
      redeemSubmitting.value = false
    }
  }

  const resetForm = async () => {
    if (formRef.value) formRef.value.resetFields()
    Object.assign(configForm, {
      type: 'container',
      imageId: '',
      cpu: 1,
      memory: 512,
      disk: 20,
      bandwidth: 100,
      description: ''
    })
    if (selectedProvider.value) await loadFilteredImages()
  }

  return {
    submitting,
    redeemCodeInput,
    redeemSubmitting,
    availableImages,
    instanceConfig,
    userLimits,
    configForm,
    formRef,
    configRules,
    availableCpuSpecs,
    selectedImageInfo,
    availableMemorySpecs,
    availableDiskSpecs,
    availableBandwidthSpecs,
    canSelectSpec,
    formatCpuSpecName,
    formatImageRequirements,
    loadUserLimits,
    loadInstanceConfig,
    loadFilteredImages,
    autoSelectFirstAvailableSpecs,
    onInstanceTypeChange,
    submitApplication,
    submitRedemption,
    resetForm
  }
}
