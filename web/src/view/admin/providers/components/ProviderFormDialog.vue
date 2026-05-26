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

      <!-- 签到续期配置 -->
      <el-tab-pane
        v-if="isEditing && formData.id"
        :label="$t('admin.providers.checkinConfig')"
        name="checkin"
      >
        <CheckinConfigTab
          :provider-id="formData.id"
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
import AdvancedTab from './formTabs/AdvancedTab.vue'
import HardwareConfigTab from './formTabs/HardwareConfigTab.vue'
import CheckinConfigTab from './formTabs/CheckinConfigTab.vue'
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

const emit = defineEmits(['update:visible', 'submit', 'cancel', 'reset-level-limits'])

const {
  dialogVisible,
  activeTab,
  basicInfoTabRef,
  formData,
  rules,
  isAgentMode,
  hasAgentMappedNetworking,
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
  handleResetLevelLimits,
  handleSubmit,
  handleBeforeClose,
  handleClose
} = useProviderForm(props, emit)
</script>

<style scoped>
.server-config-tabs {
  margin-bottom: 20px;
}

.server-form {
  max-height: 500px;
  overflow-y: auto;
  padding-right: 10px;
}

.form-tip {
  margin-top: 5px;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
</style>
