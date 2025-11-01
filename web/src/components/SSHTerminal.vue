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
  terminal.writeln('\r\n正在连接SSH服务器...\r\n')

  // 获取token
  const token = localStorage.getItem('token')
  if (!token) {
    terminal.writeln('\r\n\x1b[31m错误: 未找到认证令牌\x1b[0m\r\n')
    emit('error', '未找到认证令牌')
    isConnecting = false
    return
  }

  // 构建WebSocket URL
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const host = window.location.host
  const wsUrl = `${protocol}//${host}/api/v1/user/instances/${props.instanceId}/ssh?token=${token}`

  try {
    websocket = new WebSocket(wsUrl)

    websocket.onopen = () => {
      isConnecting = false
      terminal.writeln('\r\n\x1b[32m已连接到SSH服务器\x1b[0m\r\n')
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
      terminal.writeln('\r\n\x1b[31mWebSocket连接错误\x1b[0m\r\n')
      ElMessage.error('SSH连接出错')
      emit('error', error)
      isConnecting = false
    }

    websocket.onclose = (event) => {
      isConnecting = false
      if (event.code !== 1000) {
        terminal.writeln('\r\n\x1b[33mSSH连接已断开\x1b[0m\r\n')
        ElMessage.warning('SSH连接已断开')
      } else {
        terminal.writeln('\r\n\x1b[32mSSH连接正常关闭\x1b[0m\r\n')
      }
    }
  } catch (error) {
    console.error('创建WebSocket连接失败:', error)
    terminal.writeln('\r\n\x1b[31m无法创建WebSocket连接\x1b[0m\r\n')
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

// 暴露方法给父组件
defineExpose({
  cleanup
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
