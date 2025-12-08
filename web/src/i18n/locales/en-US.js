// English (US) Language Pack - Modular Structure
// 公共模块
import common from './en-US/common.js'
import navbar from './en-US/navbar.js'
import validation from './en-US/validation.js'
import message from './en-US/message.js'

// 认证模块  
import login from './en-US/auth/login.js'
import adminLogin from './en-US/auth/adminLogin.js'
import register from './en-US/auth/register.js'
import forgotPassword from './en-US/auth/forgotPassword.js'
import resetPassword from './en-US/auth/resetPassword.js'
import oauth2Callback from './en-US/auth/oauth2Callback.js'
import init from './en-US/auth/init.js'

// 公共页面模块
import home from './en-US/public/home.js'
import sidebar from './en-US/public/sidebar.js'
import notFound from './en-US/public/notFound.js'

// 用户模块
import userDashboard from './en-US/user/dashboard.js'
import userProfile from './en-US/user/profile.js'
import userInstances from './en-US/user/instances.js'
import userInstanceDetail from './en-US/user/instanceDetail.js'
import userTasks from './en-US/user/tasks.js'
import userTrafficOverview from './en-US/user/trafficOverview.js'
import userTraffic from './en-US/user/traffic.js'
import userResources from './en-US/user/resources.js'
import userApply from './en-US/user/apply.js'

// 管理员模块
import adminDashboard from './en-US/admin/dashboard.js'
import adminUsers from './en-US/admin/users.js'
import adminProviders from './en-US/admin/providers.js'
import adminConfig from './en-US/admin/config.js'
import adminAnnouncements from './en-US/admin/announcements.js'
import adminInviteCodes from './en-US/admin/inviteCodes.js'
import adminSystemImages from './en-US/admin/systemImages.js'
import adminInstances from './en-US/admin/instances.js'
import adminTasks from './en-US/admin/tasks.js'
import adminTraffic from './en-US/admin/traffic.js'
import adminPortMapping from './en-US/admin/portMapping.js'
import adminOauth2 from './en-US/admin/oauth2.js'
import adminPerformance from './en-US/admin/performance.js'

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
