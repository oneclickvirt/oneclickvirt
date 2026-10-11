import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { detectLocalProvider } from '@/api/admin/providers'
import { copyToClipboard as copyToClipboardUtil } from '@/utils/clipboard'
import { extractAgentWsURL, agentWsProbeURL, replaceAgentWsProtocol, startAgentWsProbe } from './agentWsProbeCore'

export function useConnectionTab(props, emit) {
  const { t } = useI18n()
  const localCommand = ref('')
  const useCDN = ref(true)
  const useWSS = ref(true)
  const useControllerSource = ref(true)
  const detectingLocal = ref(false)
  const localDetectionResult = ref(null)
  // wssUnavailable: true when the probe found ws:// as the working fallback.
  // The command is downgraded automatically; the toggle remains available so
  // an administrator can retry wss:// after changing the proxy configuration.
  const wssUnavailable = ref(false)
  const probingWSS = ref(false)
  const detectedAgentProtocol = ref('')
  let activeProbe = null
  let probeGeneration = 0

  const isAgentMode = computed(() => props.modelValue.connectionType === 'agent')
  const isLocalMode = computed(() => props.modelValue.connectionType === 'local')
  const hasAgentMappedNetworking = computed(() => false)
  const showSSHSettings = computed(() => !isAgentMode.value && !isLocalMode.value)
  const effectiveAgentStatus = computed(() => props.modelValue.agentRuntimeStatus || props.modelValue.agentStatus || 'offline')
  const agentAlertType = computed(() => {
    if (effectiveAgentStatus.value === 'online') return 'success'
    return 'error'
  })
  const agentStatusLabel = computed(() => {
    if (effectiveAgentStatus.value === 'online') return t('admin.providers.agentStatusOnline')
    return t('admin.providers.agentStatusOffline')
  })
  const localCommandChecks = computed(() => {
    const commands = localDetectionResult.value?.commands || {}
    return Object.keys(commands).sort().map((key) => commands[key]).filter(Boolean)
  })

  const installCmdDisplay = computed(() => {
    let cmd = useControllerSource.value ? props.agentConnectCmd : props.agentConnectCmdGithub
    if (!cmd) return ''
    // WSS/WS toggle: replace scheme in --ws-url parameter
    cmd = replaceAgentWsProtocol(cmd, useWSS.value ? 'wss' : 'ws')
    // CDN toggle: strip CDN prefix for direct connection
    if (!useCDN.value) {
      cmd = cmd.replace(/https:\/\/cdn[^/]*\.[^/]+\//, '')
    }
    return cmd
  })

  watch(useControllerSource, (isController) => {
    if (isController) {
      useCDN.value = false
    } else {
      useCDN.value = true
    }
  }, { immediate: true })

  const extractWsOrigin = extractAgentWsURL
  const selectedAgentCommand = computed(() => (
    useControllerSource.value ? props.agentConnectCmd : props.agentConnectCmdGithub
  ))

  const probeWssAvailability = async (cmd) => {
    const generation = ++probeGeneration
    activeProbe?.cancel()
    activeProbe = null
    wssUnavailable.value = false
    probingWSS.value = false
    const endpoint = extractAgentWsURL(cmd)
    detectedAgentProtocol.value = ''
    if (!endpoint) return
    // The controller has already selected an inferred protocol. Keep it as a
    // usable command immediately, then replace it only after a real browser
    // handshake succeeds.
    useWSS.value = endpoint.protocol === 'wss:'
    detectedAgentProtocol.value = useWSS.value ? 'wss' : 'ws'
    probingWSS.value = true
    try {
      const pageIsSecure = typeof window !== 'undefined' && window.location.protocol === 'https:'
      const candidates = ['wss:', 'ws:']
      for (const protocol of candidates) {
        // Browsers reject mixed-content WS from HTTPS before the handshake.
        // Keep the controller's inferred protocol in that case; it is the
        // only protocol that can still be used by the Agent installer.
        if (protocol === 'ws:' && pageIsSecure) continue
        activeProbe = startAgentWsProbe(agentWsProbeURL(endpoint, protocol))
        const available = await activeProbe.promise
        if (generation !== probeGeneration) return
        if (available) {
          useWSS.value = protocol === 'wss:'
          detectedAgentProtocol.value = protocol === 'wss:' ? 'wss' : 'ws'
          wssUnavailable.value = protocol !== 'wss:'
          return
        }
      }
    } finally {
      if (generation === probeGeneration) {
        activeProbe = null
        probingWSS.value = false
      }
    }
  }

  watch(selectedAgentCommand, (command) => {
    probeWssAvailability(command)
  }, { immediate: true })

  onBeforeUnmount(() => {
    ++probeGeneration
    activeProbe?.cancel()
    activeProbe = null
  })

  // 格式化在线时长（输入: ISO时间字符串或Date，输出: "1h 23m" 或 "5m 30s"）
  const formatOnlineDuration = (connectedAt) => {
    if (!connectedAt) return '-'
    const start = new Date(connectedAt)
    const now = new Date()
    const diffMs = now - start
    if (diffMs < 0) return '-'
    const totalSeconds = Math.floor(diffMs / 1000)
    const days = Math.floor(totalSeconds / 86400)
    const hours = Math.floor((totalSeconds % 86400) / 3600)
    const minutes = Math.floor((totalSeconds % 3600) / 60)
    if (days > 0) return `${days}d ${hours}h ${minutes}m`
    if (hours > 0) return `${hours}h ${minutes}m`
    if (minutes > 0) return `${minutes}m ${totalSeconds % 60}s`
    return `${totalSeconds}s`
  }

  // 格式化日期时间
  const formatDateTime = (dt) => {
    if (!dt) return '-'
    const d = new Date(dt)
    const pad = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  }

  const copyCmd = async (cmd) => {
    await copyToClipboardUtil(cmd, t('common.copySuccess'))
  }

  const formatLocalDetectStatus = (value) => {
    return value ? t('admin.providers.localDetectAvailable') : t('admin.providers.localDetectUnavailable')
  }

  const handleDetectLocalProvider = async () => {
    detectingLocal.value = true
    localDetectionResult.value = null
    try {
      const res = await detectLocalProvider()
      localDetectionResult.value = res.data || {}
      if (localDetectionResult.value.available) {
        ElMessage.success(t('admin.providers.localDetectSuccess'))
      } else {
        ElMessage.warning(t('admin.providers.localDetectFailed'))
      }
    } catch (error) {
      const message = error?.details || error?.message || t('admin.providers.localDetectFailed')
      ElMessage.error(message)
    } finally {
      detectingLocal.value = false
    }
  }

  return {
    t,
    localCommand,
    useCDN,
    useWSS,
    detectedAgentProtocol,
    useControllerSource,
    wssUnavailable,
    probingWSS,
    detectingLocal,
    localDetectionResult,
    localCommandChecks,
    isAgentMode,
    isLocalMode,
    hasAgentMappedNetworking,
    showSSHSettings,
    effectiveAgentStatus,
    agentAlertType,
    agentStatusLabel,
    installCmdDisplay,
    extractWsOrigin,
    probeWssAvailability,
    formatOnlineDuration,
    formatDateTime,
    formatLocalDetectStatus,
    handleDetectLocalProvider,
    copyCmd,
  }
}
