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
  // wssUnavailable: true when the probe detected that wss:// does NOT work
  // on this host. The toggle is forced off and a warning is shown.
  const wssUnavailable = ref(false)
  const probingWSS = ref(false)
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

  const probeWssAvailability = async (cmd) => {
    const generation = ++probeGeneration
    activeProbe?.cancel()
    activeProbe = null
    wssUnavailable.value = false
    probingWSS.value = false
    const endpoint = extractAgentWsURL(cmd)
    if (!endpoint) return
    // Keep the server's inferred protocol until a successful probe says otherwise.
    useWSS.value = endpoint.protocol === 'wss:'
    probingWSS.value = true
    try {
      activeProbe = startAgentWsProbe(agentWsProbeURL(endpoint, 'wss:'))
      const secure = await activeProbe.promise
      if (generation !== probeGeneration) return
      if (secure) {
        useWSS.value = true
        return
      }
      // HTTPS pages cannot open a plain WebSocket; retain the server's choice
      // when this browser cannot verify the alternative.
      if (window.location.protocol === 'https:') return
      activeProbe = startAgentWsProbe(agentWsProbeURL(endpoint, 'ws:'))
      const plain = await activeProbe.promise
      if (generation !== probeGeneration) return
      if (plain) {
        useWSS.value = false
        wssUnavailable.value = true
      }
    } finally {
      if (generation === probeGeneration) {
        activeProbe = null
        probingWSS.value = false
      }
    }
  }

  watch(() => [props.agentConnectCmd, props.agentConnectCmdGithub], ([controllerCmd, githubCmd]) => {
    probeWssAvailability(controllerCmd || githubCmd)
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
