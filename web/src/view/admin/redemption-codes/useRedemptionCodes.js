import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { copyToClipboard } from '@/utils/clipboard'
import {
  getRedemptionCodes,
  batchCreateRedemptionCodes,
  exportRedemptionCodes,
  batchDeleteRedemptionCodes,
  getProviderList,
  getStoppedContainers,
  detectProviderGPUs
} from '@/api/admin'
import {
  getFilteredImages,
  getProviderCapabilities,
  getInstanceConfig
} from '@/api/user'

export default function useRedemptionCodes() {
  const { t, locale } = useI18n()

  // ── 列表状态 ──────────────────────────────────────────────
  const loading = ref(false)
  const tableData = ref([])
  const total = ref(0)
  const currentPage = ref(1)
  const pageSize = ref(20)

  const filterCode = ref('')
  const filterStatus = ref('')
  const filterProvider = ref('')

  const selectedRows = ref([])

  // ── 所有节点（用于筛选 & 创建对话框） ─────────────────────
  const allProviders = ref([])

  // ── 创建对话框 ────────────────────────────────────────────
  const showCreateDialog = ref(false)
  const createLoading = ref(false)
  const createFormRef = ref(null)

  const createForm = reactive({
    providerId: null,
    instanceType: '',
    imageId: '',
    cpuId: '',
    memoryId: '',
    diskId: '',
    bandwidthId: '',
    count: 1,
    remark: '',
    creationMode: 'standard',
    sourceContainer: '',
    gpuEnabled: false,
    gpuDeviceIds: ''
  })

  const createRules = computed(() => {
    const isCopy = createForm.creationMode === 'copy'
    return {
      providerId: [{ required: true, message: t('admin.redemptionCodes.providerRequired'), trigger: 'change' }],
      instanceType: [{ required: !isCopy, message: t('admin.redemptionCodes.instanceTypeRequired'), trigger: 'change' }],
      imageId: [{ required: !isCopy, message: t('admin.redemptionCodes.imageRequired'), trigger: 'change' }],
      cpuId: [{ required: !isCopy, message: t('admin.redemptionCodes.cpuRequired'), trigger: 'change' }],
      memoryId: [{ required: !isCopy, message: t('admin.redemptionCodes.memoryRequired'), trigger: 'change' }],
      diskId: [{ required: !isCopy, message: t('admin.redemptionCodes.diskRequired'), trigger: 'change' }],
      bandwidthId: [{ required: !isCopy, message: t('admin.redemptionCodes.bandwidthRequired'), trigger: 'change' }],
      sourceContainer: [{ required: isCopy, message: t('admin.redemptionCodes.sourceContainerRequired'), trigger: 'change' }],
      count: [
        { required: true, message: t('admin.redemptionCodes.countRequired'), trigger: 'blur' },
        { type: 'number', min: 1, max: 100, message: t('admin.redemptionCodes.countRange'), trigger: 'blur' }
      ]
    }
  })

  // 动态规格列表
  const providerCaps = reactive({ containerEnabled: false, vmEnabled: false })
  const availableImages = ref([])
  const cpuSpecs = ref([])
  const memorySpecs = ref([])
  const diskSpecs = ref([])
  const bandwidthSpecs = ref([])

  // 复制模式：停止中容器列表
  const stoppedContainers = ref([])
  const stoppedContainerOptions = ref([])
  const stoppedContainersLoading = ref(false)
  const isLxdIncusProvider = computed(() => {
    if (!createForm.providerId) return false
    const p = allProviders.value.find(p => p.id === createForm.providerId)
    return p && (p.type === 'lxd' || p.type === 'incus')
  })

  const canConfigureGpuPassthrough = computed(() => {
    if (!isLxdIncusProvider.value) return false
    return createForm.creationMode === 'copy' || createForm.instanceType === 'container'
  })

  // ── GPU 相关 ──────────────────────────────────────────────
  const gpuDetecting = ref(false)
  const gpuChecked = ref(false)
  const detectedGpus = ref([])
  const selectedGpuIndices = ref([])

  const detectGpus = async () => {
    if (!createForm.providerId) return
    gpuDetecting.value = true
    gpuChecked.value = true
    try {
      const res = await detectProviderGPUs(createForm.providerId)
      detectedGpus.value = res.data?.gpus || res.data?.data || []
      // Auto-select all GPUs
      if (detectedGpus.value.length > 0) {
        selectedGpuIndices.value = detectedGpus.value.map((_, i) => i)
      }
    } catch (_) {
      ElMessage.warning(t('admin.redemptionCodes.gpuFetchFailed'))
    } finally {
      gpuDetecting.value = false
    }
  }

  const selectAllGpus = () => {
    selectedGpuIndices.value = detectedGpus.value.map((_, i) => i)
  }

  const deselectAllGpus = () => {
    selectedGpuIndices.value = []
  }

  const resetGpuSelection = () => {
    createForm.gpuEnabled = false
    createForm.gpuDeviceIds = ''
    detectedGpus.value = []
    selectedGpuIndices.value = []
    gpuChecked.value = false
  }

  watch(() => createForm.creationMode, (mode) => {
    if (mode === 'copy') {
      createForm.instanceType = 'container'
      createForm.imageId = ''
      createForm.cpuId = ''
      createForm.memoryId = ''
      createForm.diskId = ''
      createForm.bandwidthId = ''
      availableImages.value = []
      cpuSpecs.value = []
      memorySpecs.value = []
      diskSpecs.value = []
      bandwidthSpecs.value = []
      return
    }
    createForm.sourceContainer = ''
    if (createForm.instanceType !== 'container') {
      resetGpuSelection()
    }
  })

  watch(canConfigureGpuPassthrough, (canConfigure) => {
    if (!canConfigure) {
      resetGpuSelection()
    }
  })

  // ── 导出对话框 ─────────────────────────────────────────────
  const showExportDialog = ref(false)
  const exportedCodesText = ref('')
  const exportResult = ref(null)
  const exportLoading = ref(false)
  const exportFields = ref(['code', 'status', 'provider', 'instanceType', 'cpu', 'memory', 'disk', 'bandwidth'])

  const allExportFields = computed(() => [
    { value: 'code', label: t('admin.redemptionCodes.colCode') },
    { value: 'status', label: t('admin.redemptionCodes.colStatus') },
    { value: 'provider', label: t('admin.redemptionCodes.colProvider') },
    { value: 'instanceType', label: t('admin.redemptionCodes.colInstanceType') },
    { value: 'cpu', label: 'CPU' },
    { value: 'memory', label: t('admin.redemptionCodes.memory') },
    { value: 'disk', label: t('admin.redemptionCodes.disk') },
    { value: 'bandwidth', label: t('admin.redemptionCodes.bandwidth') },
    { value: 'instanceName', label: t('admin.redemptionCodes.colInstanceName') },
    { value: 'createdBy', label: t('admin.redemptionCodes.colCreatedBy') },
    { value: 'createdAt', label: t('admin.redemptionCodes.colCreatedAt') },
    { value: 'redeemedAt', label: t('admin.redemptionCodes.colRedeemedAt') },
    { value: 'remark', label: t('admin.redemptionCodes.colRemark') }
  ])

  // ── 状态颜色 ──────────────────────────────────────────────
  const statusTagType = (status) => {
    switch (status) {
      case 'pending_create': return 'info'
      case 'creating': return 'warning'
      case 'pending_use': return 'success'
      case 'used': return ''
      case 'deleting': return 'danger'
      default: return 'info'
    }
  }

  const statusLabel = (status) => {
    const keyMap = {
      pending_create: 'statusPendingCreate',
      creating: 'statusCreating',
      pending_use: 'statusPendingUse',
      used: 'statusUsed',
      deleting: 'statusDeleting'
    }
    return keyMap[status] ? t(`admin.redemptionCodes.${keyMap[status]}`) : status
  }

  // ── 数据加载 ──────────────────────────────────────────────
  const loadData = async () => {
    loading.value = true
    try {
      const params = {
        page: currentPage.value,
        pageSize: pageSize.value
      }
      if (filterCode.value) params.code = filterCode.value
      if (filterStatus.value) params.status = filterStatus.value
      if (filterProvider.value) params.providerId = filterProvider.value

      const res = await getRedemptionCodes(params)
      tableData.value = res.data?.list || res.data?.data || []
      total.value = res.data?.total || 0
    } catch (e) {
      ElMessage.error(e?.response?.data?.msg || e.message)
    } finally {
      loading.value = false
    }
  }

  const loadProviders = async () => {
    try {
      const res = await getProviderList({ page: 1, pageSize: 999 })
      allProviders.value = res.data?.list || res.data?.data || []
    } catch (_) {
      // ignore
    }
  }

  // ── 过滤 ───────────────────────────────────────────────────
  const handleFilterChange = () => {
    currentPage.value = 1
    loadData()
  }

  const handleSelectionChange = (rows) => {
    selectedRows.value = rows
  }

  // ── 创建对话框逻辑 ─────────────────────────────────────────
  const openCreateDialog = () => {
    showCreateDialog.value = true
  }

  const cancelCreate = () => {
    showCreateDialog.value = false
    createFormRef.value?.resetFields()
    Object.assign(createForm, {
      providerId: null,
      instanceType: '',
      imageId: '',
      cpuId: '',
      memoryId: '',
      diskId: '',
      bandwidthId: '',
      count: 1,
      remark: '',
      creationMode: 'standard',
      sourceContainer: '',
      gpuEnabled: false,
      gpuDeviceIds: ''
    })
    providerCaps.containerEnabled = false
    providerCaps.vmEnabled = false
    availableImages.value = []
    cpuSpecs.value = []
    memorySpecs.value = []
    diskSpecs.value = []
    bandwidthSpecs.value = []
    stoppedContainers.value = []
    stoppedContainerOptions.value = []
    detectedGpus.value = []
    selectedGpuIndices.value = []
    gpuChecked.value = false
  }

  const handleCreateDialogClose = (done) => {
    const isFormDirty = !!(createForm.providerId || createForm.instanceType || createForm.remark)
    if (isFormDirty) {
      ElMessageBox.confirm(
        t('common.unsavedChangesConfirm'),
        t('common.unsavedChanges'),
        {
          confirmButtonText: t('common.discardChanges'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        }
      ).then(() => {
        if (typeof done === 'function') done()
        cancelCreate()
      }).catch(() => {})
    } else {
      if (typeof done === 'function') done()
      cancelCreate()
    }
  }

  const onProviderChange = async (providerId) => {
    // Reset dependent fields
    createForm.instanceType = ''
    createForm.imageId = ''
    createForm.cpuId = ''
    createForm.memoryId = ''
    createForm.diskId = ''
    createForm.bandwidthId = ''
    availableImages.value = []
    cpuSpecs.value = []
    memorySpecs.value = []
    diskSpecs.value = []
    bandwidthSpecs.value = []
    resetGpuSelection()

    if (!providerId) return
    try {
      const res = await getProviderCapabilities(providerId)
      const caps = res.data || {}
      providerCaps.containerEnabled = caps.containerEnabled || false
      providerCaps.vmEnabled = caps.vmEnabled || false
    } catch (_) {
      // ignore
    }

    // 如果是 lxd/incus 节点，加载已停止的容器列表和GPU列表
    const p = allProviders.value.find(p => p.id === providerId)
    if (p && (p.type === 'lxd' || p.type === 'incus')) {
      createForm.creationMode = 'standard'
      createForm.sourceContainer = ''
      stoppedContainers.value = []
      stoppedContainerOptions.value = []
      stoppedContainersLoading.value = true
      try {
        const r = await getStoppedContainers(providerId)
        const rawNames = (r.data && r.data.containers) || []
        const rawDetails = (r.data && r.data.containerDetails) || []
        stoppedContainers.value = rawNames
        if (rawDetails.length > 0) {
          stoppedContainerOptions.value = rawDetails.map(item => ({
            name: item.name,
            label: item.hasGpu ? `${item.name} [GPU${item.gpuDeviceIds ? `: ${item.gpuDeviceIds}` : ''}]` : item.name
          }))
        } else {
          stoppedContainerOptions.value = rawNames.map(name => ({ name, label: name }))
        }
      } catch (e) {
        // 区分网络/超时错误与真正的「无容器」
        const errMsg = e?.response?.data?.msg || e?.message || ''
        if (errMsg) {
          ElMessage.warning(t('admin.redemptionCodes.loadContainersFailed', { reason: errMsg }))
        }
        stoppedContainers.value = []
        stoppedContainerOptions.value = []
      } finally {
        stoppedContainersLoading.value = false
      }
      // 自动检测 GPU
      gpuDetecting.value = true
      gpuChecked.value = true
      try {
        const gpuRes = await detectProviderGPUs(providerId)
        detectedGpus.value = gpuRes.data?.gpus || gpuRes.data?.data || []
        if (detectedGpus.value.length > 0) {
          selectedGpuIndices.value = detectedGpus.value.map((_, i) => i)
        }
      } catch (_) {
        // ignore silently
      } finally {
        gpuDetecting.value = false
      }
    } else {
      createForm.creationMode = 'standard'
      createForm.sourceContainer = ''
      stoppedContainers.value = []
      stoppedContainerOptions.value = []
      resetGpuSelection()
    }
  }

  const onInstanceTypeChange = async (type) => {
    createForm.imageId = ''
    createForm.cpuId = ''
    createForm.memoryId = ''
    createForm.diskId = ''
    createForm.bandwidthId = ''
    availableImages.value = []
    cpuSpecs.value = []
    memorySpecs.value = []
    diskSpecs.value = []
    bandwidthSpecs.value = []

    if (type !== 'container') {
      resetGpuSelection()
    }

    if (!createForm.providerId || !type) return
    try {
      const [imgRes, cfgRes] = await Promise.all([
        getFilteredImages({ provider_id: createForm.providerId, instance_type: type }),
        getInstanceConfig(createForm.providerId)
      ])
      availableImages.value = imgRes.data || []
      const cfg = cfgRes.data || {}
      cpuSpecs.value = cfg.cpuSpecs || []
      memorySpecs.value = cfg.memorySpecs || []
      diskSpecs.value = cfg.diskSpecs || []
      bandwidthSpecs.value = cfg.bandwidthSpecs || []
      // Auto-select first options
      if (cpuSpecs.value.length) createForm.cpuId = cpuSpecs.value[0].id
      if (memorySpecs.value.length) createForm.memoryId = memorySpecs.value[0].id
      if (diskSpecs.value.length) createForm.diskId = diskSpecs.value[0].id
      if (bandwidthSpecs.value.length) createForm.bandwidthId = bandwidthSpecs.value[0].id
    } catch (_) {
      // ignore
    }
  }

  const submitCreate = async () => {
    try {
      await createFormRef.value.validate()
      // 额外校验复制模式下的源容器
      if (createForm.creationMode === 'copy' && !createForm.sourceContainer) {
        ElMessage.warning(t('admin.redemptionCodes.sourceContainerRequired'))
        return
      }
      if (createForm.gpuEnabled && !canConfigureGpuPassthrough.value) {
        ElMessage.warning(t('admin.redemptionCodes.gpuUnsupportedTarget'))
        return
      }
      createLoading.value = true
      await batchCreateRedemptionCodes({
        providerId: createForm.providerId,
        instanceType: createForm.creationMode === 'copy' ? 'container' : createForm.instanceType,
        imageId: createForm.creationMode === 'copy' ? 0 : createForm.imageId,
        cpuId: createForm.creationMode === 'copy' ? '' : createForm.cpuId,
        memoryId: createForm.creationMode === 'copy' ? '' : createForm.memoryId,
        diskId: createForm.creationMode === 'copy' ? '' : createForm.diskId,
        bandwidthId: createForm.creationMode === 'copy' ? '' : createForm.bandwidthId,
        count: createForm.count,
        remark: createForm.remark,
        creationMode: createForm.creationMode,
        sourceContainer: createForm.sourceContainer,
        gpuEnabled: canConfigureGpuPassthrough.value && createForm.gpuEnabled,
        gpuDeviceIds: canConfigureGpuPassthrough.value && createForm.gpuEnabled ? selectedGpuIndices.value.join(',') : ''
      })
      ElMessage.success(t('admin.redemptionCodes.createSuccess', { count: createForm.count }))
      cancelCreate()
      await loadData()
    } catch (e) {
      if (e?.response?.data?.msg) {
        ElMessage.error(e.response.data.msg || t('admin.redemptionCodes.createFailed'))
      }
      // validation errors silently ignored (form shows them)
    } finally {
      createLoading.value = false
    }
  }

  // ── 导出 ────────────────────────────────────────────────────
  const handleExport = async () => {
    if (selectedRows.value.length === 0) {
      ElMessage.warning(t('admin.redemptionCodes.exportEmpty'))
      return
    }
    exportResult.value = null
    exportedCodesText.value = ''
    showExportDialog.value = true
  }

  const resetExportDialog = () => {
    exportResult.value = null
    exportedCodesText.value = ''
  }

  const doExport = async () => {
    exportLoading.value = true
    try {
      const ids = selectedRows.value.map(r => r.id)
      const lang = locale.value || 'zh-CN'
      const res = await exportRedemptionCodes({ ids, fields: exportFields.value, lang })
      const items = res.data?.items || []
      // 每条记录单独一行，字段用 | 分隔，记录之间用空行分隔
      exportedCodesText.value = items.map((item, idx) => {
        const fields = Object.entries(item)
          .filter(([, v]) => v !== undefined && v !== null && v !== '')
          .map(([k, v]) => `${k}: ${v}`)
          .join(' | ')
        return `[${idx + 1}] ${fields}`
      }).join('\n\n')
      exportResult.value = items
    } catch (e) {
      ElMessage.error(e?.response?.data?.msg || e.message)
    } finally {
      exportLoading.value = false
    }
  }

  const copyExportedCodes = async () => {
    if (!exportedCodesText.value) return
    await copyToClipboard(exportedCodesText.value, t('admin.redemptionCodes.copiedToClipboard'))
  }

  // ── 删除 ────────────────────────────────────────────────────
  const handleBatchDelete = async () => {
    if (selectedRows.value.length === 0) {
      ElMessage.warning(t('admin.redemptionCodes.noSelection'))
      return
    }
    try {
      await ElMessageBox.confirm(
        t('admin.redemptionCodes.confirmDeleteMsg', { count: selectedRows.value.length }),
        t('admin.redemptionCodes.confirmDeleteTitle'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        }
      )
      const ids = selectedRows.value.map(r => r.id)
      await batchDeleteRedemptionCodes({ ids })
      ElMessage.success(t('admin.redemptionCodes.deleteSuccess'))
      selectedRows.value = []
      await loadData()
    } catch (e) {
      if (e !== 'cancel' && e?.response?.data?.msg) {
        ElMessage.error(e.response.data.msg || t('common.operationFailed'))
      }
    }
  }

  // ── 分页 ────────────────────────────────────────────────────
  const handleSizeChange = (val) => {
    pageSize.value = val
    currentPage.value = 1
    loadData()
  }

  const handleCurrentChange = (val) => {
    currentPage.value = val
    loadData()
  }

  onMounted(async () => {
    await Promise.all([loadProviders(), loadData()])
  })

  return {
    loading, tableData, total, currentPage, pageSize,
    filterCode, filterStatus, filterProvider,
    selectedRows, allProviders,
    showCreateDialog, createLoading, createFormRef,
    createForm, createRules,
    providerCaps, availableImages,
    cpuSpecs, memorySpecs, diskSpecs, bandwidthSpecs,
    stoppedContainerOptions, stoppedContainersLoading,
    isLxdIncusProvider, canConfigureGpuPassthrough,
    gpuDetecting, gpuChecked, detectedGpus, selectedGpuIndices,
    showExportDialog, exportedCodesText, exportResult, exportLoading, exportFields,
    allExportFields,
    statusTagType, statusLabel,
    handleFilterChange, handleSelectionChange,
    openCreateDialog, cancelCreate, handleCreateDialogClose,
    onProviderChange, onInstanceTypeChange,
    detectGpus, selectAllGpus, deselectAllGpus,
    submitCreate,
    handleExport, resetExportDialog, doExport, copyExportedCodes,
    handleBatchDelete,
    handleSizeChange, handleCurrentChange
  }
}
