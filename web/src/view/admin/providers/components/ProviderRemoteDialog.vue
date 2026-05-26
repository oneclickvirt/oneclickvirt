<template>
  <el-dialog
    :model-value="visible"
    class="remote-terminal-dialog"
    :title="$t('admin.providers.remoteConnect') + ' - ' + (row?.name || '')"
    width="92%"
    top="3vh"
    destroy-on-close
    @update:model-value="$emit('update:visible', $event)"
    @closed="$emit('closed')"
  >
    <div class="remote-terminal-wrapper">
      <AdminProviderTerminal
        v-if="row && visible"
        :key="terminalKey"
        :provider-id="row.id"
        :provider-name="row.name"
      />
    </div>
    <template #footer>
      <el-button @click="$emit('update:visible', false)">
        {{ $t('common.close') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import AdminProviderTerminal from '@/components/AdminProviderTerminal.vue'

defineProps({
  visible: { type: Boolean, default: false },
  row: { type: Object, default: null },
  terminalKey: { type: Number, default: 0 }
})

defineEmits(['update:visible', 'closed'])
</script>

<style scoped>
.remote-terminal-wrapper {
  height: 72vh;
  min-height: 520px;
}

:deep(.remote-terminal-dialog .el-dialog__body) {
  padding: 12px 16px;
}

@media (max-width: 768px) {
  .remote-terminal-wrapper {
    height: 62vh;
    min-height: 360px;
  }
}
</style>
