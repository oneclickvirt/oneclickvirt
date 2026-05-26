import { ElMessage, ElMessageBox } from 'element-plus'
import { ref, readonly } from 'vue'
import router from '@/router'
import { useUserStore } from '@/pinia/modules/user'
import i18n from '@/i18n'

const t = (...args) => i18n.global.t(...args)

/**
 * 统一错误处理工具类
 */
export const errorHandler = {
  // 错误码映射 - 统一使用HTTP状态码
  get codeMap() {
    return {
      // 成功
      200: t('common.success'),

      // HTTP标准错误码
      400: t('errors.validationFailed'),
      401: t('errors.unauthorized'),
      403: t('errors.forbidden'),
      404: t('errors.notFound'),
      409: t('errors.conflict'),
      413: t('common.requestTooLarge'),
      500: t('errors.internalError'),
      502: t('errors.externalApiFailed'),
      503: t('errors.databaseError')
    }
  },

  /**
   * 处理API响应错误
   * @param {Object} error - axios错误对象或自定义错误
   * @param {Object} options - 配置选项
   * @returns {Object} 处理后的错误信息
   */
  handleApiError(error, options = {}) {
    const {
      showMessage = true,        // 是否显示错误消息
      autoRedirect = true,       // 是否自动重定向
      customMessage = null,      // 自定义错误消息
      messageType = 'error'      // 消息类型
    } = options

    let code = -1
    let message = t('common.unknownError')
    let details = ''

    // 处理后端返回的业务错误
    if (error.response && error.response.data) {
      const { data } = error.response
      code = data.code || data.status || error.response.status
      // 优先使用后端返回的具体错误消息，仅在无具体消息时才使用通用码映射
      message = data.message || data.msg || data.error || this.codeMap[code] || t('errors.requestFailed')
      details = data.details || ''

      // 特殊错误码处理
      if (autoRedirect) {
        this.handleSpecialErrorCodes(code, message, details)
      }
    } 
    // 处理网络错误或其他错误
    else if (error.request) {
      code = -1
      message = t('errors.networkError')
    } 
    // 处理请求配置错误
    else {
      code = -2
      message = t('errors.requestConfigError')
    }

    // 使用自定义消息（如果提供）
    const finalMessage = customMessage || message
    const finalDetails = details ? `: ${details}` : ''

    // 显示错误消息
    if (showMessage) {
      this.showErrorMessage(finalMessage + finalDetails, messageType)
    }

    return {
      code,
      message: finalMessage,
      details,
      originalError: error
    }
  },

  /**
   * 处理特殊错误码（需要特殊处理的错误）
   * @param {number} code - 错误码
   * @param {string} message - 错误消息
   * @param {string} details - 错误详情
   */
  handleSpecialErrorCodes(code, message, details) {
    const userStore = useUserStore()
    const currentRoute = router.currentRoute.value

    switch (code) {
      case 401: // 未授权访问
        // 检查错误消息，如果是Token被撤销，给出更明确的提示
        if (message && (message.includes('已失效') || message.includes('已撤销') || message.includes('revoked') || message.includes('expired') || message.includes('invalidated') || message.includes('被禁用'))) {
          userStore.clearUserData()
          router.push('/login')
          ElMessage.warning(t('common.loginInvalid'))
        } else if (currentRoute.meta?.requiresAuth) {
          // 只有在需要认证的页面才处理认证错误
          userStore.clearUserData()
          router.push('/login')
          ElMessage.warning(t('common.loginExpired'))
        }
        break

      case 403: // 禁止访问
        ElMessage.error(t('common.noPermission'))
        break

      case 413: // 请求体过大
        ElMessage.error(t('common.requestTooLarge'))
        break

      default:
        // 其他错误码不需要特殊处理
        break
    }
  },

  /**
   * 显示错误消息
   * @param {string} message - 错误消息
   * @param {string} type - 消息类型
   */
  showErrorMessage(message, type = 'error') {
    switch (type) {
      case 'warning':
        ElMessage.warning(message)
        break
      case 'info':
        ElMessage.info(message)
        break
      case 'success':
        ElMessage.success(message)
        break
      case 'error':
      default:
        ElMessage.error(message)
        break
    }
  },

  /**
   * 显示确认对话框（用于危险操作）
   * @param {string} message - 确认消息
   * @param {string} title - 对话框标题
   * @param {Object} options - 配置选项
   * @returns {Promise} 确认结果
   */
  async showConfirmDialog(message, title, options = {}) {
    if (!title) title = t('common.confirm')
    const {
      confirmButtonText,
      cancelButtonText,
      type = 'warning'
    } = options

    try {
      await ElMessageBox.confirm(message, title, {
        confirmButtonText: confirmButtonText || t('common.confirm'),
        cancelButtonText: cancelButtonText || t('common.cancel'),
        type
      })
      return true
    } catch (error) {
      return false
    }
  },

  /**
   * 处理表单验证错误
   * @param {Object} errors - 验证错误对象
   * @param {string} prefix - 错误消息前缀
   */
  handleValidationErrors(errors, prefix) {
    if (!prefix) prefix = t('errors.validationFailed')
    if (!errors || typeof errors !== 'object') {
      ElMessage.error(prefix)
      return
    }

    const errorMessages = Object.entries(errors).map(([field, messages]) => {
      const fieldMessages = Array.isArray(messages) ? messages : [messages]
      return `${field}: ${fieldMessages.join(', ')}`
    })

    const fullMessage = `${prefix}: ${errorMessages.join('; ')}`
    ElMessage.error(fullMessage)
  },

  /**
   * 包装async函数，自动处理错误
   * @param {Function} asyncFn - 异步函数
   * @param {Object} options - 错误处理选项
   * @returns {Function} 包装后的函数
   */
  wrapAsyncFunction(asyncFn, options = {}) {
    return async (...args) => {
      try {
        const result = await asyncFn(...args)
        return { success: true, data: result, error: null }
      } catch (error) {
        const errorInfo = this.handleApiError(error, options)
        return { success: false, data: null, error: errorInfo }
      }
    }
  },

  /**
   * 创建带错误处理的composable
   * @param {Function} apiFunction - API函数
   * @param {Object} options - 配置选项
   * @returns {Object} 包含loading状态和执行函数的对象
   */
  createErrorHandledComposable(apiFunction, options = {}) {
    const loading = ref(false)
    const error = ref(null)
    const data = ref(null)

    const execute = async (...args) => {
      loading.value = true
      error.value = null

      try {
        const result = await apiFunction(...args)
        data.value = result
        return { success: true, data: result }
      } catch (err) {
        const errorInfo = this.handleApiError(err, options)
        error.value = errorInfo
        return { success: false, error: errorInfo }
      } finally {
        loading.value = false
      }
    }

    return {
      loading: readonly(loading),
      error: readonly(error),
      data: readonly(data),
      execute
    }
  }
}

// 默认导出
export default errorHandler
