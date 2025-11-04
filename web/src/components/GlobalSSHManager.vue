<template>
  <Teleport to="body">
    <!-- 所有SSH终端连接（包括显示和最小化的） -->
    <div 
      v-for="conn in allConnections" 
      :key="conn.instanceId"
    >
      <!-- SSH对话框 -->
      <el-dialog
        v-model="conn.visible"
        :title="`SSH Terminal - ${conn.instanceName}`"
        width="80%"
        :before-close="() => minimizeConnection(conn.instanceId)"
        :destroy-on-close="false"
        :append-to-body="true"
        :close-on-click-modal="false"
        class="ssh-terminal-dialog"
      >
        <template #header>
          <div class="ssh-dialog-header">
            <span class="ssh-dialog-title">SSH Terminal - {{ conn.instanceName }}</span>
            <div class="ssh-dialog-actions">
              <el-button 
                :icon="Minus"
                size="small" 
                @click="minimizeConnection(conn.instanceId)"
                title="Minimize"
              >
                Minimize
              </el-button>
              <el-button 
                :icon="Refresh"
                size="small" 
                @click="reconnectSSH(conn.instanceId)"
                title="Reconnect"
              >
                Reconnect
              </el-button>
              <el-button 
                :icon="FullScreen"
                size="small" 
                @click="openSSHInNewWindow(conn)"
                title="Open in New Window"
              >
                New Window
              </el-button>
            </div>
          </div>
        </template>
        <div class="ssh-dialog-content">
          <SSHTerminal
            :ref="el => setTerminalRef(conn.instanceId, el)"
            :instance-id="conn.instanceId"
            :instance-name="conn.instanceName"
            @close="() => closeConnection(conn.instanceId)"
            @error="(error) => handleSSHError(conn.instanceId, error)"
          />
        </div>
      </el-dialog>
    </div>

    <!-- 所有最小化的SSH连接悬浮窗 -->
    <div 
      v-for="(conn, index) in minimizedConnections" 
      :key="`min-${conn.instanceId}`"
      class="ssh-minimized-container"
      :style="{ bottom: `${20 + index * 60}px` }"
    >
      <div class="ssh-minimized-header" @click="restoreConnection(conn.instanceId)">
        <span>SSH Terminal - {{ conn.instanceName }}</span>
        <el-button 
          :icon="Close"
          text
          size="small" 
          @click.stop="closeConnection(conn.instanceId)"
          class="close-btn"
        />
      </div>
    </div>
  </Teleport>
</template>

<script setup>
import { computed, ref } from 'vue'
import { Close, Minus, Refresh, FullScreen } from '@element-plus/icons-vue'
import { useSSHStore } from '@/pinia/modules/ssh'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import SSHTerminal from '@/components/SSHTerminal.vue'

const sshStore = useSSHStore()
const router = useRouter()

// 存储所有终端组件的引用
const terminalRefs = ref({})

const allConnections = computed(() => {
  return Object.entries(sshStore.connections).map(([instanceId, conn]) => ({
    instanceId,
    ...conn
  }))
})

const minimizedConnections = computed(() => sshStore.minimizedConnections)

const setTerminalRef = (instanceId, el) => {
  if (el) {
    terminalRefs.value[instanceId] = el
  }
}

const restoreConnection = (instanceId) => {
  sshStore.showConnection(instanceId)
}

const minimizeConnection = (instanceId) => {
  sshStore.minimizeConnection(instanceId)
}

const closeConnection = (instanceId) => {
  // 清理终端连接
  const terminal = terminalRefs.value[instanceId]
  if (terminal && terminal.cleanup) {
    terminal.cleanup()
  }
  delete terminalRefs.value[instanceId]
  sshStore.closeConnection(instanceId)
}

const reconnectSSH = (instanceId) => {
  const terminal = terminalRefs.value[instanceId]
  if (terminal && terminal.reconnect) {
    terminal.reconnect()
  } else {
    ElMessage.warning('Terminal not ready')
  }
}

const handleSSHError = (instanceId, error) => {
  console.error(`SSH连接错误 (${instanceId}):`, error)
  ElMessage.error('SSH connection failed')
}

