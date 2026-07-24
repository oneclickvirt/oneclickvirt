<template>
  <el-dialog 
    v-model="dialogVisible" 
    :title="isEditing ? $t('admin.providers.editServer') : $t('admin.providers.addServer')" 
    width="1000px"
    :close-on-click-modal="false"
    :before-close="handleBeforeClose"
  >
    <!-- 配置分类标签页 -->
    <el-tabs
      v-model="activeTab"
      type="border-card"
      class="server-config-tabs"
      :lazy="false"
    >
      <!-- 基本信息 -->
      <el-tab-pane
        :label="$t('admin.providers.basicInfo')"
        name="basic"
      >
        <BasicInfoTab
          ref="basicInfoTabRef"
          v-model="formData"
          :rules="rules"
        />
      </el-tab-pane>

      <!-- 连接配置 -->
      <el-tab-pane
        :label="$t('admin.providers.connectionConfig')"
        name="connection"
      >
        <ConnectionTab
          v-model="formData"
          :is-editing="isEditing"
          :testing-connection="testingConnection"
          :connection-test-result="connectionTestResult"
          :generating-secret="generatingSecret"
          :agent-connect-cmd="agentConnectCmd"
          :agent-connect-cmd-github="agentConnectCmdGithub"
          :exec-loading="execLoading"
          :exec-result="execResult"
          :checking-agent-status="checkingAgentStatus"
          @test-connection="handleTestConnection"
          @apply-timeout="handleApplyTimeout"
          @auth-method-change="handleAuthMethodChange"
          @generate-agent-secret="handleGenerateAgentSecret"
          @check-agent-status="handleCheckAgentStatus"
          @exec-command="handleExecCommand"
          @clear-exec-result="execResult = null"
        />
      </el-tab-pane>

      <!-- 地理位置 -->
      <el-tab-pane
        :label="$t('admin.providers.location')"
        name="location"
      >
        <LocationTab
          v-model="formData"
          :grouped-countries="groupedCountries"
        />
      </el-tab-pane>

      <!-- 虚拟化配置 -->
      <el-tab-pane
        :label="$t('admin.providers.virtualizationConfig')"
        name="virtualization"
      >
        <VirtualizationTab
          v-model="formData"
        />
      </el-tab-pane>

      <!-- IP映射配置 -->
      <el-tab-pane
        :label="$t('admin.providers.ipMappingConfig')"
        name="mapping"
      >
        <MappingTab
          v-model="formData"
          @provider-updated="handleProviderUpdated"
        />
      </el-tab-pane>

      <!-- 带宽配置 -->
      <el-tab-pane
        :label="$t('admin.providers.bandwidthConfig')"
        name="bandwidth"
      >
        <BandwidthTab
          v-model="formData"
        />
      </el-tab-pane>

      <!-- 等级限制配置 -->
      <el-tab-pane
        :label="$t('admin.providers.levelLimits')"
        name="levelLimits"
      >
        <LevelLimitsTab
          v-model="formData"
          @reset-defaults="handleResetLevelLimits"
        />
      </el-tab-pane>

      <!-- 过期设置 -->
      <el-tab-pane
        :label="$t('admin.providers.expirySettings')"
        name="expiry"
      >
        <ExpirySettingsTab
          v-model="formData"
          :provider-id="formData.id"
          :is-editing="isEditing"
        />
      </el-tab-pane>

      <!-- 高级设置 -->
      <el-tab-pane
        :label="$t('admin.providers.advancedSettings')"
        name="advanced"
      >
        <AdvancedTab
          v-model="formData"
        />
      </el-tab-pane>

      <!-- 硬件配置（LXD/Incus 容器和虚拟机） -->
      <el-tab-pane
        v-if="showHardwareConfigTab"
        :label="$t('admin.providers.hardwareConfig')"
        name="hardwareConfig"
      >
        <HardwareConfigTab
          v-model="formData"
        />
      </el-tab-pane>
    </el-tabs>
    
    <template #footer>
      <span class="dialog-footer">
        <el-button @click="handleClose">{{ $t('common.cancel') }}</el-button>
        <el-button
          type="primary"
          :loading="loading"
          @click="handleSubmit"
        >{{ $t('common.save') }}</el-button>
      </span>
    </template>
  </el-dialog>
</template>

<script setup>
// 导入子标签页组件
import BasicInfoTab from './formTabs/BasicInfoTab.vue'
import ConnectionTab from './formTabs/ConnectionTab.vue'
import LocationTab from './formTabs/LocationTab.vue'
import VirtualizationTab from './formTabs/VirtualizationTab.vue'
import MappingTab from './formTabs/MappingTab.vue'
import BandwidthTab from './formTabs/BandwidthTab.vue'
import LevelLimitsTab from './formTabs/LevelLimitsTab.vue'
import ExpirySettingsTab from './formTabs/ExpirySettingsTab.vue'
import AdvancedTab from './formTabs/AdvancedTab.vue'
import HardwareConfigTab from './formTabs/HardwareConfigTab.vue'
import { useProviderForm } from './composables/useProviderForm'

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  isEditing: {
    type: Boolean,
    default: false
  },
  providerData: {
    type: Object,
    default: () => ({})
  },
  groupedCountries: {
    type: Object,
    default: () => ({})
  },
  loading: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:visible', 'submit', 'cancel', 'reset-level-limits', 'provider-updated'])

const {
  dialogVisible,
  activeTab,
  basicInfoTabRef,
  formData,
  rules,
  groupedCountries,
  showHardwareConfigTab,
  testingConnection,
  connectionTestResult,
  generatingSecret,
  checkingAgentStatus,
  agentConnectCmd,
  agentConnectCmdGithub,
  execLoading,
  execResult,
  handleTestConnection,
  handleApplyTimeout,
  handleAuthMethodChange,
  handleGenerateAgentSecret,
  handleExecCommand,
  handleCheckAgentStatus,
  acknowledgePersistedFields,
  handleResetLevelLimits,
  handleSubmit,
  handleBeforeClose,
  handleClose
} = useProviderForm(props, emit)

const handleProviderUpdated = (updates) => {
  acknowledgePersistedFields(updates)
  emit('provider-updated', updates)
}
</script>

<style scoped>
.server-config-tabs {
  margin-bottom: 20px;
}

:deep(.server-form) {
  max-height: 500px;
  overflow-y: auto;
  padding-right: 10px;
}

:deep(.form-tip) {
  margin-top: 5px;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

@media (max-width: 768px) {
  .server-config-tabs {
    margin-bottom: 12px;
  }

  :deep(.server-config-tabs .el-tabs__content) {
    padding: 12px;
  }

  :deep(.server-form) {
    max-height: 62vh;
    padding-right: 0;
  }

  :deep(.server-config-tabs [style*="margin-left: 120px"]),
  :deep(.server-config-tabs [style*="margin-left: 160px"]),
  :deep(.server-config-tabs [style*="margin-left: 180px"]) {
    margin-left: 0 !important;
    margin-top: 4px !important;
  }

  :deep(.server-config-tabs [style*="width: 300px"]),
  :deep(.server-config-tabs [style*="width: 400px"]) {
    width: 100% !important;
    max-width: 100%;
  }

  .dialog-footer {
    flex-wrap: wrap;
  }

  .dialog-footer .el-button {
    flex: 1 1 120px;
    margin-left: 0;
  }
}
</style>
