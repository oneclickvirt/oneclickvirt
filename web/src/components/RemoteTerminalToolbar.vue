<template>
  <div class="remote-terminal-toolbar">
    <div class="remote-left">
      <span
        v-if="title"
        class="remote-title"
      >
        {{ title }}
      </span>
      <div
        v-if="showViewSwitch"
        class="view-buttons"
      >
        <el-button
          size="small"
          :type="activeView === 'terminal' ? 'primary' : 'default'"
          @click="emit('update:activeView', 'terminal')"
        >
          {{ t('common.terminalTab') }}
        </el-button>
        <el-button
          v-if="supportsSftp"
          size="small"
          :type="activeView === 'sftp' ? 'primary' : 'default'"
          @click="emit('update:activeView', 'sftp')"
        >
          {{ t('common.fileTransferTab') }}
        </el-button>
      </div>
    </div>

    <div class="remote-actions">
      <el-button
        v-for="action in actions"
        :key="action.key"
        size="small"
        :loading="!!action.loading"
        :disabled="!!action.disabled"
        :title="action.title || action.label"
        @click="emit('action', action.key)"
      >
        <el-icon v-if="action.icon">
          <component :is="action.icon" />
        </el-icon>
        {{ action.label }}
      </el-button>
    </div>
  </div>
</template>

<script setup>
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

defineProps({
  title: {
    type: String,
    default: ''
  },
  activeView: {
    type: String,
    default: 'terminal'
  },
  supportsSftp: {
    type: Boolean,
    default: false
  },
  showViewSwitch: {
    type: Boolean,
    default: true
  },
  actions: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['update:activeView', 'action'])
</script>

<style scoped>
.remote-terminal-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.remote-left {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.remote-title {
  color: var(--text-color-primary);
  font-size: 15px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.view-buttons {
  display: flex;
  gap: 8px;
}

.remote-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.remote-actions :deep(.el-button .el-icon) {
  margin-right: 6px;
}
</style>
