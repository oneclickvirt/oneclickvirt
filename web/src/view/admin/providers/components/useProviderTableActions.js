import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { saveHardwareReport, getHardwareTestReport } from '@/api/admin'

export default function useProviderTableActions(emit) {
  const { t } = useI18n()

  const actionsDialogVisible = ref(false)
  const currentRow = ref(null)

  const showActionsDialog = (row) => {
    currentRow.value = row
    actionsDialogVisible.value = true
  }

  const handleAction = (action) => {
    if (!currentRow.value) return
    
    actionsDialogVisible.value = false
    
    switch (action) {
      case 'auto-configure':
        emit('auto-configure', currentRow.value)
        break
      case 'traffic-monitor':
        emit('traffic-monitor', currentRow.value)
        break
      case 'health-check':
        emit('health-check', currentRow.value.id)
        break
      case 'set-expiry':
        emit('set-expiry', currentRow.value)
        break
      case 'freeze':
        emit('freeze', currentRow.value.id)
        break
      case 'unfreeze':
        emit('unfreeze', currentRow.value)
        break
      case 'cleanup-orphans':
        emit('cleanup-orphans', currentRow.value)
        break
      case 'remote-connect':
        showRemoteDialog(currentRow.value)
        return // 不关闭 actionsDialogVisible，交给 remote dialog
    }
    
    currentRow.value = null
  }

  // ── 远程连接对话框 ──────────────────────────────────
  const remoteDialogVisible = ref(false)
  const remoteRow = ref(null)
  const terminalKey = ref(0)

  const showRemoteDialog = (row) => {
    remoteRow.value = row
    terminalKey.value++  // 强制 AdminProviderTerminal 重新挂载
    remoteDialogVisible.value = true
    actionsDialogVisible.value = false
  }

  const handleRemoteDialogClosed = () => {
    remoteRow.value = null
    currentRow.value = null
  }

  const pasteUrlDialogVisible = ref(false)
  const pasteUrlInput = ref('')
  const pasteUrlSaving = ref(false)
  let pasteUrlProviderId = null

  const showPasteUrlDialog = () => {
    if (!currentRow.value) return
    pasteUrlProviderId = currentRow.value.id
    pasteUrlInput.value = ''
    pasteUrlDialogVisible.value = true
    actionsDialogVisible.value = false
  }

  const submitPasteUrl = async () => {
    if (!pasteUrlInput.value || !pasteUrlProviderId) return
    pasteUrlSaving.value = true
    try {
      await saveHardwareReport(pasteUrlProviderId, pasteUrlInput.value)
      ElMessage.success(t('admin.providers.reportSaved'))
      pasteUrlDialogVisible.value = false
    } catch (error) {
      console.error('Save hardware report failed:', error)
    } finally {
      pasteUrlSaving.value = false
    }
  }

  const handleViewHardwareReport = async () => {
    if (!currentRow.value) return
    const providerId = currentRow.value.id
    actionsDialogVisible.value = false
    try {
      const res = await getHardwareTestReport(providerId)
      const data = res.data || {}
      if (data.pasteUrl) {
        window.open(data.pasteUrl, '_blank', 'noopener,noreferrer')
      } else {
        ElMessage.warning(t('admin.providers.noHardwareReport'))
      }
    } catch (error) {
      console.error('Failed to load hardware report:', error)
      ElMessage.error(t('admin.providers.noHardwareReport'))
    } finally {
      currentRow.value = null
    }
  }

  return {
    actionsDialogVisible, currentRow, showActionsDialog, handleAction,
    remoteDialogVisible, remoteRow, terminalKey, showRemoteDialog, handleRemoteDialogClosed,
    pasteUrlDialogVisible, pasteUrlInput, pasteUrlSaving, showPasteUrlDialog, submitPasteUrl,
    handleViewHardwareReport
  }
}
