import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import {
  clearProviderIPv6Pool,
  deleteProviderIPv6PoolEntry,
  getProviderIPv6Pool,
  setProviderIPv6Pool,
  syncProviderIPv6Pool,
  updateProvider
} from '@/api/admin'

export function useIPv6Pool(props, onProviderUpdated = () => {}) {
  const { t, locale } = useI18n()
  const ipv6PoolEntries = ref([])
  const ipv6PoolStats = ref({ total: 0, allocated: 0, available: 0 })
  const ipv6PoolLoading = ref(false)
  const newIPv6Addresses = ref('')
  const ipv6PoolSaving = ref(false)
  const ipv6FileSaving = ref(false)
  const ipv6FileSyncing = ref(false)
  const ipv6SyncResult = ref(null)

  const ipv6LastSyncedAt = computed(() => {
    const value = ipv6SyncResult.value?.syncedAt || props.modelValue.ipv6AddressFileSyncedAt
    if (!value) return ''
    const date = new Date(value)
    return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString(locale.value)
  })

  const apiErrorMessage = error => error?.response?.data?.msg || error?.response?.data?.message || error?.message || t('common.operationFailed')
  const notifyProviderUpdated = () => onProviderUpdated({
    ipv6AddressFilePath: props.modelValue.ipv6AddressFilePath || '',
    ipv6AddressFileSyncedAt: props.modelValue.ipv6AddressFileSyncedAt || null,
    ipv6AddressFileSyncError: props.modelValue.ipv6AddressFileSyncError || ''
  })

  async function loadIPv6Pool() {
    if (!props.modelValue.id) return
    ipv6PoolLoading.value = true
    try {
      const res = await getProviderIPv6Pool(props.modelValue.id, { page: 1, pageSize: 200 })
      ipv6PoolEntries.value = res.data?.list || []
      ipv6PoolStats.value = res.data?.stats || { total: 0, allocated: 0, available: 0 }
    } catch {
      ElMessage.error(t('admin.providers.ipv6Pool.loadFailed'))
    } finally {
      ipv6PoolLoading.value = false
    }
  }

  async function addIPv6ToPool() {
    if (!newIPv6Addresses.value.trim()) return
    ipv6PoolSaving.value = true
    try {
      const res = await setProviderIPv6Pool(props.modelValue.id, { addresses: newIPv6Addresses.value })
      const invalid = res.data?.invalidLines || []
      if (invalid.length) ElMessage.warning(t('admin.providers.ipv6Pool.addPartial', { count: invalid.length }))
      else ElMessage.success(t('admin.providers.ipv6Pool.addSuccess'))
      newIPv6Addresses.value = ''
      await loadIPv6Pool()
    } catch {
      ElMessage.error(t('admin.providers.ipv6Pool.addFailed'))
    } finally {
      ipv6PoolSaving.value = false
    }
  }

  async function persistIPv6FilePath(filePath, successKey) {
    if (!props.modelValue.id) return false
    ipv6FileSaving.value = true
    try {
      await updateProvider(props.modelValue.id, { ipv6AddressFilePath: filePath })
      props.modelValue.ipv6AddressFilePath = filePath
      if (!filePath) ipv6SyncResult.value = null
      ElMessage.success(t(successKey))
      await notifyProviderUpdated()
      return true
    } catch (error) {
      ElMessage.error(apiErrorMessage(error) || t('admin.providers.ipv6Pool.fileSaveFailed'))
      return false
    } finally {
      ipv6FileSaving.value = false
    }
  }

  async function saveIPv6FilePath() {
    const filePath = String(props.modelValue.ipv6AddressFilePath || '').trim()
    await persistIPv6FilePath(filePath, 'admin.providers.ipv6Pool.fileSaveSuccess')
  }

  async function clearIPv6FilePath() {
    await persistIPv6FilePath('', 'admin.providers.ipv6Pool.fileClearSuccess')
  }

  async function syncIPv6File() {
    if (!props.modelValue.id) return
    const filePath = String(props.modelValue.ipv6AddressFilePath || '').trim()
    if (!filePath) {
      ElMessage.warning(t('admin.providers.ipv6Pool.filePathRequired'))
      return
    }
    ipv6FileSyncing.value = true
    try {
      const res = await syncProviderIPv6Pool(props.modelValue.id, { filePath })
      const result = res.data || {}
      ipv6SyncResult.value = result
      props.modelValue.ipv6AddressFilePath = result.path || filePath
      props.modelValue.ipv6AddressFileSyncedAt = result.syncedAt || null
      props.modelValue.ipv6AddressFileSyncError = ''
      if (result.stats) ipv6PoolStats.value = result.stats
      ElMessage.success(t('admin.providers.ipv6Pool.syncSuccess'))
      await loadIPv6Pool()
      await notifyProviderUpdated()
    } catch (error) {
      const message = apiErrorMessage(error)
      props.modelValue.ipv6AddressFileSyncError = message
      ElMessage.error(message || t('admin.providers.ipv6Pool.syncFailed'))
      await notifyProviderUpdated()
    } finally {
      ipv6FileSyncing.value = false
    }
  }

  async function clearIPv6Pool() {
    try {
      await clearProviderIPv6Pool(props.modelValue.id)
      ElMessage.success(t('admin.providers.ipv6Pool.clearSuccess'))
      await loadIPv6Pool()
    } catch {
      ElMessage.error(t('admin.providers.ipv6Pool.loadFailed'))
    }
  }

  async function deleteIPv6Entry(entryId) {
    try {
      await deleteProviderIPv6PoolEntry(props.modelValue.id, entryId)
      ElMessage.success(t('admin.providers.ipv6Pool.deleteSuccess'))
      await loadIPv6Pool()
    } catch {
      ElMessage.error(t('admin.providers.ipv6Pool.loadFailed'))
    }
  }

  watch(() => props.modelValue.id, (id, previousId) => {
    if (id !== previousId) ipv6SyncResult.value = null
    if (id) loadIPv6Pool()
  }, { immediate: true })
  watch(() => props.modelValue.networkType, (networkType) => {
    if (['nat_ipv4_ipv6', 'dedicated_ipv4_ipv6', 'ipv6_only'].includes(networkType) && props.modelValue.id) loadIPv6Pool()
  })

  return {
    ipv6PoolEntries,
    ipv6PoolStats,
    ipv6PoolLoading,
    newIPv6Addresses,
    ipv6PoolSaving,
    ipv6FileSaving,
    ipv6FileSyncing,
    ipv6SyncResult,
    ipv6LastSyncedAt,
    loadIPv6Pool,
    addIPv6ToPool,
    clearIPv6Pool,
    deleteIPv6Entry,
    saveIPv6FilePath,
    clearIPv6FilePath,
    syncIPv6File
  }
}
