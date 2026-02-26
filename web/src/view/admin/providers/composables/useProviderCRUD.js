// Provider CRUD、分页、搜索、批量操作
import { ref, reactive } from 'vue'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import {
  getProviderList,
  deleteProvider,
  freezeProvider,
  unfreezeProvider,
  setProviderExpiry,
  checkProviderHealth
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

  const handleDeleteProvider = async (provider) => {
    try {
      const isOffline =
        provider.status === 'inactive' ||
        (provider.sshStatus === 'offline' && provider.apiStatus === 'offline')

      const firstConfirmMsg = isOffline
        ? t('admin.providers.deleteOfflineConfirm', { name: provider.name })
        : t('admin.providers.deleteConfirm', { name: provider.name })

      await ElMessageBox.confirm(firstConfirmMsg, t('common.warning'), {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
        dangerouslyUseHTMLString: true
      })

      if (isOffline) {
        try {
          await deleteProvider(provider.id, false)
          ElMessage.success(t('admin.providers.serverDeleteSuccess'))
          await loadProviders()
        } catch (normalError) {
          const errorMsg = normalError?.response?.data?.msg || ''
          if (errorMsg.includes('运行中的实例') || errorMsg.includes('实例')) {
            await ElMessageBox.confirm(
              t('admin.providers.forceDeleteConfirm', { name: provider.name }),
              t('admin.providers.forceDeleteTitle'),
              {
                confirmButtonText: t('admin.providers.forceDeleteButton'),
                cancelButtonText: t('common.cancel'),
                type: 'error',
                dangerouslyUseHTMLString: true,
                distinguishCancelAndClose: true
              }
            )
            await deleteProvider(provider.id, true)
            ElMessage.success(t('admin.providers.serverDeleteSuccess'))
            await loadProviders()
          } else {
            throw normalError
          }
        }
      } else {
        await deleteProvider(provider.id, false)
        ElMessage.success(t('admin.providers.serverDeleteSuccess'))
        await loadProviders()
      }
    } catch (error) {
      if (error !== 'cancel') {
        const errorMsg =
          error?.response?.data?.msg ||
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
    const offlineProviders = selectedProviders.value.filter(
      p =>
        p.status === 'inactive' ||
        (p.sshStatus === 'offline' && p.apiStatus === 'offline')
    )
    const hasOffline = offlineProviders.length > 0

    try {
      const confirmMsg = hasOffline
        ? t('admin.providers.batchDeleteWithOfflineConfirm', {
            total: selectedProviders.value.length,
            offline: offlineProviders.length
          })
        : t('admin.providers.batchDeleteConfirm', {
            count: selectedProviders.value.length
          })

      await ElMessageBox.confirm(confirmMsg, t('common.warning'), {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
        dangerouslyUseHTMLString: true
      })

      const loadingInstance = ElLoading.service({
        lock: true,
        text: t('admin.providers.batchDeleting'),
        background: 'rgba(0, 0, 0, 0.7)'
      })

      let successCount = 0
      let failCount = 0
      const errors = []
      const needsForceDelete = []

      for (const provider of selectedProviders.value) {
        try {
          await deleteProvider(provider.id, false)
          successCount++
        } catch (error) {
          const errorMsg = error?.response?.data?.msg || ''
          const isOffline =
            provider.status === 'inactive' ||
            (provider.sshStatus === 'offline' && provider.apiStatus === 'offline')
          if (
            isOffline &&
            (errorMsg.includes('运行中的实例') || errorMsg.includes('实例'))
          ) {
            needsForceDelete.push(provider)
          } else {
            failCount++
            errors.push(
              `${provider.name}: ${errorMsg || error?.message || t('common.failed')}`
            )
          }
        }
      }
      loadingInstance.close()

      if (needsForceDelete.length > 0) {
        try {
          await ElMessageBox.confirm(
            t('admin.providers.batchForceDeleteConfirm', {
              count: needsForceDelete.length
            }),
            t('admin.providers.forceDeleteTitle'),
            {
              confirmButtonText: t('admin.providers.forceDeleteButton'),
              cancelButtonText: t('common.cancel'),
              type: 'error',
              dangerouslyUseHTMLString: true
            }
          )
          const forceLoadingInstance = ElLoading.service({
            lock: true,
            text: t('admin.providers.forceDeleting'),
            background: 'rgba(0, 0, 0, 0.7)'
          })
          for (const provider of needsForceDelete) {
            try {
              await deleteProvider(provider.id, true)
              successCount++
            } catch (error) {
              failCount++
              errors.push(
                `${provider.name}: ${error?.response?.data?.msg || error?.message || t('common.failed')}`
              )
            }
          }
          forceLoadingInstance.close()
        } catch (cancelError) {
          if (cancelError !== 'cancel') {
            failCount += needsForceDelete.length
            needsForceDelete.forEach(p =>
              errors.push(`${p.name}: ${t('admin.providers.forceCancelled')}`)
            )
          }
        }
      }

      const buildResultHtml = (success, fail, errs, tFn) =>
        `<div>
          <p>${tFn('admin.providers.batchOperationResult')}</p>
          <p style="color:#67C23A;">${tFn('admin.providers.successCount')}: ${success}</p>
          <p style="color:#F56C6C;">${tFn('admin.providers.failCount')}: ${fail}</p>
          ${errs.length > 0
            ? `<div style="margin-top:10px;max-height:200px;overflow-y:auto;">
                <p style="font-weight:bold;">${tFn('admin.providers.errorDetails')}:</p>
                ${errs.map(e => `<p style="color:#F56C6C;font-size:12px;">• ${e}</p>`).join('')}
              </div>`
            : ''}
        </div>`

      if (failCount === 0) {
        ElMessage.success(
          t('admin.providers.batchDeleteSuccess', { count: successCount })
        )
      } else {
        ElMessageBox.alert(
          buildResultHtml(successCount, failCount, errors, t),
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
        ElMessage.error(t('admin.providers.batchDeleteFailed'))
      }
    }
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
        providerID: provider.id,
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
        '此操作将冻结该服务器，冻结后普通用户无法使用该服务器创建实例，是否继续？',
        '确认冻结',
        { confirmButtonText: '确定', cancelButtonText: '取消', type: 'warning' }
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
        '请输入新的过期时间（格式：YYYY-MM-DD HH:MM:SS 或 YYYY-MM-DD），留空则默认设置为31天后过期',
        '解冻服务器',
        {
          confirmButtonText: '确定',
          cancelButtonText: '取消',
          inputPattern: /^(\d{4}-\d{2}-\d{2}( \d{2}:\d{2}:\d{2})?)?$/,
          inputErrorMessage: t('admin.providers.validation.dateFormatError'),
          inputPlaceholder: '如：2024-12-31 23:59:59 或留空'
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
        ElMessage.error(result.msg || t('admin.providers.healthCheckFailed'))
      }
    } catch (error) {
      loadingMessage.close()
      let errorMsg = '健康检查失败'
      if (error.message?.includes('timeout')) {
        errorMsg = '健康检查超时，请检查网络连接或服务器状态'
      } else if (error.message) {
        errorMsg = '健康检查失败: ' + error.message
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
    checkHealth
  }
}
