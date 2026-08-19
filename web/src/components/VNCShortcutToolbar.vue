<template>
  <div
    class="vnc-shortcut-toolbar"
    role="toolbar"
    :aria-label="t('user.instanceDetail.vncKeyboardShortcuts')"
  >
    <el-button
      size="small"
      :disabled="!connected"
      :title="t('user.instanceDetail.vncShortcutCtrlAltDel')"
      @click="emit('shortcut', 'ctrlAltDel')"
    >
      <el-icon><Keyboard /></el-icon>
      {{ t('user.instanceDetail.vncShortcutCtrlAltDel') }}
    </el-button>

    <el-dropdown
      trigger="click"
      :disabled="!connected"
      @command="command => emit('shortcut', command)"
    >
      <el-button
        size="small"
        :disabled="!connected"
        :title="t('user.instanceDetail.vncKeyboardShortcuts')"
      >
        <el-icon><MoreFilled /></el-icon>
        {{ t('user.instanceDetail.vncKeyboardShortcuts') }}
        <el-icon class="dropdown-arrow">
          <ArrowDown />
        </el-icon>
      </el-button>
      <template #dropdown>
        <el-dropdown-menu class="vnc-shortcut-menu">
          <el-dropdown-item
            v-for="shortcut in shortcuts"
            :key="shortcut.id"
            :command="shortcut.id"
          >
            <span>{{ shortcutLabel(shortcut) }}</span>
            <kbd>{{ shortcut.display }}</kbd>
          </el-dropdown-item>
        </el-dropdown-menu>
      </template>
    </el-dropdown>

    <el-button
      size="small"
      :disabled="!connected"
      :title="t('user.instanceDetail.vncFocusScreen')"
      @click="emit('focus')"
    >
      <el-icon><FullScreen /></el-icon>
      {{ t('user.instanceDetail.vncFocusScreen') }}
    </el-button>

    <el-button
      size="small"
      :disabled="!connected || !clipboardPasteAvailable"
      :title="clipboardPasteTitle || t('user.instanceDetail.vncClipboardPaste')"
      @click="emit('clipboard-paste')"
    >
      <el-icon><DocumentCopy /></el-icon>
      {{ t('user.instanceDetail.vncClipboardPaste') }}
    </el-button>

    <el-button
      size="small"
      :disabled="!connected || !remoteClipboardAvailable"
      :title="t('user.instanceDetail.vncClipboardCopy')"
      @click="emit('clipboard-copy')"
    >
      <el-icon><CopyDocument /></el-icon>
      {{ t('user.instanceDetail.vncClipboardCopy') }}
    </el-button>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { VNC_SHORTCUTS } from '@/utils/vncKeyboard'

defineProps({
  connected: { type: Boolean, default: false },
  clipboardPasteAvailable: { type: Boolean, default: false },
  clipboardPasteTitle: { type: String, default: '' },
  remoteClipboardAvailable: { type: Boolean, default: false }
})

const emit = defineEmits(['shortcut', 'focus', 'clipboard-paste', 'clipboard-copy'])
const { t } = useI18n()
const shortcuts = computed(() => VNC_SHORTCUTS)

function shortcutLabel(shortcut) {
  if (shortcut.id.startsWith('ctrlAltF')) {
    return t(shortcut.labelKey, { key: shortcut.display.replace('Ctrl+Alt+', '') })
  }
  return t(shortcut.labelKey)
}
</script>

<style scoped>
.vnc-shortcut-toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
}

.dropdown-arrow {
  margin-left: 4px;
}

.vnc-shortcut-menu :deep(.el-dropdown-menu__item) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  min-width: 220px;
}

kbd {
  padding: 1px 5px;
  color: var(--el-text-color-secondary);
  font-family: inherit;
  font-size: 11px;
  border: 1px solid var(--el-border-color);
  border-radius: 3px;
  white-space: nowrap;
}

@media (max-width: 640px) {
  .vnc-shortcut-toolbar {
    justify-content: flex-start;
  }
}
</style>
