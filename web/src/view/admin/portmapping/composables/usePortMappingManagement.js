import { ref, reactive, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { 
  getPortMappings, 
  createPortMapping,
  deletePortMapping, 
  batchDeletePortMappings,
  checkPortAvailable,
  getProviderList,
  getAllInstances,
  syncPortMappings
} from '@/api/admin'

export function usePortMappingManagement() {
  const { t } = useI18n()

  const loading = ref(false)
  const portMappings = ref([])
  const providers = ref([])
  const instances = ref([])
  const currentPage = ref(1)
  const pageSize = ref(10)
  const total = ref(0)
  const selectedPortMappings = ref([])
  let autoRefreshTimer = null
  const syncPreviewVisible = ref(false)
  const syncPreviewLoading = ref(false)
  const syncSubmitting = ref(false)
  const syncPreview = ref({ providers: [], candidateCount: 0 })
  const selectedSyncPortIds = ref([])

  const syncCandidates = computed(() => {
    const providersPreview = syncPreview.value.providers || []
    return providersPreview.flatMap(provider => (provider.candidates || []).map(candidate => ({
      ...candidate,
      providerName: candidate.providerName || provider.providerName,
      providerId: candidate.providerId || provider.providerId
    })))
  })
  const unhealthySyncProviders = computed(() => (syncPreview.value.providers || []).filter(provider => !provider.healthy))
  const allSyncSelected = computed(() => syncCandidates.value.length > 0 && selectedSyncPortIds.value.length === syncCandidates.value.length)

  const searchForm = reactive({ keyword: '', providerId: '', protocol: '', status: '' })

  const addDialogVisible = ref(false)
  const addFormRef = ref()
  const addLoading = ref(false)
  const addForm = reactive({ instanceId: '', guestPort: null, hostPort: 0, portCount: 1, protocol: 'both', description: '', mappingType: 'node', internalHost: '' })

  const checkingPort = ref(false)
  const portCheckResult = ref(null)
  let checkPortTimeout = null

  const addRules = {
    instanceId: [{ required: true, message: t('admin.portMapping.pleaseSelectInstance'), trigger: 'change' }],
    guestPort: [
      { required: true, message: t('admin.portMapping.pleaseEnterInternalPort'), trigger: 'blur' },
      { type: 'number', min: 1, max: 65535, message: t('admin.portMapping.portRangeError'), trigger: 'blur' }
    ],
    portCount: [{ type: 'number', min: 1, max: 100, message: t('admin.portMapping.portCountRangeError'), trigger: 'blur' }],
    protocol: [{ required: true, message: t('admin.portMapping.pleaseSelectProtocol'), trigger: 'change' }]
  }

  const getInstanceProviderType = (instance) => {
    if (!instance) return null
    if (instance.providerId && providers.value.length > 0) {
      const prov = providers.value.find(p => p.id === instance.providerId)
      if (prov && prov.type) return prov.type
    }
    if (instance.type) return instance.type
    if (instance.providerType) return instance.providerType
    if (instance.provider) {
      const lower = String(instance.provider).toLowerCase()
      if (['lxd', 'incus', 'proxmox', 'docker', 'qemu', 'kubevirt'].includes(lower)) return lower
      return instance.provider
    }
    if (instance.providerName) {
      const lower = String(instance.providerName).toLowerCase()
      if (['lxd', 'incus', 'proxmox', 'docker', 'qemu', 'kubevirt'].includes(lower)) return lower
      return instance.providerName
    }
    return null
  }

  const supportedInstances = computed(() => {
    return instances.value.filter(instance => {
      const type = getInstanceProviderType(instance)?.toLowerCase()
      return type === 'lxd' || type === 'incus' || type === 'proxmox' || type === 'qemu' || type === 'kubevirt' || type === 'docker' || type === 'podman' || type === 'containerd'
    })
  })

  const selectedInstanceProvider = computed(() => {
    if (!addForm.instanceId) return '-'
    const instance = instances.value.find(i => i.id === addForm.instanceId)
    if (!instance) return '-'
    return getInstanceProviderType(instance) || '-'
  })

  const portRangePreview = computed(() => {
    if (!addForm.portCount || addForm.portCount <= 1) return ''
    const guestStart = addForm.guestPort || 0
    const guestEnd = guestStart + addForm.portCount - 1
    if (!addForm.hostPort || addForm.hostPort === 0) {
      return t('admin.portMapping.guestPortRange', { start: guestStart, end: guestEnd }) + ' → ' + t('admin.portMapping.autoAssign')
    }
    const hostStart = addForm.hostPort
    const hostEnd = hostStart + addForm.portCount - 1
    return t('admin.portMapping.guestPortRange', { start: guestStart, end: guestEnd }) + ' → ' + 
           t('admin.portMapping.hostPortRange', { start: hostStart, end: hostEnd })
  })

  const updatePortRange = () => {
    portCheckResult.value = null
    if (addForm.guestPort && addForm.portCount) {
      if (addForm.guestPort + addForm.portCount - 1 > 65535) ElMessage.warning(t('admin.portMapping.portRangeExceedsLimit'))
    }
    if (addForm.hostPort && addForm.hostPort > 0 && addForm.portCount) {
      if (addForm.hostPort + addForm.portCount - 1 > 65535) ElMessage.warning(t('admin.portMapping.portRangeExceedsLimit'))
    }
  }

  // 动态端口映射提示：根据所选实例的 Provider 类型显示不同提示
  const portMappingHint = computed(() => {
    if (!addForm.instanceId) return t('admin.portMapping.onlyLxdIncusProxmox')
    const instance = instances.value.find(i => i.id === addForm.instanceId)
    if (!instance) return t('admin.portMapping.onlyLxdIncusProxmox')
    const providerType = getInstanceProviderType(instance)?.toLowerCase()
    if (['docker', 'podman', 'containerd'].includes(providerType)) {
      return t('admin.portMapping.dockerNotSupported')
    }
    return t('admin.portMapping.onlyLxdIncusProxmox')
  })

  const checkPortAvailabilityDebounced = () => {
    if (checkPortTimeout) clearTimeout(checkPortTimeout)
    checkPortTimeout = setTimeout(() => checkPortAvailabilityFn(), 500)
  }

  const checkPortAvailabilityFn = async () => {
    if (!addForm.hostPort || addForm.hostPort === 0) { portCheckResult.value = null; return }
    if (!addForm.instanceId) { ElMessage.warning(t('admin.portMapping.pleaseSelectInstanceFirst')); return }
    const selectedInstance = supportedInstances.value.find(inst => inst.id === addForm.instanceId)
    if (!selectedInstance || !selectedInstance.providerId) { ElMessage.error(t('admin.portMapping.cannotGetProviderInfo')); return }
    const portCount = addForm.portCount || 1
    checkingPort.value = true
    portCheckResult.value = null
    try {
      const response = await checkPortAvailable({ providerId: selectedInstance.providerId, hostPort: addForm.hostPort, protocol: addForm.protocol, portCount })
      if ((response.code === 200) && response.data) {
        const data = response.data
        portCheckResult.value = {
          available: data.available,
          message: data.available 
            ? (portCount > 1 ? t('admin.portMapping.portRangeAvailable', { start: addForm.hostPort, end: addForm.hostPort + portCount - 1 }) : t('admin.portMapping.portAvailable', { port: addForm.hostPort }))
            : (data.unavailablePorts?.length > 0 ? t('admin.portMapping.portsUnavailable', { ports: data.unavailablePorts.join(', ') }) : t('admin.portMapping.portUnavailable', { port: addForm.hostPort })),
          suggestion: data.suggestion || ''
        }
      } else { throw new Error(response.message || 'Check failed') }
    } catch (error) {
      portCheckResult.value = { available: false, message: t('admin.portMapping.portCheckFailed'), suggestion: '' }
    } finally { checkingPort.value = false }
  }

  const instanceFilterText = ref('')
  const filteredInstancesAll = computed(() => {
    if (!instanceFilterText.value) return supportedInstances.value
    const searchText = instanceFilterText.value.toLowerCase()
    return supportedInstances.value.filter(instance => {
      const name = (instance.name || '').toLowerCase()
      const id = String(instance.id || '').toLowerCase()
      const providerType = (getInstanceProviderType(instance) || '').toLowerCase()
      const providerName = (instance.providerName || '').toLowerCase()
      return name.includes(searchText) || id.includes(searchText) || providerType.includes(searchText) || providerName.includes(searchText)
    })
  })
  const filteredInstances = computed(() => filteredInstancesAll.value.slice(0, 10))
  const filteredInstancesCount = computed(() => filteredInstancesAll.value.length)
  const filterInstances = (query) => { instanceFilterText.value = query }

  const getProviderTagType = (providerType) => {
    const type = providerType?.toLowerCase()
    switch (type) {
      case 'lxd': return 'success'
      case 'incus': return 'primary'
      case 'proxmox': return 'warning'
      case 'docker': return 'info'
      case 'qemu': case 'kubevirt': return 'danger'
      default: return 'info'
    }
  }

  const loadPortMappings = async () => {
    loading.value = true
    try {
      const params = { page: currentPage.value, pageSize: pageSize.value, ...searchForm }
      const response = await getPortMappings(params)
      portMappings.value = response.data.items || []
      total.value = response.data.total || 0
      checkAndStartAutoRefresh()
    } catch (error) {
      ElMessage.error(t('admin.portMapping.loadListFailed'))
    } finally { loading.value = false }
  }

  const checkAndStartAutoRefresh = () => {
    const hasProcessingPorts = portMappings.value.some(port => port.status === 'creating' || port.status === 'deleting' || port.status === 'pending')
    if (hasProcessingPorts) {
      if (!autoRefreshTimer) { autoRefreshTimer = setInterval(() => loadPortMappings(), 5000) }
    } else {
      if (autoRefreshTimer) { clearInterval(autoRefreshTimer); autoRefreshTimer = null }
    }
  }

  const loadProviders = async () => {
    try { const response = await getProviderList({ page: 1, pageSize: 1000 }); providers.value = response.data.list || [] }
    catch (error) { ElMessage.error(t('admin.portMapping.loadProvidersFailed')) }
  }

  const loadInstances = async () => {
    try { const response = await getAllInstances({ page: 1, pageSize: 1000 }); instances.value = response.data.list || [] }
    catch (error) { ElMessage.error(t('admin.portMapping.loadInstancesFailed')) }
  }

  const searchPortMappings = () => { currentPage.value = 1; loadPortMappings() }
  const resetSearch = () => { Object.assign(searchForm, { keyword: '', providerId: '', protocol: '', status: '' }); searchPortMappings() }
  const isDeletablePort = (row) => row.portType === 'manual' || row.portType === 'batch'
  const handleSelectionChange = (selection) => { selectedPortMappings.value = selection }
  const handleSizeChange = (val) => { pageSize.value = val; loadPortMappings() }
  const handleCurrentChange = (val) => { currentPage.value = val; loadPortMappings() }
  const formatTime = (time) => { if (!time) return ''; return new Date(time).toLocaleString() }

  const deletePortMappingHandler = async (id) => {
    try {
      await ElMessageBox.confirm(t('admin.portMapping.deleteConfirm'), t('common.warning'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      await deletePortMapping(id)
      ElMessage.success(t('admin.portMapping.deletePortTaskCreated'))
      loadPortMappings()
    } catch (error) { if (error !== 'cancel') ElMessage.error(error.message || t('admin.portMapping.deletePortFailed')) }
  }

  const batchDeleteDirect = async () => {
    if (selectedPortMappings.value.length === 0) { ElMessage.warning(t('admin.portMapping.selectPortsToDelete')); return }
    if (selectedPortMappings.value.some(item => item.portType !== 'manual' && item.portType !== 'batch')) { ElMessage.warning(t('admin.portMapping.onlyManualPortsCanDelete')); return }
    try {
      await ElMessageBox.confirm(t('admin.portMapping.batchDeleteConfirm', { count: selectedPortMappings.value.length }), t('admin.portMapping.batchDeleteTitle'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      const ids = selectedPortMappings.value.map(item => item.id)
      const response = await batchDeletePortMappings(ids)
      const data = response.data || {}
      const taskIds = data.taskIds || []
      const failedPorts = data.failedPorts || []
      if (failedPorts.length > 0) ElMessage.warning(t('admin.portMapping.batchDeletePartialSuccess', { success: taskIds.length, failed: failedPorts.length }))
      else ElMessage.success(t('admin.portMapping.batchDeleteTasksCreated', { count: taskIds.length }))
      selectedPortMappings.value = []
      loadPortMappings()
    } catch (error) { if (error !== 'cancel') ElMessage.error(error.message || t('admin.portMapping.batchDeleteFailed')) }
  }

  const openAddDialog = async () => {
    Object.assign(addForm, { instanceId: '', guestPort: null, hostPort: 0, portCount: 1, protocol: 'both', description: '', mappingType: 'node', internalHost: '' })
    portCheckResult.value = null
    checkingPort.value = false
    if (instances.value.length === 0) await loadInstances()
    if (supportedInstances.value.length === 0) ElMessage.warning(t('admin.portMapping.noSupportedInstances'))
    addDialogVisible.value = true
  }

  const onInstanceChange = () => {
    // Docker/Podman/Containerd/Orbstack 实例自动切换为控制端转发模式
    if (addForm.instanceId) {
      const instance = instances.value.find(i => i.id === addForm.instanceId)
      if (instance) {
        const providerType = getInstanceProviderType(instance)?.toLowerCase()
        if (['docker', 'podman', 'containerd', 'orbstack'].includes(providerType)) {
          addForm.mappingType = 'controller'
        }
      }
    }
  }

  const submitAdd = async () => {
    if (!addFormRef.value) return
    try {
      await addFormRef.value.validate()
      const instance = instances.value.find(i => i.id === addForm.instanceId)
      if (!instance) { ElMessage.error(t('admin.portMapping.instanceNotFound')); return }
      const providerType = getInstanceProviderType(instance)?.toLowerCase()
      // Docker/Podman/Containerd/Orbstack 仅支持控制端转发模式
      if (['docker', 'podman', 'containerd', 'orbstack'].includes(providerType)) {
        if (addForm.mappingType !== 'controller') {
          ElMessage.error(t('admin.portMapping.dockerOnlyController'))
          return
        }
      }
      // 验证支持的 Provider 类型
      const supportedTypes = ['lxd', 'incus', 'proxmox', 'qemu', 'kubevirt', 'vmware', 'virtualbox', 'multipass', 'vagrant', 'docker', 'podman', 'containerd', 'orbstack']
      if (!supportedTypes.includes(providerType)) { ElMessage.error(t('admin.portMapping.onlyLxdIncusProxmoxSupported')); return }
      addLoading.value = true
      const data = { instanceId: addForm.instanceId, guestPort: addForm.guestPort, hostPort: addForm.hostPort || 0, portCount: addForm.portCount || 1, protocol: addForm.protocol, description: addForm.description, mappingType: addForm.mappingType || 'node', internalHost: addForm.internalHost || '' }
      await createPortMapping(data)
      if (data.portCount > 1) ElMessage.success(t('admin.portMapping.batchAddPortTaskCreated', { count: data.portCount }))
      else ElMessage.success(t('admin.portMapping.addPortTaskCreated'))
      addDialogVisible.value = false
      loadPortMappings()
    } catch (error) { ElMessage.error(error.message || t('admin.portMapping.addPortFailed')) }
    finally { addLoading.value = false }
  }

  const handleSyncPortMappings = async () => {
    syncPreviewLoading.value = true
    try {
      const response = await syncPortMappings({ dryRun: true })
      const preview = response.data || { providers: [], candidateCount: 0 }
      syncPreview.value = preview
      selectedSyncPortIds.value = syncCandidates.value.map(item => item.portId)
      if (!preview.candidateCount) {
        const failedCount = (preview.providers || []).filter(provider => !provider.healthy).length
        if (failedCount > 0) ElMessage.warning(t('admin.portMapping.syncPreviewNoCandidatesWithErrors', { count: failedCount }))
        else ElMessage.success(t('admin.portMapping.syncPreviewNoCandidates'))
        return
      }
      syncPreviewVisible.value = true
    } catch (error) { if (error !== 'cancel') ElMessage.error(error.message || t('admin.portMapping.syncFailed')) }
    finally { syncPreviewLoading.value = false }
  }

  const toggleAllSyncCandidates = () => {
    if (allSyncSelected.value) selectedSyncPortIds.value = []
    else selectedSyncPortIds.value = syncCandidates.value.map(item => item.portId)
  }

  const confirmSyncPortMappings = async () => {
    if (selectedSyncPortIds.value.length === 0) {
      ElMessage.warning(t('admin.portMapping.syncSelectAtLeastOne'))
      return
    }
    syncSubmitting.value = true
    try {
      await ElMessageBox.confirm(
        t('admin.portMapping.syncExecuteConfirm', { count: selectedSyncPortIds.value.length }),
        t('admin.portMapping.syncConfirmTitle'),
        { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' }
      )
      const selectedSet = new Set(selectedSyncPortIds.value)
      const selectedCandidates = syncCandidates.value.filter(item => selectedSet.has(item.portId))
      const excludedPortIds = syncCandidates.value.filter(item => !selectedSet.has(item.portId)).map(item => item.portId)
      const providerIds = [...new Set(selectedCandidates.map(item => item.providerId).filter(Boolean))]
      await syncPortMappings({ providerIds, includedPortIds: selectedSyncPortIds.value, excludedPortIds })
      ElMessage.success(t('admin.portMapping.syncTaskCreated'))
      syncPreviewVisible.value = false
      setTimeout(() => loadPortMappings(), 1000)
    } catch (error) {
      if (error !== 'cancel') ElMessage.error(error.message || t('admin.portMapping.syncFailed'))
    } finally {
      syncSubmitting.value = false
    }
  }

  const formatSyncReason = (reason) => {
    if (reason === 'no_port_mapping') return t('admin.portMapping.syncReasonNoPortMapping')
    if (reason === 'orphan_instance') return t('admin.portMapping.syncReasonOrphanInstance')
    return reason || '-'
  }

  const cleanupAutoRefresh = () => {
    if (autoRefreshTimer) { clearInterval(autoRefreshTimer); autoRefreshTimer = null }
  }

  return {
    loading, portMappings, providers, instances, currentPage, pageSize, total,
    selectedPortMappings, searchForm,
    syncPreviewVisible, syncPreviewLoading, syncSubmitting, syncPreview,
    selectedSyncPortIds, syncCandidates, unhealthySyncProviders, allSyncSelected,
    addDialogVisible, addFormRef, addLoading, addForm, addRules,
    checkingPort, portCheckResult,
    supportedInstances, selectedInstanceProvider, portRangePreview, portMappingHint,
    instanceFilterText, filteredInstances, filteredInstancesCount,
    getInstanceProviderType, getProviderTagType,
    loadPortMappings, loadProviders, loadInstances,
    searchPortMappings, resetSearch, isDeletablePort,
    handleSelectionChange, handleSizeChange, handleCurrentChange,
    deletePortMappingHandler, batchDeleteDirect,
    formatTime, openAddDialog, onInstanceChange, submitAdd,
    handleSyncPortMappings, confirmSyncPortMappings, toggleAllSyncCandidates, formatSyncReason, filterInstances,
    updatePortRange, checkPortAvailabilityDebounced,
    checkPortAvailability: checkPortAvailabilityFn,
    cleanupAutoRefresh,
    t
  }
}
