<template>
  <div class="admin-terminal-container">
    <RemoteTerminalToolbar
      :active-view="activeView"
      :supports-sftp="supportsFileTransfer"
      :actions="toolbarActions"
      @update:active-view="setActiveView"
      @action="handleToolbarAction"
    />

    <el-alert
      v-if="!supportsFileTransfer"
      :title="t(agentUnavailableAlertTitle)"
      :description="t(agentUnavailableAlertDesc)"
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
          @contextmenu="handleContextMenu"
          @mousedown="handleMouseDown"
        />
    </div>

    <div
      v-if="supportsFileTransfer && activeView === 'sftp'"
      class="sftp-panel"
    >
      <SFTPPanel
        ref="sftpPanelRef"
        :entity-type="fileTransferEntityType"
        :entity-id="providerId"
        :active="activeView === 'sftp'"
      />
    </div>

    <!-- 右键上下文菜单 -->
    <Teleport to="body">
      <div
        v-if="contextMenu.visible"
        class="terminal-context-menu"
        :style="{ left: contextMenu.x + 'px', top: contextMenu.y + 'px' }"
        @click.stop
        @contextmenu.prevent
      >
        <div class="menu-item" @click="handleCopy">
          <span class="menu-label">{{ t('common.copy') }}</span>
          <span class="menu-shortcut">{{ copyShortcut }}</span>
        </div>
        <div class="menu-item" @click="handlePaste">
          <span class="menu-label">{{ t('common.paste') }}</span>
          <span class="menu-shortcut">{{ pasteShortcut }}</span>
        </div>
        <div class="menu-divider" />
        <div class="menu-item" @click="handleSelectAll">
          <span class="menu-label">{{ t('common.selectAll') }}</span>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, ref, onMounted, onBeforeUnmount, nextTick, watch, reactive } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { Unicode11Addon } from '@xterm/addon-unicode11'
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
  providerAuthMethod: { type: String, default: '' },
  connectionType: { type: String, default: 'ssh' },
  agentConnected: { type: Boolean, default: false }
})

const emit = defineEmits(['close'])

const terminalRef = ref(null)
const sftpPanelRef = ref(null)
const activeView = ref('terminal')
const manualReconnecting = ref(false)
let terminal = null
let fitAddon = null
let webLinksAddon = null
let unicode11Addon = null
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

// ── 右键上下文菜单 ──────────────────────────────────────────────────
const contextMenu = reactive({
  visible: false,
  x: 0,
  y: 0
})

const isMac = computed(() => navigator.platform.toUpperCase().indexOf('MAC') >= 0)
const copyShortcut = computed(() => isMac.value ? '⌘C' : 'Ctrl+Shift+C')
const pasteShortcut = computed(() => isMac.value ? '⌘V' : 'Ctrl+Shift+V')

const hideContextMenu = () => {
  contextMenu.visible = false
}

const handleContextMenu = (event) => {
  event.preventDefault()
  contextMenu.x = event.clientX
  contextMenu.y = event.clientY
  contextMenu.visible = true
}

const handleMouseDown = (event) => {
  if (event.button === 1) {
    event.preventDefault()
    pasteFromClipboard()
  }
  if (event.button !== 2) {
    hideContextMenu()
  }
}

const handleCopy = () => {
  copySelectionToClipboard()
  hideContextMenu()
}

const handlePaste = () => {
  pasteFromClipboard()
  hideContextMenu()
}

const handleSelectAll = () => {
  if (terminal) {
    terminal.selectAll()
  }
  hideContextMenu()
}

const onGlobalClick = () => {
  hideContextMenu()
}

const hasProviderSshCredentials = computed(() => {
  return !!(props.providerUsername && props.providerAuthMethod)
})

// 有 SSH 凭据 → SFTP；Agent 模式且在线 → agent-fm
const supportsFileTransfer = computed(() => {
  if (hasProviderSshCredentials.value) return true
  return props.connectionType === 'agent' && props.agentConnected
})

const fileTransferMode = computed(() => {
  if (hasProviderSshCredentials.value) return 'sftp'
  return 'agent-fm'
})

const fileTransferEntityType = computed(() => {
  return fileTransferMode.value === 'sftp' ? 'admin-provider' : 'agent-fm-provider'
})

// Alert i18n keys based on connection type
const agentUnavailableAlertTitle = computed(() => {
  if (props.connectionType === 'agent') return 'common.agentFMUnavailableTitle'
  return 'admin.providers.providerSftpUnavailableTitle'
})

