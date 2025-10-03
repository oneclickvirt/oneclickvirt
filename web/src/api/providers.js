import request from '@/utils/request'
import { createLongTimeoutRequest } from '@/utils/longTimeoutRequest'

// 创建实例操作专用请求实例（120秒超时）
const instanceOperationRequest = createLongTimeoutRequest(120000, {
  requestPrefix: 'provider_instance'
})

export function connectNodeApi(data) {
  return request({
    url: '/v1/providers/connect',
    method: 'post',
    data
  })
}

export function getSupportedProviders() {
  return request({
    url: '/v1/providers',
    method: 'get'
  })
}

// 改为使用Provider ID而不是type
export function getProviderStatusApi(providerId) {
  return request({
    url: `/v1/providers/${providerId}/status`,
    method: 'get'
  })
}

export function getProviderCapabilities(providerId) {
  return request({
    url: `/v1/providers/${providerId}/capabilities`,
    method: 'get'
  })
}

export const getInstancesApi = (providerId) => {
  return request({
    url: `/v1/providers/${providerId}/instances`,
    method: 'get'
  })
}

export const createInstanceApi = (providerId, data) => {
  return instanceOperationRequest({
    url: `/v1/providers/${providerId}/instances`,
    method: 'post',
    data
  })
}

export const getInstanceApi = (providerId, instanceName) => {
  return request({
    url: `/v1/providers/${providerId}/instances/${instanceName}`,
    method: 'get'
  })
}

export const startInstanceApi = (providerId, instanceName) => {
  return request({
    url: `/v1/providers/${providerId}/instances/${instanceName}/start`,
    method: 'post'
  })
}

export const stopInstanceApi = (providerId, instanceName) => {
  return request({
    url: `/v1/providers/${providerId}/instances/${instanceName}/stop`,
    method: 'post'
  })
}

export const deleteInstanceApi = (providerId, instanceName) => {
  return instanceOperationRequest({
    url: `/v1/providers/${providerId}/instances/${instanceName}`,
    method: 'delete'
  })
}

export const getImagesApi = (providerId) => {
  return request({
    url: `/v1/providers/${providerId}/images`,
    method: 'get'
  })
}

export const pullImageApi = (providerId, data) => {
  return request({
    url: `/v1/providers/${providerId}/images/pull`,
    method: 'post',
    data,
    timeout: 100000
  })
}

export const deleteImageApi = (providerId, imageName) => {
  return request({
    url: `/v1/providers/${providerId}/images/${imageName}`,
    method: 'delete'
  })
}