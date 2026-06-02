<template>
  <div class="task-steps-panel">
    <div
      v-for="(step, index) in visibleSteps"
      :key="index"
      class="task-step"
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
      <span class="step-name">{{ translateKey(step.key) }}</span>
    </div>
    <div
      v-if="visibleSteps.length === 0"
      class="no-steps"
    >
      {{ t('user.tasks.noProgressLogs') }}
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const { t, te } = useI18n()

const props = defineProps({
  taskType: { type: String, required: true },
  progressLogs: { type: String, default: '' },
  taskStatus: { type: String, required: true }
})

// Ordered step sequences for each task type.
// Optional steps (e.g. skipSSHDetection) are included; if never logged they are hidden.
const TASK_STEP_SEQUENCES = {
  create: [
    'step.preparingCreate',
    'step.dbPreprocessing',
    'step.callingProviderCreate',
    'step.providerCreateSuccess',
    'step.waitingSSHReady',
    'step.skipSSHDetection',
    'step.configuringPortMappings',
    'step.verifyingMonitorStatus',
    'step.settingSSHPassword',
    'step.verifyingSSHPassword',
    'step.configuringNetworkMonitor',
    'step.startingTrafficSync',
    'step.createCompleted',
    'step.createCompletedWithWarning'
  ],
  create_redemption_instance: [
    'step.preparingRedemptionCreate',
    'step.dbPreprocessing',
    'step.callingProviderCreate',
    'step.providerCreateSuccess',
    'step.waitingSSHReady',
    'step.skipSSHDetection',
    'step.configuringPortMappings',
    'step.verifyingMonitorStatus',
    'step.configuringAgentMonitor',
    'step.startingTrafficSync',
    'step.redemptionCreateCompleted'
  ],
  start: [
    'step.parseTaskData',
    'step.getInstanceInfo',
    'step.getProviderConfig',
    'step.connectProvider',
    'step.startingInstance',
    'step.verifyingStart',
    'step.updatingInstanceStatus',
    'step.initMonitoring',
    'step.initTrafficMonitor',
    'step.syncTrafficData'
  ],
  stop: [
    'step.parseTaskData',
    'step.getInstanceInfo',
    'step.getProviderConfig',
    'step.syncTrafficData',
    'step.stoppingInstance',
    'step.verifyingStop',
    'step.updatingInstanceStatus'
  ],
  restart: [
    'step.parseTaskData',
    'step.getInstanceInfo',
    'step.getProviderConfig',
    'step.syncTrafficData',
    'step.restartingInstance',
    'step.verifyingRestart',
    'step.updatingInstanceStatus',
    'step.reinitMonitoring',
    'step.reinitTrafficMonitor',
    'step.finalSyncTrafficData'
  ],
  'reset-password': [
    'step.parseTaskData',
    'step.getInstanceInfo',
    'step.verifyingInstanceStatus',
    'step.generatingPassword',
    'step.settingPassword',
    'step.settingPasswordRetry',
    'step.updatingDatabase'
  ],
  reset: [
    'step.preparingReset',
    'step.deletingOldInstance',
    'step.cleaningOldData',
    'step.creatingNewInstance',
    'step.callingProviderCreate',
    'step.settingPassword',
    'step.updatingInstanceInfo',
    'step.restoringPortMappings',
    'step.reinitMonitoringService',
    'step.resetCompleted'
  ],
  rebuild: [
    'step.preparingReset',
    'step.deletingOldInstance',
    'step.cleaningOldData',
    'step.creatingNewInstance',
    'step.callingProviderCreate',
    'step.settingPassword',
    'step.updatingInstanceInfo',
    'step.restoringPortMappings',
    'step.reinitMonitoringService',
    'step.resetCompleted'
  ],
  delete: [
    'step.parseTaskData',
    'step.getInstanceInfo',
    'step.getProviderConfig',
    'step.syncTrafficData',
    'step.deletingInstance',
    'step.deletingInstanceRetry',
    'step.cleaningMonitorData',
    'step.cleaningDatabaseRecords'
  ],
  'create-port-mapping': [
    'step.parseTaskData',
    'step.getPortMappingInfo',
    'step.getInstanceInfo',
    'step.getProviderConfig',
    'step.getInstancePrivateIP',
    'step.configuringPortMapping',
    'step.startingPortForward',
    'step.configuringRemotePortMapping',
    'step.applyingRemotePortMapping',
    'step.updatingPortStatus'
  ],
  'delete-port-mapping': [
    'step.parseTaskData',
    'step.getPortMappingInfo',
    'step.getInstanceInfo',
    'step.getProviderConfig',
    'step.deletingPortMappingInfo',
    'step.cleaningDatabaseRecords'
  ],
  'sync-port-mappings': [
    'step.parseTaskData',
    'step.getProviderInfo',
    'step.syncProviderPortMappings',
    'step.generatingReport'
  ]
}

