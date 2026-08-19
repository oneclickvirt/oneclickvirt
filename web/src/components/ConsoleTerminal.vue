<template>
  <div
    class="console-terminal-container"
    @contextmenu="showContextMenu"
    @mousedown="handleMouseDown"
  >
    <div
      ref="terminalRef"
      class="terminal"
    />
    <Teleport to="body">
      <div
        v-if="contextMenu.visible"
        class="terminal-context-menu"
        :style="{ left: `${contextMenu.x}px`, top: `${contextMenu.y}px` }"
        @click.stop
        @contextmenu.prevent
      >
        <button
          type="button"
          class="menu-item"
          @click="copySelection"
        >
          <span>{{ t('common.copy') }}</span>
          <kbd>{{ copyShortcut }}</kbd>
        </button>
        <button
          type="button"
          class="menu-item"
          @click="pasteFromClipboard"
        >
          <span>{{ t('common.paste') }}</span>
          <kbd>{{ pasteShortcut }}</kbd>
        </button>
        <div class="menu-divider" />
        <button
          type="button"
          class="menu-item"
          @click="selectAll"
        >
          <span>{{ t('common.selectAll') }}</span>
        </button>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import '@xterm/xterm/css/xterm.css'
import { getUserInstanceConsoleTerminalWsUrl } from '@/api/user'
import { copyToClipboard, readFromClipboard } from '@/utils/clipboard'
import { applyTerminalTheme } from '@/utils/terminalTheme'

const props = defineProps({
  instanceId: { type: [Number, String], required: true },
  protocol: { type: String, required: true }
})

const { t } = useI18n()
const terminalRef = ref(null)
const contextMenu = reactive({ visible: false, x: 0, y: 0 })
const isMac = computed(() => /mac/i.test(navigator.platform || ''))
const copyShortcut = computed(() => (isMac.value ? 'Cmd+Shift+C' : 'Ctrl+Shift+C'))
const pasteShortcut = computed(() => (isMac.value ? 'Cmd+Shift+V' : 'Ctrl+Shift+V'))

let terminal = null
let fitAddon = null
let websocket = null
let heartbeat = null
let resizeObserver = null
let themeObserver = null
let intentionallyClosed = false
let socketFailed = false

function hideContextMenu() {
  contextMenu.visible = false
}

function showContextMenu(event) {
  event.preventDefault()
  contextMenu.x = event.clientX
  contextMenu.y = event.clientY
  contextMenu.visible = true
}

function handleMouseDown(event) {
  if (event.button === 1) {
    event.preventDefault()
    pasteFromClipboard()
  }
  if (event.button !== 2) hideContextMenu()
}

async function copySelection() {
  hideContextMenu()
  if (!terminal) return
  await copyToClipboard(terminal.getSelection(), t('common.copySuccess'), t('common.copyFailed'))
}

async function pasteFromClipboard() {
  hideContextMenu()
  if (!terminal || !websocket || websocket.readyState !== WebSocket.OPEN) {
    ElMessage.warning(t('user.instanceDetail.consoleTerminalClosed'))
    return
  }
  const text = await readFromClipboard(t('common.pasteFailed'))
  if (text !== null && websocket?.readyState === WebSocket.OPEN) {
    websocket.send(text)
  }
}

function selectAll() {
  hideContextMenu()
  terminal?.selectAll()
}

function sendResize() {
  if (!fitAddon || !terminal) return
  try {
    fitAddon.fit()
  } catch {
    return
  }
  if (websocket?.readyState === WebSocket.OPEN) {
    websocket.send(JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }))
  }
}

function startHeartbeat() {
  stopHeartbeat()
  heartbeat = window.setInterval(() => {
    if (websocket?.readyState === WebSocket.OPEN) {
      websocket.send(JSON.stringify({ type: 'ping' }))
    }
  }, 30000)
}

function stopHeartbeat() {
  if (heartbeat !== null) {
    window.clearInterval(heartbeat)
    heartbeat = null
  }
}

