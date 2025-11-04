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
  }
})

const emit = defineEmits(['close', 'error'])

const terminalRef = ref(null)
let terminal = null
let fitAddon = null
let websocket = null
let isConnecting = false

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
    rows: 30,
    cols: 100
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
  terminal.writeln('Connecting to SSH server...')

  // 获取token - 从 sessionStorage 获取（与 user store 保持一致）
  const token = sessionStorage.getItem('token')
  if (!token) {
    terminal.writeln('\x1b[31mError: Authentication token not found\x1b[0m')
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
  
  // 根据是否为管理员模式选择不同的API端点
  const apiPath = props.isAdmin 
    ? `/api/v1/admin/instances/${props.instanceId}/ssh`
    : `/api/v1/user/instances/${props.instanceId}/ssh`
  
  const wsUrl = `${protocol}//${host}${apiPath}?token=${token}`

  try {
    websocket = new WebSocket(wsUrl)

    websocket.onopen = () => {
      isConnecting = false
      terminal.writeln('\x1b[32mConnected to SSH server\x1b[0m')
      terminal.focus()
      
      // 发送初始终端大小
      const size = {
        type: 'resize',
        cols: terminal.cols,
        rows: terminal.rows
      }
      websocket.send(JSON.stringify(size))
    }

    websocket.onmessage = (event) => {
      terminal.write(event.data)
    }

    websocket.onerror = (error) => {
      console.error('WebSocket错误:', error)
      terminal.writeln('\x1b[31mWebSocket connection error\x1b[0m')
      ElMessage.error('SSH连接出错')
      emit('error', error)
      isConnecting = false
    }

    websocket.onclose = (event) => {
      isConnecting = false
      if (event.code !== 1000) {
        terminal.writeln('\x1b[33mSSH connection closed\x1b[0m')
        ElMessage.warning('SSH连接已断开')
      } else {
        terminal.writeln('\x1b[32mSSH connection closed normally\x1b[0m')
      }
    }
  } catch (error) {
    console.error('创建WebSocket连接失败:', error)
    terminal.writeln('\x1b[31mFailed to create WebSocket connection\x1b[0m')
    ElMessage.error('无法创建SSH连接')
    emit('error', error)
    isConnecting = false
  }
}

const cleanup = () => {
  window.removeEventListener('resize', handleResize)
  
  if (websocket) {
    websocket.close()
    websocket = null
  }
  
  if (terminal) {
    terminal.dispose()
    terminal = null
  }
  
  if (fitAddon) {
    fitAddon.dispose()
    fitAddon = null
  }
}

const reconnect = () => {
  cleanup()
  nextTick(() => {
    initTerminal()
    connect()
  })
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
