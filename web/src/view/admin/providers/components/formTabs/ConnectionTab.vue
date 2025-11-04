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
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.connectTimeoutTip') }}
        </el-text>
      </div>
    </el-form-item>
    
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
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.executeTimeoutTip') }}
        </el-text>
      </div>
    </el-form-item>
    
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
</template>

<script setup>
import { Connection } from '@element-plus/icons-vue'

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
  }
})

const emit = defineEmits(['test-connection', 'apply-timeout', 'auth-method-change'])
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
