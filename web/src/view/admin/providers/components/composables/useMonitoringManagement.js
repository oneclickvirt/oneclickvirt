import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { copyToClipboard } from '@/utils/clipboard'
import { formatEndpointHostForUrl } from '@/utils/endpoint'
import {
  getMonitoringConfig,
  updateMonitoringConfig,
  deployAgent,
  uninstallAgent,
  getAgentStatus,
  getProviderMonitors,
  syncProviderMonitors,
  getProviderMonitorSyncTask,
  clearProviderMonitors,
  listAgentMonitors
} from '@/api/admin'
import { isMonitorSyncActive, isMonitorSyncFailed, isMonitorSyncTerminal } from './monitorSyncStatus'

export function useMonitoringManagement(props, emit) {
  const { t } = useI18n()
  const isAgentProvider = computed(() => props.provider?.connectionType === 'agent')

  const activeTab = ref('agent')
  const showConfigEditor = ref(false)
  const configLoading = ref(false)
  const deployLoading = ref(false)
  const uninstallLoading = ref(false)
  const statusLoading = ref(false)
  const saveConfigLoading = ref(false)
  const syncLoading = ref(false)
  const clearMonitorsLoading = ref(false)
  const monitorsLoading = ref(false)
  const listAgentLoading = ref(false)
  const deployOutput = ref('')
  const monitors = ref([])
  const agentOnlineChecked = ref(false)
  const agentIsOnline = ref(false)
  const showToken = ref(false)
  const showAgentMonitors = ref(false)
  const agentMonitors = ref([])
  const monitorsPagination = reactive({ page: 1, pageSize: 10, total: 0 })
  const agentMonitorsPagination = reactive({ page: 1, pageSize: 10, total: 0 })
  const syncTask = ref(null)
  let syncPollTimer = null
  let syncPollGeneration = 0
  let viewGeneration = 0
  let configRequestGeneration = 0
  let monitorsRequestGeneration = 0
  let statusRequestGeneration = 0
  let agentMonitorsRequestGeneration = 0
  const MAX_SYNC_POLL_ATTEMPTS = 60
  const isCurrentView = (generation, providerId) =>
    props.visible && generation === viewGeneration && props.provider?.id === providerId
  const monitorMutationBusy = computed(() =>
    deployLoading.value || uninstallLoading.value || syncLoading.value ||
    clearMonitorsLoading.value || saveConfigLoading.value
  )

  const config = reactive({
    monitoring_mode: 'agent',
    traffic_collect_method: 'nft',
    agent_token: '',
    agent_port: 23782,
    agent_installed: false,
    agent_version: '',
    collect_interval: 5,
    resource_collect_interval: 30,
    extra_exclude_cidrs_v4: '',
    extra_exclude_cidrs_v6: ''
  })

  const editConfig = reactive({
    monitoring_mode: 'agent',
    traffic_collect_method: 'nft',
    agent_port: 23782,
    collect_interval: 5,
    resource_collect_interval: 30,
    extra_exclude_cidrs_v4: '',
    extra_exclude_cidrs_v6: ''
  })

  const agentSwaggerUrl = computed(() => {
    // Agent-mode providers connect via WebSocket tunnel, the agent HTTP port is not directly reachable
    if (isAgentProvider.value) return ''
    if (!props.provider) return ''
    const cleanHost = formatEndpointHostForUrl(props.provider.portIP || props.provider.endpoint || '')
    if (!cleanHost) return ''
    const port = config.agent_port || 23782
    return 'http://' + cleanHost + ':' + port + '/swagger-ui/'
  })

  const agentStatusType = computed(() => {
    if (agentOnlineChecked.value) return agentIsOnline.value ? 'success' : 'danger'
    if (config.agent_installed) return 'warning'
    return 'info'
  })

  const agentStatusText = computed(() => {
    if (agentOnlineChecked.value) return agentIsOnline.value ? t('admin.providers.agentOnline') : t('admin.providers.agentOffline')
    if (config.agent_installed) return t('admin.providers.agentInstalled')
    return t('admin.providers.agentNotInstalled')
  })

  const resetViewState = (resetLoadedData = true) => {
    syncPollGeneration += 1
    viewGeneration += 1
    if (syncPollTimer) {
      clearTimeout(syncPollTimer)
      syncPollTimer = null
    }
    activeTab.value = 'agent'
    syncLoading.value = false
    configLoading.value = false
    monitorsLoading.value = false
    statusLoading.value = false
    listAgentLoading.value = false
    syncTask.value = null
    showConfigEditor.value = false
    deployOutput.value = ''
    agentOnlineChecked.value = false
    agentIsOnline.value = false
    showToken.value = false
    showAgentMonitors.value = false
    agentMonitors.value = []
    monitorsPagination.page = 1
    monitorsPagination.pageSize = 10
    monitorsPagination.total = 0
    agentMonitorsPagination.page = 1
    agentMonitorsPagination.pageSize = 10
    agentMonitorsPagination.total = 0
    if (resetLoadedData) {
      monitors.value = []
      Object.assign(config, {
        monitoring_mode: 'agent',
        traffic_collect_method: 'nft',
        agent_token: '',
        agent_port: 23782,
        agent_installed: false,
        agent_version: '',
        collect_interval: 5,
        resource_collect_interval: 30,
        extra_exclude_cidrs_v4: '',
        extra_exclude_cidrs_v6: ''
      })
      Object.assign(editConfig, {
        monitoring_mode: 'agent',
        traffic_collect_method: 'nft',
        agent_port: 23782,
        collect_interval: 5,
        resource_collect_interval: 30,
        extra_exclude_cidrs_v4: '',
        extra_exclude_cidrs_v6: ''
      })
    }
  }

  const loadDialogData = async () => {
    if (!props.provider) return
    const generation = viewGeneration
    const providerId = props.provider.id
    configLoading.value = true
    try {
      await loadConfig()
      if (!isCurrentView(generation, providerId)) return
      await loadMonitors()
      if (!isCurrentView(generation, providerId)) return
      if (config.agent_installed) handleCheckStatus()
    } finally {
      if (isCurrentView(generation, providerId)) configLoading.value = false
    }
  }

  watch(() => props.visible, async (val) => {
    if (val && props.provider) {
      resetViewState(true)
      await loadDialogData()
    } else if (!val) {
      resetViewState(false)
    }
  })

  watch(() => props.provider?.id, async (id, oldId) => {
    if (!props.visible || !id || id === oldId) return
    resetViewState(true)
    await loadDialogData()
  })

  const loadConfig = async () => {
    if (!props.provider) return
    const generation = viewGeneration
    const providerId = props.provider.id
    const requestGeneration = ++configRequestGeneration
    try {
      const res = await getMonitoringConfig(providerId)
      if (!isCurrentView(generation, providerId) || requestGeneration !== configRequestGeneration) return
      if (res.code === 200) {
        const data = res.data || {}
        Object.assign(config, {
          monitoring_mode: data.monitoring_mode || 'agent',
          traffic_collect_method: data.traffic_collect_method || 'nft',
          agent_token: data.agent_token || '',
          agent_port: data.agent_port || 23782,
          agent_installed: data.agent_installed || false,
          agent_version: data.agent_version || '',
          collect_interval: data.collect_interval || 5,
          resource_collect_interval: data.resource_collect_interval || 30,
          extra_exclude_cidrs_v4: data.extra_exclude_cidrs_v4 || '',
          extra_exclude_cidrs_v6: data.extra_exclude_cidrs_v6 || ''
        })
        Object.assign(editConfig, {
          monitoring_mode: config.monitoring_mode,
          traffic_collect_method: config.traffic_collect_method,
          agent_port: config.agent_port,
          collect_interval: config.collect_interval,
          resource_collect_interval: config.resource_collect_interval,
          extra_exclude_cidrs_v4: config.extra_exclude_cidrs_v4,
          extra_exclude_cidrs_v6: config.extra_exclude_cidrs_v6
        })
      }
    } catch (e) { console.error('Failed to load monitoring config:', e) }
  }

  const loadMonitors = async () => {
    if (!props.provider) return
    const generation = viewGeneration
    const providerId = props.provider.id
    const requestGeneration = ++monitorsRequestGeneration
    monitorsLoading.value = true
    try {
      const res = await getProviderMonitors(providerId, { page: monitorsPagination.page, pageSize: monitorsPagination.pageSize })
      if (!isCurrentView(generation, providerId) || requestGeneration !== monitorsRequestGeneration) return
      if (res.code === 200) {
        const data = res.data || {}
        monitors.value = data.list || []
        monitorsPagination.total = data.total || 0
      }
    } catch (e) { if (isCurrentView(generation, providerId)) console.error('Failed to load monitors:', e) }
    finally {
      if (requestGeneration === monitorsRequestGeneration) monitorsLoading.value = false
    }
  }

  const handleCopyToken = async () => {
    await copyToClipboard(config.agent_token, t('admin.providers.tokenCopied'))
  }

  const handleCopyUrl = async (url) => {
    await copyToClipboard(url, t('admin.providers.urlCopied'))
  }

  const handleDeployAgent = async () => {
    if (!props.provider || monitorMutationBusy.value) return
    if (isAgentProvider.value) {
      ElMessage.warning(t('admin.providers.agentNodeAlreadyManaged'))
      return
    }
    const generation = viewGeneration
    const providerId = props.provider.id
    deployLoading.value = true
    try {
      await ElMessageBox.confirm(t('admin.providers.deployAgentConfirm'), t('common.confirm'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'info' })
      if (!isCurrentView(generation, providerId)) return
      deployOutput.value = ''
      const res = await deployAgent(providerId)
      if (!isCurrentView(generation, providerId)) return
      if (res.code === 200) {
        ElMessage.success(res.msg || t('admin.providers.deployAgentSuccess'))
        const taskId = res.data?.taskId || res.data?.task_id
        deployOutput.value = taskId ? `Task ID: ${taskId}` : ''
        await loadConfig()
        if (isCurrentView(generation, providerId)) await loadMonitors()
      } else {
        ElMessage.error(res.msg || t('admin.providers.deployAgentFailed'))
        deployOutput.value = res.data?.output || res.msg || ''
      }
    } catch (e) {
      if (e !== 'cancel' && isCurrentView(generation, providerId)) {
        ElMessage.error(e?.response?.data?.msg || t('admin.providers.deployAgentFailed'))
        deployOutput.value = e?.response?.data?.data?.output || e?.response?.data?.msg || e.message || ''
      }
    } finally { deployLoading.value = false }
  }

  const handleUninstallAgent = async () => {
    if (!props.provider || monitorMutationBusy.value) return
    if (isAgentProvider.value) {
      ElMessage.warning(t('admin.providers.agentNodeUninstallBlocked'))
      return
    }
    const generation = viewGeneration
    const providerId = props.provider.id
    uninstallLoading.value = true
    try {
      await ElMessageBox.confirm(t('admin.providers.uninstallAgentConfirm'), t('common.confirm'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      if (!isCurrentView(generation, providerId)) return
      const res = await uninstallAgent(providerId)
      if (!isCurrentView(generation, providerId)) return
      if (res.code === 200) {
        ElMessage.success(res.msg || t('admin.providers.uninstallAgentSuccess'))
        const taskId = res.data?.taskId || res.data?.task_id
        deployOutput.value = taskId ? `Task ID: ${taskId}` : ''
      } else { ElMessage.error(res.msg || t('admin.providers.uninstallAgentFailed')) }
    } catch (e) { if (e !== 'cancel' && isCurrentView(generation, providerId)) ElMessage.error(e?.response?.data?.msg || t('admin.providers.uninstallAgentFailed')) }
    finally { uninstallLoading.value = false }
  }

  const handleCheckStatus = async () => {
    if (!props.provider || statusLoading.value) return
    const generation = viewGeneration
    const providerId = props.provider.id
    const requestGeneration = ++statusRequestGeneration
    statusLoading.value = true
    try {
      const res = await getAgentStatus(providerId)
      if (!isCurrentView(generation, providerId) || requestGeneration !== statusRequestGeneration) return
      if (res.code === 200) {
        const data = res.data
        agentOnlineChecked.value = true; agentIsOnline.value = !!data.is_running
        if (data.is_running) ElMessage.success(t('admin.providers.agentOnline') + (data.version ? ' (v' + data.version + ')' : ''))
        else ElMessage.warning(t('admin.providers.agentOffline'))
        if (data.config) {
          const cfg = data.config
          Object.assign(config, {
            monitoring_mode: cfg.monitoring_mode || config.monitoring_mode,
            agent_token: cfg.agent_token || config.agent_token,
            agent_port: cfg.agent_port || config.agent_port,
            agent_installed: cfg.agent_installed !== undefined ? cfg.agent_installed : config.agent_installed,
            agent_version: cfg.agent_version || config.agent_version,
            collect_interval: cfg.collect_interval || config.collect_interval,
            resource_collect_interval: cfg.resource_collect_interval || config.resource_collect_interval,
            extra_exclude_cidrs_v4: cfg.extra_exclude_cidrs_v4 || config.extra_exclude_cidrs_v4,
            extra_exclude_cidrs_v6: cfg.extra_exclude_cidrs_v6 || config.extra_exclude_cidrs_v6
          })
        }
      }
    } catch {
      if (isCurrentView(generation, providerId) && requestGeneration === statusRequestGeneration) {
        agentOnlineChecked.value = true; agentIsOnline.value = false; ElMessage.error(t('admin.providers.checkStatusFailed'))
      }
    } finally {
      if (requestGeneration === statusRequestGeneration) statusLoading.value = false
    }
  }

  const renderSyncSummaryMessage = (task) => {
    const summary = task?.summary || task || {}
    return `${t('admin.providers.syncMonitorsSuccess')}：新增 ${summary.created || 0}，更新 ${summary.updated || 0}，不变 ${summary.unchanged || 0}，清理 ${summary.cleaned || 0}，失败 ${summary.failed || 0}`
  }

  const finishSyncTask = async (task, failed = false) => {
    syncTask.value = task || syncTask.value
    syncLoading.value = false
    if (syncPollTimer) {
      clearTimeout(syncPollTimer)
      syncPollTimer = null
    }
    if (failed) {
      ElMessage.error(task?.error_message || t('admin.providers.syncMonitorsFailed'))
    } else {
      const message = renderSyncSummaryMessage(task)
      if (((task?.summary || task || {}).failed || 0) > 0) ElMessage.warning(message)
      else ElMessage.success(message)
    }
    await loadMonitors()
    if (showAgentMonitors.value) await handleListAgentMonitors()
  }

  const pollSyncTask = async (taskId, generation, attempt = 1) => {
    if (!props.provider || !taskId || generation !== syncPollGeneration || !props.visible) return
    try {
      const res = await getProviderMonitorSyncTask(props.provider.id, taskId)
      if (generation !== syncPollGeneration || !props.visible) return
      if (res.code === 200 && res.data) {
        syncTask.value = res.data
        if (isMonitorSyncTerminal(res.data.status)) {
          await finishSyncTask(res.data, isMonitorSyncFailed(res.data.status))
          return
        }
      }
    } catch (e) {
      if (attempt === 1 || attempt % 10 === 0) console.error('Failed to poll monitor sync task:', e)
    }
    if (generation !== syncPollGeneration || !props.visible) return
    if (attempt >= MAX_SYNC_POLL_ATTEMPTS) {
      syncLoading.value = false
      ElMessage.warning(t('admin.providers.syncMonitorsPollingStopped', { taskId }))
      return
    }
    syncPollTimer = setTimeout(() => pollSyncTask(taskId, generation, attempt + 1), 2000)
  }

  const handleSyncMonitors = async () => {
    if (!props.provider || monitorMutationBusy.value) return
    if (isMonitorSyncActive(syncTask.value?.status)) {
      if (syncTask.value?.task_id) {
        syncLoading.value = true
        pollSyncTask(syncTask.value.task_id, syncPollGeneration)
      }
      return
    }
    const providerId = props.provider.id
    const generation = syncPollGeneration
    syncLoading.value = true
    try {
      await ElMessageBox.confirm(t('admin.providers.syncMonitorsConfirm'), t('common.confirm'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'info' })
      if (generation !== syncPollGeneration || props.provider?.id !== providerId || !props.visible) return
      const res = await syncProviderMonitors(providerId)
      if (generation !== syncPollGeneration || props.provider?.id !== providerId || !props.visible) return
      if (res.code === 200) {
        const task = res.data || {}
        syncTask.value = task
        if (task.task_id && isMonitorSyncActive(task.status)) {
          ElMessage.info(t('admin.providers.syncMonitorsSuccess'))
          pollSyncTask(task.task_id, syncPollGeneration)
          return
        }
        await finishSyncTask(task, isMonitorSyncFailed(task.status))
      } else { ElMessage.error(res.msg || t('admin.providers.syncMonitorsFailed')) }
    } catch (e) { if (e !== 'cancel') ElMessage.error(e?.response?.data?.msg || t('admin.providers.syncMonitorsFailed')) }
    finally {
      if (generation === syncPollGeneration && (!syncTask.value?.task_id || !isMonitorSyncActive(syncTask.value.status))) {
        syncLoading.value = false
      }
    }
  }

  const handleClearMonitors = async () => {
    if (!props.provider || monitorMutationBusy.value) return
    const generation = viewGeneration
    const providerId = props.provider.id
    const expected = props.provider.name || String(providerId)
    clearMonitorsLoading.value = true
    try {
      await ElMessageBox.prompt(
        `${t('admin.providers.clearMonitorsConfirm')}<br><br>${t('admin.providers.typeToConfirm', { expected })}`,
        t('common.confirm'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          inputPlaceholder: expected,
          inputValidator: (value) =>
            String(value || '').trim() === String(expected).trim() ||
            t('admin.providers.confirmTextMismatch', { expected }),
          type: 'warning',
          dangerouslyUseHTMLString: true
        }
      )
      if (!isCurrentView(generation, providerId)) return
      const res = await clearProviderMonitors(providerId)
      if (!isCurrentView(generation, providerId)) return
      if (res.code === 200) {
        ElMessage.success(t('admin.providers.clearMonitorsSuccess'))
        monitors.value = []
        agentMonitors.value = []
        monitorsPagination.total = 0
        agentMonitorsPagination.total = 0
        showAgentMonitors.value = false
        await loadMonitors()
      } else { ElMessage.error(res.msg || t('admin.providers.clearMonitorsFailed')) }
    } catch (e) { if (e !== 'cancel' && isCurrentView(generation, providerId)) ElMessage.error(e?.response?.data?.msg || t('admin.providers.clearMonitorsFailed')) }
    finally { clearMonitorsLoading.value = false }
  }

  const handleListAgentMonitors = async () => {
    if (!props.provider) return
    const generation = viewGeneration
    const providerId = props.provider.id
    const requestGeneration = ++agentMonitorsRequestGeneration
    listAgentLoading.value = true
    try {
      const res = await listAgentMonitors(providerId, { page: agentMonitorsPagination.page, pageSize: agentMonitorsPagination.pageSize })
      if (!isCurrentView(generation, providerId) || requestGeneration !== agentMonitorsRequestGeneration) return
      if (res.code === 200) {
        const data = res.data || {}; agentMonitors.value = data.monitors || []; agentMonitorsPagination.total = data.total || 0; showAgentMonitors.value = true
      } else { ElMessage.error(res.msg || t('common.failed')) }
    } catch (e) { if (isCurrentView(generation, providerId)) ElMessage.error(e?.response?.data?.msg || t('common.failed')) }
    finally {
      if (requestGeneration === agentMonitorsRequestGeneration) listAgentLoading.value = false
    }
  }

  const handleSaveConfig = async () => {
    if (!props.provider || monitorMutationBusy.value) return
    const generation = viewGeneration
    const providerId = props.provider.id
    saveConfigLoading.value = true
    try {
      const res = await updateMonitoringConfig(providerId, { ...editConfig })
      if (!isCurrentView(generation, providerId)) return
      if (res.code === 200) {
        ElMessage.success(t('common.saveSuccess'))
        await loadConfig()
        if (isCurrentView(generation, providerId)) showConfigEditor.value = false
      }
      else { ElMessage.error(res.msg || t('common.saveFailed')) }
    } catch (e) { if (isCurrentView(generation, providerId)) ElMessage.error(e?.response?.data?.msg || t('common.saveFailed')) }
    finally { saveConfigLoading.value = false }
  }

  const handleClose = () => { emit('update:visible', false); emit('close') }

  const formatDateTime = (dateTime) => {
    if (!dateTime) return '-'
    const value = typeof dateTime === 'number' && dateTime > 0 && dateTime < 1000000000000
      ? dateTime * 1000
      : dateTime
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return '-'
    return parsed.toLocaleString()
  }

  const formatBytes = (bytes) => {
    if (bytes === 0) return '0 B'
    const k = 1024; const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(k))
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
  }

  const getTaskTypeLabel = (taskType) => {
    const labels = { 'enable_all': t('admin.providers.trafficMonitorTaskTypeEnableAll'), 'disable_all': t('admin.providers.trafficMonitorTaskTypeDisableAll'), 'detect_all': t('admin.providers.trafficMonitorTaskTypeDetectAll') }
    return labels[taskType] || taskType
  }

  const getTaskTypeTagType = (taskType) => {
    const types = { 'enable_all': 'success', 'disable_all': 'danger', 'detect_all': 'info' }
    return types[taskType] || 'info'
  }

  const getTaskStatusLabel = (status) => {
    const labels = { 'pending': t('admin.providers.trafficMonitorTaskStatusPending'), 'running': t('admin.providers.trafficMonitorTaskStatusRunning'), 'completed': t('admin.providers.trafficMonitorTaskStatusCompleted'), 'failed': t('admin.providers.trafficMonitorTaskStatusFailed') }
    return labels[status] || status
  }

  const getTaskStatusTagType = (status) => {
    const types = { 'pending': 'info', 'running': 'warning', 'completed': 'success', 'failed': 'danger' }
    return types[status] || 'info'
  }

  return {
    activeTab, showConfigEditor, configLoading, deployLoading, uninstallLoading, monitorMutationBusy,
    statusLoading, saveConfigLoading, syncLoading, clearMonitorsLoading,
    monitorsLoading, listAgentLoading, deployOutput, monitors,
    agentOnlineChecked, agentIsOnline, showToken, showAgentMonitors, agentMonitors, syncTask,
    monitorsPagination, agentMonitorsPagination, config, editConfig,
    agentSwaggerUrl, agentStatusType, agentStatusText,
    isAgentProvider,
    loadConfig, loadMonitors, handleCopyToken, handleCopyUrl,
    handleDeployAgent, handleUninstallAgent, handleCheckStatus,
    handleSyncMonitors, handleClearMonitors, handleListAgentMonitors,
    handleSaveConfig, handleClose,
    formatDateTime, formatBytes,
    getTaskTypeLabel, getTaskTypeTagType, getTaskStatusLabel, getTaskStatusTagType,
    t
  }
}
