import request from '@/utils/request'

// 端口映射管理API
export const getPortMappings = (params) => {
  return request({
    url: '/v1/admin/port-mappings',
    method: 'get',
    params
  })
}

// 创建端口映射（仅支持手动添加单个端口，仅支持 LXD/Incus/PVE）
export const createPortMapping = (data) => {
  return request({
    url: '/v1/admin/port-mappings',
    method: 'post',
    data
  })
}

// 删除端口映射（仅支持删除手动添加的端口，区间映射的端口不能删除）
export const deletePortMapping = (id) => {
  return request({
    url: `/v1/admin/port-mappings/${id}`,
    method: 'delete'
  })
}

export const batchDeletePortMappings = (ids) => {
  return request({
    url: '/v1/admin/port-mappings/batch-delete',
    method: 'post',
    data: { ids }
  })
}

// 同步端口映射（检测并清理孤立的端口映射记录）
export const syncPortMappings = (data) => {
  return request({
    url: '/v1/admin/port-mappings/sync',
    method: 'post',
    data
  })
}

// 检查端口可用性
export const checkPortAvailable = (data) => {
  return request({
    url: '/v1/admin/ports/check',
    method: 'post',
    data
  })
}
