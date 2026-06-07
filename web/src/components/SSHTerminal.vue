<template>
  <div class="ssh-terminal-container">
    <div 
      ref="terminalRef" 
      class="terminal"
      @contextmenu="handleContextMenu"
      @mousedown="handleMouseDown"
    />
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
import { ref, onMounted, onBeforeUnmount, nextTick, computed, reactive } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import '@xterm/xterm/css/xterm.css'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { applyTerminalTheme } from '@/utils/terminalTheme'

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
  },
  shareToken: {
    type: String,
    default: ''
  }
})

const emit = defineEmits(['close', 'error'])

const terminalRef = ref(null)
let terminal = null
let fitAddon = null
let webLinksAddon = null
let unicode11Addon = null
let websocket = null
let isConnecting = false
let heartbeatInterval = null
let reconnectTimeout = null
let isIntentionallyClosed = false
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
  // 中键粘贴（Unix 终端标准行为）
  if (event.button === 1) {
    event.preventDefault()
    pasteFromClipboard()
  }
  // 点击其他地方关闭右键菜单
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

// 全局点击关闭菜单
const onGlobalClick = () => {
  hideContextMenu()
}

const ignoreNonCriticalTerminalError = (error) => {
  void error
}

onMounted(() => {
  document.addEventListener('click', onGlobalClick)
  nextTick(() => {
    initTerminal()
    connect()
  })
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
      // 降级方案：使用 textarea
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
    // 降级方案不可用于粘贴
    console.error('粘贴失败:', error)
  }
}

const initTerminal = () => {
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
    allowMouseReporting: true,        // 允许 vim/htop/tmux 等程序接收鼠标事件
    rightClickSelectsWord: false,      // 右键不选词，留给自定义上下文菜单
    // ── Mac 键盘支持 ────────────────────────────────────────────
    macOptionIsMeta: true,             // Mac Option 键作为 Meta/Alt 键
    macOptionClickForcesSelection: true // Mac Option+点击强制选择（不发送到终端）
  })

  fitAddon = new FitAddon()
  terminal.loadAddon(fitAddon)

  // Web 链接识别（Ctrl+点击打开链接）
  webLinksAddon = new WebLinksAddon((event, uri) => {
    event.preventDefault()
    window.open(uri, '_blank', 'noopener,noreferrer')
  })
  terminal.loadAddon(webLinksAddon)

  // Unicode 11 支持（更宽的字符、Emoji 等）
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

    // Ctrl+Shift+C / Cmd+Shift+C → 复制（终端标准快捷键）
    if (ctrlOrCmd && shift && event.code === 'KeyC') {
      copySelectionToClipboard()
      return false
    }

    // Cmd+C (Mac) 有选区时 → 复制；无选区时交给终端
    if (isMac.value && event.metaKey && !shift && event.code === 'KeyC') {
      if (hasSelection) {
        copySelectionToClipboard()
        return false
      }
      return true
    }

    // Ctrl+Insert → 复制（经典终端快捷键）
    if (event.ctrlKey && event.code === 'Insert') {
      copySelectionToClipboard()
      return false
    }

    // Shift+Insert → 粘贴（经典终端快捷键）
    if (event.shiftKey && event.code === 'Insert') {
      pasteFromClipboard()
      return false
    }

    // Ctrl+Shift+V / Cmd+Shift+V → 粘贴
    if (ctrlOrCmd && shift && event.code === 'KeyV') {
      pasteFromClipboard()
      return false
    }

    return true
  })

  // ── 选区变化监听 ──────────────────────────────────────────────
  terminal.onSelectionChange(() => {
    // 可以在此做选区变化时的 UI 更新（如显示复制按钮等）
  })

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
  const token = props.shareToken ? '' : sessionStorage.getItem('token')
  if (!props.shareToken && !token) {
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
  const apiPath = props.shareToken
    ? `/api/v1/public/instance-shares/${encodeURIComponent(props.shareToken)}/${endpoint}`
    : (props.isAdmin
      ? `/api/v1/admin/instances/${props.instanceId}/${endpoint}`
      : `/api/v1/user/instances/${props.instanceId}/${endpoint}`)
  
  const wsUrl = props.shareToken
    ? `${protocol}//${host}${apiPath}`
    : `${protocol}//${host}${apiPath}?token=${encodeURIComponent(token)}`

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
  stopThemeSync()
  window.removeEventListener('resize', handleResize)
  
  if (websocket) {
    const ws = websocket
    websocket = null
    try { ws.close(1000, 'User closed terminal') } catch (error) { ignoreNonCriticalTerminalError(error) }
  }
  
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

const reconnect = () => {
  if (isIntentionallyClosed) return
  stopHeartbeat()
  
  if (websocket) {
    const ws = websocket
    websocket = null
    try { ws.close() } catch (error) { ignoreNonCriticalTerminalError(error) }
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
  background-color: var(--terminal-bg);
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
  /* 确保文本可选 */
  -webkit-user-select: text;
  user-select: text;
}

:deep(.xterm-screen) {
  height: 100% !important;
}

:deep(.xterm-selection) {
  /* 确保选区层在字符之上但允许鼠标事件穿透 */
  pointer-events: none;
}

:deep(.xterm-helpers) {
  /* 确保 xterm 的 textarea 辅助元素不会干扰选择 */
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
