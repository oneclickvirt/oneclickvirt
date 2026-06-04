import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '@/pinia/modules/user'
import { errorHandler } from './errorHandler'
import router from '@/router'

const service = axios.create({
  baseURL: import.meta.env.VITE_BASE_API,
  timeout: 6000, // 恢复原来的6秒全局超时
  headers: {
    'Content-Type': 'application/json'
  }
})

function createNormalizedError(errorInfo, response, originalError) {
  const normalizedError = new Error(errorInfo.message)
  normalizedError.code = errorInfo.code
  normalizedError.status = response?.status
  normalizedError.details = errorInfo.details
  normalizedError.response = response
  normalizedError.originalError = originalError
  return normalizedError
}

function generateRequestId() {
  return 'req_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9)
}

service.interceptors.request.use(
  config => {
    const userStore = useUserStore()
    
    if (userStore.token) {
      config.headers.Authorization = `Bearer ${userStore.token}`
    }
    
    config.headers['X-Request-ID'] = generateRequestId()
    
    if (config.method === 'get') {
      config.params = {
        ...config.params,
        _t: Date.now()
      }
    }
    
    return config
  },
  error => {
    console.error('请求拦截器错误:', error)
    return Promise.reject(error)
  }
)

service.interceptors.response.use(
  response => {
    const res = response.data
    
    if (response.headers['content-type']?.includes('application/octet-stream')) {
      return response
    }
    
    if (res.code !== undefined) {
      if (res.code === 200) {
        return res
      } else {
        // 使用统一错误处理，但不自动显示错误消息
        const errorInfo = errorHandler.handleApiError({
          response: {
            data: res,
            status: response.status
          }
        }, {
          showMessage: false, // 不自动显示错误消息，让组件自己处理
          autoRedirect: false // 不自动重定向
        })
        return Promise.reject(createNormalizedError(errorInfo, {
          ...response,
          status: response.status === 200 ? res.code : (response.status || res.code),
          data: res
        }, response))
      }
    }
    
    return response
  },
  async error => {
    const config = error.config
    
    // 重试逻辑
    if (config && config.retry && config.__retryCount < config.retry) {
      config.__retryCount = config.__retryCount || 0
      config.__retryCount += 1
      
      const delay = config.retryDelay || 1000
      await new Promise(resolve => setTimeout(resolve, delay))
      
      return service(config)
    }
    
    // 使用统一错误处理，但不自动显示错误消息
    const errorInfo = errorHandler.handleApiError(error, {
      showMessage: false, // 不自动显示错误消息，让组件自己处理
      autoRedirect: false // 不自动重定向
    })
    return Promise.reject(createNormalizedError(errorInfo, error.response, error))
  }
)

export const request = service

export const get = (url, params, config = {}) => {
  return service({
    method: 'get',
    url,
    params,
    ...config
  })
}

export const post = (url, data, config = {}) => {
  return service({
    method: 'post',
    url,
    data,
    ...config
  })
}

export const put = (url, data, config = {}) => {
  return service({
    method: 'put',
    url,
    data,
    ...config
  })
}

export const del = (url, config = {}) => {
  return service({
    method: 'delete',
    url,
    ...config
  })
}

export const upload = (url, formData, config = {}) => {
  return service({
    method: 'post',
    url,
    data: formData,
    headers: {
      'Content-Type': 'multipart/form-data'
    },
    ...config
  })
}

export const download = (url, params, config = {}) => {
  return service({
    method: 'get',
    url,
    params,
    responseType: 'blob',
    ...config
  })
}

export default service
