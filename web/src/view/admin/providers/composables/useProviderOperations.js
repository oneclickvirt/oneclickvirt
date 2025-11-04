// Provider CRUD业务操作逻辑
import { ref } from 'vue'
import { ElMessage, ElMessageBox, ElLoading } from 'element-plus'
import { 
  getProviderList, 
  createProvider, 
  updateProvider, 
  deleteProvider,
  freezeProvider,
  unfreezeProvider,
  checkProviderHealth,
  autoConfigureProvider,
  getConfigurationTaskDetail
} from '@/api/admin'
import { useI18n } from 'vue-i18n'

export function useProviderOperations() {
  const { t } = useI18n()
  
  const loading = ref(false)
  const providers = ref([])
  const selectedProviders = ref([])
  const total = ref(0)

  // 加载Provider列表
  const loadProviders = async (params) => {
    loading.value = true
    try {
      const response = await getProviderList(params)
      providers.value = response.data.list || []
      total.value = response.data.total || 0
      return response
    } catch (error) {
      ElMessage.error(t('admin.providers.loadProvidersFailed'))
      throw error
    } finally {
      loading.value = false
    }
  }

  // 创建Provider
  const createProviderHandler = async (providerData) => {
    try {
      const response = await createProvider(providerData)
      ElMessage.success(t('admin.providers.serverCreated'))
      return response
    } catch (error) {
      const errorMsg = error?.response?.data?.msg || error?.message || t('admin.providers.serverCreateFailed')
      ElMessage.error(errorMsg)
      throw error
    }
  }

  // 更新Provider
  const updateProviderHandler = async (id, providerData) => {
    try {
      const response = await updateProvider(id, providerData)
      ElMessage.success(t('admin.providers.serverUpdated'))
      return response
    } catch (error) {
      const errorMsg = error?.response?.data?.msg || error?.message || t('admin.providers.serverUpdateFailed')
      ElMessage.error(errorMsg)
      throw error
    }
  }

  // 删除Provider
  const deleteProviderHandler = async (id) => {
    try {
      await ElMessageBox.confirm(
        '此操作将永久删除该服务器，是否继续？',
        '警告',
        {
          confirmButtonText: '确定',
          cancelButtonText: '取消',
          type: 'warning'
        }
      )

      await deleteProvider(id)
      ElMessage.success(t('admin.providers.serverDeleteSuccess'))
      return true
    } catch (error) {
      if (error !== 'cancel') {
        const errorMsg = error?.response?.data?.msg || error?.message || t('admin.providers.serverDeleteFailed')
        ElMessage.error(errorMsg)
      }
      return false
    }
  }

  // 批量删除
  const batchDeleteProviders = async (providers) => {
    if (!providers || providers.length === 0) {
      ElMessage.warning(t('admin.providers.pleaseSelectProviders'))
      return { success: false }
    }

    try {
      await ElMessageBox.confirm(
        t('admin.providers.batchDeleteConfirm', { count: providers.length }),
        t('common.warning'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning',
          dangerouslyUseHTMLString: true
        }
      )

      const loadingInstance = ElLoading.service({
        lock: true,
        text: t('admin.providers.batchDeleting'),
        background: 'rgba(0, 0, 0, 0.7)'
      })

      let successCount = 0
      let failCount = 0
      const errors = []

      for (const provider of providers) {
        try {
          await deleteProvider(provider.id)
          successCount++
        } catch (error) {
          failCount++
          errors.push(`${provider.name}: ${error?.response?.data?.msg || error?.message || t('common.failed')}`)
        }
      }

      loadingInstance.close()

      if (failCount === 0) {
        ElMessage.success(t('admin.providers.batchDeleteSuccess', { count: successCount }))
      } else {
        ElMessageBox.alert(
          `${t('admin.providers.batchDeletePartialSuccess', { success: successCount, fail: failCount })}<br><br>${errors.join('<br>')}`,
          t('admin.providers.batchOperationResult'),
          {
            dangerouslyUseHTMLString: true,
            type: failCount === providers.length ? 'error' : 'warning'
          }
        )
      }

      return { success: true, successCount, failCount, errors }
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t('admin.providers.batchDeleteFailed'))
      }
      return { success: false }
    }
  }

  // 批量冻结
  const batchFreezeProviders = async (providers) => {
    if (!providers || providers.length === 0) {
      ElMessage.warning(t('admin.providers.pleaseSelectProviders'))
      return { success: false }
    }

    try {
      await ElMessageBox.confirm(
        t('admin.providers.batchFreezeConfirm', { count: providers.length }),
        t('common.warning'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        }
      )

      const loadingInstance = ElLoading.service({
        lock: true,
        text: t('admin.providers.batchFreezing'),
        background: 'rgba(0, 0, 0, 0.7)'
      })

      let successCount = 0
      let failCount = 0
      const errors = []

      for (const provider of providers) {
        try {
          await freezeProvider(provider.id)
          successCount++
        } catch (error) {
          failCount++
          errors.push(`${provider.name}: ${error?.response?.data?.msg || error?.message || t('common.failed')}`)
        }
      }

      loadingInstance.close()

      if (failCount === 0) {
        ElMessage.success(t('admin.providers.batchFreezeSuccess', { count: successCount }))
      } else {
        ElMessageBox.alert(
          `${t('admin.providers.batchFreezePartialSuccess', { success: successCount, fail: failCount })}<br><br>${errors.join('<br>')}`,
          t('admin.providers.batchOperationResult'),
          {
            dangerouslyUseHTMLString: true,
            type: 'warning'
          }
        )
      }

      return { success: true, successCount, failCount, errors }
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t('admin.providers.batchFreezeFailed'))
      }
      return { success: false }
    }
  }

  // 批量健康检查
  const batchHealthCheck = async (providers) => {
    if (!providers || providers.length === 0) {
      ElMessage.warning(t('admin.providers.pleaseSelectProviders'))
      return { success: false }
    }

    const loadingInstance = ElLoading.service({
      lock: true,
      text: t('admin.providers.batchHealthChecking'),
      background: 'rgba(0, 0, 0, 0.7)'
    })

    let successCount = 0
    let failCount = 0
    const errors = []

    for (const provider of providers) {
      try {
        await checkProviderHealth(provider.id)
        successCount++
      } catch (error) {
        failCount++
        errors.push(`${provider.name}: ${error?.response?.data?.msg || error?.message || t('common.failed')}`)
      }
    }

    loadingInstance.close()

    if (failCount === 0) {
      ElMessage.success(t('admin.providers.batchHealthCheckSuccess', { count: successCount }))
    } else {
      ElMessageBox.alert(
        `${t('admin.providers.batchHealthCheckPartialSuccess', { success: successCount, fail: failCount })}<br><br>${errors.join('<br>')}`,
        t('admin.providers.batchOperationResult'),
        {
          dangerouslyUseHTMLString: true,
          type: 'warning'
        }
      )
    }

    return { success: true, successCount, failCount, errors }
  }

  // 冻结Provider
  const freezeProviderHandler = async (id) => {
    try {
      await ElMessageBox.confirm(
        '此操作将冻结该服务器，冻结后普通用户无法使用该服务器创建实例，是否继续？',
        '确认冻结',
        {
          confirmButtonText: '确定',
          cancelButtonText: '取消',
          type: 'warning'
        }
      )

      await freezeProvider(id)
      ElMessage.success(t('admin.providers.serverFrozen'))
      return true
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t('admin.providers.serverFreezeFailed'))
      }
      return false
    }
  }

  // 解冻Provider
  const unfreezeProviderHandler = async (server) => {
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
      return true
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t('admin.providers.serverUnfreezeFailed'))
      }
      return false
    }
  }

  // 健康检查
  const checkHealth = async (providerId) => {
    const loadingInstance = ElLoading.service({
      lock: true,
      text: t('admin.providers.healthChecking'),
      background: 'rgba(0, 0, 0, 0.7)'
    })

    try {
      const response = await checkProviderHealth(providerId)
      loadingInstance.close()

      if (response.code === 200) {
        const result = response.data
        const statusMessages = []

        if (result.apiStatus === 'online') {
          statusMessages.push('✅ ' + t('admin.providers.apiStatusOnline'))
        } else {
          statusMessages.push('❌ ' + t('admin.providers.apiStatusOffline') + ': ' + (result.apiError || t('common.unknown')))
        }

        if (result.sshStatus === 'online') {
          statusMessages.push('✅ ' + t('admin.providers.sshStatusOnline'))
        } else {
          statusMessages.push('❌ ' + t('admin.providers.sshStatusOffline') + ': ' + (result.sshError || t('common.unknown')))
        }

        ElMessageBox.alert(
          statusMessages.join('<br>'),
          t('admin.providers.healthCheckResult'),
          {
            dangerouslyUseHTMLString: true,
            type: (result.apiStatus === 'online' && result.sshStatus === 'online') ? 'success' : 'error'
          }
        )
      }

      return response
    } catch (error) {
      loadingInstance.close()
      const errorMsg = error?.response?.data?.msg || error?.message || t('admin.providers.healthCheckFailed')
      ElMessage.error(errorMsg)
      throw error
    }
  }

  // 自动配置API
  const autoConfigureAPIHandler = async (provider, force = false) => {
    try {
      const checkResponse = await autoConfigureProvider({
        providerId: provider.id,
        checkOnly: true
      })

      return checkResponse
    } catch (error) {
      const errorMsg = error?.response?.data?.msg || error?.message || t('admin.providers.autoConfigureFailed')
      ElMessage.error(errorMsg)
      throw error
    }
  }

  // 获取配置任务详情
  const getTaskDetail = async (taskId) => {
    try {
      const response = await getConfigurationTaskDetail(taskId)
      return response
    } catch (error) {
      const errorMsg = error?.response?.data?.msg || error?.message || t('admin.providers.getTaskDetailFailed')
      ElMessage.error(errorMsg)
      throw error
    }
  }

  // 选择变更处理
  const handleSelectionChange = (selection) => {
    selectedProviders.value = selection
  }

  return {
    loading,
    providers,
    selectedProviders,
    total,
    loadProviders,
    createProviderHandler,
    updateProviderHandler,
    deleteProviderHandler,
    batchDeleteProviders,
    batchFreezeProviders,
    batchHealthCheck,
    freezeProviderHandler,
    unfreezeProviderHandler,
    checkHealth,
    autoConfigureAPIHandler,
    getTaskDetail,
    handleSelectionChange
  }
}
