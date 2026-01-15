import { useUserStore } from '@/pinia/modules/user'
import { checkSystemInit } from '@/api/init'
import { ElMessage } from 'element-plus'
import i18n from '@/i18n'
import NProgress from 'nprogress'
import 'nprogress/nprogress.css'

NProgress.configure({ showSpinner: false })

export function setupRouterGuards(router) {
  // 定义白名单（放在最前面，供所有逻辑使用）
  const whiteList = ['/home', '/login', '/register', '/forgot-password', '/init', '/admin/login']
  
  router.beforeEach(async (to, from, next) => {
    NProgress.start()
    
    const userStore = useUserStore()
    
    // 检查URL参数中是否有OAuth2 token（避免跨域localStorage隔离问题）
    const urlParams = new URLSearchParams(window.location.search)
    const oauth2Token = urlParams.get('oauth2_token')
    const oauth2Username = urlParams.get('username')
    
    if (oauth2Token) {
      console.log('从URL参数中检测到OAuth2 token，开始处理...')
      
      // 保存token到localStorage和sessionStorage
      localStorage.setItem('token', oauth2Token)
      sessionStorage.setItem('token', oauth2Token)
      userStore.setToken(oauth2Token)
      
      if (oauth2Username) {
        localStorage.setItem('username', oauth2Username)
      }
      
      // 清理URL参数（避免token暴露在URL中）
      const cleanURL = window.location.pathname + window.location.hash
      window.history.replaceState({}, document.title, cleanURL)
      
      // 获取用户信息
      try {
        console.log('正在获取用户信息...')
        await userStore.fetchUserInfo()
        
        console.log('OAuth2登录成功，用户信息已加载:', {
          user: userStore.user,
          userType: userStore.userType,
          viewMode: userStore.viewMode,
          userInfo: userStore.userInfo
        })
        
        // 根据用户类型和视图模式跳转到相应页面
        const userType = userStore.userType
        const viewMode = userStore.viewMode || userType
        console.log('OAuth2登录完成，用户类型:', userType, '视图模式:', viewMode)
        
        // 只有管理员可以访问管理员界面，且只有管理员可以切换视图
        if (userType === 'admin' && viewMode === 'admin') {
          next('/admin/dashboard')
          return
        } else {
          // 普通用户只能访问用户界面
          next('/user/dashboard')
          return
        }
      } catch (error) {
        console.error('OAuth2登录后获取用户信息失败:', error)
        // 清理无效token
        localStorage.removeItem('token')
        sessionStorage.removeItem('token')
        userStore.logout()
        next('/home')
        return
      }
    }
    
    console.log('路由守卫检查:', {
      to: to.path,
      from: from.path,
      hasUserStoreToken: !!userStore.token,
      hasLocalStorageToken: !!localStorage.getItem('token'),
      hasUser: !!userStore.user,
      userType: userStore.userType
    })
    
    // OAuth2登录后token处理：如果localStorage有token但userStore没有，则同步
    if (!userStore.token && localStorage.getItem('token')) {
      console.log('检测到localStorage有token但userStore没有，开始同步...')
      const storedToken = localStorage.getItem('token')
      userStore.setToken(storedToken)
      sessionStorage.setItem('token', storedToken)
      
      // 获取用户信息
      try {
        console.log('正在获取用户信息...')
        await userStore.fetchUserInfo()
        
        // 清理OAuth2回调相关的localStorage（可选）
        localStorage.removeItem('username')
        
        console.log('OAuth2登录成功，用户信息已加载:', {
          user: userStore.user,
          userType: userStore.userType,
          viewMode: userStore.viewMode,
          userInfo: userStore.userInfo
        })
        
        // 如果在首页且已登录，根据用户类型和视图模式跳转
        if (to.path === '/' || to.path === '/home') {
          const userType = userStore.userType
          const viewMode = userStore.viewMode || userType
          console.log('从首页跳转，用户类型:', userType, '视图模式:', viewMode)
          
          // 只有管理员可以访问管理员界面
          if (userType === 'admin' && viewMode === 'admin') {
            next('/admin/dashboard')
            return
          } else {
            // 普通用户或管理员切换到用户视图时，进入用户界面
            next('/user/dashboard')
            return
          }
        }
        // 如果不是首页，继续正常流程，不要return
      } catch (error) {
        console.error('localStorage token失效，获取用户信息失败:', error)
        // 清理无效token（包括localStorage）
        userStore.clearUserData()
        
        // 如果当前在需要认证的页面，重定向到首页
        if (to.meta.requiresAuth || (!whiteList.includes(to.path) && to.path !== '/home')) {
          console.log('Token失效且访问受保护页面，重定向到首页')
          next('/home')
          return
        }
        
        // 如果当前在公开页面（如登录页、首页），则继续访问
        console.log('Token失效但访问公开页面，允许继续访问')
        // 继续正常流程，不要return，让后续逻辑处理
      }
    }
    
    // 重新获取token（在OAuth2同步后）
    const token = userStore.token || sessionStorage.getItem('token') || localStorage.getItem('token')
    console.log('最终token检查:', !!token)
    
    // 检查系统初始化状态（除了初始化页面本身）
    if (to.name !== 'SystemInit') {
      try {
        const response = await checkSystemInit()
        console.log('检查初始化状态响应:', response)
        if (response && response.code === 0 && response.data && response.data.needInit === true) {
          console.log('系统需要初始化，跳转到初始化页面')
          next({ path: '/init' })
          return
        }
      } catch (error) {
        console.error('检查系统初始化状态失败:', error)
        // 如果是网络错误或服务器错误，可能是数据库未初始化导致的
        if (error.message.includes('Network Error') || 
            error.response?.status >= 500 || 
            error.code === 'ECONNREFUSED') {
          console.warn('服务器连接失败，可能需要初始化，跳转到初始化页面')
          next({ path: '/init' })
          return
        }
        // 其他错误，允许继续访问，但在控制台记录错误
        console.warn('初始化检查失败，允许继续访问页面')
        // 不要阻塞，继续正常的路由逻辑
      }
    } else {
      // 如果已经在初始化页面，检查是否还需要初始化
      try {
        const response = await checkSystemInit()
        console.log('在初始化页面检查状态:', response)
        if (response && response.code === 0 && response.data && response.data.needInit === false) {
          console.log('系统已初始化，跳转到首页')
          next({ path: '/home' })
          return
        }
      } catch (error) {
        console.error('在初始化页面检查系统初始化状态失败:', error)
        // 如果检查失败，允许停留在初始化页面
        console.warn('初始化页面状态检查失败，允许停留在初始化页面')
      }
    }
    
    // whiteList 已在函数开头定义，这里不需要重复定义
    
    if (whiteList.includes(to.path)) {
      next()
      return
    }
    
    if (to.meta.requiresAuth || !whiteList.includes(to.path)) {
      if (!token) {
        console.log('需要认证但无token，跳转到首页')
        next('/home')
        return
      }
      
      // 检查用户信息和状态
      if (!userStore.user) {
        try {
          await userStore.fetchUserInfo()
        } catch (error) {
          console.error('获取用户信息失败:', error)
          userStore.logout()
          next('/home')
          return
        }
      } else {
        // 对于敏感操作页面，重新验证用户状态
        const sensitivePages = ['/admin/', '/user/settings', '/user/security']
        const isSensitivePage = sensitivePages.some(page => to.path.startsWith(page))
        
        if (isSensitivePage) {
          try {
            const isValid = await userStore.checkUserStatus()
            if (!isValid) {
              console.log('用户状态验证失败，跳转到首页')
              next('/home')
              return
            }
          } catch (error) {
            console.error('用户状态验证失败:', error)
            userStore.logout()
            next('/home')
            return
          }
        }
      }
      
      // 严格检查：普通用户不能访问管理员路由
      if (to.path.startsWith('/admin/') && userStore.userType !== 'admin') {
        console.log('普通用户尝试访问管理员页面，拒绝访问')
        ElMessage.warning(i18n.global.t('navbar.noPermission'))
        next('/user/dashboard')
        return
      }
      
      if (to.meta.roles && to.meta.roles.length > 0) {
        const userRole = userStore.userType
        // 管理员可以访问所有页面（包括用户页面）
        // 用户只能访问标记为 'user' 角色的页面
        const hasAccess = userRole === 'admin' || to.meta.roles.includes(userRole)
        
        if (!hasAccess) {
          console.log('用户角色不匹配，当前角色:', userRole, '需要角色:', to.meta.roles)
          // 根据用户类型跳转到相应的首页
          if (userRole === 'admin') {
            next('/admin/dashboard')
          } else if (userRole === 'user') {
            next('/user/dashboard')
          } else {
            next('/home')
          }
          return
        }
      }
    }
    
    if (to.path === '/' && token) {
      // 根据用户类型和视图模式跳转
      const userType = userStore.userType
      const viewMode = userStore.viewMode || userType
      
      // 只有管理员可以访问管理员界面
      if (userType === 'admin' && viewMode === 'admin') {
        next('/admin/dashboard')
        return
      } else {
        // 普通用户或管理员切换到用户视图时，进入用户界面
        next('/user/dashboard')
        return
      }
    } else if (to.path === '/' && !token) {
      next('/home')
      return
    }
    
    next()
  })
  
  router.afterEach((to, from) => {
    NProgress.done()
    document.title = to.meta.title ? `${to.meta.title} - OneClickVirt` : 'OneClickVirt'
    
    // 对于用户页面，确保每次导航都触发组件刷新
    if (to.path.startsWith('/user/') && from.path !== to.path) {
      // 延迟触发，确保组件已经挂载
      setTimeout(() => {
        window.dispatchEvent(new CustomEvent('force-page-refresh', { 
          detail: { path: to.path, from: from.path } 
        }))
      }, 50)
    }
  })
}