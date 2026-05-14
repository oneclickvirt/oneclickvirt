<template>
  <div class="admin-terminal-container">
    <div ref="terminalRef" class="terminal" />
  </div>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = defineProps({
  providerId: { type: [Number, String], required: true },
  providerName: { type: String, default: '' }
})

const emit = defineEmits(['close'])

const terminalRef = ref(null)
let terminal = null
let fitAddon = null
let websocket = null
let isIntentionallyClosed = false
let reconnectAttempts = 0
let reconnectTimer = null

onMounted(() => nextTick(() => initTerminal()))

onBeforeUnmount(() => cleanup())

const initTerminal = () => {
  terminal = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: 'Monaco, Menlo, "Courier New", monospace',
    theme: {
      background: '#1e1e1e',
      foreground: '#d4d4d4',
      cursor: '#d4d4d4'
    },
    rows: 24,
    cols: 80,
    scrollback: 2000,
    convertEol: false
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
  if (isIntentionallyClosed) return

  const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
  const host = window.location.host
  const wsUrl = `${protocol}://${host}/api/v1/admin/providers/${props.providerId}/terminal`

  terminal.write('\x1b[33mConnecting to ' + (props.providerName || 'provider') + '...\x1b[0m\r\n')

  websocket = new WebSocket(wsUrl)
  websocket.binaryType = 'arraybuffer'

  websocket.onopen = () => {
    reconnectAttempts = 0
    terminal.write('\x1b[32mConnected.\x1b[0m\r\n')

    // Send terminal size
    const dims = { cols: terminal.cols, rows: terminal.rows }
    websocket.send(JSON.stringify({ type: 'resize', ...dims }))
  }

  websocket.onmessage = (event) => {
    if (event.data instanceof ArrayBuffer) {
      terminal.write(new Uint8Array(event.data))
    } else if (typeof event.data === 'string') {
      terminal.write(event.data)
    }
  }

  websocket.onclose = () => {
    terminal.write('\r\n\x1b[31mConnection closed.\x1b[0m\r\n')
    if (!isIntentionallyClosed && reconnectAttempts < 3) {
      reconnectAttempts++
      terminal.write(`\x1b[33mReconnecting (${reconnectAttempts}/3)...\x1b[0m\r\n`)
      reconnectTimer = setTimeout(connect, 2000)
    }
  }

  websocket.onerror = () => {
    terminal.write('\x1b[31mConnection error.\x1b[0m\r\n')
  }

  // Send input to server
  terminal.onData((data) => {
    if (websocket && websocket.readyState === WebSocket.OPEN) {
      websocket.send(data)
    }
  })

  // Resize → send to server
  terminal.onResize(({ cols, rows }) => {
    if (websocket && websocket.readyState === WebSocket.OPEN) {
      websocket.send(JSON.stringify({ type: 'resize', cols, rows }))
      try { fitAddon.fit() } catch {}
    }
  })
}

const cleanup = () => {
  isIntentionallyClosed = true
  if (reconnectTimer) clearTimeout(reconnectTimer)
  if (websocket) { websocket.close(); websocket = null }
  if (terminal) { terminal.dispose(); terminal = null }
}
</script>

<style scoped>
.admin-terminal-container {
  width: 100%;
  height: 400px;
}
.terminal {
  width: 100%;
  height: 100%;
}
</style>
