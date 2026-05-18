import { ref, reactive, computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getUserTasks, cancelUserTask, getAvailableProviders } from '@/api/user'

export function useUserTaskManagement() {
  const { t, locale } = useI18n()
  const route = useRoute()

  const loading = ref(false)
  const tasks = ref([])
  const providers = ref([])
  const total = ref(0)
  const expandedHistory = ref([])

  const filterForm = reactive({ status: '', taskType: '', providerId: '', search: '' })
  const pagination = reactive({ page: 1, pageSize: 10 })

  let refreshTimer = null

  const groupedTasks = computed(() => {
    const groups = new Map()
    tasks.value.forEach(task => {
      const providerId = task.providerId
      if (filterForm.providerId !== '' && String(task.providerId) !== String(filterForm.providerId)) return
      if (!groups.has(providerId)) {
        groups.set(providerId, { providerId, providerName: task.providerName, currentTasks: [], pendingTasks: [], historyTasks: [] })
      }
      const group = groups.get(providerId)
      if (task.status === 'running' || task.status === 'processing') group.currentTasks.push(task)
      else if (task.status === 'pending') group.pendingTasks.push(task)
      else group.historyTasks.push(task)
    })
    groups.forEach(group => {
      group.currentTasks.sort((a, b) => new Date(a.createdAt) - new Date(b.createdAt))
      group.pendingTasks.sort((a, b) => new Date(a.createdAt) - new Date(b.createdAt))
      group.historyTasks.sort((a, b) => new Date(b.createdAt) - new Date(a.createdAt))
    })
    const groupArray = Array.from(groups.values())
    groupArray.sort((a, b) => {
      const aHasActive = a.currentTasks.length + a.pendingTasks.length
      const bHasActive = b.currentTasks.length + b.pendingTasks.length
      if (aHasActive > 0 && bHasActive === 0) return -1
      if (aHasActive === 0 && bHasActive > 0) return 1
      return a.providerName.localeCompare(b.providerName)
    })
    return groupArray
  })

  const loadTasks = async (showSuccessMsg = false) => {
    try {
      loading.value = true
      const hasFilter = filterForm.providerId || filterForm.taskType || filterForm.status
      const params = {
        page: pagination.page,
        pageSize: pagination.pageSize
      }
      if (filterForm.providerId) params.providerId = filterForm.providerId
      if (filterForm.taskType) params.taskType = filterForm.taskType
      if (filterForm.status) params.status = filterForm.status
      const response = await getUserTasks(params)
      if (response.code === 200) {
        tasks.value = response.data.list || []; total.value = response.data.total || 0
        if (showSuccessMsg) {
          if (hasFilter) ElMessage.success(t('user.tasks.refreshedTotal', { count: total.value }))
          else ElMessage.success(t('user.tasks.loadSuccess'))
        }
      } else {
        tasks.value = []; total.value = 0
        if (response.message) ElMessage.warning(response.message)
      }
    } catch (error) {
      tasks.value = []; total.value = 0
      ElMessage.error(t('user.tasks.loadFailedNetwork'))
    } finally { loading.value = false }
  }

  const loadProviders = async () => {
    try {
      const response = await getAvailableProviders()
      if (response.code === 200) providers.value = response.data || []
    } catch (error) { console.error('获取提供商列表失败:', error) }
  }

  const resetFilter = () => {
    Object.assign(filterForm, { providerId: '', taskType: '', status: '' })
    pagination.page = 1; loadTasks(true)
  }

  const cancelTask = async (task) => {
    try {
      await ElMessageBox.confirm(`${t('user.tasks.confirmCancel')} "${getTaskTypeText(task.taskType)}"?`, t('user.tasks.confirmCancel'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      const response = await cancelUserTask(task.id)
      if (response.code === 200) { ElMessage.success(t('user.tasks.taskCancelled')); loadTasks() }
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('user.tasks.cancelTaskFailed')) }
  }

  const getTaskTypeText = (type) => {
    const typeMap = { 'create': t('user.tasks.taskTypeCreate'), 'start': t('user.tasks.taskTypeStart'), 'stop': t('user.tasks.taskTypeStop'), 'restart': t('user.tasks.taskTypeRestart'), 'reset': t('user.tasks.taskTypeReset'), 'delete': t('user.tasks.taskTypeDelete'), 'create_redemption_instance': t('user.tasks.taskTypeCreateRedemptionInstance') }
    return typeMap[type] || type
  }

  const formatDurationSeconds = (seconds) => {
    if (!seconds || seconds <= 0) return t('user.tasks.calculating')
    const hours = Math.floor(seconds / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    const secs = Math.floor(seconds % 60)
    const parts = []
    if (hours > 0) parts.push(`${hours}${t('user.tasks.hours')}`)
    if (minutes > 0) parts.push(`${minutes}${t('user.tasks.minutes')}`)
    if (secs > 0 || parts.length === 0) parts.push(`${secs}${t('user.tasks.seconds')}`)
    return parts.join(' ')
  }

  const getTaskStatusType = (status) => {
    const statusMap = { 'pending': 'info', 'processing': 'warning', 'running': 'warning', 'completed': 'success', 'failed': 'danger', 'cancelled': 'info', 'cancelling': 'warning', 'timeout': 'danger' }
    return statusMap[status] || 'info'
  }

  const shouldShowInstanceConfig = (task) => task.taskType === 'create' || task.preallocatedCpu > 0

  const getTaskStatusText = (status) => {
    const statusMap = { 'pending': t('user.tasks.statusPending'), 'processing': t('user.tasks.statusProcessing'), 'running': t('user.tasks.statusRunning'), 'completed': t('user.tasks.statusCompleted'), 'failed': t('user.tasks.statusFailed'), 'cancelled': t('user.tasks.statusCancelled'), 'cancelling': t('user.tasks.statusCancelling'), 'timeout': t('user.tasks.statusTimeout') }
    return statusMap[status] || status
  }

  const getDefaultStatusMessage = (status) => {
    const messageMap = { 'pending': t('user.tasks.statusMessagePending'), 'processing': t('user.tasks.statusMessageProcessing'), 'running': t('user.tasks.statusMessageRunning'), 'cancelling': t('user.tasks.statusMessageCancelling') }
    return messageMap[status] || t('user.tasks.statusMessageDefault')
  }

  const formatDate = (dateString) => {
    const localeCode = locale.value === 'zh-CN' ? 'zh-CN' : 'en-US'
    return new Date(dateString).toLocaleString(localeCode)
  }

  const getEstimatedTime = (task) => {
    if (!task.estimatedDuration) return t('user.tasks.unknown')
    const startTime = new Date(task.startedAt || task.createdAt)
    const estimatedEnd = new Date(startTime.getTime() + task.estimatedDuration * 1000)
    const localeCode = locale.value === 'zh-CN' ? 'zh-CN' : 'en-US'
    return estimatedEnd.toLocaleTimeString(localeCode)
  }

  const calculateDuration = (startTime, endTime) => {
    const start = new Date(startTime); const end = new Date(endTime)
    const duration = Math.floor((end - start) / 1000)
    if (duration < 60) return `${duration}${t('user.tasks.seconds')}`
    if (duration < 3600) return `${Math.floor(duration / 60)}${t('user.tasks.minutes')}`
    return `${Math.floor(duration / 3600)}${t('user.tasks.hours')}${Math.floor((duration % 3600) / 60)}${t('user.tasks.minutes')}`
  }

  const startAutoRefresh = () => { refreshTimer = setInterval(() => loadTasks(), 10000) }
  const stopAutoRefresh = () => { if (refreshTimer) { clearInterval(refreshTimer); refreshTimer = null } }

  watch(() => route.path, (newPath, oldPath) => {
    if (newPath === '/user/tasks' && oldPath !== newPath) { loadTasks(); loadProviders(); startAutoRefresh() }
    else if (oldPath === '/user/tasks' && newPath !== oldPath) { stopAutoRefresh() }
  }, { immediate: false })

  watch([() => filterForm.providerId, () => filterForm.taskType, () => filterForm.status], () => { pagination.page = 1 }, { deep: true })

  const handleRouterNavigation = (event) => {
    if (event.detail && event.detail.path === '/user/tasks') { loadTasks(); loadProviders(); startAutoRefresh() }
  }

  const handleForceRefresh = async (event) => {
    if (event.detail && event.detail.path === '/user/tasks') {
      await Promise.allSettled([loadTasks(), loadProviders()])
    }
  }

  return {
    loading, tasks, providers, total, expandedHistory,
    filterForm, pagination, groupedTasks,
    loadTasks, loadProviders, resetFilter, cancelTask,
    getTaskTypeText, formatDurationSeconds, getTaskStatusType,
    shouldShowInstanceConfig, getTaskStatusText, getDefaultStatusMessage,
    formatDate, getEstimatedTime, calculateDuration,
    startAutoRefresh, stopAutoRefresh,
    handleRouterNavigation, handleForceRefresh,
    t
  }
}