const agentUnavailableAlertDesc = computed(() => {
  if (props.connectionType === 'agent') return 'common.agentFMUnavailableTip'
  return 'admin.providers.providerSftpUnavailableTip'
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

const ignoreNonCriticalTerminalError = (error) => {
  void error
}

onMounted(() => {
  document.addEventListener('click', onGlobalClick)
  nextTick(() => initTerminal())
})

onBeforeUnmount(() => {
  document.removeEventListener('click', onGlobalClick)
  cleanup()
})

// 复制选中文本到系统剪贴板
const copySelectionToClipboard = () => {
  if (!terminal) return
  const selection = terminal.getSelection()
  if (selection) {
    try {
      navigator.clipboard.writeText(selection).catch((err) => {
        console.error('复制到剪贴板失败:', err)
      })
    } catch (error) {
      const textarea = document.createElement('textarea')
      textarea.value = selection
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      try {
        document.execCommand('copy')
      } catch (e) {
        // ignore
      }
      document.body.removeChild(textarea)
    }
  }
}

// 从系统剪贴板粘贴到终端
const pasteFromClipboard = () => {
  if (!terminal || !websocket || websocket.readyState !== WebSocket.OPEN) return
  try {
    navigator.clipboard.readText().then((text) => {
      if (websocket && websocket.readyState === WebSocket.OPEN) {
        websocket.send(text)
      }
    }).catch((err) => {
      console.error('从剪贴板读取失败:', err)
    })
  } catch (error) {
    console.error('粘贴失败:', error)
  }
}

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
    allowProposedApi: true,
    // ── 鼠标支持 ─────────────────────────────────────────────────
    allowMouseReporting: true,
    rightClickSelectsWord: false,
    // ── Mac 键盘支持 ────────────────────────────────────────────
    macOptionIsMeta: true,
    macOptionClickForcesSelection: true
  })
  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)

  // Web 链接识别（Ctrl+点击打开链接）
  webLinksAddon = new WebLinksAddon((event, uri) => {
    event.preventDefault()
    window.open(uri, '_blank', 'noopener,noreferrer')
  })
  terminal.loadAddon(webLinksAddon)

  // Unicode 11 支持
  unicode11Addon = new Unicode11Addon()
  terminal.loadAddon(unicode11Addon)
  terminal.unicode.activeVersion = '11'

  terminal.open(terminalRef.value)
  applyTerminalTheme(terminal)
  startThemeSync()

  // ── 键盘快捷键：复制/粘贴 ──────────────────────────────────────────
  terminal.attachCustomKeyEventHandler((event) => {
    const hasSelection = terminal.hasSelection()
    const ctrlOrCmd = isMac.value ? event.metaKey : event.ctrlKey
    const shift = event.shiftKey

    if (ctrlOrCmd && shift && event.code === 'KeyC') {
      copySelectionToClipboard()
      return false
    }
    if (isMac.value && event.metaKey && !shift && event.code === 'KeyC') {
      if (hasSelection) { copySelectionToClipboard(); return false }
      return true
    }
    if (event.ctrlKey && event.code === 'Insert') {
      copySelectionToClipboard()
      return false
    }
    if (event.shiftKey && event.code === 'Insert') {
      pasteFromClipboard()
      return false
    }
    if (ctrlOrCmd && shift && event.code === 'KeyV') {
      pasteFromClipboard()
      return false
    }
    return true
  })

  terminal.onSelectionChange(() => {
    // 选区变化时可在此做 UI 更新
  })

  // Auto-fit after render
  nextTick(() => {
    try { fitAddon.fit() } catch (error) { ignoreNonCriticalTerminalError(error) }
  })

  // Resize → fit
  resizeObserver = new ResizeObserver(() => {
    try { fitAddon.fit() } catch (error) { ignoreNonCriticalTerminalError(error) }
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
      try { fitAddon.fit() } catch (error) { ignoreNonCriticalTerminalError(error) }
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
        try { ws.close() } catch (error) { ignoreNonCriticalTerminalError(error) }
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
    try { ws.close(1000, reason) } catch (error) { ignoreNonCriticalTerminalError(error) }
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

    if (supportsFileTransfer.value && activeView.value === 'sftp' && sftpPanelRef.value?.refreshNow) {
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
  if (view === 'sftp' && !supportsFileTransfer.value) {
    return
  }
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
    try { ws.close(1000, 'User closed terminal') } catch (error) { ignoreNonCriticalTerminalError(error) }
  }

  if (dataDisposable) {
    try { dataDisposable.dispose() } catch (error) { ignoreNonCriticalTerminalError(error) }
    dataDisposable = null
  }

  if (resizeDisposable) {
    try { resizeDisposable.dispose() } catch (error) { ignoreNonCriticalTerminalError(error) }
    resizeDisposable = null
  }

  // 再清理终端
  if (terminal) {
    try { terminal.dispose() } catch (error) { ignoreNonCriticalTerminalError(error) }
    terminal = null
  }

  if (fitAddon) {
    try { fitAddon.dispose() } catch (error) { ignoreNonCriticalTerminalError(error) }
    fitAddon = null
  }

  if (webLinksAddon) {
    try { webLinksAddon.dispose() } catch (error) { ignoreNonCriticalTerminalError(error) }
    webLinksAddon = null
  }

  if (unicode11Addon) {
    try { unicode11Addon.dispose() } catch (error) { ignoreNonCriticalTerminalError(error) }
    unicode11Addon = null
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
    try { fitAddon?.fit() } catch (error) { ignoreNonCriticalTerminalError(error) }
  })
})

watch(supportsFileTransfer, (enabled) => {
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

:deep(.xterm-viewport) {
  /* 确保文本可选 */
  -webkit-user-select: text;
  user-select: text;
}

:deep(.xterm-selection) {
  pointer-events: none;
}

:deep(.xterm-helpers) {
  pointer-events: none;
}

/* ── 右键上下文菜单 ──────────────────────────────────────────────── */
.terminal-context-menu {
  position: fixed;
  z-index: 99999;
  min-width: 200px;
  background: var(--el-bg-color-overlay, #ffffff);
  border: 1px solid var(--el-border-color-light, #e4e7ed);
  border-radius: 8px;
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.12);
  padding: 4px 0;
  font-size: 13px;
  animation: contextMenuFadeIn 0.15s ease;
}

@keyframes contextMenuFadeIn {
  from { opacity: 0; transform: scale(0.95); }
  to { opacity: 1; transform: scale(1); }
}

.menu-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 16px;
  cursor: pointer;
  color: var(--el-text-color-primary, #303133);
  transition: background 0.1s;
}

.menu-item:hover {
  background: var(--el-fill-color-light, #f5f7fa);
}

.menu-label {
  flex: 1;
}

.menu-shortcut {
  margin-left: 24px;
  color: var(--el-text-color-secondary, #909399);
  font-size: 12px;
}

.menu-divider {
  height: 1px;
  background: var(--el-border-color-lighter, #ebeef5);
  margin: 4px 0;
}
</style>
