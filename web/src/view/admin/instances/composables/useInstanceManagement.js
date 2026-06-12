import { ref, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getAllInstances, adminInstanceAction, adminBatchInstanceAction, resetInstancePassword, getAdminInstanceNewPassword, setInstanceExpiry, freezeInstance, unfreezeInstance, getUserList, createAdminInstanceShare } from '@/api/admin'
import { adminTransferInstance } from '@/api/features'
import { useSSHStore } from '@/pinia/modules/ssh'
import { copyToClipboard } from '@/utils/clipboard'
import { normalizeShareURL, showShareLinkDialog } from '@/utils/share-link'

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

  const pollForAdminNewPassword = (instanceId, taskId) => {
    let attempts = 0
    const maxAttempts = 20

    const attempt = async () => {
      attempts++
      try {
        const res = await getAdminInstanceNewPassword(instanceId, taskId)
        if (res.code === 200 && res.data?.newPassword) {
          const pwd = res.data.newPassword
          await ElMessageBox.alert(
            `<div style="word-break:break-all">${t('admin.instances.newPassword')}: <strong style="user-select:all;font-family:monospace">${pwd}</strong></div>`,
            t('admin.instances.resetPasswordTitle'),
            { dangerouslyUseHTMLString: true, confirmButtonText: t('common.confirm') }
          )
          await loadInstances()
          return
        }
      } catch {
        // task not ready yet, continue polling
      }
      if (attempts < maxAttempts) {
        setTimeout(attempt, 3000)
      } else {
        ElMessage.warning(t('admin.instances.taskCreated', { action: t('admin.instances.resetPassword') }))
      }
    }

    setTimeout(attempt, 3000)
  }

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
      if (action === 'resetPassword') {
        const pwdRes = await resetInstancePassword(instanceId)
        const taskId = pwdRes?.data?.taskId
        ElMessage.success(t('admin.instances.taskCreated', { action: actionText }))
        actionDialogVisible.value = false
        actionInstance.value = null
        if (taskId) {
          pollForAdminNewPassword(instanceId, taskId)
        }
      } else {
        await adminInstanceAction(instanceId, action)
        ElMessage.success(t('admin.instances.taskCreated', { action: actionText }))
        actionDialogVisible.value = false
        actionInstance.value = null
        setTimeout(() => loadInstances(), action === 'delete' ? 1000 : 500)
      }
    } catch (error) {
      if (error !== 'cancel') { ElMessage.error(t('admin.instances.actionFailed', { action: actionText })); await loadInstances() }
    } finally {
      actionLoading.value = false
    }
  }

  const getStatusType = (status) => {
    const types = { running: 'success', stopped: 'info', error: 'danger', failed: 'danger', starting: 'warning', stopping: 'warning', creating: 'warning', restarting: 'warning', rebuilding: 'warning', resetting: 'warning', deleting: 'danger', deleted: 'info' }
    return types[status] || 'info'
  }

  const getStatusText = (status) => {
    const texts = {
      running: t('admin.instances.statusRunning'), stopped: t('admin.instances.statusStopped'), error: t('admin.instances.statusError'),
      failed: t('admin.instances.statusFailed'), starting: t('admin.instances.statusStarting'), stopping: t('admin.instances.statusStopping'),
      creating: t('admin.instances.statusCreating'), restarting: t('admin.instances.statusRestarting'), rebuilding: t('admin.instances.statusRebuilding'), resetting: t('admin.instances.statusResetting'), deleting: t('admin.instances.statusDeleting'), deleted: t('admin.instances.statusDeleted')
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
    if (!instance.hasSshMapping && instance.networkType === 'no_port_mapping') { ElMessage.warning(t('admin.instances.sshNoPortMapping')); return }
    if (!sshStore.hasConnection(instance.id)) { sshStore.createConnection(instance.id, instance.name, true) } else { sshStore.showConnection(instance.id) }
  }

  const handleSelectionChange = (selection) => { selectedInstances.value = selection }

  const setBatchOptimisticStatus = (ids, status) => {
    ids.forEach(id => {
      const idx = instances.value.findIndex(item => item.id === id)
      if (idx !== -1) instances.value[idx].status = status
    })
  }

  const showBatchActionResult = (action, success, fail) => {
    if (fail === 0) {
      const key = action === 'delete' ? 'batchDeleteSuccess' : action === 'start' ? 'batchStartSuccess' : 'batchStopSuccess'
      ElMessage.success(t(`admin.instances.${key}`, { count: success }))
      return
    }
    if (success === 0) {
      const key = action === 'delete' ? 'batchDeleteAllFailed' : action === 'start' ? 'batchStartAllFailed' : 'batchStopAllFailed'
      ElMessage.error(t(`admin.instances.${key}`))
      return
    }
    const key = action === 'delete' ? 'batchDeletePartialSuccess' : action === 'start' ? 'batchStartPartialSuccess' : 'batchStopPartialSuccess'
    ElMessage.warning(t(`admin.instances.${key}`, { success, fail }))
  }

  const runBatchInstanceAction = async (action, options) => {
    const selected = [...selectedInstances.value]
    if (selected.length === 0) { ElMessage.warning(t(options.emptyWarning)); return }
    try {
      if (options.confirmMessage) {
        await ElMessageBox.confirm(
          t(options.confirmMessage, { count: selected.length }),
          t(options.confirmTitle),
          { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: options.confirmType || 'warning' }
        )
      }
      const ids = selected.map(item => item.id)
      if (options.optimisticStatus) setBatchOptimisticStatus(ids, options.optimisticStatus)
      const response = await adminBatchInstanceAction(ids, action)
      const data = response.data || {}
      showBatchActionResult(action, data.successCount || 0, data.failCount || 0)
      selectedInstances.value = []
      setTimeout(() => loadInstances(), action === 'delete' ? 1000 : 500)
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t(options.failedMessage))
        await loadInstances()
      }
    }
  }

  const batchDeleteInstances = () => runBatchInstanceAction('delete', {
    emptyWarning: 'admin.instances.selectDeleteWarning',
    confirmMessage: 'admin.instances.batchDeleteConfirm',
    confirmTitle: 'admin.instances.batchDeleteTitle',
    confirmType: 'warning',
    optimisticStatus: 'deleting',
    failedMessage: 'admin.instances.batchDeleteFailed'
  })

  const batchStartInstances = () => runBatchInstanceAction('start', {
    emptyWarning: 'admin.instances.selectStartWarning',
    confirmMessage: 'admin.instances.batchStartConfirm',
    confirmTitle: 'admin.instances.batchStartTitle',
    confirmType: 'warning',
    optimisticStatus: 'starting',
    failedMessage: 'admin.instances.batchStartFailed'
  })

  const batchStopInstances = () => runBatchInstanceAction('stop', {
    emptyWarning: 'admin.instances.selectStopWarning',
    confirmMessage: 'admin.instances.batchStopConfirm',
    confirmTitle: 'admin.instances.batchStopTitle',
    confirmType: 'warning',
    optimisticStatus: 'stopping',
    failedMessage: 'admin.instances.batchStopFailed'
  })

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

  const createShareLink = async (instance) => {
    if (!instance || !instance.id) {
      ElMessage.error(t('admin.instances.instanceNotFound'))
      return
    }
    try {
      const { value } = await ElMessageBox.prompt(
        t('admin.instances.shareExpiryPrompt'),
        t('admin.instances.createShareLink'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          inputValue: '30',
          inputPattern: /^([1-9]\d{0,3}|100[0-7]\d|10080)$/,
          inputErrorMessage: t('admin.instances.shareExpiryInvalid')
        }
      )
      const minutes = Number(value || 30)
      const response = await createAdminInstanceShare(instance.id, { expiresInMinutes: minutes })
      const url = normalizeShareURL(response.data?.url)
      if (!url) {
        ElMessage.error(t('admin.instances.shareLinkCreateFailed'))
        return
      }
      await showShareLinkDialog(url, { title: t('admin.instances.createShareLink'), t })
    } catch (error) {
      if (error !== 'cancel') {
        console.error('创建分享链接失败:', error)
        ElMessage.error(t('admin.instances.shareLinkCreateFailed'))
      }
    }
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
    createShareLink,
    t
  }
}
