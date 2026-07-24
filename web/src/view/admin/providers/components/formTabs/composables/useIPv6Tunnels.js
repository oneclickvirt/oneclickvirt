import { ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import {
  checkProviderIPv6Tunnels,
  createProviderIPv6Tunnel,
  deleteProviderIPv6Tunnel,
  disableProviderIPv6Tunnel,
  enableProviderIPv6Tunnel,
  getProviderIPv6Tunnels,
  updateProviderIPv6Tunnel
} from '@/api/admin'

const emptyForm = () => ({
  name: '',
  mode: 'sit',
  interfaceName: 'he-ipv6',
  localIpv4: '',
  remoteIpv4: '',
  localIpv6: '',
  remoteIpv6: '',
  routedCidr: '',
  mtu: 1480,
  ttl: 255,
  routeMetric: 100,
  defaultRoute: false,
  enabled: true
})

export function useIPv6Tunnels(providerId) {
  const { t } = useI18n()
  const tunnels = ref([])
  const loading = ref(false)
  const saving = ref(false)
  const dialogVisible = ref(false)
  const editingTunnel = ref(null)
  const form = ref(emptyForm())
  const actionLoading = ref(new Set())

  const apiErrorMessage = error => error?.response?.data?.msg || error?.response?.data?.message || error?.userMessage || error?.message || t('common.operationFailed')
  const isBusy = id => actionLoading.value.has(id)
  const setBusy = (id, busy) => {
    const next = new Set(actionLoading.value)
    if (busy) next.add(id)
    else next.delete(id)
    actionLoading.value = next
  }

  async function load() {
    if (!providerId.value) return
    loading.value = true
    try {
      const response = await getProviderIPv6Tunnels(providerId.value)
      tunnels.value = response.data?.list || []
    } catch (error) {
      ElMessage.error(apiErrorMessage(error))
    } finally {
      loading.value = false
    }
  }

  function openCreate() {
    editingTunnel.value = null
    form.value = emptyForm()
    dialogVisible.value = true
  }

  function openEdit(tunnel) {
    editingTunnel.value = tunnel
    form.value = {
      ...emptyForm(),
      name: tunnel.name,
      mode: tunnel.mode,
      interfaceName: tunnel.interfaceName,
      localIpv4: tunnel.localIpv4,
      remoteIpv4: tunnel.remoteIpv4,
      localIpv6: tunnel.localIpv6,
      remoteIpv6: tunnel.remoteIpv6,
      routedCidr: tunnel.routedCidr || '',
      mtu: tunnel.mtu,
      ttl: tunnel.ttl,
      routeMetric: tunnel.routeMetric,
      defaultRoute: Boolean(tunnel.defaultRoute),
      enabled: Boolean(tunnel.enabled)
    }
    dialogVisible.value = true
  }

  async function submit() {
    if (!providerId.value) return
    if (form.value.defaultRoute) {
      try {
        await ElMessageBox.confirm(t('admin.providers.ipv6Pool.tunnelDefaultRouteConfirm'), t('common.warning'), {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        })
      } catch {
        return
      }
    }
    saving.value = true
    try {
      const payload = { ...form.value }
      if (editingTunnel.value) {
        delete payload.enabled
        await updateProviderIPv6Tunnel(providerId.value, editingTunnel.value.id, payload)
        ElMessage.success(t('admin.providers.ipv6Pool.tunnelUpdateSuccess'))
      } else {
        await createProviderIPv6Tunnel(providerId.value, payload)
        ElMessage.success(t('admin.providers.ipv6Pool.tunnelCreateSuccess'))
      }
      dialogVisible.value = false
      await load()
    } catch (error) {
      ElMessage.error(apiErrorMessage(error))
      await load()
    } finally {
      saving.value = false
    }
  }

  async function toggle(tunnel) {
    setBusy(tunnel.id, true)
    try {
      if (tunnel.enabled) {
        await disableProviderIPv6Tunnel(providerId.value, tunnel.id)
        ElMessage.success(t('admin.providers.ipv6Pool.tunnelDisableSuccess'))
      } else {
        await enableProviderIPv6Tunnel(providerId.value, tunnel.id)
        ElMessage.success(t('admin.providers.ipv6Pool.tunnelEnableSuccess'))
      }
      await load()
    } catch (error) {
      ElMessage.error(apiErrorMessage(error))
      await load()
    } finally {
      setBusy(tunnel.id, false)
    }
  }

  async function check() {
    loading.value = true
    try {
      await checkProviderIPv6Tunnels(providerId.value)
      ElMessage.success(t('admin.providers.ipv6Pool.tunnelCheckSuccess'))
      await load()
    } catch (error) {
      ElMessage.error(apiErrorMessage(error))
      await load()
    } finally {
      loading.value = false
    }
  }

  async function remove(tunnel) {
    try {
      await ElMessageBox.confirm(t('admin.providers.ipv6Pool.tunnelDeleteConfirm', { name: tunnel.name }), t('common.warning'), {
        confirmButtonText: t('common.delete'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      })
    } catch {
      return
    }
    setBusy(tunnel.id, true)
    try {
      await deleteProviderIPv6Tunnel(providerId.value, tunnel.id)
      ElMessage.success(t('admin.providers.ipv6Pool.tunnelDeleteSuccess'))
      await load()
    } catch (error) {
      ElMessage.error(apiErrorMessage(error))
      await load()
    } finally {
      setBusy(tunnel.id, false)
    }
  }

  watch(providerId, id => {
    if (id) load()
    else tunnels.value = []
  }, { immediate: true })

  return {
    tunnels,
    loading,
    saving,
    dialogVisible,
    editingTunnel,
    form,
    isBusy,
    load,
    openCreate,
    openEdit,
    submit,
    toggle,
    check,
    remove
  }
}