// Extract the base step key, stripping ":param" suffix (e.g. "step.settingPasswordRetry:2" → "step.settingPasswordRetry")
function parseLogKey(m) {
  if (!m || !m.startsWith('step.')) return null
  const colonIdx = m.indexOf(':')
  return colonIdx !== -1 ? m.substring(0, colonIdx) : m
}

const parsedLogs = computed(() => {
  if (!props.progressLogs) return []
  try {
    return JSON.parse(props.progressLogs)
  } catch {
    return []
  }
})

// Ordered list of all logged base step keys (with duplicates preserved)
const loggedKeys = computed(() =>
  parsedLogs.value.map(log => parseLogKey(log.m)).filter(Boolean)
)

const canonicalSteps = computed(() => TASK_STEP_SEQUENCES[props.taskType] || [])

const visibleSteps = computed(() => {
  const steps = canonicalSteps.value
  const logged = loggedKeys.value
  const status = props.taskStatus
  const result = []

  for (let i = 0; i < steps.length; i++) {
    const key = steps[i]
    const isLogged = logged.includes(key)

    if (isLogged) {
      // Determine if any canonical step AFTER this position was also logged
      const laterSteps = steps.slice(i + 1)
      const hasLaterStep = laterSteps.some(k => logged.includes(k))

      if (hasLaterStep || status === 'completed') {
        result.push({ key, status: 'success' })
      } else if (status === 'failed' || status === 'cancelled' || status === 'timeout') {
        result.push({ key, status: 'failed' })
      } else {
        // Task is still running — this is the current step
        result.push({ key, status: 'in_progress' })
      }
    } else {
      // This canonical step was not logged.
      // If a later canonical step WAS logged, this step was skipped → hide it.
      // Otherwise it's an upcoming step → show as pending (only for active tasks).
      const laterSteps = steps.slice(i + 1)
      const hasLaterLoggedStep = laterSteps.some(k => logged.includes(k))

      if (!hasLaterLoggedStep) {
        if (status === 'running' || status === 'processing' || status === 'pending') {
          result.push({ key, status: 'pending' })
        }
        // For completed/failed/cancelled tasks, don't show unlogged trailing steps
      }
      // If hasLaterLoggedStep: step was skipped/optional → don't show
    }
  }

  return result
})

function translateKey(key) {
  const userKey = `user.tasks.${key}`
  const adminKey = `admin.tasks.${key}`
  if (te(userKey)) return t(userKey)
  if (te(adminKey)) return t(adminKey)
  return key
}
</script>

<style scoped>
.task-steps-panel {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 4px 0;
}

.task-step {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 8px;
  border-radius: 4px;
  font-size: 13px;
  line-height: 1.5;
}

.step-success {
  color: var(--el-color-success);
}

.step-failed {
  color: var(--el-color-danger);
}

.step-in_progress {
  color: var(--el-color-primary);
  font-weight: 500;
}

.step-pending {
  color: var(--el-text-color-secondary);
}

.step-icon {
  width: 20px;
  text-align: center;
  flex-shrink: 0;
  font-size: 14px;
}

.spin {
  display: inline-block;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.no-steps {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  padding: 4px 8px;
  font-style: italic;
}
</style>
