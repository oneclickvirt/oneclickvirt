import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getProviderIPv4Pool, setProviderIPv4Pool, clearProviderIPv4Pool, deleteProviderIPv4PoolEntry } from '@/api/admin'

export function useIPv4Pool(props) {
  const { t } = useI18n()

  // ---- IPv4 地址池状态 ----
  const poolEntries = ref([])
  const poolStats = ref({ total: 0, allocated: 0, available: 0 })
  const poolLoading = ref(false)
  const newAddresses = ref('')
  const saving = ref(false)

  async function loadPool() {
    if (!props.modelValue.id) return
    poolLoading.value = true
    try {
      const res = await getProviderIPv4Pool(props.modelValue.id, { page: 1, page_size: 200 })
      if (res.data) {
        poolEntries.value = res.data.list || []
        poolStats.value = res.data.stats || { total: 0, allocated: 0, available: 0 }
      }
    } catch {
      ElMessage.error(t('admin.providers.ipv4Pool.loadFailed'))
    } finally {
      poolLoading.value = false
    }
  }

  async function addToPool() {
    if (!newAddresses.value.trim()) return
    saving.value = true
    try {
      await setProviderIPv4Pool(props.modelValue.id, { addresses: newAddresses.value })
      ElMessage.success(t('admin.providers.ipv4Pool.addSuccess'))
      newAddresses.value = ''
      await loadPool()
    } catch {
      ElMessage.error(t('admin.providers.ipv4Pool.addFailed'))
    } finally {
      saving.value = false
    }
  }

  async function clearPool() {
    try {
      await clearProviderIPv4Pool(props.modelValue.id)
      ElMessage.success(t('admin.providers.ipv4Pool.clearSuccess'))
      await loadPool()
    } catch {
      ElMessage.error(t('admin.providers.ipv4Pool.loadFailed'))
    }
  }

  async function deleteEntry(entryId) {
    try {
      await deleteProviderIPv4PoolEntry(props.modelValue.id, entryId)
      ElMessage.success(t('admin.providers.ipv4Pool.deleteSuccess'))
      await loadPool()
    } catch {
      ElMessage.error(t('admin.providers.ipv4Pool.loadFailed'))
    }
  }

  // 当提供商 ID 变更（首次登载 / 的切换编辑）时重收pool
  watch(() => props.modelValue.id, (id) => {
    if (id) loadPool()
  }, { immediate: true })

  // 当 networkType 切换为 dedicated_ipv4* 且已有 id 时加载
  watch(() => props.modelValue.networkType, (nt) => {
    if ((nt === 'dedicated_ipv4' || nt === 'dedicated_ipv4_ipv6') && props.modelValue.id) {
      loadPool()
    }
  })

  return {
    poolEntries,
    poolStats,
    poolLoading,
    newAddresses,
    saving,
    loadPool,
    addToPool,
    clearPool,
    deleteEntry,
  }
}
