import { onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import {
  checkProviderIPv6Tunnels,
  createProviderIPv6Tunnel,
  deleteProviderIPv6Tunnel,
  detectProviderIPv6TunnelLocalIPv4,
  disableProviderIPv6Tunnel,
  enableProviderIPv6Tunnel,
  getProviderIPv6Tunnels,
  updateProviderIPv6Tunnel
} from '@/api/admin'
import { ipv6TunnelErrorMessage, showIPv6TunnelError } from './ipv6TunnelError'

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

const isIPv4Candidate = value => /^(?:\d{1,3}\.){3}\d{1,3}$/.test(value.trim())

export function useIPv6Tunnels(providerId) {
  const { t } = useI18n()
  const tunnels = ref([])
  const loading = ref(false)
  const saving = ref(false)
  const dialogVisible = ref(false)
  const editingTunnel = ref(null)
  const form = ref(emptyForm())
  const actionLoading = ref(new Set())
  const detectingLocalIpv4 = ref(false)
  const detectionError = ref('')
  let detectTimer = null
  let detectionRequest = 0
  let lastDetectedRoute = ''
  let initialEditRoute = ''

  const isBusy = id => actionLoading.value.has(id)
  const setBusy = (id, busy) => {
    const next = new Set(actionLoading.value)
    if (busy) next.add(id)
    else next.delete(id)
    actionLoading.value = next
  }

  const operation = key => t(`admin.providers.ipv6Pool.${key}`)
  const isNotFound = error => Number(error?.response?.status || error?.status || error?.response?.data?.code) === 404

  async function reportError(error, operationKey, { dialog = true } = {}) {
    if (dialog || isNotFound(error)) {
      await showIPv6TunnelError(error, t, operation(operationKey))
      return
    }
    ElMessage.error(ipv6TunnelErrorMessage(error, t('common.operationFailed')))
  }

  async function load({ reportFailure = true } = {}) {
    if (!providerId.value) return
    loading.value = true
    try {
      const response = await getProviderIPv6Tunnels(providerId.value)
      tunnels.value = response.data?.list || []
    } catch (error) {
      if (reportFailure) await reportError(error, 'tunnelLoadOperation')
    } finally {
      loading.value = false
    }
  }

  function openCreate() {
    editingTunnel.value = null
    form.value = emptyForm()
    detectionError.value = ''
    lastDetectedRoute = ''
    initialEditRoute = ''
    dialogVisible.value = true
  }

  function openEdit(tunnel) {
    editingTunnel.value = tunnel
    const remoteIpv4 = tunnel.remoteIpv4 || ''
    initialEditRoute = `${providerId.value}|${remoteIpv4}`
    lastDetectedRoute = initialEditRoute
    detectionError.value = ''
    form.value = {
      ...emptyForm(),
      name: tunnel.name,
      mode: tunnel.mode,
      interfaceName: tunnel.interfaceName,
      localIpv4: tunnel.localIpv4,
      remoteIpv4,
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

  async function detectLocalIPv4({ interactive = false } = {}) {
    if (!providerId.value) return false
    const remoteIpv4 = form.value.remoteIpv4.trim()
    if (!remoteIpv4) {
      detectionError.value = t('admin.providers.ipv6Pool.tunnelRemoteIpv4RequiredForDetect')
      if (interactive) ElMessage.warning(detectionError.value)
      return false
    }

    const currentRequest = ++detectionRequest
    detectingLocalIpv4.value = true
    detectionError.value = ''
    try {
      const response = await detectProviderIPv6TunnelLocalIPv4(providerId.value, remoteIpv4)
      const detected = response.data?.localIpv4
      if (!detected) throw new Error(t('admin.providers.ipv6Pool.tunnelDetectEmpty'))
      if (currentRequest !== detectionRequest) return false
      form.value.localIpv4 = detected
      lastDetectedRoute = `${providerId.value}|${remoteIpv4}`
      return true
    } catch (error) {
      if (currentRequest !== detectionRequest) return false
      detectionError.value = ipv6TunnelErrorMessage(
        error,
        t('admin.providers.ipv6Pool.tunnelDetectFailed'),
        t('admin.providers.ipv6Pool.tunnelProxyError', {
          status: Number(error?.response?.status || error?.status || error?.response?.data?.code || 0)
        })
      )
      if (interactive || isNotFound(error)) {
        await reportError(error, 'tunnelDetectOperation')
      }
      return false
    } finally {
      if (currentRequest === detectionRequest) detectingLocalIpv4.value = false
    }
  }

  function queueLocalIPv4Detection() {
    if (detectTimer) clearTimeout(detectTimer)
    if (!dialogVisible.value || !providerId.value) return
    const remoteIpv4 = form.value.remoteIpv4.trim()
    if (!isIPv4Candidate(remoteIpv4)) return
    const routeKey = `${providerId.value}|${remoteIpv4}`
    if (routeKey === initialEditRoute || routeKey === lastDetectedRoute) return
    detectTimer = setTimeout(() => {
      detectTimer = null
      void detectLocalIPv4()
    }, 450)
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
      await load({ reportFailure: false })
    } catch (error) {
      await reportError(error, editingTunnel.value ? 'tunnelUpdateOperation' : 'tunnelCreateOperation')
      await load({ reportFailure: false })
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
      await load({ reportFailure: false })
    } catch (error) {
      await reportError(error, tunnel.enabled ? 'tunnelDisableOperation' : 'tunnelEnableOperation')
      await load({ reportFailure: false })
    } finally {
      setBusy(tunnel.id, false)
    }
  }

  async function check() {
    loading.value = true
    try {
      await checkProviderIPv6Tunnels(providerId.value)
      ElMessage.success(t('admin.providers.ipv6Pool.tunnelCheckSuccess'))
      await load({ reportFailure: false })
    } catch (error) {
      await reportError(error, 'tunnelCheckOperation')
      await load({ reportFailure: false })
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
      await load({ reportFailure: false })
    } catch (error) {
      await reportError(error, 'tunnelDeleteOperation')
      await load({ reportFailure: false })
    } finally {
      setBusy(tunnel.id, false)
    }
  }

  watch(() => form.value.remoteIpv4, () => {
    queueLocalIPv4Detection()
  })

  watch(providerId, id => {
    if (id) load()
    else tunnels.value = []
  }, { immediate: true })

  onBeforeUnmount(() => {
    if (detectTimer) clearTimeout(detectTimer)
    detectionRequest += 1
  })

  return {
    tunnels,
    loading,
    saving,
    dialogVisible,
    editingTunnel,
    form,
    isBusy,
    detectingLocalIpv4,
    detectionError,
    load,
    openCreate,
    openEdit,
    detectLocalIPv4,
    submit,
    toggle,
    check,
    remove
  }
}
