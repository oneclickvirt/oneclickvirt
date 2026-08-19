<template>
  <el-dialog
    :model-value="modelValue"
    :title="title"
    width="900px"
    destroy-on-close
    class="vnc-dialog"
    @update:model-value="emit('update:modelValue', $event)"
    @closed="disconnect"
  >
    <div
      v-loading="loadingCapabilities"
      class="console-shell"
    >
      <div
        v-if="capabilities.length"
        class="console-protocols"
        role="tablist"
      >
        <el-button
          v-for="capability in capabilities"
          :key="capability.protocol"
          size="small"
          :type="selectedProtocol === capability.protocol ? 'primary' : 'default'"
          :disabled="!capability.available && !capability.repairable && !capability.nativeURL"
          @click="selectProtocol(capability.protocol)"
        >
          {{ protocolLabel(capability.protocol) }}
        </el-button>
      </div>

      <el-alert
        v-if="selectedCapability && (!selectedCapability.available || selectedCapability.protocol === 'unsupported')"
        :title="selectedCapability.protocol === 'unsupported' ? t('user.instanceDetail.consoleUnsupported') : t('user.instanceDetail.consoleNotReady')"
        type="warning"
        :closable="false"
        show-icon
      >
        <div class="console-error">
          <span>{{ selectedCapability.reason || t('user.instanceDetail.consoleUnknownError') }}</span>
          <el-button
            v-if="selectedCapability.repairable"
            size="small"
            type="primary"
            :loading="repairing"
            @click="repairConsole"
          >
            {{ repairing ? t('user.instanceDetail.consoleRepairing') : t('user.instanceDetail.consoleRepair') }}
          </el-button>
          <el-button
            v-if="selectedCapability.nativeURL"
            size="small"
            type="primary"
            @click="openNativeConsole"
          >
            {{ t('user.instanceDetail.consoleOpenNative') }}
          </el-button>
        </div>
      </el-alert>

      <div
        v-if="selectedProtocol === 'vnc' && selectedCapability?.available"
        class="vnc-shell"
      >
        <div class="vnc-status">
          <el-tag
            size="small"
            :type="statusType"
          >
            {{ statusText }}
          </el-tag>
          <el-tag
            v-if="status === 'connected'"
            size="small"
            :type="clipboardStatusType"
            :title="clipboardStatusText"
          >
            {{ clipboardStatusText }}
          </el-tag>
        </div>
        <VNCShortcutToolbar
          :connected="status === 'connected'"
          :clipboard-paste-available="clipboardPasteAvailable"
          :clipboard-paste-title="clipboardPasteTitle"
          :remote-clipboard-available="remoteClipboardText !== null"
          @shortcut="sendShortcut"
          @focus="focusScreen"
          @clipboard-paste="pasteToRemote"
          @clipboard-copy="copyRemoteClipboard"
        />
        <div
          ref="screenRef"
          v-loading="connecting"
          class="vnc-screen"
          @paste="handleNativePaste"
        />
      </div>

      <div
        v-else-if="selectedProtocol === 'spice' && selectedCapability?.available"
        class="spice-shell"
      >
        <div class="console-status-hint">
          {{ t('user.instanceDetail.consoleSpiceHint') }}
        </div>
        <iframe
          :key="spiceAssetUrl"
          class="spice-frame"
          :src="spiceAssetUrl"
          :title="t('user.instanceDetail.consoleSpice')"
          allow="clipboard-read; clipboard-write"
        />
      </div>

      <div
        v-else-if="selectedCapability?.available && selectedCapability.terminal"
        class="terminal-shell"
      >
        <ConsoleTerminal
          :instance-id="instanceId"
          :protocol="selectedProtocol"
        />
        <el-button
          v-if="selectedCapability.nativeURL"
          text
          @click="openNativeConsole"
        >
          {{ t('user.instanceDetail.consoleOpenNative') }}
        </el-button>
      </div>

      <div
        v-else-if="selectedCapability?.available && selectedProtocol !== 'vnc' && selectedProtocol !== 'spice'"
        class="native-console"
      >
        <p>{{ t('user.instanceDetail.consoleNativeHint') }}</p>
        <el-button
          type="primary"
          @click="openNativeConsole"
        >
          {{ t('user.instanceDetail.consoleOpenNative') }}
        </el-button>
      </div>
    </div>
  </el-dialog>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import RFB from '@novnc/novnc/lib/rfb.js'
