<template>
  <div class="admin-terminal-container">
    <RemoteTerminalToolbar
      :active-view="activeView"
      :supports-sftp="supportsSFTP"
      :actions="toolbarActions"
      @update:active-view="setActiveView"
      @action="handleToolbarAction"
    />

    <el-alert
      v-if="!supportsSFTP"
      :title="t('admin.providers.providerSftpUnavailableTitle')"
      :description="t('admin.providers.providerSftpUnavailableTip')"
      type="info"
      :closable="false"
      class="remote-connect-alert"
    />

    <div
      v-show="activeView === 'terminal'"
      class="terminal-panel"
    >
        <div
          ref="terminalRef"
          class="terminal"
        />
    </div>

    <div
      v-show="supportsSFTP && activeView === 'sftp'"
      class="sftp-panel"
    >
      <SFTPPanel
        ref="sftpPanelRef"
        entity-type="admin-provider"
        :entity-id="providerId"
        :active="activeView === 'sftp'"
      />
    </div>
  </div>
</template>

<script setup>
import { computed, ref, onMounted, onBeforeUnmount, nextTick, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import SFTPPanel from '@/components/SFTPPanel.vue'
import { Refresh } from '@element-plus/icons-vue'
import RemoteTerminalToolbar from '@/components/RemoteTerminalToolbar.vue'
import { applyTerminalTheme } from '@/utils/terminalTheme'

const { t } = useI18n()

const props = defineProps({
  providerId: { type: [Number, String], required: true },
  providerName: { type: String, default: '' },
  providerUsername: { type: String, default: '' },
  providerAuthMethod: { type: String, default: '' }
})

const emit = defineEmits(['close'])

const terminalRef = ref(null)
const sftpPanelRef = ref(null)
const activeView = ref('terminal')
const manualReconnecting = ref(false)
let terminal = null
let fitAddon = null
let websocket = null
let resizeObserver = null
let isCleanedUp = false
let isConnecting = false
let isIntentionallyClosed = false
let heartbeatInterval = null
let reconnectTimeout = null
let dataDisposable = null
let resizeDisposable = null
let themeObserver = null

const supportsSFTP = computed(() => {
  return Boolean(props.providerUsername && props.providerAuthMethod)
})

const toolbarActions = computed(() => ([
  {
    key: 'reconnect',
    label: t('admin.providers.reconnectNow'),
    title: t('admin.providers.reconnectNow'),
    icon: Refresh,
    loading: manualReconnecting.value
  }
]))

onMounted(() => nextTick(() => initTerminal()))

onBeforeUnmount(() => cleanup())

const initTerminal = () => {
  if (isCleanedUp) return
  if (terminal) return

  terminal = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: 'Monaco, Menlo, "Courier New", monospace',
    theme: {},
    rows: 24,
    cols: 80,
    scrollback: 1000,
    convertEol: false,
    allowProposedApi: true
  })
  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.open(terminalRef.value)
  applyTerminalTheme(terminal)
  startThemeSync()

  // Auto-fit after render
  nextTick(() => {
    try { fitAddon.fit() } catch {}
  })

  // Resize → fit
  resizeObserver = new ResizeObserver(() => {
    try { fitAddon.fit() } catch {}
  })
  if (terminalRef.value) resizeObserver.observe(terminalRef.value)

  dataDisposable = terminal.onData((data) => {
    if (!isCleanedUp && websocket && websocket.readyState === WebSocket.OPEN) {
      websocket.send(data)
    }
  })

  resizeDisposable = terminal.onResize(({ cols, rows }) => {
    if (!isCleanedUp && websocket && websocket.readyState === WebSocket.OPEN) {
      websocket.send(JSON.stringify({ type: 'resize', cols, rows }))
      try { fitAddon.fit() } catch {}
    }
  })

  connect()
}

const connect = () => {
  if (isCleanedUp) return
  if (isConnecting) return
  if (websocket && (websocket.readyState === WebSocket.OPEN || websocket.readyState === WebSocket.CONNECTING)) {
    return
  }

  const token = sessionStorage.getItem('token')
  if (!token) {
    if (terminal) terminal.write('\x1b[31mAuthentication token not found.\x1b[0m\r\n')
    ElMessage.error('Authentication token not found')
    return
  }

  const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
  let host = window.location.host

  // 开发环境：如果前端运行在 Vite 端口，WebSocket 直接连接到后端端口
  if (import.meta.env.MODE === 'development' && import.meta.env.VITE_SERVER_PORT) {
    const serverPort = import.meta.env.VITE_SERVER_PORT
    host = `${window.location.hostname}:${serverPort}`
  }

  const wsUrl = `${protocol}://${host}/api/v1/admin/providers/${props.providerId}/terminal?token=${encodeURIComponent(token)}`

  if (terminal) terminal.write('\x1b[33mConnecting to ' + (props.providerName || 'provider') + '...\x1b[0m\r\n')

  try {
    isConnecting = true
    isIntentionallyClosed = false
    const ws = new WebSocket(wsUrl)
    websocket = ws
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      if (websocket !== ws) {
        return
      }
      isConnecting = false
      if (isCleanedUp) {
        try { ws.close() } catch {}
        return
      }
      if (terminal) terminal.write('\x1b[32mConnected.\x1b[0m\r\n')

      // Send terminal size
      const dims = { cols: terminal.cols, rows: terminal.rows }
      ws.send(JSON.stringify({ type: 'resize', ...dims }))
      startHeartbeat()
    }

    ws.onmessage = (event) => {
      if (websocket !== ws) {
        return
      }
      if (isCleanedUp || !terminal) return
      if (event.data instanceof ArrayBuffer) {
        terminal.write(new Uint8Array(event.data))
      } else if (typeof event.data === 'string') {
        terminal.write(event.data)
      }
    }

    ws.onclose = (event) => {
      if (websocket !== ws) {
        return
      }
      isConnecting = false
      stopHeartbeat()
      if (isCleanedUp) return
      websocket = null

      if (isIntentionallyClosed || event.code === 1000) {
        if (terminal) {
          terminal.write('\r\n\x1b[32mConnection closed.\x1b[0m\r\n')
        }
        return
      }

      if (terminal) {
        terminal.write('\r\n\x1b[33mConnection lost, retrying in 3s...\x1b[0m\r\n')
      }
      scheduleReconnect()
    }

    ws.onerror = (error) => {
      if (websocket !== ws) {
        return
      }
      isConnecting = false
      if (!isCleanedUp && terminal) {
        terminal.write('\x1b[31mConnection error.\x1b[0m\r\n')
      }
      console.error('Provider terminal websocket error:', error)
    }
  } catch (err) {
    isConnecting = false
    if (!isCleanedUp && terminal) {
      terminal.write('\x1b[31mFailed to create connection: ' + err.message + '\x1b[0m\r\n')
    }
    scheduleReconnect()
  }
}

