<template>
  <el-form
    :model="modelValue"
    label-width="120px"
    class="server-form"
  >
    <el-form-item
      :label="$t('admin.providers.username')"
      prop="username"
    >
      <el-input
        v-model="modelValue.username"
        :placeholder="$t('admin.providers.usernamePlaceholder')"
      />
    </el-form-item>
    
    <!-- 认证方式选择 -->
    <el-form-item
      :label="$t('admin.providers.authMethod')"
      prop="authMethod"
    >
      <el-radio-group 
        v-model="modelValue.authMethod"
        @change="emit('auth-method-change', $event)"
      >
        <el-radio-button label="password">
          {{ $t('admin.providers.usePassword') }}
        </el-radio-button>
        <el-radio-button label="sshKey">
          {{ $t('admin.providers.useSSHKey') }}
        </el-radio-button>
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
      <div 
        v-if="isEditing"
        class="form-tip"
      >
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.passwordKeepTip') }}
        </el-text>
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
      <div 
        v-if="isEditing"
        class="form-tip"
      >
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.sshKeyEditTip') }}
        </el-text>
      </div>
    </el-form-item>
    
    <el-divider content-position="left">
      {{ $t('admin.providers.sshTimeoutConfig') }}
    </el-divider>
    
    <el-form-item
      :label="$t('admin.providers.connectTimeout')"
      prop="sshConnectTimeout"
    >
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
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.connectTimeoutTip') }}
      </el-text>
    </div>
    
    <el-form-item
      :label="$t('admin.providers.executeTimeout')"
      prop="sshExecuteTimeout"
    >
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
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.executeTimeoutTip') }}
      </el-text>
    </div>
    
    <el-form-item :label="$t('admin.providers.connectionTest')">
      <el-button
        type="primary"
        :loading="testingConnection"
        :disabled="!modelValue.host || !modelValue.username || (modelValue.authMethod === 'password' ? !modelValue.password : !modelValue.sshKey)"
        @click="emit('test-connection')"
      >
        <el-icon v-if="!testingConnection">
          <Connection />
        </el-icon>
        {{ testingConnection ? $t('admin.providers.testing') : $t('admin.providers.testSSH') }}
      </el-button>
      <div
        v-if="connectionTestResult"
        class="form-tip"
        style="margin-top: 10px;"
      >
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
              <el-button
                type="primary"
                size="small"
                style="margin-top: 8px;"
                @click="emit('apply-timeout')"
              >
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

  <!-- 连接方式（Agent 反向连接） -->
  <el-form
    :model="modelValue"
    label-width="120px"
    class="server-form"
    style="margin-top: 20px; padding-top: 16px; border-top: 1px solid var(--el-border-color-lighter);"
  >
    <el-form-item :label="$t('admin.providers.connectionType')">
      <el-radio-group v-model="modelValue.connectionType">
        <el-radio-button label="ssh">
          {{ $t('admin.providers.connectionTypeSSH') }}
        </el-radio-button>
        <el-radio-button label="agent">
          {{ $t('admin.providers.connectionTypeAgent') }}
        </el-radio-button>
      </el-radio-group>
      <div class="form-tip" style="margin-top: 6px;">
        <el-text size="small" type="info">{{ $t('admin.providers.connectionTypeTip') }}</el-text>
      </div>
    </el-form-item>

    <!-- Agent 状态和密钥（仅编辑模式且已选择 agent 时显示） -->
    <template v-if="modelValue.connectionType === 'agent' && isEditing">
      <el-form-item :label="$t('admin.providers.agentStatus')">
        <el-tag :type="modelValue.agentStatus === 'online' ? 'success' : 'danger'">
          {{ modelValue.agentStatus === 'online' ? $t('admin.providers.agentStatusOnline') : $t('admin.providers.agentStatusOffline') }}
        </el-tag>
        <span v-if="modelValue.agentLastSeen" style="margin-left: 12px; font-size: 12px; color: var(--el-text-color-secondary);">
          {{ $t('admin.providers.agentLastSeen') }}: {{ modelValue.agentLastSeen }}
        </span>
        <span v-if="modelValue.agentRemoteIP" style="margin-left: 12px; font-size: 12px; color: var(--el-text-color-secondary);">
          {{ $t('admin.providers.agentRemoteIP') }}: {{ modelValue.agentRemoteIP }}
        </span>
      </el-form-item>

      <el-form-item :label="$t('admin.providers.agentSecret')">
        <el-button
          type="primary"
          :loading="generatingSecret"
          @click="emit('generate-agent-secret')"
        >
          {{ $t('admin.providers.generateAgentSecret') }}
        </el-button>
        <div class="form-tip" style="margin-top: 6px;">
          <el-text size="small" type="info">{{ $t('admin.providers.generateAgentSecretTip') }}</el-text>
        </div>
      </el-form-item>

      <el-form-item v-if="agentConnectCmd" :label="$t('admin.providers.agentConnectHint')">
        <el-input
          :model-value="agentConnectCmd"
          type="textarea"
          :rows="4"
          readonly
        />
        <div class="form-tip" style="margin-top: 6px;">
          <el-text size="small" type="warning">{{ $t('admin.providers.agentInstallNote') }}</el-text>
        </div>
      </el-form-item>
    </template>

    <!-- Web 终端：SSH 模式编辑时始终显示；Agent 模式仅在 online 时显示 -->
    <template v-if="isEditing && (modelValue.connectionType === 'ssh' || (modelValue.connectionType === 'agent' && modelValue.agentStatus === 'online'))">
      <el-divider content-position="left">
        <span style="color: #666; font-size: 14px;">{{ $t('admin.providers.webTerminal') }}</span>
      </el-divider>
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
        <el-button
          v-if="execResult"
          @click="localCommand = ''; emit('clear-exec-result')"
        >
          {{ $t('common.clear') }}
        </el-button>
      </el-form-item>
      <el-form-item v-if="execResult !== null" :label="$t('admin.providers.execResult')">
        <div
          style="
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
          "
        >
          <div v-if="execResult.stdout">{{ execResult.stdout }}</div>
          <div v-if="execResult.stderr" style="color: #f48771;">{{ execResult.stderr }}</div>
          <div v-if="!execResult.stdout && !execResult.stderr" style="color: #888;">{{ $t('admin.providers.execNoOutput') }}</div>
        </div>
      </el-form-item>
    </template>
  </el-form>
</template>

<script setup>
import { ref } from 'vue'
import { Connection } from '@element-plus/icons-vue'

const localCommand = ref('')

defineProps({
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
  }
})

const emit = defineEmits(['test-connection', 'apply-timeout', 'auth-method-change', 'generate-agent-secret', 'exec-command', 'clear-exec-result'])
</script>

<style scoped>
.server-form {
  max-height: 500px;
  overflow-y: auto;
  padding-right: 10px;
}

.form-tip {
  margin-top: 5px;
}
</style>