import {
  getUserInstanceConsoleInfo,
  getUserInstanceConsoleSpiceAssetUrl,
  getUserInstanceConsoleWsUrl,
  repairUserInstanceConsole
} from '@/api/user'
import VNCShortcutToolbar from '@/components/VNCShortcutToolbar.vue'
import ConsoleTerminal from '@/components/ConsoleTerminal.vue'
import { sendVNCShortcut } from '@/utils/vncKeyboard'
import { copyToClipboard, readFromClipboard } from '@/utils/clipboard'
import { getTextFromVNCPasteEvent, getVNCClipboardMode } from '@/utils/vncClipboard'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  instanceId: { type: [Number, String], required: true },
  instanceName: { type: String, default: '' },
  initialProtocol: { type: String, default: '' }
})

const emit = defineEmits(['update:modelValue'])
const { t } = useI18n()

const screenRef = ref(null)
const rfb = ref(null)
const connecting = ref(false)
const loadingCapabilities = ref(false)
const repairing = ref(false)
const status = ref('idle')
const statusMessage = ref('')
const capabilities = ref([])
const selectedProtocol = ref('')
let autoRepairStarted = false
const remoteClipboardText = ref(null)
const clipboardMode = ref('unknown')
let clipboardCapabilityTimers = []

const hasBrowserClipboardRead = () => typeof navigator !== 'undefined'
  && !!navigator.clipboard
  && typeof navigator.clipboard.readText === 'function'
  && typeof window !== 'undefined'
  && !!window.isSecureContext

const clipboardPasteAvailable = computed(() => {
  const client = rfb.value
  return status.value === 'connected'
    && hasBrowserClipboardRead()
    && !!client
    && !client.viewOnly
    && typeof client.clipboardPasteFrom === 'function'
})

const clipboardStatusText = computed(() => {
  if (clipboardMode.value === 'readonly') return t('user.instanceDetail.vncClipboardReadOnly')
  if (clipboardMode.value === 'unsupported') return t('user.instanceDetail.vncClipboardUnsupported')
  if (!hasBrowserClipboardRead()) return t('user.instanceDetail.vncClipboardKeyboardPasteAvailable')
  if (clipboardMode.value === 'extended') return t('user.instanceDetail.vncClipboardModeExtended')
  if (clipboardMode.value === 'standard') return t('user.instanceDetail.vncClipboardModeStandard')
  return t('user.instanceDetail.vncClipboardModeUnknown')
})

const clipboardStatusType = computed(() => {
  if (clipboardMode.value === 'readonly' || clipboardMode.value === 'unsupported') return 'warning'
  if (clipboardMode.value === 'extended') return 'success'
  return 'info'
})

const clipboardPasteTitle = computed(() => clipboardPasteAvailable.value
  ? t('user.instanceDetail.vncClipboardPaste')
  : clipboardStatusText.value)

const title = computed(() => props.instanceName
  ? `${t('user.instanceDetail.webConsole')} - ${props.instanceName}`
  : t('user.instanceDetail.webConsole'))

const selectedCapability = computed(() => capabilities.value.find(item => item.protocol === selectedProtocol.value) || null)
const spiceAssetUrl = computed(() => getUserInstanceConsoleSpiceAssetUrl(props.instanceId))

const protocolLabel = protocol => {
  const labels = {
    vnc: t('user.instanceDetail.consoleVnc'),
    spice: t('user.instanceDetail.consoleSpice'),
    exec: t('user.instanceDetail.consoleExec'),
    attach: t('user.instanceDetail.consoleAttach'),
    namespace: t('user.instanceDetail.consoleNamespace'),
    'native-console': t('user.instanceDetail.consoleNative'),
    serial: t('user.instanceDetail.consoleSerial'),
    rdp: t('user.instanceDetail.consoleRdp'),
    'virtio-console': t('user.instanceDetail.consoleVirtio'),
    vsock: t('user.instanceDetail.consoleVsock'),
    unsupported: t('user.instanceDetail.consoleUnsupported')
  }
  return labels[protocol] || protocol
}

const statusText = computed(() => {
  if (statusMessage.value) return statusMessage.value
  const map = {
    idle: t('user.instanceDetail.vncIdle'),
    connecting: t('user.instanceDetail.vncConnecting'),
    connected: t('user.instanceDetail.vncConnected'),
    disconnected: t('user.instanceDetail.vncDisconnected'),
    error: t('user.instanceDetail.vncUnavailable')
  }
  return map[status.value] || status.value
})

const statusType = computed(() => {
  if (status.value === 'connected') return 'success'
  if (status.value === 'connecting') return 'warning'
  if (status.value === 'error') return 'danger'
  return 'info'
})

function focusScreen() {
  if (rfb.value && status.value === 'connected') {
    rfb.value.focus({ preventScroll: true })
  }
}

