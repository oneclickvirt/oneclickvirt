<template>
  <Teleport to="body">
    <!-- 所有SSH终端连接（包括显示和最小化的） -->
    <div 
      v-for="conn in allConnections" 
      :key="conn.connectionKey"
    >
      <!-- SSH对话框 -->
      <el-dialog
        v-model="conn.visible"
        :title="t('user.instanceDetail.sshTerminalTitle', { name: conn.instanceName })"
        width="80%"
        :before-close="() => closeConnection(conn.connectionKey)"
        :destroy-on-close="false"
        :append-to-body="true"
        :close-on-click-modal="false"
        class="ssh-terminal-dialog"
      >
        <template #header>
          <div class="ssh-dialog-header">
            <RemoteTerminalToolbar
              :title="t('user.instanceDetail.sshTerminalTitle', { name: conn.instanceName })"
              :active-view="conn.activeView || 'terminal'"
              :supports-sftp="conn.mode !== 'exec'"
              :show-view-switch="conn.mode !== 'exec'"
              :actions="getToolbarActions(conn)"
              @update:active-view="(view) => setConnectionView(conn.connectionKey, view)"
              @action="(action) => handleToolbarAction(action, conn)"
            />
          </div>
        </template>
        <div class="ssh-dialog-content">
          <div
            v-show="conn.mode === 'exec' || (conn.activeView || 'terminal') === 'terminal'"
            class="terminal-panel"
          >
            <SSHTerminal
            :ref="el => setTerminalRef(conn.connectionKey, el)"
            :instance-id="conn.instanceId"
            :instance-name="conn.instanceName"
            :is-admin="conn.isAdmin || false"
            :mode="conn.mode || 'ssh'"
            @close="() => closeConnection(conn.connectionKey)"
            @error="(error) => handleSSHError(conn.connectionKey, error)"
            />
          </div>

          <div
            v-if="conn.mode !== 'exec' && (conn.activeView || 'terminal') === 'sftp'"
            class="sftp-panel"
          >
            <SFTPPanel
              :ref="el => setSftpRef(conn.connectionKey, el)"
              :entity-type="conn.isAdmin ? 'admin-instance' : 'user-instance'"
              :entity-id="conn.instanceId"
              :active="(conn.activeView || 'terminal') === 'sftp'"
            />
          </div>
        </div>
      </el-dialog>
    </div>

    <!-- 所有最小化的SSH连接悬浮窗 -->
    <div 
      v-for="(conn, index) in minimizedConnections" 
      :key="`min-${conn.connectionKey}`"
      class="ssh-minimized-container"
      :style="{ bottom: `${20 + index * 60}px` }"
    >
      <div
        class="ssh-minimized-header"
        @click="restoreConnection(conn.connectionKey)"
      >
        <span>{{ t('user.instanceDetail.sshTerminalTitle', { name: conn.instanceName }) }}</span>
        <el-button 
          :icon="Close"
          text
          size="small" 
          class="close-btn"
          @click.stop="closeConnection(conn.connectionKey)"
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
import { useI18n } from 'vue-i18n'
import SSHTerminal from '@/components/SSHTerminal.vue'
import SFTPPanel from '@/components/SFTPPanel.vue'
import RemoteTerminalToolbar from '@/components/RemoteTerminalToolbar.vue'

const { t } = useI18n()

const sshStore = useSSHStore()
const router = useRouter()

// 存储所有终端组件的引用
const terminalRefs = ref({})
const sftpRefs = ref({})

const allConnections = computed(() => {
  return Object.entries(sshStore.connections).map(([connectionKey, conn]) => ({
    connectionKey,
    ...conn
  }))
})

const minimizedConnections = computed(() => sshStore.minimizedConnections)

const setTerminalRef = (instanceId, el) => {
  if (el) {
    terminalRefs.value[instanceId] = el
  } else {
    delete terminalRefs.value[instanceId]
  }
}

const setSftpRef = (instanceId, el) => {
  if (el) {
    sftpRefs.value[instanceId] = el
  } else {
    delete sftpRefs.value[instanceId]
  }
}

const setConnectionView = (connectionKey, view) => {
  sshStore.setActiveView(connectionKey, view)
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
  delete sftpRefs.value[instanceId]
  sshStore.closeConnection(instanceId)
}

const reconnectSSH = async (instanceId) => {
  const terminal = terminalRefs.value[instanceId]
  const sftpPanel = sftpRefs.value[instanceId]

  if (terminal && terminal.reconnect) {
    terminal.reconnect()
  } else {
    ElMessage.warning(t('user.instanceDetail.sshTerminalNotReady'))
    return
  }

  if (sftpPanel && sftpPanel.refreshNow) {
    try {
      await sftpPanel.refreshNow(true)
    } catch (error) {
      console.warn('SFTP refresh after reconnect failed:', error)
    }
  }
}