const openSSHInNewWindow = (conn) => {
  const token = sessionStorage.getItem('token')
  
  if (!token) {
    ElMessage.error('Authentication token not found')
    return
  }
  
  // 构建WebSocket URL
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  let wsHost = window.location.host
  
  // 开发环境处理
  if (import.meta.env.MODE === 'development' && import.meta.env.VITE_SERVER_PORT) {
    const serverPort = import.meta.env.VITE_SERVER_PORT
    wsHost = `${window.location.hostname}:${serverPort}`
  }
  
  const wsUrl = `${protocol}//${wsHost}/api/v1/user/instances/${conn.instanceId}/ssh?token=${encodeURIComponent(token)}`
  
  // 创建新窗口HTML内容
  const htmlContent = `<!DOCTYPE html>
<html>
<head>
  <title>SSH Terminal - ${conn.instanceName}</title>
  <meta charset="UTF-8">
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { 
      background-color: #1e1e1e; 
      font-family: Arial, sans-serif;
      overflow: hidden;
      display: flex;
      flex-direction: column;
      height: 100vh;
    }
    .header {
      background-color: #ffffff;
      color: #000000;
      padding: 12px 20px;
      font-size: 14px;
      font-weight: 500;
      border-bottom: 1px solid #e0e0e0;
      box-shadow: 0 1px 4px rgba(0,0,0,0.1);
    }
    .terminal-container {
      flex: 1;
      padding: 10px;
      overflow: hidden;
    }
    #terminal {
      width: 100%;
      height: 100%;
    }
  </style>
  <link rel="stylesheet" href="https://unpkg.com/xterm@5.3.0/css/xterm.css">
</head>
<body>
  <div class="header">SSH Terminal - ${conn.instanceName}</div>
  <div class="terminal-container">
    <div id="terminal"></div>
  </div>
  <script src="https://unpkg.com/xterm@5.3.0/lib/xterm.js"><\/script>
  <script src="https://unpkg.com/xterm-addon-fit@0.8.0/lib/xterm-addon-fit.js"><\/script>
  <script>
    (function() {
      const terminal = new window.Terminal({
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
        }
      });
      
      const fitAddon = new window.FitAddon.FitAddon();
      terminal.loadAddon(fitAddon);
      terminal.open(document.getElementById('terminal'));
      
      setTimeout(function() { 
        fitAddon.fit(); 
        terminal.focus();
      }, 100);
      
      window.addEventListener('resize', function() { 
        fitAddon.fit(); 
      });
      
      terminal.writeln('Connecting to SSH server...');
      
      const websocket = new WebSocket('${wsUrl}');
      
      websocket.onopen = function() {
        terminal.writeln('\\x1b[32mConnected to SSH server\\x1b[0m');
        terminal.focus();
        websocket.send(JSON.stringify({
          type: 'resize',
          cols: terminal.cols,
          rows: terminal.rows
        }));
      };
      
      websocket.onmessage = function(event) {
        terminal.write(event.data);
      };
      
      websocket.onerror = function() {
        terminal.writeln('\\x1b[31mWebSocket connection error\\x1b[0m');
      };
      
      websocket.onclose = function(event) {
        if (event.code !== 1000) {
          terminal.writeln('\\x1b[33mSSH connection closed\\x1b[0m');
        } else {
          terminal.writeln('\\x1b[32mSSH connection closed normally\\x1b[0m');
        }
      };
      
      terminal.onData(function(data) {
        if (websocket.readyState === WebSocket.OPEN) {
          websocket.send(data);
        }
      });
      
      window.addEventListener('beforeunload', function() {
        websocket.close();
      });
    })();
  <\/script>
</body>
</html>`
  
  const width = 1000
  const height = 700
  const left = Math.max(0, (screen.width - width) / 2)
  const top = Math.max(0, (screen.height - height) / 2)
  
  const newWindow = window.open(
    'about:blank',
    `ssh-terminal-${conn.instanceId}`,
    `width=${width},height=${height},left=${left},top=${top},resizable=yes,scrollbars=no,menubar=no,toolbar=no,location=no,status=no`
  )
  
  if (newWindow) {
    newWindow.document.open()
    newWindow.document.write(htmlContent)
    newWindow.document.close()
  } else {
    ElMessage.error('Unable to open new window, please check browser popup settings')
  }
}
</script>

<style scoped>
/* SSH终端对话框样式 */
.ssh-terminal-dialog :deep(.el-dialog__header) {
  padding: 0;
  margin: 0;
  border-bottom: 1px solid #e0e0e0;
}

.ssh-dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 20px;
  background-color: #ffffff;
}

.ssh-dialog-title {
  color: #000000;
  font-size: 15px;
  font-weight: 600;
}

.ssh-dialog-actions {
  display: flex;
  gap: 10px;
}

.ssh-dialog-actions .el-button {
  background-color: #ffffff;
  color: #000000;
  border: 1px solid #d0d0d0;
  font-weight: 500;
}

.ssh-dialog-actions .el-button:hover {
  background-color: #f5f5f5;
  border-color: #b0b0b0;
}

.ssh-dialog-content {
  height: 600px;
  background-color: #1e1e1e;
  border-radius: 4px;
  overflow: hidden;
}

.ssh-terminal-dialog :deep(.el-dialog__body) {
  padding: 0;
}

.ssh-terminal-dialog :deep(.el-dialog) {
  border-radius: 8px;
}

/* 最小化SSH终端样式 - 右下角悬浮 */
.ssh-minimized-container {
  position: fixed;
  right: 20px;
  z-index: 9999;
  background-color: #ffffff;
  border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15);
  cursor: pointer;
  transition: all 0.3s ease;
  border: 1px solid #e0e0e0;
}

.ssh-minimized-container:hover {
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.2);
  transform: translateY(-2px);
}

.ssh-minimized-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  color: #000000;
  font-size: 14px;
  font-weight: 600;
  min-width: 280px;
  background-color: #ffffff;
  border-radius: 8px;
}

.ssh-minimized-header span {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin-right: 10px;
}

.ssh-minimized-header .close-btn {
  color: #666666;
  padding: 4px;
}

.ssh-minimized-header .close-btn:hover {
  color: #000000;
  background-color: #f0f0f0;
}
</style>
