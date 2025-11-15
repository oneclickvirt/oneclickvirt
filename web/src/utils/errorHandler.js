import { ElMessage, ElMessageBox } from 'element-plus'
import { ref, readonly } from 'vue'
import router from '@/router'
import { useUserStore } from '@/pinia/modules/user'

/**
 * 统一错误处理工具类
 */
export const errorHandler = {
  // 错误码映射 - 对应后端的错误码定义
  codeMap: {
    // 成功
    0: '操作成功',
    200: '操作成功',

    // 通用错误 1000-1999
    1000: '操作失败',
    1001: '请求参数错误',
    1002: '系统内部错误',
    1003: '未授权访问',
    1004: '禁止访问',
    1005: '资源不存在',
    1006: '资源冲突',
    1007: '数据验证失败',

    // 用户相关错误 2000-2999
    2001: '用户不存在',
    2002: '用户已存在',
    2003: '用户名或密码错误',
    2004: '用户已被禁用，有问题请联系管理员',
    2005: '用户权限不足',

    // 角色权限相关错误 3000-3999
    3001: '角色不存在',
    3002: '角色已存在',
    3003: '权限不足',
    3004: '无效的角色',
    3005: '角色正在使用中，无法删除',
    3006: '权限不存在',

    // 业务相关错误 4000-4999
    4001: '邀请码无效',
    4002: '邀请码已过期',
    4003: '邀请码已被使用',
    4004: '验证码错误',
    4005: '请提供验证码',
    4006: '令牌生成失败',
    4007: 'OAuth2认证失败',
    4008: 'OAuth2注册已达到限制',

    // 系统相关错误 5000-5999
    5001: '配置错误',
    5002: '数据库错误',
    5003: '缓存错误',
    5004: '外部API调用失败',
    5005: '请求体过大，请减少数据量'
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
    let message = '未知错误'
    let details = ''

    // 处理后端返回的业务错误
    if (error.response && error.response.data) {
      const { data } = error.response
      code = data.code || data.status || error.response.status
      message = data.message || data.msg || data.error || '请求失败'
      details = data.details || ''

      // 使用错误码映射获取标准错误消息
      const standardMessage = this.codeMap[code]
      if (standardMessage) {
        message = standardMessage
      }

      // 特殊错误码处理
      if (autoRedirect) {
        this.handleSpecialErrorCodes(code, message, details)
      }
    } 
    // 处理网络错误或其他错误
    else if (error.request) {
      code = -1
      message = '网络连接失败，请检查网络设置'
    } 
    // 处理请求配置错误
    else {
      code = -2
      message = '请求配置错误'
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
      case 1003: // 未授权访问
      case 401:
        // 检查错误消息，如果是Token被撤销，给出更明确的提示
        if (message && (message.includes('已失效') || message.includes('已撤销') || message.includes('revoked'))) {
          userStore.clearUserData()
          router.push('/login')
          ElMessage.warning('您的登录状态已失效，请重新登录')
        } else if (currentRoute.meta?.requiresAuth) {
          // 只有在需要认证的页面才处理认证错误
          userStore.clearUserData()
          router.push('/login')
          ElMessage.warning('登录已过期，请重新登录')
        }
        break

      case 1004: // 禁止访问
      case 2005: // 用户权限不足
      case 3003: // 权限不足
      case 403:
        ElMessage.error('您没有权限执行此操作')
        break

      case 2004: // 用户已被禁用
        // 用户被禁用时清除本地数据并跳转到登录页
        userStore.clearUserData()
        router.push('/login')
        ElMessage.error('您的账户已被禁用，请联系管理员')
        break

      case 5005: // 请求体过大
      case 413:
        ElMessage.error('上传的文件或数据过大，请减少文件大小或数据量')
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
  async showConfirmDialog(message, title = '确认操作', options = {}) {
    const {
      confirmButtonText = '确定',
      cancelButtonText = '取消',
      type = 'warning'
    } = options

    try {
      await ElMessageBox.confirm(message, title, {
        confirmButtonText,
        cancelButtonText,
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
  handleValidationErrors(errors, prefix = '表单验证失败') {
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
