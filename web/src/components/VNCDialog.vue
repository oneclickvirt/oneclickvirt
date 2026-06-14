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
    <div class="vnc-shell">
      <div class="vnc-status">
        <el-tag
          size="small"
          :type="statusType"
        >
          {{ statusText }}
        </el-tag>
      </div>
      <div
        ref="screenRef"
        v-loading="connecting"
        class="vnc-screen"
      />
    </div>
  </el-dialog>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import RFB from '@novnc/novnc/lib/rfb.js'
import { getUserInstanceVNCInfo, getUserInstanceVNCWsUrl } from '@/api/user'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  instanceId: { type: [Number, String], required: true },
  instanceName: { type: String, default: '' }
})

const emit = defineEmits(['update:modelValue'])
const { t } = useI18n()

const screenRef = ref(null)
const rfb = ref(null)
const connecting = ref(false)
const status = ref('idle')
const statusMessage = ref('')

const title = computed(() => props.instanceName
  ? `${t('user.instanceDetail.webVNC')} - ${props.instanceName}`
  : t('user.instanceDetail.webVNC'))

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

function disconnect() {
  if (rfb.value) {
    try {
      rfb.value.disconnect()
    } catch {
      // ignore disconnect races
    }
    rfb.value = null
  }
  connecting.value = false
  if (status.value !== 'error') {
    status.value = 'disconnected'
  }
}

async function connect() {
  if (!props.instanceId || !screenRef.value) return
  disconnect()
  connecting.value = true
  status.value = 'connecting'
  statusMessage.value = ''
  try {
    const res = await getUserInstanceVNCInfo(props.instanceId)
    const info = res.data || {}
    if (!info.enabled) {
      status.value = 'error'
      statusMessage.value = info.reason || t('user.instanceDetail.vncUnavailable')
      return
    }
    const client = new RFB(screenRef.value, getUserInstanceVNCWsUrl(props.instanceId))
    client.scaleViewport = true
    client.resizeSession = false
    client.clipViewport = true
    client.background = '#111827'
    client.addEventListener('connect', () => {
      status.value = 'connected'
      connecting.value = false
    })
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
