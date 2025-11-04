import { defineStore } from 'pinia'

export const useSSHStore = defineStore('ssh', {
  state: () => ({
    // SSH连接状态
    connections: {} // { instanceId: { visible, minimized, instanceName } }
  }),
  
  getters: {
    // 获取所有最小化的连接
    minimizedConnections: (state) => {
      return Object.entries(state.connections)
        .filter(([_, conn]) => conn.minimized)
        .map(([instanceId, conn]) => ({
          instanceId,
          ...conn
        }))
    },
    
    // 检查实例是否有活动连接
    hasConnection: (state) => (instanceId) => {
      return !!state.connections[instanceId]
    },
    
    // 获取特定实例的连接状态
    getConnection: (state) => (instanceId) => {
      return state.connections[instanceId] || null
    }
  },
  
  actions: {
    // 创建SSH连接
    createConnection(instanceId, instanceName) {
      this.connections[instanceId] = {
        visible: true,
        minimized: false,
        instanceName,
        createdAt: Date.now()
      }
    },
    
    // 显示SSH对话框
    showConnection(instanceId) {
      if (this.connections[instanceId]) {
        this.connections[instanceId].visible = true
        this.connections[instanceId].minimized = false
      }
    },
    
    // 最小化SSH连接
    minimizeConnection(instanceId) {
      if (this.connections[instanceId]) {
        this.connections[instanceId].visible = false
        this.connections[instanceId].minimized = true
      }
    },
    
    // 关闭SSH连接
    closeConnection(instanceId) {
      delete this.connections[instanceId]
    },
    
    // 切换连接可见性
    toggleConnection(instanceId) {
      if (this.connections[instanceId]) {
        const conn = this.connections[instanceId]
        conn.visible = !conn.visible
        conn.minimized = !conn.minimized
      }
    }
  },
  
  // 持久化配置
  persist: {
    enabled: false // 不持久化SSH连接状态，避免刷新后尝试恢复已断开的连接
  }
})
