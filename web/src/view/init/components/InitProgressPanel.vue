<template>
  <div class="progress-panel">
    <div class="progress-header">
      <span class="progress-title">{{ t('init.progress.title') }}</span>
      <el-tag
        :type="tagType"
        size="large"
      >
        {{ statusText }}
      </el-tag>
    </div>

    <el-progress
      :percentage="percent"
      :status="barStatus"
      :stroke-width="12"
      striped
      striped-flow
      class="progress-bar"
    />

    <div class="progress-steps">
      <div
        v-for="(step, index) in steps"
        :key="index"
        class="progress-step"
        :class="'step-' + step.status"
      >
        <span class="step-icon">
          <span v-if="step.status === 'success'">✅</span>
          <span v-else-if="step.status === 'failed'">❌</span>
          <span
            v-else-if="step.status === 'in_progress'"
            class="spin"
          >⟳</span>
          <span v-else>⬜</span>
        </span>
        <span class="step-name">{{ t('init.progress.steps.' + index) }}</span>
        <span
          v-if="step.message"
          class="step-error"
        >{{ step.message }}</span>
      </div>
    </div>

    <div
      v-if="status === 'failed'"
      class="progress-actions"
    >
      <el-button
        type="warning"
        @click="emit('retry')"
      >
        {{ t('init.progress.retry') }}
      </el-button>
    </div>
    <div
      v-if="status === 'success'"
      class="progress-actions"
    >
      <el-button
        type="primary"
        @click="emit('goHome')"
      >
        {{ t('init.progress.goHome') }}
      </el-button>
    </div>
  </div>
</template>

<script setup>
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

defineProps({
  status: { type: String, default: 'idle' },
  steps: { type: Array, default: () => [] },
  percent: { type: Number, default: 0 },
  barStatus: { type: String, default: undefined },
  tagType: { type: String, default: 'info' },
  statusText: { type: String, default: '' }
})

const emit = defineEmits(['retry', 'goHome'])
</script>

<style scoped>
.progress-panel {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: 8px 0;
}

.progress-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.progress-title {
  font-size: 16px;
  font-weight: 600;
  color: #374151;
}

.progress-bar {
  margin: 0 0 4px;
}

.progress-steps {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.progress-step {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border-radius: 10px;
  background: #f9fafb;
  border: 1px solid #f3f4f6;
  transition: background 0.2s;
}

.progress-step.step-success {
  background: #f0fdf4;
  border-color: #bbf7d0;
}

.progress-step.step-failed {
  background: #fef2f2;
  border-color: #fecaca;
}

.progress-step.step-in_progress {
  background: #fffbeb;
  border-color: #fed7aa;
}

.step-icon {
  font-size: 16px;
  width: 20px;
  flex-shrink: 0;
  text-align: center;
}

.step-name {
  flex: 1;
  font-size: 14px;
  color: #374151;
}

.step-error {
  font-size: 12px;
  color: #ef4444;
  max-width: 200px;
  text-align: right;
}

.progress-actions {
  display: flex;
  justify-content: center;
  padding-top: 8px;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to   { transform: rotate(360deg); }
}

.spin {
  display: inline-block;
  animation: spin 1s linear infinite;
}
</style>
