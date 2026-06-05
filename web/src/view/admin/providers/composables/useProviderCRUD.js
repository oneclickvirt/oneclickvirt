// Provider CRUD、分页、搜索、批量操作
import { ref, reactive } from 'vue'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import {
  getProviderList,
  deleteProvider,
  freezeProvider,
  unfreezeProvider,
  setProviderExpiry,
  checkProviderHealth,
  exportProvidersCSV,
  importProvidersCSV
} from '@/api/admin'
import { useI18n } from 'vue-i18n'

export function useProviderCRUD() {
  const { t } = useI18n()

  const providers = ref([])
  const selectedProviders = ref([])
  const loading = ref(false)
  const currentPage = ref(1)
  const pageSize = ref(10)
  const total = ref(0)

  const searchForm = reactive({
    name: '',
    type: '',
    status: ''
  })

  const loadProviders = async () => {
    loading.value = true
    try {
      const params = { page: currentPage.value, pageSize: pageSize.value }
      if (searchForm.name) params.name = searchForm.name
      if (searchForm.type) params.type = searchForm.type
      if (searchForm.status) params.status = searchForm.status
      const response = await getProviderList(params)
      providers.value = response.data.list || []
      total.value = response.data.total || 0
    } catch (error) {
      ElMessage.error(t('admin.providers.loadProvidersFailed'))
    } finally {
      loading.value = false
    }
  }

  const handleSearch = () => {
    currentPage.value = 1
    loadProviders()
  }

  const handleReset = () => {
    searchForm.name = ''
    searchForm.type = ''
    searchForm.status = ''
    currentPage.value = 1
    loadProviders()
  }

  const handleSizeChange = (newSize) => {
    pageSize.value = newSize
    currentPage.value = 1
    loadProviders()
  }

  const handleCurrentChange = (newPage) => {
    currentPage.value = newPage
    loadProviders()
  }

  const handleSelectionChange = (selection) => {
    selectedProviders.value = selection
  }

  const getDownloadFileName = (response, fallbackName) => {
    const header = response?.headers?.['content-disposition'] || ''
    const utf8Match = header.match(/filename\*=UTF-8''([^;]+)/i)
    if (utf8Match && utf8Match[1]) {
      try {
        return decodeURIComponent(utf8Match[1])
      } catch {
        return utf8Match[1]
      }
    }
    const plainMatch = header.match(/filename="?([^";]+)"?/i)
    if (plainMatch && plainMatch[1]) {
      return plainMatch[1]
    }
    return fallbackName
  }

  const requireTypedConfirmation = async ({ title, message, expected, confirmButtonText, type = 'warning' }) => {
    await ElMessageBox.prompt(
      `${message}<br><br>${t('admin.providers.typeToConfirm', { expected })}`,
      title,
      {
        confirmButtonText: confirmButtonText || t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        inputPlaceholder: expected,
        inputValidator: (value) =>
          String(value || '').trim() === String(expected).trim() ||
          t('admin.providers.confirmTextMismatch', { expected }),
        type,
        dangerouslyUseHTMLString: true
      }
    )
  }

  const handleExportCSV = async () => {
    try {
      const ids = selectedProviders.value.map(item => item.id)
      const response = await exportProvidersCSV(ids)
      const blob = response?.data instanceof Blob
        ? response.data
        : new Blob([response?.data || ''], { type: 'text/csv;charset=utf-8' })

      const fileName = getDownloadFileName(response, 'providers.csv')
      const url = window.URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = fileName
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      window.URL.revokeObjectURL(url)

      ElMessage.success(t('admin.providers.exportCsvSuccess'))
    } catch (error) {
      const errorMsg =
        error?.response?.data?.msg ||
        error?.response?.data?.message ||
        error?.message ||
        t('admin.providers.exportCsvFailed')
      ElMessage.error(errorMsg)
    }
  }

  const handleImportCSV = async (file) => {
    if (!file) {
      ElMessage.warning(t('admin.providers.selectCsvFile'))
      return
    }

    const formData = new FormData()
    formData.append('file', file)

    try {
      const res = await importProvidersCSV(formData)
      const data = res?.data || {}
      const created = Number(data.created || 0)
      const updated = Number(data.updated || 0)
      const skipped = Number(data.skipped || 0)
      ElMessage.success(
        t('admin.providers.importCsvResult', {
          created,
          updated,
          skipped
        })
      )

      const errors = Array.isArray(data.errors) ? data.errors : []
      if (errors.length > 0) {
        ElMessageBox.alert(
          errors.join('<br>'),
          t('admin.providers.importCsvErrorTitle'),
          {
            dangerouslyUseHTMLString: true,
            type: 'warning'
          }
        )
      }

      await loadProviders()
    } catch (error) {
      const errorMsg =
        error?.response?.data?.msg ||
        error?.response?.data?.message ||
        error?.message ||
        t('admin.providers.importCsvFailed')
      ElMessage.error(errorMsg)
    }
  }

  const handleDeleteProvider = async (provider) => {
    try {
      const isOffline =
        provider.status === 'inactive' ||
        (provider.sshStatus === 'offline' && provider.apiStatus === 'offline')

      // Use instanceCount from provider list data (already available)
      const instanceCount = provider.instanceCount || 0

      // Step 1: First confirmation - show the two options
      const firstMsg = isOffline
        ? t('admin.providers.deleteOfflineConfirm', { name: provider.name })
        : t('admin.providers.deleteConfirm', { name: provider.name })

      // Build the full message with options description
      const fullMessage = `
        <div style="text-align: left;">
          <p>${firstMsg}</p>
          <div style="margin: 16px 0; padding: 12px; border: 1px solid #dcdfe6; border-radius: 8px; background: #f5f7fa;">
            <p style="font-weight: bold; margin: 0 0 8px 0;">${t('admin.providers.deleteCascadeOption')}</p>
            <p style="margin: 0; color: #606266; font-size: 13px;">${t('admin.providers.deleteCascadeDesc')}</p>
          </div>
          <div style="margin: 16px 0; padding: 12px; border: 1px solid #f56c6c; border-radius: 8px; background: #fef0f0;">
            <p style="font-weight: bold; margin: 0 0 8px 0; color: #F56C6C;">${t('admin.providers.deleteForceOption')}</p>
            <p style="margin: 0; color: #606266; font-size: 13px;">${t('admin.providers.deleteForceDesc')}</p>
          </div>
        </div>
      `

      // Show the choice dialog
      await ElMessageBox.confirm(fullMessage, t('common.warning'), {
        confirmButtonText: t('admin.providers.deleteCascadeOption'),
        cancelButtonText: t('admin.providers.deleteForceOption'),
        distinguishCancelAndClose: true,
        type: 'warning',
        dangerouslyUseHTMLString: true,
        cancelButtonClass: 'el-button--danger'
      })

      // User clicked "Cascade Delete"
      await performCascadeDelete(provider, instanceCount)
    } catch (error) {
      if (error === 'cancel') {
        // User clicked "Force Delete" - show force delete confirmation first
        await performForceDelete(provider)
      } else if (error !== 'close') {
        const errorMsg =
          error?.response?.data?.msg ||
          error?.response?.data?.message ||
          error?.message ||
          t('admin.providers.serverDeleteFailed')
        ElMessage.error(errorMsg)
      }
    }
  }

  const performCascadeDelete = async (provider, instanceCount) => {
    try {
      await requireTypedConfirmation({
        title: t('admin.providers.cascadeDeleteTitle'),
        message: t('admin.providers.cascadeDeleteConfirm', {
          name: provider.name,
          count: instanceCount
        }),
        expected: provider.name,
        confirmButtonText: t('admin.providers.deleteCascadeOption'),
        type: 'warning'
      })

      const loadingInstance = ElLoading.service({
        lock: true,
        text: instanceCount > 0
          ? t('admin.providers.cascadeDeletingInstances', { count: instanceCount })
          : t('admin.providers.deleting'),
        background: 'rgba(0, 0, 0, 0.7)'
      })

      try {
        await deleteProvider(provider.id, false)
        ElMessage.success(t('admin.providers.serverDeleteSuccess'))
      } finally {
        loadingInstance.close()
      }

      await loadProviders()
    } catch (error) {
      if (error !== 'cancel' && error !== 'close') {
        const errorMsg =
          error?.response?.data?.msg ||
          error?.response?.data?.message ||
          error?.message ||
          t('admin.providers.serverDeleteFailed')
        // Check if the error suggests using force delete (provider unreachable)
        if (errorMsg.includes('强制删除') || errorMsg.includes('force delete')) {
          ElMessage.warning(errorMsg)
        } else {
          ElMessage.error(errorMsg)
        }
      }
    }
  }

  const performForceDelete = async (provider) => {
    try {
      await requireTypedConfirmation({
        title: t('admin.providers.forceDeleteTitle'),
        message: t('admin.providers.forceDeleteConfirm', { name: provider.name }),
        expected: provider.name,
        confirmButtonText: t('admin.providers.forceDeleteButton'),
        type: 'error'
      })

      const loadingInstance = ElLoading.service({
        lock: true,
        text: t('admin.providers.forceDeleting'),
        background: 'rgba(0, 0, 0, 0.7)'
      })

      try {
        await deleteProvider(provider.id, true)
        ElMessage.success(t('admin.providers.serverDeleteSuccess'))
      } finally {
        loadingInstance.close()
      }

      await loadProviders()
    } catch (error) {
      if (error !== 'cancel' && error !== 'close') {
        const errorMsg =
          error?.response?.data?.msg ||
          error?.response?.data?.message ||
          error?.message ||
          t('admin.providers.serverDeleteFailed')
        ElMessage.error(errorMsg)
      }
    }
  }

  const handleBatchDelete = async () => {
    if (selectedProviders.value.length === 0) {
      ElMessage.warning(t('admin.providers.pleaseSelectProviders'))
      return
    }

    const offlineCount = selectedProviders.value.filter(
      p =>
        p.status === 'inactive' ||
        (p.sshStatus === 'offline' && p.apiStatus === 'offline')
    ).length
    const onlineCount = selectedProviders.value.length - offlineCount

    try {
      // Step 1: Show the two options
      let confirmMsg = t('admin.providers.batchDeleteConfirm', {
        count: selectedProviders.value.length
      })
      if (offlineCount > 0 && onlineCount > 0) {
        confirmMsg += `<br><br><span style='color: #F56C6C;'>${offlineCount} 个节点离线</span>，建议对这些节点使用强制删除；<span style='color: #67C23A;'>${onlineCount} 个节点在线</span>，建议使用级联删除。`
      }

      const fullMessage = `
        <div style="text-align: left;">
          <p>${confirmMsg}</p>
          <div style="margin: 16px 0; padding: 12px; border: 1px solid #dcdfe6; border-radius: 8px; background: #f5f7fa;">
            <p style="font-weight: bold; margin: 0 0 8px 0;">${t('admin.providers.deleteCascadeOption')}</p>
            <p style="margin: 0; color: #606266; font-size: 13px;">${t('admin.providers.deleteCascadeDesc')}</p>
          </div>
          <div style="margin: 16px 0; padding: 12px; border: 1px solid #f56c6c; border-radius: 8px; background: #fef0f0;">
            <p style="font-weight: bold; margin: 0 0 8px 0; color: #F56C6C;">${t('admin.providers.deleteForceOption')}</p>
            <p style="margin: 0; color: #606266; font-size: 13px;">${t('admin.providers.deleteForceDesc')}</p>
          </div>
        </div>
      `

      await ElMessageBox.confirm(fullMessage, t('common.warning'), {
        confirmButtonText: t('admin.providers.deleteCascadeOption'),
        cancelButtonText: t('admin.providers.deleteForceOption'),
        distinguishCancelAndClose: true,
        type: 'warning',
        dangerouslyUseHTMLString: true,
        cancelButtonClass: 'el-button--danger'
      })

      await requireTypedConfirmation({
        title: t('admin.providers.cascadeDeleteTitle'),
        message: t('admin.providers.batchCascadeDeleteConfirm', {
          count: selectedProviders.value.length
        }),
        expected: t('admin.providers.batchCascadeConfirmText'),
        confirmButtonText: t('admin.providers.deleteCascadeOption'),
        type: 'warning'
      })
      await executeBatchDelete(selectedProviders.value, false)
    } catch (error) {
      if (error === 'cancel') {
        // Force delete all - show force delete confirmation first
        try {
          await requireTypedConfirmation({
            title: t('admin.providers.forceDeleteTitle'),
            message: t('admin.providers.batchForceDeleteConfirm', {
              count: selectedProviders.value.length
            }),
            expected: t('admin.providers.batchForceConfirmText'),
            confirmButtonText: t('admin.providers.forceDeleteButton'),
            type: 'error'
          })
          await executeBatchDelete(selectedProviders.value, true)
        } catch (cancelError) {
          if (cancelError !== 'cancel') {
            ElMessage.error(t('admin.providers.serverDeleteFailed'))
          }
        }
      } else if (error !== 'close') {
        ElMessage.error(t('admin.providers.serverDeleteFailed'))
      }
    }
  }

  const executeBatchDelete = async (providers, forceDelete) => {
    const loadingInstance = ElLoading.service({
      lock: true,
      text: forceDelete
        ? t('admin.providers.forceDeleting')
        : t('admin.providers.batchDeleting'),
      background: 'rgba(0, 0, 0, 0.7)'
    })

    let successCount = 0
    let failCount = 0
    const errors = []

    for (const provider of providers) {
      try {
        await deleteProvider(provider.id, forceDelete)
        successCount++
      } catch (error) {
        failCount++
        const errorMsg =
          error?.response?.data?.msg ||
          error?.response?.data?.message ||
          error?.message ||
          t('common.failed')
        errors.push(`${provider.name}: ${errorMsg}`)
      }
    }
    loadingInstance.close()

    if (failCount === 0) {
      ElMessage.success(
        t('admin.providers.batchDeleteSuccess', { count: successCount })
      )
    } else {
      const resultHtml = `
        <div>
          <p>${t('admin.providers.batchOperationResult')}</p>
          <p style="color:#67C23A;">${t('admin.providers.successCount')}: ${successCount}</p>
          <p style="color:#F56C6C;">${t('admin.providers.failCount')}: ${failCount}</p>
          ${errors.length > 0
            ? `<div style="margin-top:10px;max-height:200px;overflow-y:auto;">
                <p style="font-weight:bold;">${t('admin.providers.errorDetails')}:</p>
                ${errors.map(e => `<p style="color:#F56C6C;font-size:12px;">• ${e}</p>`).join('')}
              </div>`
            : ''}
        </div>`
      ElMessageBox.alert(resultHtml, t('admin.providers.operationResult'), {
        dangerouslyUseHTMLString: true,
        confirmButtonText: t('common.confirm')
      })
    }

    await loadProviders()
  }

  const handleBatchFreeze = async () => {
    if (selectedProviders.value.length === 0) {
      ElMessage.warning(t('admin.providers.pleaseSelectProviders'))
      return
    }
    const frozenProviders = selectedProviders.value.filter(p => p.isFrozen)
    const activeProviders = selectedProviders.value.filter(p => !p.isFrozen)

    if (frozenProviders.length > 0 && activeProviders.length === 0) {
      ElMessage.warning(t('admin.providers.allSelectedAlreadyFrozen'))
      return
    }

    try {
      const message =
        frozenProviders.length > 0
          ? t('admin.providers.batchFreezeConfirmMixed', {
              total: selectedProviders.value.length,
              frozen: frozenProviders.length,
              active: activeProviders.length
            })
          : t('admin.providers.batchFreezeConfirm', {
              count: selectedProviders.value.length
            })

      await ElMessageBox.confirm(message, t('admin.providers.confirmFreeze'), {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
        dangerouslyUseHTMLString: true
      })

      const loadingInstance = ElLoading.service({
        lock: true,
        text: t('admin.providers.batchFreezing'),
        background: 'rgba(0, 0, 0, 0.7)'
      })

      let successCount = 0
      let failCount = 0
      const errors = []

      for (const provider of activeProviders) {
        try {
          await freezeProvider(provider.id)
          successCount++
        } catch (error) {
          failCount++
          errors.push(
            `${provider.name}: ${error?.response?.data?.msg || error?.message || t('common.failed')}`
          )
        }
      }
      loadingInstance.close()

      if (failCount === 0) {
        ElMessage.success(
          t('admin.providers.batchFreezeSuccess', { count: successCount })
        )
      } else {
        ElMessageBox.alert(
          `<div>
            <p>${t('admin.providers.batchOperationResult')}</p>
            <p style="color:#67C23A;">${t('admin.providers.successCount')}: ${successCount}</p>
            <p style="color:#F56C6C;">${t('admin.providers.failCount')}: ${failCount}</p>
            ${errors.length > 0
              ? `<div style="margin-top:10px;max-height:200px;overflow-y:auto;">
                  <p style="font-weight:bold;">${t('admin.providers.errorDetails')}:</p>
                  ${errors.map(e => `<p style="color:#F56C6C;font-size:12px;">• ${e}</p>`).join('')}
                </div>`
              : ''}
          </div>`,
          t('admin.providers.operationResult'),
          {
            dangerouslyUseHTMLString: true,
            confirmButtonText: t('common.confirm')
          }
        )
      }
      await loadProviders()
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t('admin.providers.batchFreezeFailed'))
      }
    }
  }

  const handleSetProviderExpiry = async (provider) => {
    try {
      const { value: expiresAt } = await ElMessageBox.prompt(
        t('admin.providers.setExpiryPrompt'),
        t('admin.providers.setExpiry'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          inputPattern: /^(\d{4}-\d{2}-\d{2}( \d{2}:\d{2}:\d{2})?)?$/,
          inputErrorMessage: t('admin.providers.dateFormatError'),
          inputPlaceholder: provider.expiresAt
            ? new Date(provider.expiresAt).toISOString().slice(0, 19).replace('T', ' ')
            : '2024-12-31 23:59:59',
          inputValue: provider.expiresAt
            ? new Date(provider.expiresAt).toISOString().slice(0, 19).replace('T', ' ')
            : ''
        }
      )
      await setProviderExpiry({
        providerId: provider.id,
        expiresAt: expiresAt ? new Date(expiresAt).toISOString() : null
      })
      ElMessage.success(t('admin.providers.setExpirySuccess'))
      await loadProviders()
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t('admin.providers.setExpiryFailed'))
      }
    }
  }

  const freezeServer = async (id) => {
    try {
      await ElMessageBox.confirm(
        t('admin.providers.singleFreezeConfirm'),
        t('admin.providers.confirmFreeze'),
        { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' }
      )
      await freezeProvider(id)
      ElMessage.success(t('admin.providers.serverFrozen'))
      await loadProviders()
    } catch (error) {
      if (error !== 'cancel') ElMessage.error(t('admin.providers.serverFreezeFailed'))
    }
  }

  const unfreezeServer = async (server) => {
    try {
      const { value: expiresAt } = await ElMessageBox.prompt(
        t('admin.providers.unfreezeExpiryPrompt'),
        t('admin.providers.unfreezeServer'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          inputPattern: /^(\d{4}-\d{2}-\d{2}( \d{2}:\d{2}:\d{2})?)?$/,
          inputErrorMessage: t('admin.providers.validation.dateFormatError'),
          inputPlaceholder: t('admin.providers.unfreezeExpiryPlaceholder')
        }
      )
      await unfreezeProvider(server.id, expiresAt || '')
      ElMessage.success(t('admin.providers.serverUnfrozen'))
      await loadProviders()
    } catch (error) {
      if (error !== 'cancel') ElMessage.error(t('admin.providers.serverUnfreezeFailed'))
    }
  }

  const checkHealth = async (providerId) => {
    const loadingMessage = ElMessage({
      message: t('admin.providers.validation.healthChecking'),
      type: 'info',
      duration: 0,
      showClose: false
    })
    try {
      const result = await checkProviderHealth(providerId)
      loadingMessage.close()
      if (result.code === 200) {
        ElMessage.success(t('admin.providers.healthCheckComplete'))
        await loadProviders()
      } else {
        ElMessage.error(result.msg || result.message || t('admin.providers.healthCheckFailed'))
      }
    } catch (error) {
      loadingMessage.close()
      let errorMsg = t('admin.providers.healthCheckFailed')
      if (error.message?.includes('timeout')) {
        errorMsg = t('admin.providers.healthCheckTimeout')
      } else if (error.message) {
        errorMsg = t('admin.providers.healthCheckFailed') + ': ' + error.message
      }
      ElMessage.error(errorMsg)
    }
  }

  return {
    providers,
    selectedProviders,
    loading,
    currentPage,
    pageSize,
    total,
    searchForm,
    loadProviders,
    handleSearch,
    handleReset,
    handleSizeChange,
    handleCurrentChange,
    handleSelectionChange,
    handleDeleteProvider,
    handleBatchDelete,
    handleBatchFreeze,
    handleSetProviderExpiry,
    freezeServer,
    unfreezeServer,
    checkHealth,
    handleExportCSV,
    handleImportCSV
  }
}
