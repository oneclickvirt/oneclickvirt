import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getAdminTasks, forceStopTask, getTaskOverallStats, cancelUserTaskByAdmin, getAdminTaskDetail } from '@/api/admin'
import { getProviderList } from '@/api/admin'
import { useI18n } from 'vue-i18n'

export function useTaskManagement() {
  const { t, te, locale } = useI18n()

  const loading = ref(false)
  const tasks = ref([])
  const providers = ref([])
  const total = ref(0)

  const stats = reactive({
    totalTasks: 0,
    pendingTasks: 0,
    runningTasks: 0,
    completedTasks: 0,
    failedTasks: 0,
    timeoutTasks: 0
  })

  const filterForm = reactive({
    username: '',
    providerId: '',
    taskType: '',
    status: '',
    instanceType: ''
  })

  const pagination = reactive({
    page: 1,
    pageSize: 10
  })

  const forceStopDialog = reactive({
    visible: false,
    loading: false,
    task: null,
    form: {
      reason: ''
    },
    rules: {}
  })

  const detailDialog = reactive({
    visible: false,
    task: null,
    logsLoading: false
  })

  const expandedLogTaskIds = ref(new Set())

  const loadTasks = async () => {
    try {
      loading.value = true
      const params = {
        page: pagination.page,
        pageSize: pagination.pageSize,
        ...filterForm
      }

      const response = await getAdminTasks(params)
      if (response.code === 200) {
        tasks.value = response.data.list || []
        total.value = response.data.total || 0
      } else {
        ElMessage.error(response.message || t('admin.tasks.loadFailed'))
      }
    } catch (error) {
      console.error('获取任务列表失败:', error)
      ElMessage.error(t('admin.tasks.loadFailed'))
    } finally {
      loading.value = false
    }
  }

  const loadStats = async () => {
    try {
      const response = await getTaskOverallStats()
      if (response.code === 200) {
        Object.assign(stats, response.data)
      }
    } catch (error) {
      console.error('获取统计信息失败:', error)
    }
  }

  const loadProviders = async () => {
    try {
      const response = await getProviderList({ page: 1, pageSize: 1000 })
      if (response.code === 200) {
        providers.value = response.data.list || []
      }
    } catch (error) {
      console.error('获取节点列表失败:', error)
    }
  }

  const resetFilter = () => {
    Object.assign(filterForm, {
      username: '',
      providerId: '',
      taskType: '',
      status: '',
      instanceType: ''
    })
    pagination.page = 1
    loadTasks()
  }

  const showForceStopDialog = (task) => {
    forceStopDialog.task = task
    forceStopDialog.form.reason = ''
    forceStopDialog.visible = true
  }

  const confirmForceStop = async () => {
    try {
      forceStopDialog.loading = true
      const response = await forceStopTask({
        taskId: forceStopDialog.task.id,
        reason: forceStopDialog.form.reason
      })

      if (response.code === 200) {
        ElMessage.success(t('admin.tasks.forceStopSuccess'))
        forceStopDialog.visible = false
        loadTasks()
        loadStats()
      } else {
        ElMessage.error(response.message || t('message.operationFailed'))
      }
    } catch (error) {
      console.error('强制停止任务失败:', error)
      ElMessage.error(t('message.operationFailed'))
    } finally {
      forceStopDialog.loading = false
    }
  }

  const cancelTask = async (task) => {
    try {
      await ElMessageBox.confirm(
        t('admin.tasks.cancelTaskConfirm', { taskType: getTaskTypeText(task.taskType) }),
        t('admin.tasks.confirmCancel'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        }
      )

      const response = await cancelUserTaskByAdmin(task.id)
      if (response.code === 200) {
        ElMessage.success(t('admin.tasks.cancelSuccess'))
        loadTasks()
        loadStats()
      } else {
        ElMessage.error(response.message || t('message.operationFailed'))
      }
    } catch (error) {
      if (error !== 'cancel') {
        console.error('取消任务失败:', error)
        ElMessage.error(t('message.operationFailed'))
      }
    }
  }

  const viewTaskDetail = async (task) => {
    detailDialog.task = { ...task }
    detailDialog.visible = true
    detailDialog.logsLoading = true
    try {
      const response = await getAdminTaskDetail(task.id)
      if (response.code === 200 && response.data) {
        detailDialog.task = { ...detailDialog.task, ...response.data }
      }
    } catch (error) {
      console.error('\u83b7\u53d6\u4efb\u52a1\u8be6\u60c5\u5931\u8d25:', error)
    } finally {
      detailDialog.logsLoading = false
    }
  }

  const parseProgressLogs = (logsStr) => {
    if (!logsStr) return []
    try {
      return JSON.parse(logsStr)
    } catch {
      return []
    }
  }

  const translateStepMsg = (m) => {
    if (!m) return m
    const colonIdx = m.indexOf(':')
    if (colonIdx !== -1) {
      const key = m.substring(0, colonIdx)
      const param = m.substring(colonIdx + 1)
      const i18nKey = `admin.tasks.${key}`
      if (te(i18nKey)) return t(i18nKey, { n: parseInt(param) || param, name: param })
    } else {
      const i18nKey = `admin.tasks.${m}`
      if (te(i18nKey)) return t(i18nKey)
    }
    return m
  }

  const toggleProgressLogs = (taskId) => {
    const newSet = new Set(expandedLogTaskIds.value)
    if (newSet.has(taskId)) {
      newSet.delete(taskId)
    } else {
      newSet.add(taskId)
    }
    expandedLogTaskIds.value = newSet
  }

  const shouldShowPreallocatedConfig = (task) => {
    return task.taskType === 'create' || task.preallocatedCpu > 0
  }

  const getTaskTypeText = (type) => {
    const typeMap = {
      'create': t('admin.tasks.taskTypeCreate'),
      'start': t('admin.tasks.taskTypeStart'),
      'stop': t('admin.tasks.taskTypeStop'),
      'restart': t('admin.tasks.taskTypeRestart'),
      'reset': t('admin.tasks.taskTypeReset'),
      'delete': t('admin.tasks.taskTypeDelete'),
      'reset-password': t('admin.tasks.taskTypeResetPassword'),
      'create-port-mapping': t('admin.tasks.taskTypeCreatePortMapping'),
      'delete-port-mapping': t('admin.tasks.taskTypeDeletePortMapping'),
      'create_redemption_instance': t('admin.tasks.taskTypeCreateRedemptionInstance')
    }
    return typeMap[type] || type
  }

  const getTaskStatusType = (status) => {
    const statusMap = {
      'pending': 'info',
      'processing': 'warning',
      'running': 'warning',
      'completed': 'success',
      'failed': 'danger',
      'cancelled': 'info',
      'cancelling': 'warning',
      'timeout': 'danger'
    }
    return statusMap[status] || 'info'
  }

  const getTaskStatusText = (status) => {
    const statusMap = {
      'pending': t('admin.tasks.statusPending'),
      'processing': t('admin.tasks.statusProcessing'),
      'running': t('admin.tasks.statusRunning'),
      'completed': t('admin.tasks.statusCompleted'),
      'failed': t('admin.tasks.statusFailed'),
      'cancelled': t('admin.tasks.statusCancelled'),
      'cancelling': t('admin.tasks.statusCancelling'),
      'timeout': t('admin.tasks.statusTimeout')
    }
    return statusMap[status] || status
  }

  const formatDateTime = (dateTime) => {
    if (!dateTime) return '-'
    return new Date(dateTime).toLocaleString(locale.value)
  }

  const formatDuration = (seconds) => {
    if (!seconds || seconds <= 0) return '-'

    const hours = Math.floor(seconds / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    const secs = seconds % 60

    if (hours > 0) {
      return `${hours}h ${minutes}m ${secs}s`
    } else if (minutes > 0) {
      return `${minutes}m ${secs}s`
    } else {
      return `${secs}s`
    }
  }

  onMounted(() => {
    loadTasks()
    loadStats()
    loadProviders()

    setInterval(() => {
      if (!forceStopDialog.visible && !detailDialog.visible) {
        loadStats()
      }
    }, 30000)
  })

  return {
    loading, tasks, providers, total, stats,
    filterForm, pagination,
    forceStopDialog, detailDialog, expandedLogTaskIds,
    loadTasks, resetFilter,
    showForceStopDialog, confirmForceStop,
    cancelTask, viewTaskDetail,
    parseProgressLogs, translateStepMsg, toggleProgressLogs,
    shouldShowPreallocatedConfig,
    getTaskTypeText, getTaskStatusType, getTaskStatusText,
    formatDateTime, formatDuration,
    t
  }
}
