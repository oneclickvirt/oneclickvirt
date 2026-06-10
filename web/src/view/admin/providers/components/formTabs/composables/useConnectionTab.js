import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { copyToClipboard as copyToClipboardUtil } from '@/utils/clipboard'

export function useConnectionTab(props, emit) {
  const { t } = useI18n()
  const localCommand = ref('')
  const useCDN = ref(true)
  const useWSS = ref(true)
  const useControllerSource = ref(true)
  // wssUnavailable: true when the probe detected that wss:// does NOT work
  // on this host. The toggle is forced off and a warning is shown.
  const wssUnavailable = ref(false)
  const probingWSS = ref(false)
  let wssProbeSocket = null

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

  const installCmdDisplay = computed(() => {
    let cmd = useControllerSource.value ? props.agentConnectCmd : props.agentConnectCmdGithub
    if (!cmd) return ''
    // WSS/WS toggle: replace scheme in --ws-url parameter
    if (useWSS.value) {
      cmd = cmd.replace(/--ws-url ws:\/\//g, '--ws-url wss://')
    } else {
      cmd = cmd.replace(/--ws-url wss:\/\//g, '--ws-url ws://')
    }
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

  // ── wss:// availability probe ──────────────────────────────────────────
  // When the install command appears, extract the --ws-url host:port and
  // attempt a brief WebSocket connection via wss://.  If the TLS handshake
  // fails (onerror fires without onopen), force useWSS to false and show a
  // warning so the admin doesn't generate a broken wss:// install command.
  const extractWsOrigin = (cmd) => {
    const m = cmd.match(/--ws-url\s+(wss?:\/\/[^\s]+)/)
    if (!m) return null
    try {
      const u = new URL(m[1])
      return { host: u.host, path: u.pathname, wss: `wss://${u.host}${u.pathname}` }
    } catch { return null }
  }

  const probeWssAvailability = (cmd) => {
    // Clean up any previous probe socket
    if (wssProbeSocket) {
      wssProbeSocket.onerror = null
      wssProbeSocket.onopen = null
      wssProbeSocket.close()
      wssProbeSocket = null
    }

    const origin = extractWsOrigin(cmd)
    if (!origin) return

    // Only probe if the URL would be wss://
    if (!origin.wss.startsWith('wss://')) return

    probingWSS.value = true
    let resolved = false

    const finish = (available) => {
      if (resolved) return
      resolved = true
      probingWSS.value = false
      if (!available) {
        wssUnavailable.value = true
        useWSS.value = false
      }
      if (wssProbeSocket) {
        wssProbeSocket.onerror = null
        wssProbeSocket.onopen = null
        wssProbeSocket.close()
        wssProbeSocket = null
      }
    }

    try {
      wssProbeSocket = new WebSocket(origin.wss)
      wssProbeSocket.onopen = () => finish(true)
      wssProbeSocket.onerror = () => {
        // onerror fires for TLS failures almost immediately.
        // Wait a short grace period in case onopen is about to fire.
        setTimeout(() => finish(false), 800)
      }
      // Safety timeout: give up after 4 s
      setTimeout(() => finish(false), 4000)
    } catch {
      finish(false)
    }
  }

  // Re-probe whenever a new install command is generated
  watch(() => [props.agentConnectCmd, props.agentConnectCmdGithub], ([controllerCmd]) => {
    wssUnavailable.value = false
    useWSS.value = true
    if (controllerCmd) {
      probeWssAvailability(controllerCmd)
    }
  }, { immediate: true })

  onBeforeUnmount(() => {
    if (wssProbeSocket) {
      wssProbeSocket.onerror = null
      wssProbeSocket.onopen = null
      wssProbeSocket.close()
      wssProbeSocket = null
    }
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

  return {
    t,
    localCommand,
    useCDN,
    useWSS,
    useControllerSource,
    wssUnavailable,
    probingWSS,
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
    copyCmd,
  }
}
