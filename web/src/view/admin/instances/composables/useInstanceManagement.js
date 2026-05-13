import { ref, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getAllInstances, adminInstanceAction, resetInstancePassword, setInstanceExpiry, freezeInstance, unfreezeInstance, getUserList } from '@/api/admin'
import { adminTransferInstance } from '@/api/features'
import { useSSHStore } from '@/pinia/modules/ssh'

export function useInstanceManagement() {
  const { t, locale } = useI18n()
  const sshStore = useSSHStore()

  const instances = ref([])
  const loading = ref(false)
  const detailDialogVisible = ref(false)
  const actionDialogVisible = ref(false)
  const selectedInstance = ref(null)
  const actionInstance = ref(null)
  const actionLoading = ref(false)
  const showPassword = ref(false)
  const selectedInstances = ref([])
  const transferDialogVisible = ref(false)
  const transferLoading = ref(false)
  const transferForm = ref({ instanceId: null, instanceName: '', targetUserId: null })
  const searchingUsers = ref(false)
  const userOptions = ref([])
  const tableRef = ref(null)

  const filters = ref({
    instanceName: '',
    providerName: '',
    ownerName: '',
    status: '',
    instanceType: ''
  })

  const pagination = ref({
    page: 1,
    pageSize: 10,
    total: 0
  })

  const loadInstances = async () => {
    loading.value = true
    try {
      const params = {
        page: pagination.value.page,
        pageSize: pagination.value.pageSize,
        name: filters.value.instanceName || undefined,
        providerName: filters.value.providerName || undefined,
        ownerName: filters.value.ownerName || undefined,
        status: filters.value.status || undefined,
        instance_type: filters.value.instanceType || undefined
      }
      Object.keys(params).forEach(key => {
        if (params[key] === undefined) delete params[key]
      })
      const response = await getAllInstances(params)
      instances.value = response.data.list || []
      pagination.value.total = response.data.total || 0
    } catch (error) {
      ElMessage.error(t('admin.instances.loadFailed'))
      console.error('Load instances error:', error)
    } finally {
      loading.value = false
    }
  }

  const handleSearch = () => { pagination.value.page = 1; loadInstances() }
  const handleReset = () => {
    Object.assign(filters.value, { instanceName: '', providerName: '', ownerName: '', status: '', instanceType: '' })
    pagination.value.page = 1
    loadInstances()
  }
  const handleSizeChange = (val) => { pagination.value.pageSize = val; pagination.value.page = 1; loadInstances() }
  const handleCurrentChange = (val) => { pagination.value.page = val; loadInstances() }

  const viewInstanceDetail = (instance) => {
    selectedInstance.value = instance
    showPassword.value = false
    detailDialogVisible.value = true
  }

  const showActionDialog = (instance) => { actionInstance.value = instance; actionDialogVisible.value = true }

  const performAction = async (action) => {
    if (action === 'setExpiry') { actionDialogVisible.value = false; await handleSetInstanceExpiry(actionInstance.value); actionInstance.value = null; return }
    if (action === 'freeze') { actionDialogVisible.value = false; await handleFreezeInstance(actionInstance.value); actionInstance.value = null; return }
    if (action === 'unfreeze') { actionDialogVisible.value = false; await handleUnfreezeInstance(actionInstance.value); actionInstance.value = null; return }

    const actionText = {
      'start': t('common.start'), 'stop': t('common.stop'), 'restart': t('common.restart'),
      'reset': t('admin.instances.resetSystem'), 'resetPassword': t('admin.instances.resetPassword'), 'delete': t('common.delete')
    }[action]

    try {
      await ElMessageBox.confirm(
        t('admin.instances.manageConfirm', { action: actionText, name: actionInstance.value.name }),
        t('admin.instances.manageTitle', { action: actionText }),
        { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' }
      )
      actionLoading.value = true
      const instanceId = actionInstance.value.id
      const instanceIndex = instances.value.findIndex(i => i.id === instanceId)
      if (instanceIndex !== -1) {
        const statusMap = { 'start': 'starting', 'stop': 'stopping', 'restart': 'restarting', 'reset': 'resetting', 'resetPassword': instances.value[instanceIndex].status, 'delete': 'deleting' }
        instances.value[instanceIndex].status = statusMap[action]
      }
      if (action === 'resetPassword') { await resetInstancePassword(instanceId) } else { await adminInstanceAction(instanceId, action) }
      ElMessage.success(t('admin.instances.taskCreated', { action: actionText }))
      actionDialogVisible.value = false
      actionInstance.value = null
      setTimeout(() => loadInstances(), action === 'delete' ? 1000 : 500)
    } catch (error) {
      if (error !== 'cancel') { ElMessage.error(t('admin.instances.actionFailed', { action: actionText })); await loadInstances() }
    } finally {
      actionLoading.value = false
    }
  }

  const getStatusType = (status) => {
    const types = { running: 'success', stopped: 'info', error: 'danger', failed: 'danger', starting: 'warning', stopping: 'warning', creating: 'warning', restarting: 'warning', resetting: 'warning', deleting: 'danger' }
    return types[status] || 'info'
  }

  const getStatusText = (status) => {
    const texts = {
      running: t('admin.instances.statusRunning'), stopped: t('admin.instances.statusStopped'), error: t('admin.instances.statusError'),
      failed: t('admin.instances.statusFailed'), starting: t('admin.instances.statusStarting'), stopping: t('admin.instances.statusStopping'),
      creating: t('admin.instances.statusCreating'), restarting: t('admin.instances.statusRestarting'), resetting: t('admin.instances.statusResetting'), deleting: t('admin.instances.statusDeleting')
    }
    return texts[status] || status
  }

  const formatDate = (dateString) => {
    if (!dateString) return t('admin.instances.notSet')
    return new Date(dateString).toLocaleString(locale.value, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })
  }

  const formatMemory = (memory) => {
    if (!memory) return '0MB'
    return memory >= 1024 ? `${(memory / 1024).toFixed(1)}GB` : `${memory}MB`
  }

  const formatDisk = (disk) => {
    if (!disk) return '0MB'
    if (disk >= 1024 * 1024) return `${(disk / (1024 * 1024)).toFixed(1)}TB`
    return disk >= 1024 ? `${(disk / 1024).toFixed(1)}GB` : `${disk}MB`
  }

  const formatTraffic = (traffic) => {
    if (!traffic) return '0MB'
    if (traffic >= 1024 * 1024) return `${(traffic / (1024 * 1024)).toFixed(2)}TB`
    return traffic >= 1024 ? `${(traffic / 1024).toFixed(2)}GB` : `${traffic}MB`
  }

  const isExpired = (expiredAt) => { if (!expiredAt) return false; return new Date(expiredAt) < new Date() }
  const isExpiringSoon = (expiredAt) => {
    if (!expiredAt) return false
    const daysDiff = (new Date(expiredAt) - new Date()) / (1000 * 60 * 60 * 24)
    return daysDiff > 0 && daysDiff <= 7
  }

  const openSSHTerminal = (instance) => {
    if (!instance.id) { ElMessage.error(t('admin.instances.instanceNotFound')); return }
    if (instance.status !== 'running') { ElMessage.warning(t('admin.instances.instanceNotRunning')); return }
    if (!instance.password) { ElMessage.warning(t('admin.instances.noPassword')); return }
    if (instance.networkType === 'no_port_mapping') { ElMessage.warning(t('admin.instances.sshNoPortMapping')); return }
    if (!sshStore.hasConnection(instance.id)) { sshStore.createConnection(instance.id, instance.name, true) } else { sshStore.showConnection(instance.id) }
  }

  const handleSelectionChange = (selection) => { selectedInstances.value = selection }

  const batchDeleteInstances = async () => {
    if (selectedInstances.value.length === 0) { ElMessage.warning(t('admin.instances.selectDeleteWarning')); return }
    try {
      await ElMessageBox.confirm(t('admin.instances.batchDeleteConfirm', { count: selectedInstances.value.length }), t('admin.instances.batchDeleteTitle'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      let successCount = 0, failCount = 0
      for (const instance of selectedInstances.value) {
        try { await adminInstanceAction(instance.id, 'delete'); successCount++ } catch (error) { failCount++ }
      }
      if (failCount === 0) ElMessage.success(t('admin.instances.batchDeleteSuccess', { count: successCount }))
      else if (successCount === 0) ElMessage.error(t('admin.instances.batchDeleteAllFailed'))
      else ElMessage.warning(t('admin.instances.batchDeletePartialSuccess', { success: successCount, fail: failCount }))
      await loadInstances()
      selectedInstances.value = []
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.instances.batchDeleteFailed')) }
  }

  const batchStartInstances = async () => {
    if (selectedInstances.value.length === 0) { ElMessage.warning(t('admin.instances.selectStartWarning')); return }
    try {
      let success = 0, fail = 0
      selectedInstances.value.forEach(inst => { const idx = instances.value.findIndex(i => i.id === inst.id); if (idx !== -1) instances.value[idx].status = 'starting' })
      for (const inst of selectedInstances.value) {
        try { await adminInstanceAction(inst.id, 'start'); success++ } catch (e) { fail++; const idx = instances.value.findIndex(i => i.id === inst.id); if (idx !== -1) instances.value[idx].status = 'stopped' }
      }
      if (fail === 0) ElMessage.success(t('admin.instances.batchStartSuccess', { count: success }))
      else ElMessage.warning(t('admin.instances.batchStartPartialSuccess', { success, fail }))
      setTimeout(() => loadInstances(), 500)
      selectedInstances.value = []
    } catch (err) { ElMessage.error(t('admin.instances.batchStartFailed')); await loadInstances() }
  }

  const batchStopInstances = async () => {
    if (selectedInstances.value.length === 0) { ElMessage.warning(t('admin.instances.selectStopWarning')); return }
    try {
      let success = 0, fail = 0
      selectedInstances.value.forEach(inst => { const idx = instances.value.findIndex(i => i.id === inst.id); if (idx !== -1) instances.value[idx].status = 'stopping' })
      for (const inst of selectedInstances.value) {
        try { await adminInstanceAction(inst.id, 'stop'); success++ } catch (e) { fail++; const idx = instances.value.findIndex(i => i.id === inst.id); if (idx !== -1) instances.value[idx].status = 'running' }
      }
      if (fail === 0) ElMessage.success(t('admin.instances.batchStopSuccess', { count: success }))
      else ElMessage.warning(t('admin.instances.batchStopPartialSuccess', { success, fail }))
      setTimeout(() => loadInstances(), 500)
      selectedInstances.value = []
    } catch (err) { ElMessage.error(t('admin.instances.batchStopFailed')); await loadInstances() }
  }

  const handleSetInstanceExpiry = async (instance) => {
    try {
      const { value: expiresAt } = await ElMessageBox.prompt(t('admin.instances.setExpiryPrompt'), t('admin.instances.setExpiry'), {
        confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'),
        inputPattern: /^(\d{4}-\d{2}-\d{2}( \d{2}:\d{2}:\d{2})?)?$/,
        inputErrorMessage: t('admin.instances.dateFormatError'),
        inputPlaceholder: instance.expiresAt ? formatDate(instance.expiresAt) : '2024-12-31 23:59:59',
        inputValue: instance.expiresAt ? formatDate(instance.expiresAt) : ''
      })
      await setInstanceExpiry({ instanceId: instance.id, expiresAt: expiresAt ? new Date(expiresAt).toISOString() : null })
      ElMessage.success(t('admin.instances.setExpirySuccess'))
      await loadInstances()
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.instances.setExpiryFailed')) }
  }

  const handleFreezeInstance = async (instance) => {
    try {
      const { value: reason } = await ElMessageBox.prompt(t('admin.instances.freezePrompt'), t('admin.instances.freeze'), {
        confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), inputPlaceholder: t('admin.instances.enterFreezeReason')
      })
      await freezeInstance({ instanceId: instance.id, reason: reason || '' })
      ElMessage.success(t('admin.instances.freezeSuccess'))
      await loadInstances()
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.instances.freezeFailed')) }
  }

  const handleUnfreezeInstance = async (instance) => {
    try {
      await ElMessageBox.confirm(t('admin.instances.unfreezeConfirm'), t('admin.instances.unfreeze'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      await unfreezeInstance({ instanceId: instance.id })
      ElMessage.success(t('admin.instances.unfreezeSuccess'))
      await loadInstances()
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.instances.unfreezeFailed')) }
  }

  const handleWindowResize = () => { nextTick(() => { if (tableRef.value) tableRef.value.doLayout() }) }

  const showTransferDialog = (instance) => {
    transferForm.value = { instanceId: instance.id, instanceName: instance.name || instance.uuid, targetUserId: null }
    userOptions.value = []
    transferDialogVisible.value = true
  }

  const searchUsers = async (query) => {
    if (!query) {
      userOptions.value = []
      return
    }
    searchingUsers.value = true
    try {
      const res = await getUserList({ nickname: query, page: 1, pageSize: 20 })
      const list = res?.data?.list || res?.data || []
      userOptions.value = list.map(u => ({ id: u.id, username: u.username, nickname: u.nickname }))
    } catch {
      userOptions.value = []
    } finally {
      searchingUsers.value = false
    }
  }

  const confirmTransfer = async () => {
    try {
      transferLoading.value = true
      const response = await adminTransferInstance({ instanceId: transferForm.value.instanceId, targetUserId: transferForm.value.targetUserId })
      if (response.code === 200) {
        ElMessage.success(t('admin.instances.transferSuccess'))
        transferDialogVisible.value = false
        await loadInstances()
      }
    } catch (error) {
      ElMessage.error(error.response?.data?.message || error.message || t('common.operationFailed'))
    } finally { transferLoading.value = false }
  }

  return {
    instances, loading, detailDialogVisible, actionDialogVisible,
    selectedInstance, actionInstance, actionLoading, showPassword,
    selectedInstances, transferDialogVisible, transferLoading, transferForm, tableRef,
    filters, pagination,
    loadInstances, handleSearch, handleReset, handleSizeChange, handleCurrentChange,
    viewInstanceDetail, showActionDialog, performAction,
    getStatusType, getStatusText, formatDate, formatMemory, formatDisk, formatTraffic,
    isExpired, isExpiringSoon, openSSHTerminal,
    handleSelectionChange, batchDeleteInstances, batchStartInstances, batchStopInstances,
    showTransferDialog, confirmTransfer, handleWindowResize,
    searchUsers, searchingUsers, userOptions,
    t
  }
}
