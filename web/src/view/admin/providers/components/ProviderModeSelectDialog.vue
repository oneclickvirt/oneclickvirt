<template>
  <el-dialog
    v-model="dialogVisible"
    :title="$t('admin.providers.selectEntryMode')"
    width="560px"
    :close-on-click-modal="true"
    align-center
  >
    <div class="mode-select-container">
      <div
        class="mode-card"
        :class="{ selected: selectedMode === 'ssh' }"
        @click="selectedMode = 'ssh'"
      >
        <div class="mode-icon">
          <el-icon size="40"><Monitor /></el-icon>
        </div>
        <div class="mode-title">{{ $t('admin.providers.modeSSH') }}</div>
        <div class="mode-desc">{{ $t('admin.providers.modeSSHDesc') }}</div>
        <el-tag type="success" size="small" style="margin-top: 10px;">
          {{ $t('admin.providers.modeSSHTag') }}
        </el-tag>
      </div>

      <div
        class="mode-card"
        :class="{ selected: selectedMode === 'agent' }"
        @click="selectedMode = 'agent'"
      >
        <div class="mode-icon">
          <el-icon size="40"><Connection /></el-icon>
        </div>
        <div class="mode-title">{{ $t('admin.providers.modeAgent') }}</div>
        <div class="mode-desc">{{ $t('admin.providers.modeAgentDesc') }}</div>
        <el-tag type="warning" size="small" style="margin-top: 10px;">
          {{ $t('admin.providers.modeAgentTag') }}
        </el-tag>
      </div>
    </div>

    <template #footer>
      <el-button @click="handleClose">{{ $t('common.cancel') }}</el-button>
      <el-button type="primary" @click="handleConfirm">
        {{ $t('common.next') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, computed } from 'vue'
import { Monitor, Connection } from '@element-plus/icons-vue'

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:visible', 'confirm'])

const dialogVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const selectedMode = ref('ssh')

const handleConfirm = () => {
  emit('confirm', selectedMode.value)
  dialogVisible.value = false
}

const handleClose = () => {
  dialogVisible.value = false
  selectedMode.value = 'ssh'
}
</script>

<style scoped>
.mode-select-container {
  display: flex;
  gap: 20px;
  padding: 10px 0;
}

.mode-card {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 24px 16px;
  border: 2px solid var(--el-border-color);
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.2s;
  text-align: center;
  background: var(--el-fill-color-blank);
}

.mode-card:hover {
  border-color: var(--el-color-primary-light-3);
  background: var(--el-color-primary-light-9);
}

.mode-card.selected {
  border-color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}

.mode-icon {
  color: var(--el-color-primary);
  margin-bottom: 12px;
}

.mode-title {
  font-size: 16px;
  font-weight: 600;
  margin-bottom: 8px;
  color: var(--el-text-color-primary);
}

.mode-desc {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  line-height: 1.5;
}
</style>
