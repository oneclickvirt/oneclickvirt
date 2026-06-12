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
      <span class="step-name">{{ translateStep(step) }}</span>
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
    'step.providerCreateFailed',
    'step.waitingSSHReady',
    'step.waitingSSHReadyFailed',
    'step.instanceNotRunnable',
    'step.skipSSHDetection',
    'step.configuringPortMappings',
    'step.verifyingMonitorStatus',
    'step.settingSSHPassword',
    'step.verifyingSSHPassword',
    'step.configuringNetworkMonitor',
    'step.startingTrafficSync',
    'step.createCompleted',
    'step.createCompletedWithWarning',
    'step.createPostProcessFailed',
    'step.taskFailed',
    'step.taskFailedDetail'
  ],
  create_redemption_instance: [
    'step.preparingRedemptionCreate',
    'step.dbPreprocessing',
    'step.callingProviderCreate',
    'step.providerCreateSuccess',
    'step.providerCreateFailed',
    'step.waitingSSHReady',
    'step.waitingSSHReadyFailed',
    'step.instanceNotRunnable',
    'step.skipSSHDetection',
    'step.configuringPortMappings',
    'step.verifyingMonitorStatus',
    'step.configuringAgentMonitor',
    'step.startingTrafficSync',
    'step.redemptionCreateCompleted',
    'step.createPostProcessFailed',
    'step.taskFailed',
    'step.taskFailedDetail'
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
  ],
  'snapshot-create': [
    'snapshot.taskStarted',
    'snapshot.buildCommand',
    'snapshot.executeRemote',
    'snapshot.updateDatabase',
    'snapshot.taskCompleted'
  ],
  'snapshot-delete': [
    'snapshot.taskStarted',
    'snapshot.deleteRemote',
    'snapshot.taskCompleted'
  ],
  'snapshot-restore': [
    'snapshot.taskStarted',
    'snapshot.restoreRemote',
    'snapshot.taskCompleted'
  ]
}

// Extract the base step key and optional param.
// Examples:
// - "step.settingPasswordRetry:2" -> { key: "step.settingPasswordRetry", params: { n: 2, name: "2" } }
// - "step.syncProviderPortMappings:node-a" -> { key: "step.syncProviderPortMappings", params: { n: "node-a", name: "node-a" } }
function parseLogStep(m) {
  if (!m || (!m.startsWith('step.') && !m.startsWith('snapshot.'))) return null
  const colonIdx = m.indexOf(':')
  if (colonIdx === -1) {
    return { key: m, params: {} }
  }
  const key = m.substring(0, colonIdx)
  const rawParam = m.substring(colonIdx + 1)
  const numericParam = Number(rawParam)
  return {
    key,
    params: {
      n: Number.isFinite(numericParam) ? numericParam : rawParam,
      name: rawParam
    }
  }
}

const parsedLogs = computed(() => {
  if (!props.progressLogs) return []
  try {
    return JSON.parse(props.progressLogs)
  } catch {
    return []
  }
})

// Ordered list of all logged base step keys (with duplicates preserved).
const loggedSteps = computed(() => parsedLogs.value.map(log => parseLogStep(log.m)).filter(Boolean))
const loggedKeys = computed(() => loggedSteps.value.map(step => step.key))
const latestLoggedStepByKey = computed(() => {
  const map = new Map()
  for (const step of loggedSteps.value) {
    map.set(step.key, step)
  }
  return map
})

const canonicalSteps = computed(() => TASK_STEP_SEQUENCES[props.taskType] || [])

const visibleSteps = computed(() => {
  const steps = canonicalSteps.value
  const logged = loggedKeys.value
  const status = props.taskStatus
  const result = []

  for (let i = 0; i < steps.length; i++) {
    const key = steps[i]
    const isLogged = logged.includes(key)
    const loggedStep = latestLoggedStepByKey.value.get(key)

    if (isLogged) {
      // Determine if any canonical step AFTER this position was also logged
      const laterSteps = steps.slice(i + 1)
      const hasLaterStep = laterSteps.some(k => logged.includes(k))

      if (hasLaterStep || status === 'completed') {
        result.push({ key, params: loggedStep?.params || {}, status: 'success' })
      } else if (status === 'failed' || status === 'cancelled' || status === 'timeout') {
        result.push({ key, params: loggedStep?.params || {}, status: 'failed' })
      } else {
        // Task is still running — this is the current step
        result.push({ key, params: loggedStep?.params || {}, status: 'in_progress' })
      }
    } else {
      // This canonical step was not logged.
      // If a later canonical step WAS logged, this step was skipped → hide it.
      // Otherwise it's an upcoming step → show as pending (only for active tasks).
      const laterSteps = steps.slice(i + 1)
      const hasLaterLoggedStep = laterSteps.some(k => logged.includes(k))

      if (!hasLaterLoggedStep) {
        if (status === 'running' || status === 'processing' || status === 'pending') {
          result.push({ key, params: {}, status: 'pending' })
        }
        // For completed/failed/cancelled tasks, don't show unlogged trailing steps
      }
      // If hasLaterLoggedStep: step was skipped/optional → don't show
    }
  }

  return result
})

function translateStep(step) {
  const key = step.key
  const userKey = `user.tasks.${key}`
  const adminKey = `admin.tasks.${key}`
  if (te(userKey)) return t(userKey, step.params || {})
  if (te(adminKey)) return t(adminKey, step.params || {})
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
