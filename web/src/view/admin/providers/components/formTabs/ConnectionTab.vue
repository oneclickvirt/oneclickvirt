<template>
  <div class="connection-tab">
    <!-- ======================================================
         SSH 模式内容（connectionType === 'ssh'）
         ====================================================== -->
    <el-form
      v-if="showSSHSettings"
      :model="modelValue"
      label-width="120px"
      class="server-form"
    >
      <div v-if="isAgentMode" class="form-tip" style="margin-top: -4px; margin-bottom: 12px; margin-left: 120px;">
        <el-text size="small" type="info">{{ $t('admin.providers.agentMappedSshOptionalTip') }}</el-text>
      </div>

      <el-form-item :label="$t('admin.providers.username')" prop="username">
        <el-input
          v-model="modelValue.username"
          :placeholder="$t('admin.providers.usernamePlaceholder')"
        />
      </el-form-item>

      <!-- 认证方式选择 -->
      <el-form-item :label="$t('admin.providers.authMethod')" prop="authMethod">
        <el-radio-group
          v-model="modelValue.authMethod"
          @change="emit('auth-method-change', $event)"
        >
          <el-radio-button label="password">{{ $t('admin.providers.usePassword') }}</el-radio-button>
          <el-radio-button label="sshKey">{{ $t('admin.providers.useSSHKey') }}</el-radio-button>
        </el-radio-group>
      </el-form-item>

      <!-- 密码认证 -->
      <el-form-item
        v-if="modelValue.authMethod === 'password'"
        :label="$t('admin.providers.password')"
        prop="password"
      >
        <el-input
          v-model="modelValue.password"
          type="password"
          :placeholder="isEditing ? $t('admin.providers.passwordEditPlaceholder') : $t('admin.providers.passwordPlaceholder')"
          show-password
        />
        <div v-if="isEditing" class="form-tip">
          <el-text size="small" type="info">{{ $t('admin.providers.passwordKeepTip') }}</el-text>
        </div>
      </el-form-item>

      <!-- SSH密钥认证 -->
      <el-form-item
        v-if="modelValue.authMethod === 'sshKey'"
        :label="$t('admin.providers.sshKey')"
        prop="sshKey"
      >
        <el-input
          v-model="modelValue.sshKey"
          type="textarea"
          :rows="4"
          :placeholder="isEditing ? $t('admin.providers.sshKeyEditPlaceholder') : $t('admin.providers.sshKeyPlaceholder')"
        />
        <div v-if="isEditing" class="form-tip">
          <el-text size="small" type="info">{{ $t('admin.providers.sshKeyEditTip') }}</el-text>
        </div>
      </el-form-item>

      <el-divider content-position="left">{{ $t('admin.providers.sshTimeoutConfig') }}</el-divider>

      <el-form-item :label="$t('admin.providers.connectTimeout')" prop="sshConnectTimeout">
        <el-input-number
          v-model="modelValue.sshConnectTimeout"
          :min="5"
          :max="300"
          :step="5"
          :controls="false"
          placeholder="30"
        />
        <span style="margin-left: 10px;">{{ $t('admin.providers.seconds') }}</span>
      </el-form-item>
      <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
        <el-text size="small" type="info">{{ $t('admin.providers.connectTimeoutTip') }}</el-text>
      </div>

      <el-form-item :label="$t('admin.providers.executeTimeout')" prop="sshExecuteTimeout">
        <el-input-number
          v-model="modelValue.sshExecuteTimeout"
          :min="30"
          :max="3600"
          :step="30"
          :controls="false"
          placeholder="300"
        />
        <span style="margin-left: 10px;">{{ $t('admin.providers.seconds') }}</span>
      </el-form-item>
      <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
        <el-text size="small" type="info">{{ $t('admin.providers.executeTimeoutTip') }}</el-text>
      </div>

      <el-form-item :label="$t('admin.providers.connectionTest')">
        <el-button
          type="primary"
          :loading="testingConnection"
          :disabled="!modelValue.host || !modelValue.username || (modelValue.authMethod === 'password' ? !modelValue.password : !modelValue.sshKey)"
          @click="emit('test-connection')"
        >
          <el-icon v-if="!testingConnection"><Connection /></el-icon>
          {{ testingConnection ? $t('admin.providers.testing') : $t('admin.providers.testSSH') }}
        </el-button>
        <div v-if="connectionTestResult" class="form-tip" style="margin-top: 10px;">
          <el-alert
            :title="connectionTestResult.title"
            :type="connectionTestResult.type"
            :closable="false"
            show-icon
          >
            <template v-if="connectionTestResult.success">
              <div style="margin-top: 8px;">
                <p><strong>{{ $t('admin.providers.testResults') }}:</strong></p>
                <p>{{ $t('admin.providers.minLatency') }}: {{ connectionTestResult.minLatency }}ms</p>
                <p>{{ $t('admin.providers.maxLatency') }}: {{ connectionTestResult.maxLatency }}ms</p>
                <p>{{ $t('admin.providers.avgLatency') }}: {{ connectionTestResult.avgLatency }}ms</p>
                <p style="margin-top: 8px;">
                  <strong>{{ $t('admin.providers.recommendedTimeout') }}: {{ connectionTestResult.recommendedTimeout }}{{ $t('common.seconds') }}</strong>
                </p>
                <el-button type="primary" size="small" style="margin-top: 8px;" @click="emit('apply-timeout')">
                  {{ $t('admin.providers.applyRecommended') }}
                </el-button>
              </div>
            </template>
            <template v-else>
              <p>{{ connectionTestResult.error }}</p>
            </template>
          </el-alert>
        </div>
      </el-form-item>
    </el-form>

    <!-- ======================================================
         Agent 模式内容（connectionType === 'agent'）
         ====================================================== -->
    <div v-if="modelValue.connectionType === 'agent'" class="agent-mode-content">
      <!-- Agent 状态（编辑模式） -->
      <el-alert
        v-if="isEditing"
        :type="modelValue.agentStatus === 'online' ? 'success' : 'warning'"
        :closable="false"
        style="margin-bottom: 20px;"
      >
        <template #title>
          <span>
            {{ $t('admin.providers.agentStatus') }}:
            <strong>{{ modelValue.agentStatus === 'online' ? $t('admin.providers.agentStatusOnline') : $t('admin.providers.agentStatusOffline') }}</strong>
          </span>
          <span v-if="modelValue.agentConnectedAt && modelValue.agentStatus === 'online'" style="margin-left: 16px; font-size: 12px; opacity: 0.8;">
            {{ $t('admin.providers.agentOnlineDuration') }}: {{ formatOnlineDuration(modelValue.agentConnectedAt) }}
          </span>
          <span v-if="modelValue.agentLastSeen" style="margin-left: 16px; font-size: 12px; opacity: 0.8;">
            {{ $t('admin.providers.agentLastSeen') }}: {{ formatDateTime(modelValue.agentLastSeen) }}
          </span>
          <span v-if="modelValue.agentRemoteIP" style="margin-left: 16px; font-size: 12px; opacity: 0.8;">
            {{ $t('admin.providers.agentRemoteIP') }}: {{ modelValue.agentRemoteIP }}
          </span>
        </template>
      </el-alert>

      <!-- 新增模式：只有步骤说明，命令在保存后生成 -->
      <el-alert
        v-if="!isEditing"
        type="info"
        :closable="false"
        style="margin-bottom: 20px;"
      >
        <template #title>
          {{ $t('admin.providers.agentModeNewHint') }}
        </template>
        <div style="margin-top: 8px; line-height: 1.8; font-size: 13px;">
          <p>① {{ $t('admin.providers.agentStep1') }}</p>
          <p>② {{ $t('admin.providers.agentStep2') }}</p>
          <p>③ {{ $t('admin.providers.agentStep3') }}</p>
        </div>
      </el-alert>

      <!-- 编辑模式：Agent密钥生成 + 安装命令 -->
      <template v-if="isEditing">
        <el-divider content-position="left">
          <span style="font-size: 14px; color: #666;">{{ $t('admin.providers.agentInstallSection') }}</span>
        </el-divider>

        <div style="margin-bottom: 16px;">
          <el-button
            type="primary"
            :loading="generatingSecret"
            @click="emit('generate-agent-secret')"
          >
            {{ agentConnectCmd ? $t('admin.providers.regenerateAgentSecret') : $t('admin.providers.generateAgentSecret') }}
          </el-button>
          <div class="form-tip" style="margin-top: 6px;">
            <el-text size="small" type="info">{{ $t('admin.providers.generateAgentSecretTip') }}</el-text>
          </div>
        </div>

        <!-- 安装命令 -->
        <div v-if="agentConnectCmd" class="install-cmd-box">
          <div class="install-cmd-header">
            <span>{{ $t('admin.providers.agentCmdInstall') }}</span>
            <div style="display:flex;align-items:center;gap:8px;">
              <el-switch
                v-model="useCDN"
                size="small"
                :active-text="$t('admin.providers.cdnAccel')"
                :inactive-text="$t('admin.providers.cdnDirect')"
                style="--el-switch-on-color: #13ce66;"
              />
              <el-button size="small" @click="copyCmd(installCmdDisplay)">{{ $t('common.copy') }}</el-button>
            </div>
          </div>
          <div class="install-cmd-content">{{ installCmdDisplay }}</div>
        </div>

        <!-- 卸载命令 -->
        <div v-if="agentConnectCmd" class="install-cmd-box" style="margin-top: 12px;">
          <div class="install-cmd-header">
            <span>{{ $t('admin.providers.agentCmdUninstall') }}</span>
            <el-button size="small" @click="copyCmd('ocv uninstall')">{{ $t('common.copy') }}</el-button>
          </div>
          <div class="install-cmd-content">ocv uninstall</div>
        </div>

        <!-- 升级命令 -->
        <div v-if="agentConnectCmd" class="install-cmd-box" style="margin-top: 12px;">
          <div class="install-cmd-header">
            <span>{{ $t('admin.providers.agentCmdUpgrade') }}</span>
            <el-button size="small" @click="copyCmd('ocv upgrade')">{{ $t('common.copy') }}</el-button>
          </div>
          <div class="install-cmd-content">ocv upgrade</div>
        </div>

        <!-- ocv 快捷命令 -->
        <div v-if="agentConnectCmd" class="install-cmd-box" style="margin-top: 12px;">
          <div class="install-cmd-header">
            <span>{{ $t('admin.providers.agentCmdOcv') }}</span>
            <el-button size="small" @click="copyCmd('ocv')">{{ $t('common.copy') }}</el-button>
          </div>
          <div class="install-cmd-content">ocv</div>
          <div class="install-cmd-tip">
            <el-icon><InfoFilled /></el-icon>
            {{ $t('admin.providers.agentCmdOcvTip') }}
          </div>
        </div>

        <div v-if="agentConnectCmd" class="form-tip" style="margin-top: 10px;">
          <el-text size="small" type="info">{{ $t('admin.providers.agentInstallNote') }}</el-text>
        </div>

        <!-- 检测连接 -->
        <div v-if="agentConnectCmd" style="margin-top: 16px;">
          <el-button
            type="success"
            :loading="checkingAgentStatus"
            @click="emit('check-agent-status')"
          >
            <el-icon><CircleCheck /></el-icon>
            {{ $t('admin.providers.checkAgentConnection') }}
          </el-button>
          <span style="margin-left: 12px; font-size: 13px; color: var(--el-text-color-secondary);">
            {{ $t('admin.providers.checkAgentConnectionTip') }}
          </span>
        </div>

        <!-- Web 终端：Agent online 时显示 -->
        <template v-if="modelValue.agentStatus === 'online'">
          <el-divider content-position="left">
            <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.webTerminal') }}</span>
          </el-divider>
          <el-form :model="modelValue" label-width="120px" class="server-form">
            <el-form-item :label="$t('admin.providers.execCommand')">
              <el-input
                v-model="localCommand"
                :placeholder="$t('admin.providers.execCommandPlaceholder')"
                @keyup.enter="emit('exec-command', localCommand)"
              />
            </el-form-item>
            <el-form-item>
              <el-button
                type="primary"
                :loading="execLoading"
                :disabled="!localCommand.trim()"
                @click="emit('exec-command', localCommand)"
              >
                {{ $t('admin.providers.execRun') }}
              </el-button>
              <el-button v-if="execResult" @click="localCommand = ''; emit('clear-exec-result')">
                {{ $t('common.clear') }}
              </el-button>
            </el-form-item>
            <el-form-item v-if="execResult !== null" :label="$t('admin.providers.execResult')">
              <div class="exec-output">
                <div v-if="execResult.stdout">{{ execResult.stdout }}</div>
                <div v-if="execResult.stderr" style="color: #f48771;">{{ execResult.stderr }}</div>
                <div v-if="!execResult.stdout && !execResult.stderr" style="color: #888;">{{ $t('admin.providers.execNoOutput') }}</div>
              </div>
            </el-form-item>
          </el-form>
        </template>
      </template>
    </div>

    <!-- ======================================================
         SSH 模式 Web 终端（编辑时显示）
         ====================================================== -->
    <template v-if="isEditing && modelValue.connectionType !== 'agent'">
      <el-divider content-position="left">
        <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.webTerminal') }}</span>
      </el-divider>
      <el-form :model="modelValue" label-width="120px" class="server-form">
        <el-form-item :label="$t('admin.providers.execCommand')">
          <el-input
            v-model="localCommand"
            :placeholder="$t('admin.providers.execCommandPlaceholder')"
            @keyup.enter="emit('exec-command', localCommand)"
          />
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            :loading="execLoading"
            :disabled="!localCommand.trim()"
            @click="emit('exec-command', localCommand)"
          >
            {{ $t('admin.providers.execRun') }}
          </el-button>
          <el-button v-if="execResult" @click="localCommand = ''; emit('clear-exec-result')">
            {{ $t('common.clear') }}
          </el-button>
        </el-form-item>
        <el-form-item v-if="execResult !== null" :label="$t('admin.providers.execResult')">
          <div class="exec-output">
            <div v-if="execResult.stdout">{{ execResult.stdout }}</div>
            <div v-if="execResult.stderr" style="color: #f48771;">{{ execResult.stderr }}</div>
            <div v-if="!execResult.stdout && !execResult.stderr" style="color: #888;">{{ $t('admin.providers.execNoOutput') }}</div>
          </div>
        </el-form-item>
      </el-form>
    </template>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { copyToClipboard as copyToClipboardUtil } from '@/utils/clipboard'
