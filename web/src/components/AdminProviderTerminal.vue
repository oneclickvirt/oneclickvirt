<template>
  <div class="admin-terminal-container">
    <div
      ref="terminalRef"
      class="terminal"
    />
  </div>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount, nextTick, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = defineProps({
  providerId: { type: [Number, String], required: true },
  providerName: { type: String, default: '' },
  visible: { type: Boolean, default: true }
})

const emit = defineEmits(['close'])

const terminalRef = ref(null)
let terminal = null
let fitAddon = null
let websocket = null
let isCleanedUp = false

onMounted(() => nextTick(() => initTerminal()))

onBeforeUnmount(() => cleanup())

// 监听 visible 变化，当对话框关闭时主动断开
watch(() => props.visible, (val) => {
  if (!val) {
    cleanup()
  }
})

const initTerminal = () => {
  if (isCleanedUp) return

  terminal = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: 'Monaco, Menlo, "Courier New", monospace',
    theme: {
      background: '#1e1e1e',
      foreground: '#d4d4d4',
      cursor: '#d4d4d4',
      black: '#000000',
      red: '#cd3131',
      green: '#0dbc79',
      yellow: '#e5e510',
      blue: '#2472c8',
      magenta: '#bc3fbc',
      cyan: '#11a8cd',
      white: '#e5e5e5',
      brightBlack: '#666666',
      brightRed: '#f14c4c',
      brightGreen: '#23d18b',
      brightYellow: '#f5f543',
      brightBlue: '#3b8eea',
      brightMagenta: '#d670d6',
      brightCyan: '#29b8db',
      brightWhite: '#e5e5e5'
    },
    rows: 24,
    cols: 80,
    scrollback: 1000,
    convertEol: false,
    allowProposedApi: true
  })
  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.open(terminalRef.value)

  // Auto-fit after render
  nextTick(() => {
    try { fitAddon.fit() } catch {}
  })

  // Resize → fit
  const observer = new ResizeObserver(() => {
    try { fitAddon.fit() } catch {}
  })
  if (terminalRef.value) observer.observe(terminalRef.value)

  connect()
}

const connect = () => {
  if (isCleanedUp) return

  const token = sessionStorage.getItem('token')
  if (!token) {
    if (terminal) terminal.write('\x1b[31mAuthentication token not found.\x1b[0m\r\n')
    ElMessage.error('Authentication token not found')
    return
  }

  const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
  const host = window.location.host
  const wsUrl = `${protocol}://${host}/api/v1/admin/providers/${props.providerId}/terminal?token=${encodeURIComponent(token)}`

  if (terminal) terminal.write('\x1b[33mConnecting to ' + (props.providerName || 'provider') + '...\x1b[0m\r\n')

  try {
    websocket = new WebSocket(wsUrl)
    websocket.binaryType = 'arraybuffer'

    websocket.onopen = () => {
      if (isCleanedUp) {
        websocket.close()
        return
      }
      if (terminal) terminal.write('\x1b[32mConnected.\x1b[0m\r\n')

      // Send terminal size
      const dims = { cols: terminal.cols, rows: terminal.rows }
      websocket.send(JSON.stringify({ type: 'resize', ...dims }))
    }

    websocket.onmessage = (event) => {
      if (isCleanedUp || !terminal) return
      if (event.data instanceof ArrayBuffer) {
        terminal.write(new Uint8Array(event.data))
      } else if (typeof event.data === 'string') {
        terminal.write(event.data)
      }
    }

    websocket.onclose = (event) => {
      if (isCleanedUp) return
      if (terminal) {
        if (event.wasClean) {
          terminal.write('\r\n\x1b[32mConnection closed.\x1b[0m\r\n')
        } else {
          terminal.write('\r\n\x1b[31mConnection lost. Please close and reopen the terminal.\x1b[0m\r\n')
        }
      }
    }

    websocket.onerror = () => {
      if (!isCleanedUp && terminal) {
        terminal.write('\x1b[31mConnection error.\x1b[0m\r\n')
      }
    }

    // Send input to server
    terminal.onData((data) => {
      if (!isCleanedUp && websocket && websocket.readyState === WebSocket.OPEN) {
        websocket.send(data)
      }
    })

    // Resize → send to server
    terminal.onResize(({ cols, rows }) => {
      if (!isCleanedUp && websocket && websocket.readyState === WebSocket.OPEN) {
        websocket.send(JSON.stringify({ type: 'resize', cols, rows }))
        try { fitAddon.fit() } catch {}
      }
    })
  } catch (err) {
    if (!isCleanedUp && terminal) {
      terminal.write('\x1b[31mFailed to create connection: ' + err.message + '\x1b[0m\r\n')
    }
  }
}

const cleanup = () => {
  if (isCleanedUp) return
  isCleanedUp = true

  // 先关闭 WebSocket（触发后端清理）
  if (websocket) {
    const ws = websocket
    websocket = null
    // 使用 1000 (Normal Closure) 状态码通知后端这是正常关闭
    try { ws.close(1000, 'User closed terminal') } catch {}
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
</script>

<style scoped>
.admin-terminal-container {
  width: 100%;
  height: 100%;
  background-color: #1e1e1e;
  padding: 10px;
  border-radius: 4px;
  overflow: hidden;
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