function initTerminal() {
  terminal = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: 'Monaco, Menlo, "Courier New", monospace',
    rows: 24,
    cols: 80,
    scrollback: 2000,
    convertEol: false,
    allowProposedApi: true,
    allowMouseReporting: true,
    rightClickSelectsWord: false,
    macOptionIsMeta: true,
    macOptionClickForcesSelection: true
  })
  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.loadAddon(new WebLinksAddon((event, uri) => {
    event.preventDefault()
    window.open(uri, '_blank', 'noopener,noreferrer')
  }))
  const unicode = new Unicode11Addon()
  terminal.loadAddon(unicode)
  terminal.unicode.activeVersion = '11'
  terminal.open(terminalRef.value)
  applyTerminalTheme(terminal)

  terminal.attachCustomKeyEventHandler(event => {
    const ctrlOrCmd = isMac.value ? event.metaKey : event.ctrlKey
    if (ctrlOrCmd && event.shiftKey && event.code === 'KeyC') {
      copySelection()
      return false
    }
    if (ctrlOrCmd && event.shiftKey && event.code === 'KeyV') {
      pasteFromClipboard()
      return false
    }
    if (isMac.value && event.metaKey && !event.shiftKey && event.code === 'KeyC' && terminal.hasSelection()) {
      copySelection()
      return false
    }
    if (event.ctrlKey && event.code === 'Insert') {
      copySelection()
      return false
    }
    if (event.shiftKey && event.code === 'Insert') {
      pasteFromClipboard()
      return false
    }
    return true
  })
  terminal.onData(data => {
    if (websocket?.readyState === WebSocket.OPEN) websocket.send(data)
  })

  resizeObserver = new ResizeObserver(sendResize)
  resizeObserver.observe(terminalRef.value)
  themeObserver = new MutationObserver(() => applyTerminalTheme(terminal))
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  window.setTimeout(sendResize, 0)
}

function connect() {
  const token = sessionStorage.getItem('token') || ''
  if (!token) {
    terminal?.writeln(`\x1b[31m${t('user.instanceDetail.consoleTerminalAuthMissing')}\x1b[0m`)
    return
  }
  terminal?.writeln(t('user.instanceDetail.consoleTerminalConnecting'))
  try {
    websocket = new WebSocket(getUserInstanceConsoleTerminalWsUrl(props.instanceId, props.protocol))
    websocket.binaryType = 'arraybuffer'
    websocket.onopen = () => {
      socketFailed = false
      terminal?.writeln(`\x1b[32m${t('user.instanceDetail.consoleTerminalConnected')}\x1b[0m`)
      terminal?.focus()
      sendResize()
      startHeartbeat()
    }
    websocket.onmessage = event => {
      if (!terminal) return
      terminal.write(event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : event.data)
    }
    websocket.onerror = () => {
      socketFailed = true
    }
    websocket.onclose = () => {
      stopHeartbeat()
      if (!intentionallyClosed && terminal) {
        terminal.writeln(`\x1b[33m${socketFailed ? t('user.instanceDetail.consoleTerminalError') : t('user.instanceDetail.consoleTerminalClosed')}\x1b[0m`)
      }
    }
  } catch {
    socketFailed = true
    terminal?.writeln(`\x1b[31m${t('user.instanceDetail.consoleTerminalError')}\x1b[0m`)
  }
}

function cleanup() {
  intentionallyClosed = true
  stopHeartbeat()
  resizeObserver?.disconnect()
  resizeObserver = null
  themeObserver?.disconnect()
  themeObserver = null
  if (websocket) {
    try {
      websocket.close(1000, 'Console closed')
    } catch {
      // A connection that never opened cannot always be closed cleanly.
    }
    websocket = null
  }
  fitAddon?.dispose()
  fitAddon = null
  terminal?.dispose()
  terminal = null
}

onMounted(async () => {
  document.addEventListener('click', hideContextMenu)
  await nextTick()
  initTerminal()
  connect()
})

onBeforeUnmount(() => {
  document.removeEventListener('click', hideContextMenu)
  cleanup()
})
</script>

<style scoped>
.console-terminal-container {
  width: 100%;
  height: min(70vh, 620px);
  min-height: 420px;
  overflow: hidden;
  padding: 8px;
  background: #000000;
  border: 1px solid var(--el-border-color);
}

.terminal {
  width: 100%;
  height: 100%;
}

:deep(.xterm),
:deep(.xterm-screen) {
  height: 100% !important;
}

:deep(.xterm-viewport) {
  overflow-y: auto;
  background: #000000 !important;
  -webkit-user-select: text;
  user-select: text;
}

:deep(.xterm-selection),
:deep(.xterm-helpers) {
  pointer-events: none;
}

.terminal-context-menu {
  position: fixed;
  z-index: 3000;
  min-width: 170px;
  padding: 4px 0;
  background: var(--el-bg-color-overlay, #ffffff);
  border: 1px solid var(--el-border-color-light, #e4e7ed);
  border-radius: 4px;
  box-shadow: 0 6px 16px rgb(0 0 0 / 16%);
}

.menu-item {
  display: flex;
  width: 100%;
  min-height: 30px;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 5px 10px;
  color: var(--el-text-color-primary);
  background: transparent;
  border: 0;
  text-align: left;
  cursor: pointer;
}

.menu-item:hover {
  background: var(--el-fill-color-light);
}

kbd {
  color: var(--el-text-color-secondary);
  font-family: inherit;
  font-size: 11px;
}

.menu-divider {
  height: 1px;
  margin: 4px 0;
  background: var(--el-border-color-lighter);
}
</style>