const startHeartbeat = () => {
  stopHeartbeat()
  heartbeatInterval = setInterval(() => {
    if (!websocket || websocket.readyState !== WebSocket.OPEN) {
      return
    }
    try {
      websocket.send(JSON.stringify({ type: 'ping' }))
    } catch (error) {
      console.error('Provider terminal heartbeat failed:', error)
    }
  }, 30000)
}

const stopHeartbeat = () => {
  if (heartbeatInterval) {
    clearInterval(heartbeatInterval)
    heartbeatInterval = null
  }
}

const scheduleReconnect = () => {
  if (isCleanedUp || isIntentionallyClosed || reconnectTimeout) {
    return
  }
  reconnectTimeout = setTimeout(() => {
    reconnectTimeout = null
    connect()
  }, 3000)
}

const reconnectTerminal = (reason = 'Manual reconnect') => {
  isIntentionallyClosed = false
  isConnecting = false
  stopHeartbeat()
  if (reconnectTimeout) {
    clearTimeout(reconnectTimeout)
    reconnectTimeout = null
  }
  if (websocket) {
    const ws = websocket
    websocket = null
    try { ws.close(1000, reason) } catch {}
  }
  connect()
}

const handleManualReconnect = async () => {
  if (manualReconnecting.value) {
    return
  }

  manualReconnecting.value = true
  try {
    reconnectTerminal('Manual reconnect from toolbar')

    if (supportsSFTP.value && sftpPanelRef.value?.refreshNow) {
      await sftpPanelRef.value.refreshNow(true)
    }

    ElMessage.success(t('admin.providers.reconnectTriggered'))
  } catch (error) {
    ElMessage.error(error?.message || t('admin.providers.reconnectFailed'))
  } finally {
    manualReconnecting.value = false
  }
}

const handleToolbarAction = (action) => {
  if (action === 'reconnect') {
    handleManualReconnect()
  }
}

const setActiveView = (view) => {
  activeView.value = view
}

const cleanup = () => {
  if (isCleanedUp) return
  isCleanedUp = true
  isIntentionallyClosed = true
  isConnecting = false
  stopHeartbeat()
  stopThemeSync()
  if (reconnectTimeout) {
    clearTimeout(reconnectTimeout)
    reconnectTimeout = null
  }

  // 断开 ResizeObserver 防止内存泄漏
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }

  // 先关闭 WebSocket（触发后端清理）
  if (websocket) {
    const ws = websocket
    websocket = null
    try { ws.close(1000, 'User closed terminal') } catch {}
  }

  if (dataDisposable) {
    try { dataDisposable.dispose() } catch {}
    dataDisposable = null
  }

  if (resizeDisposable) {
    try { resizeDisposable.dispose() } catch {}
    resizeDisposable = null
  }

  // 再清理终端
  if (terminal) {
    try { terminal.dispose() } catch {}
    terminal = null
  }

  if (fitAddon) {
    try { fitAddon.dispose() } catch {}
    fitAddon = null
  }
}

const startThemeSync = () => {
  stopThemeSync()
  themeObserver = new MutationObserver(() => {
    applyTerminalTheme(terminal)
  })
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['class']
  })
}

const stopThemeSync = () => {
  if (themeObserver) {
    themeObserver.disconnect()
    themeObserver = null
  }
}

watch(activeView, (view) => {
  if (view !== 'terminal') {
    return
  }
  nextTick(() => {
    try { fitAddon?.fit() } catch {}
  })
})

watch(supportsSFTP, (enabled) => {
  if (!enabled && activeView.value === 'sftp') {
    activeView.value = 'terminal'
  }
}, { immediate: true })
</script>

<style scoped>
.admin-terminal-container {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.remote-connect-alert {
  margin-bottom: 0;
}

.terminal-panel,
.sftp-panel {
  flex: 1;
  min-height: 0;
}

.terminal-panel {
  background-color: var(--terminal-bg);
  border-radius: 6px;
  overflow: hidden;
  padding: 10px;
}

.sftp-panel {
  background-color: var(--el-bg-color-overlay);
  border-radius: 6px;
  padding: 10px;
  overflow: auto;
}

.terminal {
  width: 100%;
  height: 100%;
}

/* xterm.js 默认样式覆盖 */
:deep(.xterm) {
  height: 100%;
  padding: 10px;
}
</style>
