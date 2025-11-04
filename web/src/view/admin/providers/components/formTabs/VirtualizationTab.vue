<template>
  <el-form
    :model="modelValue"
    label-width="120px"
    class="server-form"
  >
    <!-- 虚拟化配置 -->
    <el-divider content-position="left">
      <el-icon><Monitor /></el-icon>
      <span style="margin-left: 8px;">{{ $t('admin.providers.virtualizationConfig') }}</span>
    </el-divider>

    <el-row
      :gutter="20"
      style="margin-bottom: 20px;"
    >
      <el-col :span="12">
        <el-card
          shadow="hover"
          style="height: 100%;"
        >
          <template #header>
            <div style="display: flex; align-items: center; font-weight: 600;">
              <el-icon
                size="18"
                style="margin-right: 8px;"
              >
                <Box />
              </el-icon>
              <span>{{ $t('admin.providers.supportTypes') }}</span>
            </div>
          </template>
          <div
            class="support-type-group"
            style="padding: 10px 0;"
          >
            <el-checkbox
              v-model="modelValue.containerEnabled"
              style="margin-right: 30px;"
            >
              <span style="font-size: 14px;">{{ $t('admin.providers.supportContainer') }}</span>
              <el-tooltip
                :content="$t('admin.providers.containerTech')"
                placement="top"
              >
                <el-icon style="margin-left: 5px;">
                  <InfoFilled />
                </el-icon>
              </el-tooltip>
            </el-checkbox>
            <el-checkbox 
              v-model="modelValue.vmEnabled"
              :disabled="modelValue.type === 'docker'"
            >
              <span style="font-size: 14px;">{{ $t('admin.providers.supportVM') }}</span>
              <el-tooltip
                :content="$t('admin.providers.vmTech')"
                placement="top"
              >
                <el-icon style="margin-left: 5px;">
                  <InfoFilled />
                </el-icon>
              </el-tooltip>
            </el-checkbox>
          </div>
          <div
            class="form-tip"
            style="margin-top: 10px;"
          >
            <el-text
              size="small"
              type="info"
            >
              {{ modelValue.type === 'docker' ? $t('admin.providers.dockerOnlyContainer') : $t('admin.providers.selectVirtualizationType') }}
            </el-text>
          </div>
        </el-card>
      </el-col>
      <el-col :span="12">
        <el-card
          shadow="hover"
          style="height: 100%;"
        >
          <template #header>
            <div style="display: flex; align-items: center; font-weight: 600;">
              <el-icon
                size="18"
                style="margin-right: 8px;"
              >
                <DocumentCopy />
              </el-icon>
              <span>{{ $t('admin.providers.instanceLimits') }}</span>
            </div>
          </template>
          <div style="padding: 5px 0;">
            <el-form-item
              :label="$t('admin.providers.maxContainers')"
              label-width="100px"
              style="margin-bottom: 15px;"
            >
              <el-input-number
                v-model="modelValue.maxContainerInstances"
                :min="0"
                :max="1000"
                :step="1"
                :controls="false"
                :placeholder="$t('admin.providers.zeroUnlimited')"
                size="small"
                style="width: 100%"
              />
              <div
                class="form-tip"
                style="margin-top: 5px;"
              >
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.maxContainersTip') }}
                </el-text>
              </div>
            </el-form-item>
            
            <el-form-item
              :label="$t('admin.providers.maxVMs')"
              label-width="100px"
              style="margin-bottom: 0;"
            >
              <el-input-number
                v-model="modelValue.maxVMInstances"
                :min="0"
                :max="1000"
                :step="1"
                :controls="false"
                :placeholder="$t('admin.providers.zeroUnlimited')"
                size="small"
                style="width: 100%"
              />
              <div
                class="form-tip"
                style="margin-top: 5px;"
              >
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('admin.providers.maxVMsTip') }}
                </el-text>
              </div>
            </el-form-item>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 容器资源限制配置 -->
    <div style="margin-top: 20px;">
      <el-card shadow="hover">
        <template #header>
          <div style="display: flex; align-items: center; justify-content: space-between;">
            <div style="display: flex; align-items: center; font-weight: 600;">
              <el-icon
                size="18"
                style="margin-right: 8px;"
              >
                <Box />
              </el-icon>
              <span>{{ $t('admin.providers.containerResourceLimits') }}</span>
            </div>
            <el-tag
              size="small"
              type="info"
            >
              Container
            </el-tag>
          </div>
        </template>
        <el-alert
          type="warning"
          :closable="false"
          show-icon
          style="margin-bottom: 20px;"
        >
          <template #title>
            <span style="font-size: 13px;">{{ $t('admin.providers.configDescription') }}</span>
          </template>
          <div style="font-size: 12px; line-height: 1.8;">
            {{ $t('admin.providers.enableLimitTip') }}<br>
            {{ $t('admin.providers.noLimitTip') }}
          </div>
        </el-alert>
        <el-row :gutter="20">
          <el-col :span="8">
            <div class="resource-limit-item">
              <div class="resource-limit-label">
                <el-icon><Cpu /></el-icon>
                <span>{{ $t('admin.providers.limitCPU') }}</span>
              </div>
              <el-switch
                v-model="modelValue.containerLimitCpu"
                :active-text="$t('admin.providers.limited')"
                :inactive-text="$t('admin.providers.unlimited')"
                inline-prompt
                style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
              />
              <div class="resource-limit-tip">
                <el-icon size="12">
                  <InfoFilled />
                </el-icon>
                <span>{{ $t('admin.providers.defaultNoLimitCPU') }}</span>
              </div>
            </div>
          </el-col>
          <el-col :span="8">
            <div class="resource-limit-item">
              <div class="resource-limit-label">
                <el-icon><Memo /></el-icon>
                <span>{{ $t('admin.providers.limitMemory') }}</span>
              </div>
              <el-switch
                v-model="modelValue.containerLimitMemory"
                :active-text="$t('admin.providers.limited')"
                :inactive-text="$t('admin.providers.unlimited')"
                inline-prompt
                style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
              />
              <div class="resource-limit-tip">
                <el-icon size="12">
                  <InfoFilled />
                </el-icon>
                <span>{{ $t('admin.providers.defaultNoLimitMemory') }}</span>
              </div>
            </div>
          </el-col>
          <el-col :span="8">
            <div class="resource-limit-item">
              <div class="resource-limit-label">
                <el-icon><Coin /></el-icon>
                <span>{{ $t('admin.providers.limitDisk') }}</span>
              </div>
              <el-switch
                v-model="modelValue.containerLimitDisk"
                :active-text="$t('admin.providers.limited')"
                :inactive-text="$t('admin.providers.unlimited')"
                inline-prompt
                style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
              />
              <div class="resource-limit-tip">
                <el-icon size="12">
                  <InfoFilled />
                </el-icon>
                <span>{{ $t('admin.providers.defaultLimitDisk') }}</span>
              </div>
            </div>
          </el-col>
        </el-row>
      </el-card>
    </div>

    <!-- 虚拟机资源限制配置 -->
    <div style="margin-top: 20px;">
      <el-card shadow="hover">
        <template #header>
          <div style="display: flex; align-items: center; justify-content: space-between;">
            <div style="display: flex; align-items: center; font-weight: 600;">
              <el-icon
                size="18"
                style="margin-right: 8px;"
              >
                <Monitor />
              </el-icon>
              <span>{{ $t('admin.providers.vmResourceLimits') }}</span>
            </div>
            <el-tag
              size="small"
              type="success"
            >
              Virtual Machine
            </el-tag>
          </div>
        </template>
        <el-alert
          type="warning"
          :closable="false"
          show-icon
          style="margin-bottom: 20px;"
        >
          <template #title>
            <span style="font-size: 13px;">{{ $t('admin.providers.configDescription') }}</span>
          </template>
          <div style="font-size: 12px; line-height: 1.8;">
            {{ $t('admin.providers.enableLimitTip') }}<br>
            {{ $t('admin.providers.noLimitTip') }}
          </div>
        </el-alert>
        <el-row :gutter="20">
          <el-col :span="8">
            <div class="resource-limit-item">
              <div class="resource-limit-label">
                <el-icon><Cpu /></el-icon>
                <span>{{ $t('admin.providers.limitCPU') }}</span>
              </div>
              <el-switch
                v-model="modelValue.vmLimitCpu"
                :active-text="$t('admin.providers.limited')"
                :inactive-text="$t('admin.providers.unlimited')"
                inline-prompt
                style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
              />
              <div class="resource-limit-tip">
                <el-icon size="12">
                  <InfoFilled />
                </el-icon>
                <span>{{ $t('admin.providers.defaultLimitCPU') }}</span>
              </div>
            </div>
          </el-col>
          <el-col :span="8">
            <div class="resource-limit-item">
              <div class="resource-limit-label">
                <el-icon><Memo /></el-icon>
                <span>{{ $t('admin.providers.limitMemory') }}</span>
              </div>
              <el-switch
                v-model="modelValue.vmLimitMemory"
                :active-text="$t('admin.providers.limited')"
                :inactive-text="$t('admin.providers.unlimited')"
                inline-prompt
                style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
              />
              <div class="resource-limit-tip">
                <el-icon size="12">
                  <InfoFilled />
                </el-icon>
                <span>{{ $t('admin.providers.defaultLimitMemory') }}</span>
              </div>
            </div>
          </el-col>
          <el-col :span="8">
            <div class="resource-limit-item">
              <div class="resource-limit-label">
                <el-icon><Coin /></el-icon>
                <span>{{ $t('admin.providers.limitDisk') }}</span>
              </div>
              <el-switch
                v-model="modelValue.vmLimitDisk"
                :active-text="$t('admin.providers.limited')"
                :inactive-text="$t('admin.providers.unlimited')"
                inline-prompt
                style="--el-switch-on-color: #13ce66; --el-switch-off-color: #ff4949;"
              />
              <div class="resource-limit-tip">
                <el-icon size="12">
                  <InfoFilled />
                </el-icon>
                <span>{{ $t('admin.providers.defaultLimitDisk') }}</span>
              </div>
            </div>
          </el-col>
        </el-row>
      </el-card>
    </div>

    <!-- ProxmoxVE存储配置 -->
    <el-form-item
      v-if="modelValue.type === 'proxmox'"
      :label="$t('admin.providers.storagePool')"
      prop="storagePool"
      style="margin-top: 20px;"
    >
      <el-input
        v-model="modelValue.storagePool"
        :placeholder="$t('admin.providers.storagePoolPlaceholder')"
        maxlength="64"
        show-word-limit
      >
        <template #prepend>
          <el-icon><FolderOpened /></el-icon>
        </template>
      </el-input>
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.proxmoxStorageTip') }}
        </el-text>
      </div>
    </el-form-item>
  </el-form>
</template>

<script setup>
import { Monitor, Box, DocumentCopy, InfoFilled, Cpu, Memo, Coin, FolderOpened } from '@element-plus/icons-vue'

defineProps({
  modelValue: {
    type: Object,
    required: true
  }
})
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

.resource-limit-item {
  text-align: center;
  padding: 15px 10px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  transition: all 0.3s;
}

.resource-limit-item:hover {
  border-color: #409eff;
  background-color: #f5f7fa;
}

.resource-limit-label {
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 10px;
  font-weight: 500;
  font-size: 14px;
}

.resource-limit-label .el-icon {
  margin-right: 5px;
}

.resource-limit-tip {
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 10px;
  font-size: 12px;
  color: #909399;
}

.resource-limit-tip .el-icon {
  margin-right: 3px;
}
</style>
