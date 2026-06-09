// 自动配置对话框 + 流量监控对话框状态与逻辑
import { reactive } from 'vue'
import { ElMessage } from 'element-plus'
import { copyToClipboard as copyToClipboardUtil } from '@/utils/clipboard'
import {
  autoConfigureProvider,
  getConfigurationTaskDetail,
  getConfigurationTasks,
  trafficMonitorOperation,
  getTrafficMonitorTasks,
  getTrafficMonitorTaskDetail
} from '@/api/admin'
import { useI18n } from 'vue-i18n'
import { ElMessageBox } from 'element-plus'

export function useProviderDialogs(loadProviders) {
  const { t } = useI18n()

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
    loading: false,
    provider: null,
    task: null,
    showHistory: false,
    runningTask: null,
    historyTasks: [],
    pagination: { page: 1, pageSize: 10, total: 0 }
  })

  // ── 自动配置 API ────────────────────────────────────

  const viewTaskLog = async (taskId) => {
    taskLogDialog.visible = true
    taskLogDialog.loading = true
    taskLogDialog.error = null
    taskLogDialog.task = null
    try {
      const response = await getConfigurationTaskDetail(taskId)
      if (response.code === 200) {
        taskLogDialog.task = response.data
      } else {
        taskLogDialog.error = response.msg || t('admin.providers.getTaskDetailsFailed')
      }
    } catch (error) {
      console.error('Failed to get task logs:', error)
      taskLogDialog.error = t('admin.providers.getTaskLogsFailed') + ': ' + (error.message || t('common.unknownError'))
    } finally {
      taskLogDialog.loading = false
    }
  }

  const copyTaskLog = async () => {
    const logOutput = taskLogDialog.task?.logOutput
    await copyToClipboardUtil(logOutput, t('admin.providers.logCopied'))
  }

  const autoConfigureAPI = async (provider) => {
    try {
      const checkResponse = await autoConfigureProvider({
        providerId: provider.id,
        showHistory: true
      })
      const result = checkResponse.data
      configDialog.provider = provider
      configDialog.runningTask = result.runningTask
      configDialog.historyTasks = result.historyTasks || []
      configDialog.pagination.total = configDialog.historyTasks.length
      configDialog.pagination.page = 1
      configDialog.showHistory = true
      configDialog.visible = true
      if (result.runningTask) {
        ElMessage.info(t('admin.providers.showTaskLog'))
        await viewTaskLog(result.runningTask.id)
      }
    } catch (error) {
      console.error('检查配置状态失败:', error)
      ElMessage.error(
        t('admin.providers.checkConfigFailed') +
          ': ' +
          (error.message || t('common.unknownError'))
      )
    }
  }

  const startNewConfiguration = async (provider, force = false) => {
    const loadingMessage = ElMessage({
      message: t('admin.providers.validation.autoConfiguring'),
      type: 'info',
      duration: 0,
      showClose: false
    })
    try {
      const response = await autoConfigureProvider({ providerId: provider.id, force })
      const result = response.data
      loadingMessage.close()
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
    }
  }

  const rerunConfiguration = () => {
    configDialog.visible = false
    startNewConfiguration(configDialog.provider, true)
  }

  const viewRunningTask = () => {
    if (configDialog.runningTask) {
      viewTaskLog(configDialog.runningTask.id)
    }
  }

  const loadConfigHistory = async (provider, page, pageSize) => {
    try {
      const res = await getConfigurationTasks({
        providerId: provider.id,
        page: page || configDialog.pagination.page,
        pageSize: pageSize || configDialog.pagination.pageSize
      })
      if (res.code === 200) {
        configDialog.historyTasks = res.data?.list || res.data || []
        configDialog.pagination.total = res.data?.total || configDialog.historyTasks.length
      }
    } catch (e) {
      console.error('加载配置历史失败:', e)
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
    trafficMonitorDialog.loading = false
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
    if (!trafficMonitorDialog.provider) return
    try {
      const historyResponse = await getTrafficMonitorTasks(
        trafficMonitorDialog.provider.id,
        {
          page: trafficMonitorDialog.pagination.page,
          pageSize: trafficMonitorDialog.pagination.pageSize
        }
      )
      trafficMonitorDialog.historyTasks = historyResponse.data?.list || []
      trafficMonitorDialog.pagination.total = historyResponse.data?.total || 0
      const runningTask = trafficMonitorDialog.historyTasks.find(
        task => task.status === 'running' || task.status === 'pending'
      )
      trafficMonitorDialog.runningTask = runningTask || null
    } catch (error) {
      console.error('Failed to load traffic monitor tasks:', error)
      ElMessage.error(t('admin.providers.loadTasksFailed'))
    }
  }

  const openTrafficMonitorDialog = async (provider) => {
    resetTrafficMonitorDialog(provider)
    await loadTrafficMonitorHistory()
    trafficMonitorDialog.showHistory = true
    trafficMonitorDialog.visible = true
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
    if (!trafficMonitorDialog.provider) return
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
      const response = await trafficMonitorOperation({
        providerId: trafficMonitorDialog.provider.id,
        operation
      })
      if (response.code === 200) {
        ElMessage.success(t('admin.providers.trafficMonitorOperationSuccess'))
        if (response.data?.taskId) {
          try {
            const taskResponse = await getTrafficMonitorTaskDetail(response.data.taskId)
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
      if (error !== 'cancel') {
        ElMessage.error(
          error?.response?.data?.msg || t('admin.providers.trafficMonitorOperationFailed')
        )
      }
    }
  }

  const viewTrafficMonitorTaskLog = async (taskId) => {
    try {
      trafficMonitorDialog.loading = true
      const response = await getTrafficMonitorTaskDetail(taskId)
      if (response.code === 200) {
        trafficMonitorDialog.showHistory = false
        trafficMonitorDialog.task = response.data
      } else {
        ElMessage.error(response.msg || t('admin.providers.loadTaskFailed'))
      }
    } catch (error) {
      console.error('Failed to load task detail:', error)
      ElMessage.error(t('admin.providers.loadTaskFailed'))
    } finally {
      trafficMonitorDialog.loading = false
    }
  }

  const viewRunningTrafficMonitorTask = () => {
    if (trafficMonitorDialog.runningTask) {
      trafficMonitorDialog.showHistory = false
      trafficMonitorDialog.task = trafficMonitorDialog.runningTask
    }
  }

  const refreshTrafficMonitorTask = async () => {
    if (!trafficMonitorDialog.task?.id) return
    try {
      trafficMonitorDialog.loading = true
      const response = await getTrafficMonitorTaskDetail(trafficMonitorDialog.task.id)
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
      console.error('Failed to refresh task:', error)
    } finally {
      trafficMonitorDialog.loading = false
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
    taskLogDialog,
    trafficMonitorDialog,
    viewTaskLog,
    copyTaskLog,
    autoConfigureAPI,
    startNewConfiguration,
    rerunConfiguration,
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
    refreshTrafficMonitorTask,
    resetTrafficMonitorDialog,
    debugAuthStatus
  }
}
