<template>
  <el-dialog
    :model-value="visible"
    :title="$t('common.actions')"
    width="400px"
    @update:model-value="$emit('update:visible', $event)"
  >
    <div
      v-if="row"
      class="actions-dialog-content"
    >
      <el-button
        v-if="row.type === 'lxd' || row.type === 'incus' || row.type === 'proxmox'"
        class="action-button"
        type="primary"
        @click="$emit('action', 'auto-configure')"
      >
        {{ $t('admin.providers.autoConfigureAPI') }}
      </el-button>

      <el-button
        class="action-button"
        type="success"
        @click="$emit('action', 'traffic-monitor')"
      >
        {{ $t('admin.providers.monitoringManagement') }}
      </el-button>

      <el-divider v-if="row.type === 'lxd' || row.type === 'incus' || row.type === 'proxmox'" />
      <el-button
        class="action-button"
        type="primary"
        @click="$emit('action', 'health-check')"
      >
        {{ $t('admin.providers.healthCheck') }}
      </el-button>

      <el-button
        class="action-button"
        type="info"
        @click="$emit('action', 'set-expiry')"
      >
        {{ $t('admin.providers.setExpiry') }}
      </el-button>

      <el-button
        v-if="row.isFrozen"
        class="action-button"
        type="success"
        @click="$emit('action', 'unfreeze')"
      >
        {{ $t('admin.providers.unfreeze') }}
      </el-button>
      <el-button
        v-else
        class="action-button"
        type="warning"
        @click="$emit('action', 'freeze')"
      >
        {{ $t('admin.providers.freeze') }}
      </el-button>

      <el-divider />
      <el-button
        class="action-button"
        type="primary"
        @click="$emit('action', 'remote-connect')"
      >
        <el-icon><Monitor /></el-icon>
        {{ $t('admin.providers.remoteConnect') }}
      </el-button>
      <el-button
        class="action-button"
        type="warning"
        @click="$emit('paste-url')"
      >
        {{ $t('admin.providers.setHardwareReport') }}
      </el-button>
      <el-button
        class="action-button"
        type="info"
        @click="$emit('view-hardware-report')"
      >
        {{ $t('admin.providers.viewHardwareReport') }}
      </el-button>
    </div>
  </el-dialog>
</template>

<script setup>
defineProps({
  visible: { type: Boolean, default: false },
  row: { type: Object, default: null }
})

defineEmits(['update:visible', 'action', 'paste-url', 'view-hardware-report'])
</script>

<style scoped>
.actions-dialog-content {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 10px 0;
}

.action-button {
  width: 100%;
  margin: 0 !important;
}

.actions-dialog-content .el-divider {
  margin: 10px 0;
}
</style>
