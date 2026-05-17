<template>
  <div class="ssh-terminal-container">
    <div 
      ref="terminalRef" 
      class="terminal"
    />
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
  instanceId: {
    type: [Number, String],
    required: true
  },
  instanceName: {
    type: String,
    default: ''
  },
  isAdmin: {
    type: Boolean,
    default: false
  },
  mode: {
    type: String,
    default: 'ssh', // 'ssh' or 'exec'
    validator: (v) => ['ssh', 'exec'].includes(v)
  }
})

const emit = defineEmits(['close', 'error'])

const terminalRef = ref(null)
let terminal = null
let fitAddon = null
let websocket = null
let isConnecting = false
let heartbeatInterval = null
let reconnectTimeout = null
let isIntentionallyClosed = false

onMounted(() => {
  nextTick(() => {
    initTerminal()
    connect()
  })
})

onBeforeUnmount(() => {
  cleanup()
})

const initTerminal = () => {
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
    // vim/vi 需要的额外配置
    scrollback: 1000,
    convertEol: false,
    // 确保能正确处理特殊按键
    allowProposedApi: true
  })

  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)
  terminal.open(terminalRef.value)
  
  // 适应容器大小
  setTimeout(() => {
    fitAddon.fit()
  }, 100)

  // 监听窗口大小变化
  window.addEventListener('resize', handleResize)

  // 监听终端输入
  terminal.onData((data) => {
    if (websocket && websocket.readyState === WebSocket.OPEN) {
      websocket.send(data)
    }
  })
}

const handleResize = () => {
  if (fitAddon && terminal) {
    fitAddon.fit()
    // 发送终端大小调整消息到后端
    if (websocket && websocket.readyState === WebSocket.OPEN) {
      const size = {
        type: 'resize',
        cols: terminal.cols,
        rows: terminal.rows
      }
      websocket.send(JSON.stringify(size))
    }
  }
}

const connect = () => {
  if (isConnecting) {
    return
  }

  isConnecting = true
  terminal.writeln(t('user.instanceDetail.sshConnecting'))

  // 获取token - 从 sessionStorage 获取（与 user store 保持一致）
  const token = sessionStorage.getItem('token')
  if (!token) {
    terminal.writeln(`\x1b[31m${t('user.instanceDetail.sshAuthTokenNotFound')}\x1b[0m`)
    emit('error', 'Authentication token not found')
    isConnecting = false
    return
  }

  // 构建WebSocket URL
  // 在开发环境中，需要使用后端服务器的地址，而不是前端开发服务器的地址
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  let host = window.location.host
  
  // 开发环境：如果前端运行在 8080 端口，WebSocket 应该连接到后端的 8888 端口
  if (import.meta.env.MODE === 'development' && import.meta.env.VITE_SERVER_PORT) {
    const serverPort = import.meta.env.VITE_SERVER_PORT
    host = `${window.location.hostname}:${serverPort}`
  }
  
  // 根据是否为管理员模式和终端类型选择不同的API端点
  const endpoint = props.mode === 'exec' ? 'exec' : 'ssh'
  const apiPath = props.isAdmin 
    ? `/api/v1/admin/instances/${props.instanceId}/${endpoint}`
    : `/api/v1/user/instances/${props.instanceId}/${endpoint}`
  
  const wsUrl = `${protocol}//${host}${apiPath}?token=${token}`

  try {
    websocket = new WebSocket(wsUrl)
    // 设置为接收二进制数据作为 ArrayBuffer
    websocket.binaryType = 'arraybuffer'

    websocket.onopen = () => {
      isConnecting = false
      terminal.writeln(`\x1b[32m${t('user.instanceDetail.sshConnected')}\x1b[0m`)
      terminal.focus()
      
      // 发送初始终端大小
      const size = {
        type: 'resize',
        cols: terminal.cols,
        rows: terminal.rows
      }
      websocket.send(JSON.stringify(size))
      
      // 启动心跳保活机制 - 每30秒发送一次心跳
      startHeartbeat()
    }

    websocket.onmessage = (event) => {
      // 处理二进制数据
      if (event.data instanceof ArrayBuffer) {
        const uint8Array = new Uint8Array(event.data)
        terminal.write(uint8Array)
      } else {
        // 处理文本数据（向后兼容）
        terminal.write(event.data)
      }
    }

    websocket.onerror = (error) => {
      console.error('WebSocket错误:', error)
      terminal.writeln(`\x1b[31m${t('user.instanceDetail.sshWebSocketError')}\x1b[0m`)
      ElMessage.error(t('user.instanceDetail.sshConnectionError'))
      emit('error', error)
      isConnecting = false
    }

    websocket.onclose = (event) => {
      isConnecting = false
      stopHeartbeat()
      
      // 1000 = Normal Closure (主动关闭)，不尝试重连
      if (event.code === 1000 || isIntentionallyClosed) {
        if (terminal) {
          terminal.writeln(`\x1b[32m${t('user.instanceDetail.sshClosedNormally')}\x1b[0m`)
        }
        return
      }
      
      if (terminal) {
        terminal.writeln(`\x1b[33m${t('user.instanceDetail.sshDisconnected')}\x1b[0m`)
      }
      ElMessage.warning(t('user.instanceDetail.sshConnectionClosed'))
      
      // 尝试自动重连
      if (!isIntentionallyClosed && terminal) {
        terminal.writeln(`\x1b[33m${t('user.instanceDetail.sshReconnecting')}\x1b[0m`)
        reconnectTimeout = setTimeout(() => {
          reconnect()
        }, 3000)
      }
    }
  } catch (error) {
    console.error('创建WebSocket连接失败:', error)
    terminal.writeln(`\x1b[31m${t('user.instanceDetail.sshWebSocketCreateFailed')}\x1b[0m`)
    ElMessage.error(t('user.instanceDetail.sshCreateFailed'))
    emit('error', error)
    isConnecting = false
  }
}

