import axios from 'axios'
import { useUserStore } from '@/pinia/modules/user'
import i18n from '@/i18n'

/**
 * 创建长时间请求的axios实例
 * @param {number} timeout 超时时间（毫秒）
 * @param {object} options 额外配置选项
 * @returns {object} axios实例
 */
export const createLongTimeoutRequest = (timeout = 60000, options = {}) => {
  const service = axios.create({
    baseURL: import.meta.env.VITE_BASE_API,
    timeout,
    headers: {
      'Content-Type': 'application/json',
      ...options.headers
    },
    ...options
  })

  // 请求拦截器
  service.interceptors.request.use(
    config => {
      const userStore = useUserStore()
      
      if (userStore.token) {
        config.headers.Authorization = `Bearer ${userStore.token}`
      }
      
      config.headers['X-Request-ID'] = generateRequestId(options.requestPrefix || 'long')
      
      if (config.method === 'get') {
        config.params = {
          ...config.params,
          _t: Date.now()
        }
      }
      
      return config
    },
    error => {
      console.error('长时间请求拦截器错误:', error)
      return Promise.reject(error)
    }
  )

  // 响应拦截器
  service.interceptors.response.use(
    response => {
      const res = response.data
      
      if (response.headers['content-type']?.includes('application/octet-stream')) {
        return response
      }
      
      if (res.code !== undefined) {
        if (res.code === 0 || res.code === 200) {
          return res
        } else {
          return Promise.reject(new Error(res.msg || i18n.global.t('common.requestFailed')))
        }
      }
      
      return response
    },
    error => {
      // 处理401认证过期 - 与主请求工具保持一致
      if (error.response?.status === 401) {
        const userStore = useUserStore()
        userStore.ClearStorage()
        window.location.href = '/login'
        return Promise.reject(error)
      }
      return Promise.reject(error)
    }
  )

  return service
}

/**
 * 生成请求ID
 * @param {string} prefix 前缀
 * @returns {string} 请求ID
 */
function generateRequestId(prefix = 'req') {
  return prefix + '_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9)
}

/**
 * 健康检查专用请求实例（60秒超时）
 */
export const healthCheckRequest = createLongTimeoutRequest(60000, {
  requestPrefix: 'health'
})

/**
 * 文件上传专用请求实例（120秒超时）
 */
export const fileUploadRequest = createLongTimeoutRequest(120000, {
  requestPrefix: 'upload'
})

/**
 * 导出操作专用请求实例（180秒超时）
 */
export const exportRequest = createLongTimeoutRequest(180000, {
  requestPrefix: 'export'
})

export default createLongTimeoutRequest