const getToolbarActions = (conn) => {
  const actions = [
    {
      key: 'minimize',
      label: t('user.instanceDetail.sshMinimize'),
      title: t('user.instanceDetail.sshMinimize'),
      icon: Minus
    },
    {
      key: 'reconnect',
      label: t('user.instanceDetail.sshReconnect'),
      title: t('user.instanceDetail.sshReconnect'),
      icon: Refresh
    }
  ]

  if (conn.mode !== 'exec') {
    actions.push({
      key: 'newWindow',
      label: t('user.instanceDetail.sshNewWindow'),
      title: t('user.instanceDetail.sshOpenInNewWindow'),
      icon: FullScreen
    })
  }

  return actions
}

const handleToolbarAction = (action, conn) => {
  if (action === 'minimize') {
    minimizeConnection(conn.connectionKey)
    return
  }
  if (action === 'reconnect') {
    reconnectSSH(conn.connectionKey)
    return
  }
  if (action === 'newWindow') {
    openSSHInNewWindow(conn)
  }
}

const handleSSHError = (instanceId, error) => {
  console.error(`SSH连接错误 (${instanceId}):`, error)
  ElMessage.error(t('user.instanceDetail.sshConnectFailed'))
}

const openSSHInNewWindow = (conn) => {
  const token = sessionStorage.getItem('token')
  
  if (!token) {
    ElMessage.error(t('errors.unauthorized'))
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
  
  const endpoint = conn.mode === 'exec' ? 'exec' : 'ssh'
  const rolePrefix = conn.isAdmin ? 'admin' : 'user'
  const wsUrl = `${protocol}//${wsHost}/api/v1/${rolePrefix}/instances/${conn.instanceId}/${endpoint}?token=${encodeURIComponent(token)}`
  
  const escapeHtml = (str) => str.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;')
  const sshTitle = escapeHtml(t('user.instanceDetail.sshTerminalTitle', { name: conn.instanceName }))
  const sshReconnectLabel = escapeHtml(t('user.instanceDetail.sshReconnect'))
  const sshCloseLabel = escapeHtml(t('user.instanceDetail.sshClose'))
  const sshConnectingMsg = escapeHtml(t('user.instanceDetail.sshConnecting'))
  const sshConnectedMsg = escapeHtml(t('user.instanceDetail.sshConnected'))
  const sshWebSocketErrorMsg = escapeHtml(t('user.instanceDetail.sshWebSocketError'))
  const sshDisconnectedMsg = escapeHtml(t('user.instanceDetail.sshDisconnected'))
  const sshReconnectingMsg = escapeHtml(t('user.instanceDetail.sshReconnecting'))
  const sshClosedNormallyMsg = escapeHtml(t('user.instanceDetail.sshClosedNormally'))
  const isDarkTheme = document.documentElement.classList.contains('dark')
  const popupTerminalBg = isDarkTheme ? '#0b1220' : '#f3f6fb'
  const popupHeaderBg = isDarkTheme ? '#162032' : '#ffffff'
  const popupHeaderText = isDarkTheme ? '#e2e8f0' : '#1f2937'
  const popupHeaderBorder = isDarkTheme ? 'rgba(22, 163, 74, 0.2)' : '#e0e0e0'
  const popupReconnectBg = '#16a34a'
  const popupReconnectHover = '#15803d'
  const popupCloseBg = isDarkTheme ? '#334155' : '#f56c6c'
  const popupCloseHover = isDarkTheme ? '#475569' : '#f78989'
  
  // 创建新窗口HTML内容
  const htmlContent = `<!DOCTYPE html>
<html>
<head>
  <title>${sshTitle}</title>
  <meta charset="UTF-8">
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { 
      background-color: ${popupTerminalBg}; 
      font-family: Arial, sans-serif;
      overflow: hidden;
      display: flex;
      flex-direction: column;
      height: 100vh;
    }
    .header {
      background-color: ${popupHeaderBg};
      color: ${popupHeaderText};
      padding: 12px 20px;
      font-size: 14px;
      font-weight: 500;
      border-bottom: 1px solid ${popupHeaderBorder};
      box-shadow: 0 1px 4px rgba(0,0,0,0.1);
      display: flex;
      justify-content: space-between;
      align-items: center;
    }
    .header-title {
      flex: 1;
    }
    .header-buttons {
      display: flex;
      gap: 8px;
    }
    .btn {
      padding: 6px 12px;
      border: none;
      border-radius: 4px;
      cursor: pointer;
      font-size: 12px;
      font-weight: 500;
      transition: all 0.2s;
    }
    .btn-reconnect {
      background-color: ${popupReconnectBg};
      color: white;
    }
    .btn-reconnect:hover {
      background-color: ${popupReconnectHover};
    }
    .btn-close {
      background-color: ${popupCloseBg};
      color: white;
    }
    .btn-close:hover {
      background-color: ${popupCloseHover};
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
  <div class="header">
    <div class="header-title">${sshTitle}</div>
    <div class="header-buttons">
      <button class="btn btn-reconnect" onclick="reconnectSSH()">${sshReconnectLabel}</button>
      <button class="btn btn-close" onclick="window.close()">${sshCloseLabel}</button>
    </div>
  </div>
  <div class="terminal-container">
    <div id="terminal"></div>
  </div>
  <script src="https://unpkg.com/xterm@5.3.0/lib/xterm.js"></${'script'}>
  <script src="https://unpkg.com/xterm-addon-fit@0.8.0/lib/xterm-addon-fit.js"></${'script'}>
  <script>
    (function() {
      let websocket = null;
      let heartbeatInterval = null;
      let reconnectTimeout = null;
      let isIntentionallyClosed = false;
      
      const terminal = new window.Terminal({
        cursorBlink: true,
        fontSize: 14,
        fontFamily: 'Monaco, Menlo, "Courier New", monospace',
        theme: {
          background: '${popupTerminalBg}',
          foreground: '${isDarkTheme ? '#d4d4d4' : '#1f2937'}',
          cursor: '${isDarkTheme ? '#d4d4d4' : '#16a34a'}',
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
        convertEol: false
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
      
      // 启动心跳保活
      function startHeartbeat() {
        stopHeartbeat();
        heartbeatInterval = setInterval(function() {
          if (websocket && websocket.readyState === WebSocket.OPEN) {
            try {
              websocket.send(JSON.stringify({ type: 'ping' }));
            } catch (error) {
              console.error('发送心跳失败:', error);
            }
          }
        }, 30000); // 每30秒发送一次心跳
      }
      
      // 停止心跳
      function stopHeartbeat() {
        if (heartbeatInterval) {
          clearInterval(heartbeatInterval);
          heartbeatInterval = null;
        }
        if (reconnectTimeout) {
          clearTimeout(reconnectTimeout);
          reconnectTimeout = null;
        }
      }
      
      // 连接WebSocket
      function connectWebSocket() {
        terminal.writeln('${sshConnectingMsg}');
        
        websocket = new WebSocket('${wsUrl}');
        websocket.binaryType = 'arraybuffer';
        
        websocket.onopen = function() {
          terminal.writeln('\x1b[32m${sshConnectedMsg}\x1b[0m');
          terminal.focus();
          websocket.send(JSON.stringify({
            type: 'resize',
            cols: terminal.cols,
            rows: terminal.rows
          }));
          startHeartbeat();
        };
        
        websocket.onmessage = function(event) {
          if (event.data instanceof ArrayBuffer) {
            const uint8Array = new Uint8Array(event.data);
            terminal.write(uint8Array);
          } else {
            terminal.write(event.data);
          }
        };
        
        websocket.onerror = function() {
          terminal.writeln('\x1b[31m${sshWebSocketErrorMsg}\x1b[0m');
        };
        
        websocket.onclose = function(event) {
          stopHeartbeat();
          if (event.code !== 1000) {
            terminal.writeln('\x1b[33m${sshDisconnectedMsg}\x1b[0m');
            
            // 如果不是主动关闭，尝试自动重连
            if (!isIntentionallyClosed) {
              terminal.writeln('\x1b[33m${sshReconnectingMsg}\x1b[0m');
              reconnectTimeout = setTimeout(function() {
                reconnectSSH();
              }, 3000);
            }
          } else {
            terminal.writeln('\x1b[32m${sshClosedNormallyMsg}\x1b[0m');
          }
        };
        
        terminal.onData(function(data) {
          if (websocket && websocket.readyState === WebSocket.OPEN) {
            websocket.send(data);
          }
        });
      }
      
      // 重连函数
      window.reconnectSSH = function() {
        isIntentionallyClosed = false;
        stopHeartbeat();
        
        if (websocket) {
          websocket.close();
          websocket = null;
        }
        
        terminal.clear();
        connectWebSocket();
      };
      
      // 初始连接
      connectWebSocket();
      
      window.addEventListener('beforeunload', function() {
        isIntentionallyClosed = true;
        stopHeartbeat();
        if (websocket) {
          websocket.close();
        }
      });
    })();
  </${'script'}>
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
    ElMessage.error(t('common.popupBlocked'))
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
  padding: 12px 20px;
  background-color: #ffffff;
}

.ssh-dialog-content {
  height: 600px;
  background-color: var(--terminal-bg);
  border-radius: 4px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.terminal-panel,
.sftp-panel {
  flex: 1;
  min-height: 0;
}

.sftp-panel {
  background-color: var(--el-bg-color-overlay);
  padding: 10px;
  overflow: auto;
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
