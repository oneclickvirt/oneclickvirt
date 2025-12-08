// 中文（简体）语言包 - 模块化结构
// 公共模块
import common from './zh-CN/common.js'
import navbar from './zh-CN/navbar.js'
import validation from './zh-CN/validation.js'
import message from './zh-CN/message.js'

// 认证模块
import login from './zh-CN/auth/login.js'
import adminLogin from './zh-CN/auth/adminLogin.js'
import register from './zh-CN/auth/register.js'
import forgotPassword from './zh-CN/auth/forgotPassword.js'
import resetPassword from './zh-CN/auth/resetPassword.js'
import oauth2Callback from './zh-CN/auth/oauth2Callback.js'
import init from './zh-CN/auth/init.js'

// 公共页面模块
import home from './zh-CN/public/home.js'
import sidebar from './zh-CN/public/sidebar.js'
import notFound from './zh-CN/public/notFound.js'

// 用户模块
import userDashboard from './zh-CN/user/dashboard.js'
import userProfile from './zh-CN/user/profile.js'
import userInstances from './zh-CN/user/instances.js'
import userInstanceDetail from './zh-CN/user/instanceDetail.js'
import userTasks from './zh-CN/user/tasks.js'
import userTrafficOverview from './zh-CN/user/trafficOverview.js'
import userTraffic from './zh-CN/user/traffic.js'
import userResources from './zh-CN/user/resources.js'
import userApply from './zh-CN/user/apply.js'

// 管理员模块
import adminDashboard from './zh-CN/admin/dashboard.js'
import adminUsers from './zh-CN/admin/users.js'
import adminProviders from './zh-CN/admin/providers.js'
import adminConfig from './zh-CN/admin/config.js'
import adminAnnouncements from './zh-CN/admin/announcements.js'
import adminInviteCodes from './zh-CN/admin/inviteCodes.js'
import adminSystemImages from './zh-CN/admin/systemImages.js'
import adminInstances from './zh-CN/admin/instances.js'
import adminTasks from './zh-CN/admin/tasks.js'
import adminTraffic from './zh-CN/admin/traffic.js'
import adminPortMapping from './zh-CN/admin/portMapping.js'
import adminOauth2 from './zh-CN/admin/oauth2.js'
import adminPerformance from './zh-CN/admin/performance.js'

export default {
  common,
  navbar,
  login,
  adminLogin,
  register,
  forgotPassword,
  resetPassword,
  oauth2Callback,
  init,
  home,
  sidebar,
  user: {
    dashboard: userDashboard,
    profile: userProfile,
    instances: userInstances,
    instanceDetail: userInstanceDetail,
    tasks: userTasks,
    trafficOverview: userTrafficOverview,
    traffic: userTraffic,
    resources: userResources,
    apply: userApply
  },
  admin: {
    dashboard: adminDashboard,
    users: adminUsers,
    providers: adminProviders,
    config: adminConfig,
    announcements: adminAnnouncements,
    inviteCodes: adminInviteCodes,
    systemImages: adminSystemImages,
    instances: adminInstances,
    tasks: adminTasks,
    traffic: adminTraffic,
    portMapping: adminPortMapping,
    oauth2: adminOauth2,
    performance: adminPerformance
  },
  validation,
  message,
  notFound
}
