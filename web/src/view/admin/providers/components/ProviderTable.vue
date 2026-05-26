<template>
  <div>
    <ProviderDataTable
      :loading="loading"
      :providers="providers"
      :current-page="currentPage"
      :page-size="pageSize"
      :total="total"
      @selection-change="handleSelectionChange"
      @edit="$emit('edit', $event)"
      @show-actions="showActionsDialog"
      @delete="$emit('delete', $event)"
      @size-change="$emit('size-change', $event)"
      @page-change="$emit('page-change', $event)"
    />

    <ProviderActionsDialog
      :visible="actionsDialogVisible"
      :row="currentRow"
      @update:visible="actionsDialogVisible = $event"
      @action="handleAction"
      @paste-url="showPasteUrlDialog"
      @view-hardware-report="handleViewHardwareReport"
    />

    <ProviderPasteUrlDialog
      :visible="pasteUrlDialogVisible"
      :input="pasteUrlInput"
      :saving="pasteUrlSaving"
      @update:visible="pasteUrlDialogVisible = $event"
      @update:input="pasteUrlInput = $event"
      @submit="submitPasteUrl"
    />

    <ProviderRemoteDialog
      :visible="remoteDialogVisible"
      :row="remoteRow"
      :terminal-key="terminalKey"
      @update:visible="remoteDialogVisible = $event"
      @closed="handleRemoteDialogClosed"
    />
  </div>
</template>

<script setup>
import ProviderDataTable from './ProviderDataTable.vue'
import ProviderActionsDialog from './ProviderActionsDialog.vue'
import ProviderPasteUrlDialog from './ProviderPasteUrlDialog.vue'
import ProviderRemoteDialog from './ProviderRemoteDialog.vue'
import useProviderTableActions from './useProviderTableActions'

defineProps({
  loading: {
    type: Boolean,
    default: false
  },
  providers: {
    type: Array,
    default: () => []
  },
  currentPage: {
    type: Number,
    default: 1
  },
  pageSize: {
    type: Number,
    default: 10
  },
  total: {
    type: Number,
    default: 0
  }
})

const emit = defineEmits([
  'selection-change',
  'edit',
  'auto-configure',
  'traffic-monitor',
  'health-check',
  'set-expiry',
  'freeze',
  'unfreeze',
  'delete',
  'size-change',
  'page-change'
])

const handleSelectionChange = (selection) => {
  emit('selection-change', selection)
}

const {
  actionsDialogVisible, currentRow, showActionsDialog, handleAction,
  remoteDialogVisible, remoteRow, terminalKey, handleRemoteDialogClosed,
  pasteUrlDialogVisible, pasteUrlInput, pasteUrlSaving, showPasteUrlDialog, submitPasteUrl,
  handleViewHardwareReport
} = useProviderTableActions(emit)
</script>