function sendShortcut(id) {
  if (status.value !== 'connected') return
  try {
    if (sendVNCShortcut(rfb.value, id)) focusScreen()
  } catch (error) {
    ElMessage.error(error?.message || t('user.instanceDetail.vncShortcutFailed'))
  }
}

async function pasteToRemote() {
  if (status.value !== 'connected' || !rfb.value) return
  const text = await readFromClipboard(
    t('common.pasteFailed'),
    t('user.instanceDetail.vncClipboardUnavailable')
  )
  if (text === null) return

  sendClipboardToRemote(text, true)
}

function sendClipboardToRemote(text, showSuccess = false) {
  const client = rfb.value
  try {
    if (!client || client.viewOnly || typeof client.clipboardPasteFrom !== 'function') {
      throw new Error(t('user.instanceDetail.vncClipboardUnsupported'))
    }
    client.clipboardPasteFrom(text)
    if (showSuccess) ElMessage.info(t('user.instanceDetail.vncClipboardPasteSent'))
    focusScreen()
    return true
  } catch (error) {
    ElMessage.error(error?.message || t('common.pasteFailed'))
    return false
  }
}

function handleNativePaste(event) {
  if (status.value !== 'connected') return
  const text = getTextFromVNCPasteEvent(event)
  if (text === null) return

  // The user already initiated this paste. Preventing the canvas default keeps
  // the browser from handling it separately before noVNC sends ClientCutText.
  event.preventDefault()
  sendClipboardToRemote(text)
}

async function copyRemoteClipboard() {
  if (remoteClipboardText.value === null) {
    ElMessage.warning(t('user.instanceDetail.vncClipboardEmpty'))
    return
  }
  await copyToClipboard(
    remoteClipboardText.value,
    t('user.instanceDetail.vncClipboardCopySuccess'),
    t('common.copyFailed')
  )
}

function handleRemoteClipboard(event) {
  if (status.value !== 'connected') return
  const text = event?.detail?.text
  if (typeof text === 'string') {
    remoteClipboardText.value = text
    refreshClipboardMode(rfb.value)
  }
}

function refreshClipboardMode(client) {
  if (rfb.value === client) {
    clipboardMode.value = getVNCClipboardMode(client)
  }
}

function clearClipboardCapabilityTimers() {
  clipboardCapabilityTimers.forEach(timer => window.clearTimeout(timer))
  clipboardCapabilityTimers = []
}

function scheduleClipboardCapabilityRefresh(client) {
  clearClipboardCapabilityTimers()
  clipboardCapabilityTimers = [250, 1000].map(delay => window.setTimeout(() => {
    if (status.value === 'connected') refreshClipboardMode(client)
  }, delay))
}

function disconnect() {
  clearClipboardCapabilityTimers()
  if (rfb.value) {
    try {
      rfb.value.disconnect()
    } catch {
      // ignore disconnect races
    }
    rfb.value = null
  }
  connecting.value = false
  remoteClipboardText.value = null
  clipboardMode.value = 'unknown'
  if (status.value !== 'error') {
    status.value = 'disconnected'
  }
}

function setCapabilities(info) {
  const next = Array.isArray(info?.capabilities) ? info.capabilities : []
  capabilities.value = next.length
    ? next
    : [{ protocol: 'unsupported', available: false, repairable: false, reason: info?.reason || t('user.instanceDetail.consoleUnknownError') }]
  const preferred = props.initialProtocol || selectedProtocol.value
  const selected = capabilities.value.find(item => item.protocol === preferred && (item.available || item.repairable)) ||
    capabilities.value.find(item => item.protocol === 'vnc' && item.available) ||
    capabilities.value.find(item => item.protocol === 'spice' && (item.available || item.repairable)) ||
    capabilities.value.find(item => item.available || item.repairable) ||
    capabilities.value[0]
  selectedProtocol.value = selected?.protocol || 'unsupported'
}

async function loadConsoleInfo() {
  loadingCapabilities.value = true
  try {
    const res = await getUserInstanceConsoleInfo(props.instanceId)
    setCapabilities(res.data || {})
    const capability = selectedCapability.value
    if (capability?.protocol === 'vnc' && capability.available) {
      // setCapabilities changes the rendered branch; wait for screenRef to be
      // mounted before constructing noVNC on the initial dialog open.
      await nextTick()
      await connectVNC()
    } else if (capability?.protocol === 'spice' && capability.repairable && !capability.available && !autoRepairStarted) {
      autoRepairStarted = true
      await repairConsole()
    }
  } catch (error) {
    setCapabilities({ reason: error?.fullMessage || error?.userMessage || error?.message || t('user.instanceDetail.consoleUnknownError') })
    status.value = 'error'
    statusMessage.value = error?.fullMessage || error?.userMessage || error?.message || t('user.instanceDetail.consoleUnavailable')
    ElMessage.error(statusMessage.value)
  } finally {
    loadingCapabilities.value = false
  }
}

