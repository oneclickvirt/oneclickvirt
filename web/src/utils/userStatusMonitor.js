import { useUserStore } from '@/pinia/modules/user'
import { ElMessage } from 'element-plus'
import router from '@/router'

class UserStatusMonitor {
  constructor() {
    this.checkInterval = null
    this.isChecking = false
    this.checkIntervalTime = 5 * 60 * 1000 // 5分钟检查一次
    this.lastCheckTime = 0
  }

  /**
   * 开始监控用户状态
   */
  startMonitoring() {
    if (this.checkInterval) {
      return
    }

    // 立即检查一次
    this.checkUserStatus()

    // 设置定期检查
    this.checkInterval = setInterval(() => {
      this.checkUserStatus()
    }, this.checkIntervalTime)

    console.log('用户状态监控已启动')
  }

  /**
   * 停止监控用户状态
   */
  stopMonitoring() {
    if (this.checkInterval) {
      clearInterval(this.checkInterval)
      this.checkInterval = null
      console.log('用户状态监控已停止')
    }
  }

  /**
   * 检查用户状态
   */
  async checkUserStatus() {
    if (this.isChecking) {
      return
    }

    const userStore = useUserStore()
    
    // 如果没有Token，停止监控
    if (!userStore.token) {
      this.stopMonitoring()
      return
    }

    // 避免频繁检查
    const now = Date.now()
    if (now - this.lastCheckTime < 30000) { // 30秒内不重复检查
      return
    }

    this.isChecking = true
    this.lastCheckTime = now

    try {
      const isValid = await userStore.checkUserStatus()
      
      if (!isValid) {
        // 用户状态无效，停止监控并重定向
        this.stopMonitoring()
        
        // 只有当前不在首页或登录页时才重定向
        const currentPath = router.currentRoute.value.path
        if (!['/home', '/login', '/register', '/forgot-password'].includes(currentPath)) {
          ElMessage.warning('您的登录状态已失效，请重新登录')
          router.push('/home')
        }
      }
    } catch (error) {
      // 检查失败时判断错误类型
      console.warn('用户状态检查失败:', error)
      
      // 如果是认证错误(401)，停止监控并清除数据
      if (error.response?.status === 401) {
        this.stopMonitoring()
        userStore.clearUserData()
        
        const currentPath = router.currentRoute.value.path
        if (!['/home', '/login', '/register', '/forgot-password'].includes(currentPath)) {
          ElMessage.warning('您的登录状态已失效，请重新登录')
          router.push('/home')
        }
      }
      // 其他错误可能是网络问题，不立即停止监控
    } finally {
      this.isChecking = false
    }
  }

  /**
   * 手动触发用户状态检查（用于关键操作前）
   */
  async forceCheckUserStatus() {
    this.lastCheckTime = 0 // 重置检查时间，强制检查
    await this.checkUserStatus()
  }

  /**
   * 重新启动监控（用于用户重新登录后）
   */
  restart() {
    this.stopMonitoring()
    this.startMonitoring()
  }
}

// 导出单例实例
export const userStatusMonitor = new UserStatusMonitor()

// 自动启动监控（当有Token时）
export function initUserStatusMonitor() {
  const userStore = useUserStore()
  
  // 监听用户登录状态变化
  userStore.$subscribe((mutation, state) => {
    if (state.token) {
      // 用户登录，启动监控
      userStatusMonitor.startMonitoring()
    } else {
      // 用户登出，停止监控
      userStatusMonitor.stopMonitoring()
    }
  })

  // 如果当前已有Token，立即启动监控
  if (userStore.token) {
    userStatusMonitor.startMonitoring()
  }

  // 监听页面可见性变化，页面重新可见时检查用户状态
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden && userStore.token) {
      userStatusMonitor.forceCheckUserStatus()
    }
  })

  // 监听窗口焦点变化，窗口重新获得焦点时检查用户状态
  window.addEventListener('focus', () => {
    if (userStore.token) {
      userStatusMonitor.forceCheckUserStatus()
    }
  })
}