// 启动心跳保活
const startHeartbeat = () => {
  stopHeartbeat()
  heartbeatInterval = setInterval(() => {
    if (websocket && websocket.readyState === WebSocket.OPEN) {
      try {
        // 发送心跳包 - 使用空字节作为心跳信号
        websocket.send(JSON.stringify({ type: 'ping' }))
      } catch (error) {
        console.error('发送心跳失败:', error)
      }
    }
  }, 30000) // 每30秒发送一次心跳
}

// 停止心跳
const stopHeartbeat = () => {
  if (heartbeatInterval) {
    clearInterval(heartbeatInterval)
    heartbeatInterval = null
  }
  if (reconnectTimeout) {
    clearTimeout(reconnectTimeout)
    reconnectTimeout = null
  }
}

const cleanup = () => {
  isIntentionallyClosed = true
  stopHeartbeat()
  window.removeEventListener('resize', handleResize)
  
  if (websocket) {
    const ws = websocket
    websocket = null
    // 使用 1000 (Normal Closure) 通知后端正常关闭
    try { ws.close(1000, 'User closed terminal') } catch {}
  }
  
  if (terminal) {
    try { terminal.dispose() } catch {}
    terminal = null
  }
  
  if (fitAddon) {
    try { fitAddon.dispose() } catch {}
    fitAddon = null
  }
}

const reconnect = () => {
  if (isIntentionallyClosed) return
  stopHeartbeat()
  
  if (websocket) {
    const ws = websocket
    websocket = null
    try { ws.close() } catch {}
  }
  
  // 清空终端内容并重新初始化
  if (terminal) {
    terminal.clear()
  } else {
    initTerminal()
  }
  
  connect()
}

// 暴露方法给父组件
defineExpose({
  cleanup,
  reconnect
})
</script>

<style scoped>
.ssh-terminal-container {
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

:deep(.xterm-viewport) {
  overflow-y: auto;
}

:deep(.xterm-screen) {
  height: 100% !important;
}
</style>