async function connectVNC() {
  if (!props.instanceId || !screenRef.value) return
  disconnect()
  connecting.value = true
  status.value = 'connecting'
  statusMessage.value = ''
  try {
    const client = new RFB(screenRef.value, getUserInstanceConsoleWsUrl(props.instanceId, 'vnc'))
    client.scaleViewport = true
    client.resizeSession = false
    client.clipViewport = true
    client.background = '#111827'
    client.addEventListener('connect', () => {
      status.value = 'connected'
      connecting.value = false
      refreshClipboardMode(client)
      scheduleClipboardCapabilityRefresh(client)
    })
    client.addEventListener('clipboard', handleRemoteClipboard)
    client.addEventListener('disconnect', event => {
      if (status.value !== 'error') {
        status.value = event.detail?.clean ? 'disconnected' : 'error'
        statusMessage.value = event.detail?.clean ? '' : t('user.instanceDetail.vncDisconnected')
      }
      connecting.value = false
    })
    client.addEventListener('securityfailure', event => {
      status.value = 'error'
      statusMessage.value = event.detail?.reason || t('user.instanceDetail.vncUnavailable')
      connecting.value = false
    })
    rfb.value = client
  } catch (error) {
    status.value = 'error'
    statusMessage.value = error?.message || t('user.instanceDetail.vncUnavailable')
  } finally {
    if (status.value !== 'connecting') {
      connecting.value = false
    }
  }
}

async function selectProtocol(protocol) {
  const capability = capabilities.value.find(item => item.protocol === protocol)
  if (!capability) return
  selectedProtocol.value = protocol
  disconnect()
  statusMessage.value = ''
  if (protocol === 'vnc' && capability.available) {
    await nextTick()
    await connectVNC()
  } else if (protocol === 'spice' && capability.repairable && !capability.available) {
    await repairConsole()
  }
}

async function repairConsole() {
  if (repairing.value) return
  repairing.value = true
  statusMessage.value = t('user.instanceDetail.consoleRepairing')
  try {
    await repairUserInstanceConsole(props.instanceId)
    for (let attempt = 0; attempt < 20; attempt += 1) {
      await new Promise(resolve => window.setTimeout(resolve, 1000))
      const res = await getUserInstanceConsoleInfo(props.instanceId)
      setCapabilities(res.data || {})
      const capability = capabilities.value.find(item => item.protocol === 'spice')
      if (capability?.available) {
        selectedProtocol.value = 'spice'
        statusMessage.value = ''
        return
      }
      if (capability?.repairStatus === 'failed') {
        throw new Error(capability.reason || t('user.instanceDetail.consoleRepairFailed'))
      }
    }
    throw new Error(t('user.instanceDetail.consoleRepairTimeout'))
  } catch (error) {
    const message = error?.fullMessage || error?.userMessage || error?.message || t('user.instanceDetail.consoleRepairFailed')
    statusMessage.value = message
    ElMessage.error(message)
  } finally {
    repairing.value = false
  }
}

function openNativeConsole() {
  const url = selectedCapability.value?.nativeURL
  if (!url) return
  window.open(url, '_blank', 'noopener,noreferrer')
}

async function connect() {
  autoRepairStarted = false
  disconnect()
  status.value = 'connecting'
  statusMessage.value = ''
  await nextTick()
  await loadConsoleInfo()
}

watch(() => props.modelValue, async visible => {
  if (visible) {
    await nextTick()
    connect()
  } else {
    disconnect()
  }
})

onBeforeUnmount(disconnect)
</script>

<style scoped>
.vnc-shell {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.console-shell {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 160px;
}

.console-protocols {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.console-error {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.console-status-hint,
.native-console p {
  color: var(--el-text-color-secondary);
}

.spice-shell {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.terminal-shell {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.spice-frame {
  width: 100%;
  height: min(70vh, 620px);
  min-height: 420px;
  border: 1px solid var(--el-border-color);
  background: #111827;
}

.vnc-status {
  display: flex;
  justify-content: flex-end;
}

.vnc-screen {
  width: 100%;
  height: min(70vh, 620px);
  min-height: 420px;
  overflow: hidden;
  background: #111827;
  border: 1px solid var(--el-border-color);
}

.vnc-screen :deep(canvas) {
  max-width: 100%;
  max-height: 100%;
}
</style>
