import { computed, ref, reactive, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { 
  getSystemTrafficOverview,
  getAllUsersTrafficRank,
  getUserTrafficStats,
  manageTrafficLimits,
  batchManageTrafficLimits,
  batchSyncUserTraffic,
  syncAllTraffic,
  clearUserTrafficRecords
} from '@/api/admin'
import { useUserStore } from '@/pinia/modules/user'

const trafficSyncRefreshDelays = [1500, 2500, 4000, 6000]

export function useTrafficManagement() {
  const { t, locale } = useI18n()
  const userStore = useUserStore()
  const isSuperAdmin = computed(() => userStore.userType === 'admin')

  const overviewLoading = ref(false)
  const systemOverview = ref(null)
  const syncingAllTraffic = ref(false)

  const rankingLoading = ref(false)
  const trafficRanking = ref([])
  const currentPage = ref(1)
  const pageSize = ref(10)
  const total = ref(0)
  const selectedUsers = ref([])

  const searchParams = reactive({ username: '', nickname: '' })

  const userTrafficDialogVisible = ref(false)
  const userTrafficLoading = ref(false)
  const selectedUserTraffic = ref(null)
  const syncingUserDetail = ref(false)

  const limitDialogVisible = ref(false)
  const limitSubmitting = ref(false)
  const limitAction = ref('limit')
  const selectedUser = ref(null)
  const syncingUsers = ref([])

  const limitForm = reactive({ reason: '' })
  let trafficRefreshTimer = null
  let resolveTrafficRefreshTimer = null
  let trafficRefreshGeneration = 0

  const limitFormRules = {
    reason: [
      { required: true, message: () => t('admin.traffic.enterLimitReason'), trigger: 'blur' },
      { min: 5, message: () => t('admin.traffic.limitReasonMinLength'), trigger: 'blur' }
    ]
  }

  const loadSystemOverview = async ({ silent = false } = {}) => {
    overviewLoading.value = true
    try {
      const response = await getSystemTrafficOverview()
      if ((response.code === 200)) {
        systemOverview.value = response.data
      } else if (!silent) {
        ElMessage.error(`${t('admin.traffic.loadOverviewFailed')}: ${response.msg}`)
      }
    } catch (error) {
      if (!silent) ElMessage.error(t('admin.traffic.loadOverviewError'))
    } finally {
      overviewLoading.value = false
    }
  }

  const loadTrafficRanking = async ({ silent = false } = {}) => {
    rankingLoading.value = true
    try {
      const params = {
        page: currentPage.value,
        pageSize: pageSize.value,
        username: searchParams.username || undefined,
        nickname: searchParams.nickname || undefined
      }
      const response = await getAllUsersTrafficRank(params)
      if ((response.code === 200)) {
        trafficRanking.value = response.data.rankings || []
        total.value = response.data.total || 0
      } else if (!silent) {
        ElMessage.error(`${t('admin.traffic.loadRankingFailed')}: ${response.msg}`)
      }
    } catch (error) {
      if (!silent) ElMessage.error(t('admin.traffic.loadRankingError'))
    } finally {
      rankingLoading.value = false
    }
  }

  const handleSearch = () => { currentPage.value = 1; loadTrafficRanking() }
  const resetSearch = () => { searchParams.username = ''; searchParams.nickname = ''; currentPage.value = 1; loadTrafficRanking() }
  const handleSizeChange = (newSize) => { pageSize.value = newSize; currentPage.value = 1; loadTrafficRanking() }
  const handleCurrentChange = (newPage) => { currentPage.value = newPage; loadTrafficRanking() }
  const handleSelectionChange = (selection) => { selectedUsers.value = selection }

  const handleBatchSync = async () => {
    if (selectedUsers.value.length === 0) { ElMessage.warning(t('admin.traffic.pleaseSelectUsers')); return }
    try {
      await ElMessageBox.confirm(t('admin.traffic.confirmBatchSync', { count: selectedUsers.value.length }), t('common.warning'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      const userIds = selectedUsers.value.map(user => user.user_id)
      const response = await batchSyncUserTraffic({ user_ids: userIds })
      if ((response.code === 200)) { ElMessage.success(t('admin.traffic.batchSyncSuccess')); void refreshTrafficAfterSync() }
      else { ElMessage.error(`${t('admin.traffic.batchSyncFailed')}: ${response.msg}`) }
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.traffic.batchSyncError')) }
  }

  const handleBatchLimit = async () => {
    if (selectedUsers.value.length === 0) { ElMessage.warning(t('admin.traffic.pleaseSelectUsers')); return }
    try {
      const { value: reason } = await ElMessageBox.prompt(t('admin.traffic.enterLimitReason'), t('admin.traffic.batchLimit'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), inputPattern: /.{5,}/, inputErrorMessage: t('admin.traffic.limitReasonMinLength') })
      const userIds = selectedUsers.value.map(user => user.user_id)
      const response = await batchManageTrafficLimits({ action: 'limit', user_ids: userIds, reason })
      if ((response.code === 200)) { ElMessage.success(response.msg || t('common.operationSuccess')); loadTrafficRanking() }
      else { ElMessage.error(`${t('admin.traffic.batchLimitFailed')}: ${response.msg}`) }
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.traffic.batchLimitError')) }
  }

  const handleBatchUnlimit = async () => {
    if (selectedUsers.value.length === 0) { ElMessage.warning(t('admin.traffic.pleaseSelectUsers')); return }
    try {
      await ElMessageBox.confirm(t('admin.traffic.confirmBatchUnlimit', { count: selectedUsers.value.length }), t('common.warning'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      const userIds = selectedUsers.value.map(user => user.user_id)
      const response = await batchManageTrafficLimits({ action: 'unlimit', user_ids: userIds })
      if ((response.code === 200)) { ElMessage.success(response.msg || t('common.operationSuccess')); loadTrafficRanking() }
      else { ElMessage.error(`${t('admin.traffic.batchUnlimitFailed')}: ${response.msg}`) }
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.traffic.batchUnlimitError')) }
  }

  const viewUserTraffic = async (userId, { silent = false } = {}) => {
    userTrafficLoading.value = true
    userTrafficDialogVisible.value = true
    try {
      const response = await getUserTrafficStats(userId)
      if ((response.code === 200)) { selectedUserTraffic.value = response.data }
      else if (!silent) { ElMessage.error(`${t('admin.traffic.loadUserDetailsFailed')}: ${response.msg}`); userTrafficDialogVisible.value = false }
    } catch (error) {
      if (!silent) { ElMessage.error(t('admin.traffic.loadUserDetailsError')); userTrafficDialogVisible.value = false }
    } finally { userTrafficLoading.value = false }
  }

  const limitUser = (user) => { selectedUser.value = user; limitAction.value = 'limit'; limitForm.reason = ''; limitDialogVisible.value = true }
  const unlimitUser = (user) => { selectedUser.value = user; limitAction.value = 'unlimit'; limitDialogVisible.value = true }

  const waitForTrafficRefresh = delay => new Promise(resolve => {
    resolveTrafficRefreshTimer = resolve
    trafficRefreshTimer = setTimeout(() => {
      trafficRefreshTimer = null
      resolveTrafficRefreshTimer = null
      resolve()
    }, delay)
  })

  const cancelTrafficRefresh = () => {
    trafficRefreshGeneration += 1
    if (!trafficRefreshTimer) return
    clearTimeout(trafficRefreshTimer)
    trafficRefreshTimer = null
    const resolve = resolveTrafficRefreshTimer
    resolveTrafficRefreshTimer = null
    resolve?.()
  }

  // Agent collection is asynchronous. Refresh a bounded number of times and
  // cancel obsolete loops so a slow node cannot leave permanent polling behind.
  const refreshTrafficAfterSync = async (userId = 0) => {
    cancelTrafficRefresh()
    const generation = trafficRefreshGeneration
    for (const delay of trafficSyncRefreshDelays) {
      await waitForTrafficRefresh(delay)
      if (generation !== trafficRefreshGeneration) return
      const refreshes = [
        loadSystemOverview({ silent: true }),
        loadTrafficRanking({ silent: true })
      ]
      if (userId && userTrafficDialogVisible.value && selectedUserTraffic.value?.user_id === userId) {
        refreshes.push(viewUserTraffic(userId, { silent: true }))
      }
      await Promise.allSettled(refreshes)
      if (generation !== trafficRefreshGeneration) return
    }
  }

  const submitLimitAction = async () => {
    if (limitAction.value === 'limit' && !limitForm.reason.trim()) { ElMessage.error(t('admin.traffic.enterLimitReason')); return }
    limitSubmitting.value = true
    try {
      const data = { type: 'user', action: limitAction.value, target_id: selectedUser.value.user_id, reason: limitForm.reason }
      const response = await manageTrafficLimits(data)
      if ((response.code === 200)) {
        ElMessage.success(t('admin.traffic.limitActionSuccess', { action: limitAction.value === 'limit' ? t('admin.traffic.limit') : t('admin.traffic.remove') }))
        limitDialogVisible.value = false
        const userIndex = trafficRanking.value.findIndex(u => u.user_id === selectedUser.value.user_id)
        if (userIndex !== -1) trafficRanking.value[userIndex].is_limited = limitAction.value === 'limit'
      } else { ElMessage.error(`${t('message.operationFailed')}: ${response.msg}`) }
    } catch (error) { ElMessage.error(t('admin.traffic.operationError')) }
    finally { limitSubmitting.value = false }
  }

  const syncUserTrafficData = async (userId) => {
    if (syncingUsers.value.includes(userId)) return
    syncingUsers.value.push(userId)
    try {
      const response = await batchSyncUserTraffic({ user_ids: [userId] })
      if ((response.code === 200)) { ElMessage.success(t('admin.traffic.syncTriggered')); void refreshTrafficAfterSync(userId) }
      else { ElMessage.error(`${t('admin.traffic.syncFailed')}: ${response.msg}`) }
    } catch (error) { ElMessage.error(t('admin.traffic.syncError')) }
    finally { const index = syncingUsers.value.indexOf(userId); if (index > -1) syncingUsers.value.splice(index, 1) }
  }

  const syncUserTrafficFromDetail = async () => {
    if (!selectedUserTraffic.value || syncingUserDetail.value) return
    syncingUserDetail.value = true
    try {
      const response = await batchSyncUserTraffic({ user_ids: [selectedUserTraffic.value.user_id] })
      if ((response.code === 200)) { ElMessage.success(t('admin.traffic.syncTriggered')); void refreshTrafficAfterSync(selectedUserTraffic.value.user_id) }
      else { ElMessage.error(`${t('admin.traffic.syncFailed')}: ${response.msg}`) }
    } catch (error) { ElMessage.error(t('admin.traffic.syncError')) }
    finally { syncingUserDetail.value = false }
  }

  const syncAllTrafficData = async () => {
    if (!isSuperAdmin.value) return
    syncingAllTraffic.value = true
    try {
      const response = await syncAllTraffic()
      if ((response.code === 200)) { ElMessage.success(t('admin.traffic.syncAllTriggered')); void refreshTrafficAfterSync() }
      else { ElMessage.error(`${t('admin.traffic.syncFailed')}: ${response.msg}`) }
    } catch (error) { ElMessage.error(t('admin.traffic.syncError')) }
    finally { syncingAllTraffic.value = false }
  }

  const clearUserTraffic = async (user) => {
    try {
      await ElMessageBox.confirm(t('admin.traffic.clearTrafficConfirm', { username: user.username }), t('common.warning'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning', dangerouslyUseHTMLString: true })
      const response = await clearUserTrafficRecords(user.user_id)
      if ((response.code === 200)) { ElMessage.success(t('admin.traffic.clearTrafficSuccess', { username: user.username, count: response.data.deleted_count })); loadTrafficRanking(); loadSystemOverview() }
      else { ElMessage.error(`${t('admin.traffic.clearTrafficFailed')}: ${response.msg}`) }
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.traffic.clearTrafficError')) }
  }

  const formatBytes = (bytes) => {
    if (!bytes || bytes === 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    let size = bytes; let unitIndex = 0
    while (size >= 1024 && unitIndex < units.length - 1) { size /= 1024; unitIndex++ }
    return `${size.toFixed(2)} ${units[unitIndex]}`
  }

  const formatTrafficMB = (mb) => {
    if (!mb || mb === 0) return '0 B'
    const GB_IN_MB = 1024; const TB_IN_MB = 1024 * 1024
    if (mb >= TB_IN_MB) return `${(mb / TB_IN_MB).toFixed(2)} TB`
    if (mb >= GB_IN_MB) return `${(mb / GB_IN_MB).toFixed(2)} GB`
    if (mb >= 1) return `${mb.toFixed(2)} MB`
    if (mb > 0) return `${(mb * 1024).toFixed(2)} KB`
    return '0 B'
  }

  const formatDate = (dateString) => {
    if (!dateString) return t('common.notSet')
    return new Date(dateString).toLocaleString(locale.value)
  }

  const getRankTagType = (rank) => {
    if (rank === 1) return 'danger'
    if (rank <= 3) return 'warning'
    if (rank <= 10) return 'primary'
    return 'info'
  }

  const getUsageColor = (percentage) => {
    if (percentage < 60) return '#67c23a'
    if (percentage < 80) return '#e6a23c'
    return '#f56c6c'
  }

  const dataSourceLabel = source => {
    const key = {
      agent: 'dataSourceAgent',
      pmacct: 'dataSourcePmacct',
      mixed: 'dataSourceMixed',
      none: 'dataSourceNone'
    }[source] || 'dataSourceNone'
    return t(`admin.traffic.${key}`)
  }

  const dataSourceTagType = source => ({
    agent: 'success',
    pmacct: 'warning',
    mixed: 'primary',
    none: 'info'
  }[source] || 'info')

  onBeforeUnmount(() => {
    cancelTrafficRefresh()
  })

  return {
    overviewLoading, systemOverview, syncingAllTraffic, isSuperAdmin,
    rankingLoading, trafficRanking, currentPage, pageSize, total, selectedUsers,
    searchParams,
    userTrafficDialogVisible, userTrafficLoading, selectedUserTraffic, syncingUserDetail,
    limitDialogVisible, limitSubmitting, limitAction, selectedUser, syncingUsers,
    limitForm, limitFormRules,
    loadSystemOverview, loadTrafficRanking,
    handleSearch, resetSearch, handleSizeChange, handleCurrentChange, handleSelectionChange,
    handleBatchSync, handleBatchLimit, handleBatchUnlimit,
    viewUserTraffic, limitUser, unlimitUser, submitLimitAction,
    syncUserTrafficData, syncUserTrafficFromDetail, syncAllTrafficData,
    clearUserTraffic,
    formatBytes, formatTrafficMB, formatDate, getRankTagType, getUsageColor, dataSourceLabel, dataSourceTagType,
    t
  }
}
