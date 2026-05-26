import request from '@/utils/request'

export const getAdminDashboard = () => {
  return request({
    url: '/v1/admin/dashboard',
    method: 'get'
  })
}
