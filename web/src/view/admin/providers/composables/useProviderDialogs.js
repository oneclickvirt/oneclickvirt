// 自动配置对话框 + 流量监控对话框状态与逻辑
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { copyToClipboard as copyToClipboardUtil } from '@/utils/clipboard'
import {
  autoConfigureProvider,
  getConfigurationTaskDetail,
  getConfigurationTasks,
  cancelConfigurationTask,
  trafficMonitorOperation,
  getTrafficMonitorTasks,
  getTrafficMonitorTaskDetail
} from '@/api/admin'
import { useI18n } from 'vue-i18n'
import { ElMessageBox } from 'element-plus'

export function useProviderDialogs(loadProviders) {
  const { t } = useI18n()
  const configSubmitting = ref(false)
  const configCanceling = ref(false)
  const configStatusLoading = ref(false)
  const configPageLoading = ref(false)
  const configHistoryLoading = computed(() => configStatusLoading.value || configPageLoading.value)
  const trafficOperationSubmitting = ref(false)
  const trafficHistoryLoading = ref(false)
  const trafficDetailLoading = ref(false)
  const trafficMonitorLoading = computed(() => trafficHistoryLoading.value || trafficDetailLoading.value)
  let configViewGeneration = 0
  let configPageGeneration = 0
  let configTaskLogGeneration = 0
  let trafficHistoryGeneration = 0
  let trafficDetailGeneration = 0

  // 自动配置对话框状态
  const configDialog = reactive({
    visible: false,
    provider: null,
    showHistory: false,
    runningTask: null,
    historyTasks: [],
    pagination: { page: 1, pageSize: 10, total: 0 }
  })

  // 任务日志对话框状态
  const taskLogDialog = reactive({
    visible: false,
    loading: false,
    error: null,
    task: null
  })

  // 流量监控任务对话框状态
  const trafficMonitorDialog = reactive({
    visible: false,
    provider: null,
    task: null,
    showHistory: false,
    runningTask: null,
    historyTasks: [],
    pagination: { page: 1, pageSize: 10, total: 0 }
  })

  // ── 自动配置 API ────────────────────────────────────

  const viewTaskLog = async (taskId) => {
    const generation = ++configTaskLogGeneration
    taskLogDialog.visible = true
    taskLogDialog.loading = true
    taskLogDialog.error = null
    taskLogDialog.task = null
    try {
      const response = await getConfigurationTaskDetail(taskId)
      if (generation !== configTaskLogGeneration || !taskLogDialog.visible) return
      if (response.code === 200) {
        taskLogDialog.task = response.data
      } else {
        taskLogDialog.error = response.msg || t('admin.providers.getTaskDetailsFailed')
      }
    } catch (error) {
      if (generation !== configTaskLogGeneration || !taskLogDialog.visible) return
      console.error('Failed to get task logs:', error)
      taskLogDialog.error = t('admin.providers.getTaskLogsFailed') + ': ' + (error.message || t('common.unknownError'))
    } finally {
      if (generation === configTaskLogGeneration) taskLogDialog.loading = false
    }
  }

  const closeTaskLogDialog = () => {
    ++configTaskLogGeneration
    taskLogDialog.visible = false
    taskLogDialog.loading = false
  }

  const copyTaskLog = async () => {
    const logOutput = taskLogDialog.task?.logOutput
    await copyToClipboardUtil(logOutput, t('admin.providers.logCopied'))
  }

  const autoConfigureAPI = async (provider, showRunningLog = true) => {
    if (!provider?.id) return
    const providerId = provider.id
    const generation = ++configViewGeneration
    ++configPageGeneration
    const isRefresh = !showRunningLog
    configStatusLoading.value = true
    try {
      const checkResponse = await autoConfigureProvider({
        providerId,
        showHistory: true
      })
      if (generation !== configViewGeneration ||
          (isRefresh && (!configDialog.visible || configDialog.provider?.id !== providerId))) return
      const result = checkResponse.data
      configDialog.provider = provider
      configDialog.runningTask = result.runningTask || null
      configDialog.historyTasks = result.historyTasks || []
      configDialog.pagination.total = configDialog.historyTasks.length
      if (!isRefresh) configDialog.pagination.page = 1
      configDialog.showHistory = true
      configDialog.visible = true
      await loadConfigHistory(provider)
      if (generation !== configViewGeneration || !configDialog.visible) return
      if (result.runningTask && showRunningLog) {
        ElMessage.info(t('admin.providers.showTaskLog'))
        await viewTaskLog(result.runningTask.id)
      }
    } catch (error) {
      if (generation !== configViewGeneration || (isRefresh && !configDialog.visible)) return
      console.error('检查配置状态失败:', error)
      ElMessage.error(
        t('admin.providers.checkConfigFailed') +
          ': ' +
          (error.message || t('common.unknownError'))
      )
    } finally {
      if (generation === configViewGeneration) configStatusLoading.value = false
    }
  }

  const startNewConfiguration = async (provider) => {
    if (!provider?.id || configSubmitting.value || configHistoryLoading.value || configDialog.runningTask) return
    const providerId = provider.id
    configSubmitting.value = true
    const loadingMessage = ElMessage({
      message: t('admin.providers.validation.autoConfiguring'),
      type: 'info',
      duration: 0,
      showClose: false
    })
    try {
      const response = await autoConfigureProvider({ providerId })
      const result = response.data
      loadingMessage.close()
      if (configDialog.provider?.id !== providerId) return
      if (result.status === 'running' || result.runningTask) {
        configDialog.runningTask = result.runningTask || null
        ElMessage.info(result.message || t('admin.providers.showTaskLog'))
        return
      }
      configDialog.visible = false
      if (result.taskId) {
        await viewTaskLog(result.taskId)
        await loadProviders()
      } else {
        ElMessage.success(t('admin.providers.apiAutoConfigSuccess'))
        await loadProviders()
      }
    } catch (error) {
      loadingMessage.close()
      console.error('启动配置失败:', error)
      ElMessage.error(
        t('admin.providers.startConfigFailed') +
          ': ' +
          (error.message || t('common.unknownError'))
      )
    } finally {
      configSubmitting.value = false
    }
  }

  const rerunConfiguration = () => {
    return startNewConfiguration(configDialog.provider)
  }

  const refreshConfiguration = () => autoConfigureAPI(configDialog.provider, false)

  const cancelRunningConfiguration = async () => {
    const task = configDialog.runningTask
    if (!task?.id || configCanceling.value) return
    try {
      await ElMessageBox.confirm(
        t('admin.providers.cancelConfigTaskConfirm'),
        t('admin.providers.cancelConfigTask'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        }
      )
      configCanceling.value = true
      await cancelConfigurationTask(task.id)
      ElMessage.success(t('admin.providers.cancelConfigTaskSubmitted'))
      if (configDialog.provider) {
        await autoConfigureAPI(configDialog.provider, false)
      }
    } catch (error) {
      if (error !== 'cancel' && error?.action !== 'cancel' && error?.action !== 'close') {
        console.error('取消配置任务失败:', error)
        ElMessage.error(error?.message || t('admin.providers.cancelConfigTaskFailed'))
      }
    } finally {
      configCanceling.value = false
    }
  }

  const closeConfigurationDialog = () => {
    ++configViewGeneration
    ++configPageGeneration
    configDialog.visible = false
    configDialog.provider = null
    configDialog.runningTask = null
    configStatusLoading.value = false
    configPageLoading.value = false
    configCanceling.value = false
  }

  const viewRunningTask = () => {
    if (configDialog.runningTask) {
      viewTaskLog(configDialog.runningTask.id)
    }
  }

  const loadConfigHistory = async (provider, page, pageSize) => {
    if (!provider?.id || !configDialog.visible) return
    const providerId = provider.id
    const viewGeneration = configViewGeneration
    const requestGeneration = ++configPageGeneration
    configPageLoading.value = true
    try {
      const res = await getConfigurationTasks({
        providerId,
        page: page || configDialog.pagination.page,
        pageSize: pageSize || configDialog.pagination.pageSize
      })
      if (viewGeneration !== configViewGeneration || requestGeneration !== configPageGeneration ||
          !configDialog.visible || configDialog.provider?.id !== providerId) return
      if (res.code === 200) {
        configDialog.historyTasks = res.data?.list || res.data || []
        configDialog.pagination.total = res.data?.total || configDialog.historyTasks.length
      }
    } catch (e) {
      if (viewGeneration === configViewGeneration && requestGeneration === configPageGeneration) {
        console.error('加载配置历史失败:', e)
      }
    } finally {
      if (requestGeneration === configPageGeneration) configPageLoading.value = false
    }
  }

  const handleConfigPageChange = (page) => {
    configDialog.pagination.page = page
    if (configDialog.provider) loadConfigHistory(configDialog.provider)
  }

  const handleConfigPageSizeChange = (size) => {
    configDialog.pagination.pageSize = size
    configDialog.pagination.page = 1
    if (configDialog.provider) loadConfigHistory(configDialog.provider)
  }

  // ── 流量监控对话框 ────────────────────────────────────

  const resetTrafficMonitorDialog = (provider = null) => {
    ++trafficHistoryGeneration
    ++trafficDetailGeneration
    trafficHistoryLoading.value = false
    trafficDetailLoading.value = false
    trafficMonitorDialog.visible = !!provider
    trafficMonitorDialog.provider = provider
    trafficMonitorDialog.task = null
    trafficMonitorDialog.showHistory = false
    trafficMonitorDialog.runningTask = null
    trafficMonitorDialog.historyTasks = []
    trafficMonitorDialog.pagination.page = 1
    trafficMonitorDialog.pagination.pageSize = 10
    trafficMonitorDialog.pagination.total = 0
  }

  const loadTrafficMonitorHistory = async () => {
    if (!trafficMonitorDialog.provider || !trafficMonitorDialog.visible) return
    const providerId = trafficMonitorDialog.provider.id
    const generation = ++trafficHistoryGeneration
    const page = trafficMonitorDialog.pagination.page
    const pageSize = trafficMonitorDialog.pagination.pageSize
    trafficHistoryLoading.value = true
    try {
      const [historyResponse, latestResponse] = await Promise.all([
        getTrafficMonitorTasks(providerId, { page, pageSize }),
        page === 1 ? Promise.resolve(null) : getTrafficMonitorTasks(providerId, { page: 1, pageSize: 1 })
      ])
      if (generation !== trafficHistoryGeneration || !trafficMonitorDialog.visible ||
          trafficMonitorDialog.provider?.id !== providerId) return
      trafficMonitorDialog.historyTasks = historyResponse.data?.list || []
      trafficMonitorDialog.pagination.total = historyResponse.data?.total || 0
      const newestTasks = latestResponse?.data?.list || trafficMonitorDialog.historyTasks
      const runningTask = newestTasks.find(
        task => task.status === 'running' || task.status === 'pending'
      )
      trafficMonitorDialog.runningTask = runningTask || null
      return true
    } catch (error) {
      if (generation !== trafficHistoryGeneration || !trafficMonitorDialog.visible ||
          trafficMonitorDialog.provider?.id !== providerId) return
      console.error('Failed to load traffic monitor tasks:', error)
      ElMessage.error(t('admin.providers.loadTasksFailed'))
      return false
    } finally {
      if (generation === trafficHistoryGeneration) {
        trafficHistoryLoading.value = false
      }
    }
  }

  const openTrafficMonitorDialog = async (provider) => {
    resetTrafficMonitorDialog(provider)
    const loading = loadTrafficMonitorHistory()
    const generation = trafficHistoryGeneration
    await loading
    if (generation === trafficHistoryGeneration && trafficMonitorDialog.visible &&
        trafficMonitorDialog.provider?.id === provider?.id) {
      trafficMonitorDialog.showHistory = true
    }
  }

  const handleEnableTrafficMonitor = async (provider) => {
    await openTrafficMonitorDialog(provider)
  }

  const handleTrafficMonitorPageChange = async (page) => {
    trafficMonitorDialog.pagination.page = page
    await loadTrafficMonitorHistory()
  }

  const handleTrafficMonitorPageSizeChange = async (size) => {
    trafficMonitorDialog.pagination.pageSize = size
    trafficMonitorDialog.pagination.page = 1
    await loadTrafficMonitorHistory()
  }

  const executeTrafficMonitorOperation = async (operation) => {
    if (!trafficMonitorDialog.provider || trafficOperationSubmitting.value) return
    const providerId = trafficMonitorDialog.provider.id
    trafficOperationSubmitting.value = true
    const confirmMessages = {
      enable: t('admin.providers.enableTrafficMonitorConfirm'),
      disable: t('admin.providers.disableTrafficMonitorConfirm'),
      detect: t('admin.providers.detectTrafficMonitorConfirm')
    }
    const confirmTypes = { enable: 'info', disable: 'warning', detect: 'info' }
    try {
      await ElMessageBox.confirm(
        confirmMessages[operation],
        t('common.confirm'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: confirmTypes[operation]
        }
      )
      if (trafficMonitorDialog.provider?.id !== providerId || !trafficMonitorDialog.visible) return
      trafficMonitorDialog.pagination.page = 1
      const loaded = await loadTrafficMonitorHistory()
      if (trafficMonitorDialog.provider?.id !== providerId || !trafficMonitorDialog.visible) return
      if (!loaded) return
      if (trafficMonitorDialog.runningTask) {
        ElMessage.info(t('admin.providers.runningTrafficMonitorTask'))
        return
      }
      const response = await trafficMonitorOperation({
        providerId,
        operation
      })
      if (trafficMonitorDialog.provider?.id !== providerId || !trafficMonitorDialog.visible) return
      if (response.code === 200) {
        ElMessage.success(t('admin.providers.trafficMonitorOperationSuccess'))
        await loadTrafficMonitorHistory()
        if (response.data?.taskId) {
          try {
            const taskResponse = await getTrafficMonitorTaskDetail(response.data.taskId)
            if (trafficMonitorDialog.provider?.id !== providerId || !trafficMonitorDialog.visible) return
            if (taskResponse.code === 200) {
              trafficMonitorDialog.showHistory = false
              trafficMonitorDialog.task = taskResponse.data
            }
          } catch (error) {
            console.error('Failed to load task detail:', error)
          }
        } else {
          trafficMonitorDialog.showHistory = false
          trafficMonitorDialog.task = response.data
        }
      } else {
        ElMessage.error(response.msg || t('admin.providers.trafficMonitorOperationFailed'))
      }
    } catch (error) {
      if (error !== 'cancel' && trafficMonitorDialog.visible && trafficMonitorDialog.provider?.id === providerId) {
        ElMessage.error(
          error?.response?.data?.msg || t('admin.providers.trafficMonitorOperationFailed')
        )
      }
    } finally {
      trafficOperationSubmitting.value = false
    }
  }

  const viewTrafficMonitorTaskLog = async (taskId) => {
    if (!trafficMonitorDialog.visible || !trafficMonitorDialog.provider) return
    const providerId = trafficMonitorDialog.provider.id
    const generation = ++trafficDetailGeneration
    trafficDetailLoading.value = true
    try {
      const response = await getTrafficMonitorTaskDetail(taskId)
      if (generation !== trafficDetailGeneration || !trafficMonitorDialog.visible ||
          trafficMonitorDialog.provider?.id !== providerId) return
      if (response.code === 200) {
        trafficMonitorDialog.showHistory = false
        trafficMonitorDialog.task = response.data
      } else {
        ElMessage.error(response.msg || t('admin.providers.loadTaskFailed'))
      }
    } catch (error) {
      if (generation !== trafficDetailGeneration || !trafficMonitorDialog.visible ||
          trafficMonitorDialog.provider?.id !== providerId) return
      console.error('Failed to load task detail:', error)
      ElMessage.error(t('admin.providers.loadTaskFailed'))
    } finally {
      if (generation === trafficDetailGeneration) trafficDetailLoading.value = false
    }
  }

  const viewRunningTrafficMonitorTask = () => {
    if (trafficMonitorDialog.runningTask) {
      ++trafficDetailGeneration
      trafficMonitorDialog.showHistory = false
      trafficMonitorDialog.task = trafficMonitorDialog.runningTask
    }
  }

  const showTrafficMonitorHistory = async () => {
    ++trafficDetailGeneration
    trafficDetailLoading.value = false
    trafficMonitorDialog.task = null
    trafficMonitorDialog.showHistory = true
    await loadTrafficMonitorHistory()
  }

  const refreshTrafficMonitorTask = async () => {
    if (!trafficMonitorDialog.task?.id || !trafficMonitorDialog.visible || !trafficMonitorDialog.provider) return
    const taskId = trafficMonitorDialog.task.id
    const providerId = trafficMonitorDialog.provider.id
    const generation = ++trafficDetailGeneration
    trafficDetailLoading.value = true
    try {
      const response = await getTrafficMonitorTaskDetail(taskId)
      if (generation !== trafficDetailGeneration || !trafficMonitorDialog.visible ||
          trafficMonitorDialog.provider?.id !== providerId || trafficMonitorDialog.task?.id !== taskId) return
      if (response.code === 200) {
        trafficMonitorDialog.task = response.data
        if (
          response.data.status === 'completed' ||
          response.data.status === 'failed'
        ) {
          await loadTrafficMonitorHistory()
        }
      }
    } catch (error) {
      if (generation === trafficDetailGeneration && trafficMonitorDialog.visible) {
        console.error('Failed to refresh task:', error)
      }
    } finally {
      if (generation === trafficDetailGeneration) trafficDetailLoading.value = false
    }
  }

  const debugAuthStatus = () => {
    console.debug('[providers] dialog state', {
      configDialogVisible: configDialog.visible,
      trafficMonitorDialogVisible: trafficMonitorDialog.visible
    })
  }

  return {
    configDialog,
    configSubmitting,
    configCanceling,
    configHistoryLoading,
    closeConfigurationDialog,
    taskLogDialog,
    closeTaskLogDialog,
    trafficMonitorDialog,
    trafficMonitorLoading,
    trafficOperationSubmitting,
    viewTaskLog,
    copyTaskLog,
    autoConfigureAPI,
    startNewConfiguration,
    rerunConfiguration,
    refreshConfiguration,
    cancelRunningConfiguration,
    viewRunningTask,
    handleConfigPageChange,
    handleConfigPageSizeChange,
    handleEnableTrafficMonitor,
    loadTrafficMonitorHistory,
    openTrafficMonitorDialog,
    handleTrafficMonitorPageChange,
    handleTrafficMonitorPageSizeChange,
    executeTrafficMonitorOperation,
    viewTrafficMonitorTaskLog,
    viewRunningTrafficMonitorTask,
    showTrafficMonitorHistory,
    refreshTrafficMonitorTask,
    resetTrafficMonitorDialog,
    debugAuthStatus
  }
}
