import { defineStore } from 'pinia'
import { login, adminLogin, getUserInfo, logout } from '@/api/auth'
import { ElMessage } from 'element-plus'

export const useUserStore = defineStore('user', {
  state: () => ({
    token: sessionStorage.getItem('token') || '',
    user: null,
    userType: sessionStorage.getItem('userType') || 'user',
    permissions: []
  }),

  getters: {
    isLoggedIn: (state) => !!state.token,
    isAdmin: (state) => state.userType === 'admin',
    isUser: (state) => state.userType === 'user',
    userInfo: (state) => state.user || {}
  },

  actions: {
    setToken(token) {
      this.token = token
      sessionStorage.setItem('token', token)
    },

    setUser(user) {
      this.user = user
      if (user.userType) {
        this.userType = user.userType
        sessionStorage.setItem('userType', user.userType)
      }
    },

    setPermissions(permissions) {
      this.permissions = permissions
    },

    async userLogin(loginForm) {
      try {
        // 添加必需的字段
        const loginData = {
          ...loginForm,
          loginType: 'username',
          userType: 'user'
        }
        const response = await login(loginData)
        if (response.code === 0 || response.code === 200) {
          this.setToken(response.data.token)
          // 使用服务器返回的实际用户类型，而不是硬编码
          const userType = response.data.user?.userType || 'user'
          this.setUser({ ...response.data.user, userType: userType })
          return { success: true }
        } else {
          return { success: false, message: response.msg }
        }
      } catch (error) {
        // 重新抛出错误，让上层错误处理器处理
        throw error
      }
    },

    async adminLogin(loginForm) {
      try {
        // 添加必需的字段
        const loginData = {
          ...loginForm,
          loginType: 'username',
          userType: 'admin'
        }
        const response = await adminLogin(loginData)
        if (response.code === 0 || response.code === 200) {
          this.setToken(response.data.token)
          // 使用服务器返回的实际用户类型，确保是admin
          const userType = response.data.user?.userType || 'admin'
          this.setUser({ ...response.data.user, userType: userType })
          return { success: true }
        } else {
          return { success: false, message: response.msg }
        }
      } catch (error) {
        // 重新抛出错误，让上层错误处理器处理
        throw error
      }
    },

    async fetchUserInfo() {
      try {
        const response = await getUserInfo()
        if (response.code === 0 || response.code === 200) {
          const currentUserType = this.userType
          console.log('获取用户信息成功，当前用户类型:', currentUserType)
          console.log('API返回的数据结构:', response.data)
          
          // 从 response.data.user 中获取用户类型，如果不存在则使用当前类型
          const userType = response.data.user?.userType || response.data.userType || currentUserType
          console.log('解析到的用户类型:', userType)
          
          // 合并用户信息，确保包含 userType
          const userData = {
            ...response.data.user,
            ...response.data,
            userType: userType
          }
          
          this.setUser(userData)
          console.log('设置用户信息后的用户类型:', this.userType)
          
          return { success: true }
        } else {
          return { success: false, message: response.msg }
        }
      } catch (error) {
        console.error('获取用户信息失败:', error)
        return { success: false, message: '获取用户信息失败' }
      }
    },

    // 退出登录
    async logout() {
      try {
        await logout()
      } catch (error) {
        console.error('Logout API error:', error)
      } finally {
        this.clearUserData()
      }
    },

    // 检查用户状态（当遇到权限错误时调用）
    async checkUserStatus() {
      if (!this.token) {
        this.clearUserData()
        return false
      }

      try {
        const response = await getUserInfo()
        if (response.code === 0 || response.code === 200) {
          // 检查用户状态是否发生变化
          const newUserType = response.data.user?.userType || response.data.userType || 'user'
          if (newUserType !== this.userType) {
            console.log('用户权限发生变化:', this.userType, '->', newUserType)
            this.setUser({ ...response.data.user, userType: newUserType })
          }
          return true
        } else {
          // 用户信息获取失败，清除本地数据
          this.clearUserData()
          return false
        }
      } catch (error) {
        console.error('检查用户状态失败:', error)
        // 如果是401错误，说明Token已失效
        if (error.response?.status === 401) {
          this.clearUserData()
          return false
        }
        return true // 其他错误不清除用户数据
      }
    },

    // 清除用户数据
    clearUserData() {
      this.token = ''
      this.user = null
      this.userType = 'user'
      this.permissions = []
      sessionStorage.removeItem('token')
      sessionStorage.removeItem('userType')
      // 同时清除localStorage中的token，防止残留
      localStorage.removeItem('token')
      localStorage.removeItem('username')
    },

    // 检查权限
    hasPermission(permission) {
      if (this.isAdmin) return true
      return this.permissions.includes(permission)
    },

    // 检查角色
    hasRole(role) {
      return this.userType === role
    },

    // 获取用户头像
    getUserAvatar() {
      if (this.user?.avatar) {
        return this.user.avatar
      }
      // 默认头像
      return `https://api.dicebear.com/7.x/initials/svg?seed=${this.user?.username || 'User'}`
    },

    // 获取用户显示名称
    getUserDisplayName() {
      return this.user?.nickname || this.user?.username || '用户'
    },

    // 获取用户类型显示文本
    getUserTypeText() {
      switch (this.userType) {
        case 'admin':
          return '管理员'
        case 'user':
          return '用户'
        default:
          return '未知'
      }
    }
  }
})