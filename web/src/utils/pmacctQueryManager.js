import { queryInstancePmacctData } from '@/api/user'

/**
 * Pmacct 查询管理器（简化版，pmacct查询是同步的）
 * 负责处理pmacct流量数据查询
 */
class PmacctQueryManager {
  constructor() {
    // pmacct查询是同步的，不需要轮询
  }

  /**
   * 执行 pmacct 查询（同步）
   * @param {number} instanceId 实例ID
   * @returns {Promise<Object>} 查询结果
   */
  async query(instanceId) {
    try {
      const response = await queryInstancePmacctData(instanceId)
      
      if (!response || !response.data) {
        throw new Error('查询pmacct数据失败')
      }

      return response.data
    } catch (error) {
      console.error('pmacct查询失败:', error)
      throw error
    }
  }
}

// 创建全局实例
export const pmacctQueryManager = new PmacctQueryManager()

// 导出类以便需要时创建新实例
export default PmacctQueryManager
