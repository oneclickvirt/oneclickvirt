<template>
  <el-dialog
    v-model="visible"
    :title="t('home.footer.updateDialogTitle')"
    width="min(760px, calc(100vw - 24px))"
    destroy-on-close
    class="system-update-dialog"
    @closed="stopPolling"
  >
    <div
      v-loading="loading"
      class="update-dialog-body"
    >
      <div class="update-summary">
        <div class="summary-item">
          <span class="summary-label">{{ t('home.footer.currentVersion') }}</span>
          <code>{{ info.currentVersion || '-' }}</code>
        </div>
        <div class="summary-item">
          <span class="summary-label">{{ t('home.footer.deploymentMode') }}</span>
          <el-tag
            size="small"
            effect="plain"
          >
            {{ info.capability?.mode || 'unknown' }}
          </el-tag>
        </div>
        <div class="summary-item">
          <span class="summary-label">{{ t('home.footer.deploymentFlavor') }}</span>
          <el-tag
            size="small"
            type="info"
            effect="plain"
          >
            {{ info.capability?.flavor || '-' }}
          </el-tag>
        </div>
      </div>

      <el-alert
        v-if="info.capability?.reason"
        :title="info.capability.reason"
        type="info"
        :closable="false"
        show-icon
        class="update-reason"
      />
      <el-alert
        v-if="info.error"
        :title="info.error"
        type="warning"
        :closable="false"
        show-icon
        class="update-reason"
      />

      <el-tabs v-model="activeTab">
        <el-tab-pane
          :label="t('home.footer.updateTab')"
          name="update"
        >
          <div class="version-row">
            <span>{{ t('home.footer.latestVersion') }}</span>
            <a
              v-if="info.releaseUrl"
              :href="info.releaseUrl"
              target="_blank"
              rel="noopener noreferrer"
            >{{ info.latestVersion || '-' }}</a>
            <span v-else>{{ info.latestVersion || '-' }}</span>
          </div>
          <el-select
            v-model="selectedUpdateVersion"
            :placeholder="t('home.footer.selectVersion')"
            clearable
            filterable
            class="version-select"
          >
            <el-option
              v-for="release in updateReleases"
              :key="release.tag"
              :label="release.tag"
              :value="release.tag"
              :disabled="!release.canUpdate"
            >
              <div class="release-option">
                <span>{{ release.tag }}</span>
                <el-tag
                  v-if="!release.canUpdate"
                  size="small"
                  type="warning"
                >
                  {{ t('home.footer.assetUnavailable') }}
                </el-tag>
              </div>
            </el-option>
          </el-select>
          <div class="action-row">
            <el-button
              type="primary"
              :disabled="!info.capability?.canUpdate || !selectedUpdateRelease?.canUpdate"
              :loading="actionLoading"
              @click="submitUpdate"
            >
              {{ t('home.footer.updateNow') }}
            </el-button>
            <el-button
              :disabled="!info.capability?.canRestart"
              :loading="actionLoading"
              @click="submitRestart"
            >
              {{ t('home.footer.restartNow') }}
            </el-button>
          </div>
          <p class="update-note">
            {{ t('home.footer.rollbackDatabaseNote') }}
          </p>
        </el-tab-pane>

        <el-tab-pane
          :label="t('home.footer.rollbackTab')"
          name="rollback"
        >
          <el-select
            v-model="selectedRollback"
            :placeholder="t('home.footer.selectRollbackVersion')"
            value-key="key"
            filterable
            class="version-select"
          >
            <el-option
              v-for="item in rollbackOptions"
              :key="item.key"
              :label="item.label"
              :value="item"
              :disabled="!item.canApply"
            >
              <div class="release-option">
                <span>{{ item.label }}</span>
                <el-tag
                  v-if="item.local"
                  size="small"
                  type="success"
                >
                  {{ t('home.footer.localBackup') }}
                </el-tag>
              </div>
            </el-option>
          </el-select>
          <div class="action-row">
            <el-button
              type="warning"
              :disabled="!info.capability?.canRollback || !selectedRollback?.canApply"
              :loading="actionLoading"
              @click="submitRollback"
            >
              {{ t('home.footer.rollbackNow') }}
            </el-button>
          </div>
          <p class="update-note">
            {{ t('home.footer.rollbackWarning') }}
          </p>
        </el-tab-pane>

        <el-tab-pane
          :label="t('home.footer.commandsTab')"
          name="commands"
        >
          <div
            v-if="!info.capability?.commands?.length"
            class="empty-state"
          >
            {{ t('home.footer.noCommands') }}
          </div>
          <div
            v-for="command in info.capability?.commands || []"
            :key="command.key"
            class="command-item"
          >
            <div class="command-heading">
              <span>{{ command.label }}</span>
              <el-tag
                v-if="command.destructive"
                size="small"
                type="warning"
              >
                {{ t('home.footer.destructiveCommand') }}
              </el-tag>
            </div>
            <p
              v-if="command.description"
              class="command-description"
            >
              {{ command.description }}
            </p>
            <div class="command-line">
              <el-input
                :model-value="resolvedCommand(command)"
                type="textarea"
                :rows="2"
                readonly
              />
              <el-button
                class="copy-command"
                :title="t('home.footer.copyCommand')"
                :aria-label="t('home.footer.copyCommand')"
                @click="copyCommand(resolvedCommand(command))"
              >
                <el-icon><DocumentCopy /></el-icon>
              </el-button>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>

      <el-alert
        v-if="operation && isOperationActive"
        :title="operation.message || t('home.footer.operationRunning')"
        type="info"
        :closable="false"
        show-icon
        class="operation-alert"
      >
        <template #default>
          <span>{{ operation.status }}</span>
          <span v-if="reconnecting"> · {{ t('home.footer.reconnecting') }}</span>
        </template>
      </el-alert>
      <el-alert
        v-if="operation?.status === 'failed'"
        :title="operation.error || t('home.footer.operationFailed')"
        type="error"
        :closable="false"
        show-icon
        class="operation-alert"
      />
    </div>

    <template #footer>
      <el-button @click="visible = false">
        {{ t('common.close') }}
      </el-button>
      <el-button
        :loading="loading"
        @click="loadInfo"
      >
        <el-icon><Refresh /></el-icon>
        {{ t('common.refresh') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { DocumentCopy, Refresh } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import {
  getRollbackVersions,
  getSystemUpdateStatus,
  getUpdateInfo,
  restartSystem,
  startSystemRollback,
  startSystemUpdate
} from '@/api/admin'

const props = defineProps({
  modelValue: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue'])
const { t } = useI18n()
const visible = computed({
  get: () => props.modelValue,
  set: value => emit('update:modelValue', value)
})

const loading = ref(false)
const actionLoading = ref(false)
const activeTab = ref('update')
const info = ref({ capability: { commands: [] }, releases: [], rollbackVersions: [] })
const rollbackReleases = ref([])
const selectedUpdateVersion = ref('')
const selectedRollback = ref(null)
const operation = ref(null)
const reconnecting = ref(false)
let pollTimer = null

const updateReleases = computed(() => (info.value.releases || []).filter(release => release.tag))
const rollbackOptions = computed(() => {
  const options = []
  for (const backup of info.value.rollbackVersions || []) {
    options.push({
      key: `backup:${backup.id}`,
      label: `${backup.version} (${t('home.footer.localBackup')})`,
      version: backup.version,
      backupId: backup.id,
      local: true,
      canApply: Boolean(backup.id)
    })
  }
  for (const release of rollbackReleases.value) {
    if (release.tag && !options.some(option => option.version === release.tag)) {
      options.push({
        key: `release:${release.tag}`,
        label: release.tag,
        version: release.tag,
        local: false,
        canApply: Boolean(release.canRollback)
      })
    }
  }
  return options
})
const isOperationActive = computed(() => ['scheduled', 'staging', 'applying'].includes(operation.value?.status))
const selectedUpdateRelease = computed(() => updateReleases.value.find(release => release.tag === selectedUpdateVersion.value))

const resolvedCommand = (command) => {
  const version = command.key === 'script-rollback'
    ? selectedRollback.value?.version
    : selectedUpdateVersion.value
  if (!version) return command.command
  return command.command.split('<版本号>').join(version)
}

const loadInfo = async () => {
  loading.value = true
  try {
    const response = await getUpdateInfo()
    if (response?.data) {
      info.value = response.data
      operation.value = response.data.operation || operation.value
    }
    if (!selectedUpdateVersion.value && info.value.latestVersion) {
      const latest = updateReleases.value.find(item => item.tag === info.value.latestVersion && item.canUpdate)
      selectedUpdateVersion.value = latest?.tag || ''
    }
    const rollbackResponse = await getRollbackVersions()
    if (rollbackResponse?.data) {
      rollbackReleases.value = rollbackResponse.data.releases || []
      info.value = {
        ...info.value,
        rollbackVersions: rollbackResponse.data.rollbackVersions || info.value.rollbackVersions || [],
        error: rollbackResponse.data.error || info.value.error
      }
    }
    if (visible.value && isOperationActive.value) startPolling()
  } catch (error) {
    ElMessage.error(error?.userMessage || error?.message || t('home.footer.updateLoadFailed'))
  } finally {
    loading.value = false
  }
}

const requireConfirmation = async (message) => {
  try {
    await ElMessageBox.confirm(message, t('common.warning'), {
      type: 'warning',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel')
    })
    return true
  } catch {
    return false
  }
}

const submitUpdate = async () => {
  if (!selectedUpdateRelease.value?.canUpdate) return
  if (!await requireConfirmation(t('home.footer.updateConfirm', { version: selectedUpdateVersion.value }))) return
  actionLoading.value = true
  try {
    const response = await startSystemUpdate(selectedUpdateVersion.value)
    operation.value = response?.data || null
    ElMessage.success(t('home.footer.operationSubmitted'))
    startPolling()
  } catch (error) {
    ElMessage.error(error?.userMessage || error?.message || t('home.footer.operationFailed'))
  } finally {
    actionLoading.value = false
  }
}

const submitRollback = async () => {
  if (!selectedRollback.value?.canApply) return
  if (!await requireConfirmation(t('home.footer.rollbackConfirm', { version: selectedRollback.value.version }))) return
  actionLoading.value = true
  try {
    const response = await startSystemRollback(selectedRollback.value.version, selectedRollback.value.backupId)
    operation.value = response?.data || null
    ElMessage.success(t('home.footer.operationSubmitted'))
    startPolling()
  } catch (error) {
    ElMessage.error(error?.userMessage || error?.message || t('home.footer.operationFailed'))
  } finally {
    actionLoading.value = false
  }
}

const submitRestart = async () => {
  if (!await requireConfirmation(t('home.footer.restartConfirm'))) return
  actionLoading.value = true
  try {
    const response = await restartSystem()
    operation.value = response?.data || null
    ElMessage.success(t('home.footer.operationSubmitted'))
    startPolling()
  } catch (error) {
    ElMessage.error(error?.userMessage || error?.message || t('home.footer.operationFailed'))
  } finally {
    actionLoading.value = false
  }
}

const startPolling = () => {
  stopPolling()
  pollTimer = window.setInterval(async () => {
    try {
      const response = await getSystemUpdateStatus()
      if (response?.data) operation.value = response.data
      reconnecting.value = false
      if (!isOperationActive.value) {
        stopPolling()
        if (operation.value?.status === 'succeeded') await loadInfo()
      }
    } catch {
      reconnecting.value = true
    }
  }, 2000)
}

const stopPolling = () => {
  if (pollTimer) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
}

const copyCommand = async (command) => {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(command)
    } else {
      const textarea = document.createElement('textarea')
      textarea.value = command
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      textarea.remove()
    }
    ElMessage.success(t('common.copySuccess'))
  } catch {
    ElMessage.error(t('common.copyFailed'))
  }
}

watch(() => props.modelValue, value => {
  if (value) loadInfo()
  else stopPolling()
})

onBeforeUnmount(stopPolling)
</script>

<style scoped>
.update-dialog-body {
  min-height: 220px;
}

.update-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 24px;
  padding: 2px 0 14px;
  border-bottom: 1px solid var(--border-color);
}

.summary-item,
.version-row,
.command-heading {
  display: flex;
  align-items: center;
  gap: 8px;
}

.command-description {
  margin: 0 0 6px;
  color: var(--text-color-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.summary-label {
  color: var(--text-color-secondary);
  font-size: 12px;
}

.update-reason,
.operation-alert {
  margin-top: 12px;
}

.version-row {
  margin: 4px 0 14px;
  color: var(--text-color-secondary);
}

.version-row a {
  color: var(--primary-color);
}

.version-select {
  width: min(100%, 420px);
}

.release-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.action-row {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 16px;
}

.update-note,
.empty-state {
  color: var(--text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}

.command-item {
  padding: 10px 0;
  border-bottom: 1px solid var(--border-color-light);
}

.command-heading {
  justify-content: space-between;
  margin-bottom: 6px;
  font-size: 13px;
}

.command-line {
  position: relative;
}

.command-line :deep(.el-textarea__inner) {
  padding-right: 42px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.copy-command {
  position: absolute;
  top: 6px;
  right: 6px;
  z-index: 1;
}

@media (max-width: 600px) {
  .update-summary {
    display: grid;
    grid-template-columns: 1fr 1fr;
  }

  .version-select {
    width: 100%;
  }
}
</style>
