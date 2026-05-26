<template>
  <el-card class="overview-card">
    <!-- 关联任务提示 -->
    <el-alert
      v-if="instance.relatedTask"
      :title="getTaskTitle(instance.relatedTask)"
      :type="getTaskAlertType(instance.relatedTask.status)"
      :description="instance.relatedTask.statusMessage || $t('user.instanceDetail.taskProgress', { progress: instance.relatedTask.progress })"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      <template #default>
        <div style="display: flex; justify-content: space-between; align-items: center;">
          <div>
            <p>{{ instance.relatedTask.statusMessage || $t('user.instanceDetail.taskInProgress', { action: getTaskTypeText(instance.relatedTask.taskType) }) }}</p>
            <el-progress
              :percentage="instance.relatedTask.progress"
              :status="instance.relatedTask.progress === 100 ? 'success' : undefined"
              style="margin-top: 10px;"
            />
          </div>
          <el-button
            type="primary"
            size="small"
            @click="$emit('view-task', instance.relatedTask.id)"
          >
            {{ $t('user.instanceDetail.viewTaskDetail') }}
          </el-button>
        </div>
      </template>
    </el-alert>

    <!-- Provider离线警告 -->
    <el-alert
      v-if="instance.providerStatus && (instance.providerStatus === 'inactive' || instance.providerStatus === 'partial')"
      :title="$t('user.instanceDetail.providerOfflineWarning')"
      type="error"
      :description="$t('user.instanceDetail.providerOfflineDesc')"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    />

    <!-- 实例不可用警告 -->
    <el-alert
      v-if="instance.status === 'unavailable'"
      :title="$t('user.instanceDetail.instanceUnavailableWarning')"
      type="warning"
      :description="$t('user.instanceDetail.instanceUnavailableDesc')"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    />

    <div class="server-overview">
      <!-- 左侧：实例基本信息 -->
      <div class="server-basic-info">
        <div class="server-header">
          <div class="server-name-section">
            <h1 class="server-name">
              {{ instance.name }}
            </h1>
            <div class="server-meta">
              <el-tag
                :type="instance.instance_type === 'vm' ? 'primary' : 'success'"
                size="small"
              >
                {{ instance.instance_type === 'vm' ? $t('user.instanceDetail.vm') : $t('user.instanceDetail.container') }}
              </el-tag>
              <el-tag
                v-if="instance.providerType"
                :type="getProviderTypeColor(instance.providerType)"
                size="small"
                style="margin-left: 8px;"
              >
                {{ getProviderTypeName(instance.providerType) }}
              </el-tag>
              <span class="server-provider">{{ instance.providerName }}</span>
            </div>
          </div>
          <div class="server-status">
            <el-tag
              :type="getStatusType(instance.status)"
              effect="dark"
              size="large"
            >
              {{ getStatusText(instance.status) }}
            </el-tag>
          </div>
        </div>

        <!-- 实例控制按钮 -->
        <div class="control-actions">
          <el-tooltip
            v-if="instance.status === 'stopped'"
            :content="monitoring.trafficData?.isLimited ? $t('user.instanceDetail.trafficLimitStartBlocked') : ''"
            :disabled="!monitoring.trafficData?.isLimited"
            placement="top"
          >
            <span>
              <el-button
                type="success"
                size="small"
                :loading="actionLoading"
                :disabled="monitoring.trafficData?.isLimited"
                @click="$emit('perform-action', 'start')"
              >
                <el-icon><VideoPlay /></el-icon>
                {{ $t('user.instanceDetail.start') }}
              </el-button>
            </span>
          </el-tooltip>
          <el-button
            v-if="instance.status === 'running'"
            type="warning"
            size="small"
            :loading="actionLoading"
            @click="$emit('perform-action', 'stop')"
          >
            <el-icon><VideoPause /></el-icon>
            {{ $t('user.instanceDetail.stop') }}
          </el-button>
          <el-button
            v-if="instance.status === 'running' && instance.canRestart !== false"
            size="small"
            :loading="actionLoading"
            @click="$emit('perform-action', 'restart')"
          >
            <el-icon><Refresh /></el-icon>
            {{ $t('user.instanceDetail.restart') }}
          </el-button>
          <el-button
            v-if="instanceTypePermissions.canResetInstance"
            type="info"
            size="small"
            :loading="actionLoading"
            @click="$emit('perform-action', 'reset')"
          >
            <el-icon><Refresh /></el-icon>
            {{ $t('user.instanceDetail.resetSystem') }}
          </el-button>
          <el-button
            v-if="instance.status === 'running'"
            type="primary"
            size="small"
            :loading="actionLoading"
            @click="$emit('reset-password')"
          >
            {{ $t('user.instanceDetail.resetPassword') }}
          </el-button>
          <!-- Web SSH按钮 -->
          <el-button
            v-if="instance.status === 'running' && instance.password"
            type="primary"
            size="small"
            :disabled="!instance.hasSshMapping && instance.networkType === 'no_port_mapping'"
            :title="(!instance.hasSshMapping && instance.networkType === 'no_port_mapping') ? $t('user.instanceDetail.sshNoPortMapping') : ''"
            @click="$emit('open-ssh')"
          >
            <el-icon><Monitor /></el-icon>
            {{ $t('user.instanceDetail.webSSH') }}
          </el-button>
          <!-- 删除按钮 -->
          <el-button
            v-if="instanceTypePermissions.canDeleteInstance"
            type="danger"
            size="small"
            :loading="actionLoading"
            @click="$emit('perform-action', 'delete')"
          >
            <el-icon><Delete /></el-icon>
            {{ $t('user.instanceDetail.delete') }}
          </el-button>
        </div>
      </div>

      <!-- 右侧：硬件信息 -->
      <div class="server-hardware">
        <h3>{{ $t('user.instanceDetail.hardware') }}</h3>
        <div class="hardware-grid">
          <div class="hardware-item">
            <span class="label">{{ $t('user.instanceDetail.cpu') }}</span>
            <span class="value">{{ instance.cpu }}{{ $t('user.instanceDetail.core') }}</span>
          </div>
          <div class="hardware-item">
            <span class="label">{{ $t('user.instanceDetail.memory') }}</span>
            <span class="value">{{ formatMemorySize(instance.memory) }}</span>
          </div>
          <div class="hardware-item">
            <span class="label">{{ $t('user.instanceDetail.storage') }}</span>
            <span class="value">{{ formatDiskSize(instance.disk) }}</span>
          </div>
          <div class="hardware-item">
            <span class="label">{{ $t('user.instanceDetail.bandwidth') }}</span>
            <span class="value">{{ instance.bandwidth }}Mbps</span>
          </div>
        </div>
      </div>
    </div>
  </el-card>
</template>

<script setup>
import { formatDiskSize, formatMemorySize } from '@/utils/unit-formatter'
import { useInstanceFormatters } from '../composables/useInstanceFormatters'

defineProps({
  instance: { type: Object, required: true },
  monitoring: { type: Object, required: true },
  actionLoading: { type: Boolean, default: false },
  instanceTypePermissions: { type: Object, required: true }
})

defineEmits(['perform-action', 'reset-password', 'open-ssh', 'view-task'])

const {
  getTaskTitle,
  getTaskAlertType,
  getTaskTypeText,
  getStatusType,
  getStatusText,
  getProviderTypeName,
  getProviderTypeColor
} = useInstanceFormatters()
</script>