import { Connection, WarningFilled, CircleCheck, InfoFilled } from '@element-plus/icons-vue'

const { t } = useI18n()
const localCommand = ref('')
const useCDN = ref(true)

const props = defineProps({
  modelValue: {
    type: Object,
    required: true
  },
  isEditing: {
    type: Boolean,
    default: false
  },
  testingConnection: {
    type: Boolean,
    default: false
  },
  connectionTestResult: {
    type: Object,
    default: null
  },
  generatingSecret: {
    type: Boolean,
    default: false
  },
  agentConnectCmd: {
    type: String,
    default: ''
  },
  execLoading: {
    type: Boolean,
    default: false
  },
  execResult: {
    type: Object,
    default: null
  },
  checkingAgentStatus: {
    type: Boolean,
    default: false
  }
})

const isAgentMode = computed(() => props.modelValue.connectionType === 'agent')
const hasAgentMappedNetworking = computed(() => Boolean(props.modelValue.host && props.modelValue.portIP))
const showSSHSettings = computed(() => !isAgentMode.value || hasAgentMappedNetworking.value)

const installCmdDisplay = computed(() => {
  const cmd = props.agentConnectCmd || ''
  if (!cmd) return ''
  if (useCDN.value) return cmd
  // Strip CDN prefix: https://cdn.spiritlhl.net/ → direct raw.githubusercontent.com
  return cmd.replace(/https:\/\/cdn[^/]*\.[^/]+\//, '')
})

const emit = defineEmits([
  'test-connection',
  'apply-timeout',
  'auth-method-change',
  'generate-agent-secret',
  'check-agent-status',
  'exec-command',
  'clear-exec-result'
])

// 格式化在线时长（输入: ISO时间字符串或Date，输出: "1h 23m" 或 "5m 30s"）
const formatOnlineDuration = (connectedAt) => {
  if (!connectedAt) return '-'
  const start = new Date(connectedAt)
  const now = new Date()
  const diffMs = now - start
  if (diffMs < 0) return '-'
  const totalSeconds = Math.floor(diffMs / 1000)
  const days = Math.floor(totalSeconds / 86400)
  const hours = Math.floor((totalSeconds % 86400) / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h ${minutes}m`
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${totalSeconds % 60}s`
  return `${totalSeconds}s`
}

// 格式化日期时间
const formatDateTime = (dt) => {
  if (!dt) return '-'
  const d = new Date(dt)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

const copyCmd = async (cmd) => {
  await copyToClipboardUtil(cmd, t('common.copySuccess'))
}
</script>

<style scoped>
.connection-tab {
  max-height: 560px;
  overflow-y: auto;
  padding-right: 8px;
}

.server-form {
  padding-right: 10px;
}

.form-tip {
  margin-top: 5px;
}

.agent-mode-content {
  padding: 4px 0;
}

.install-cmd-box {
  border: 1px solid var(--el-color-warning-light-5);
  border-radius: 6px;
  background: var(--el-color-warning-light-9);
  overflow: hidden;
}

.install-cmd-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 14px;
  background: var(--el-color-warning-light-7);
  font-size: 13px;
  font-weight: 500;
}

.install-cmd-content {
  padding: 12px 14px;
  font-family: monospace;
  font-size: 13px;
  word-break: break-all;
  white-space: pre-wrap;
  background: #1e1e1e;
  color: #d4d4d4;
  max-height: 120px;
  overflow-y: auto;
}

.install-cmd-tip {
  padding: 8px 14px;
  font-size: 12px;
  color: var(--el-color-warning);
  display: flex;
  align-items: center;
  gap: 4px;
}

.exec-output {
  background: #1e1e1e;
  color: #d4d4d4;
  font-family: monospace;
  font-size: 12px;
  padding: 12px;
  border-radius: 4px;
  width: 100%;
  max-height: 300px;
  overflow-y: auto;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
