import { createRouter, createWebHashHistory } from 'vue-router'
import { setupRouterGuards } from './guards'
import Layout from '@/view/layout/index.vue'

const routes = [
  {
    path: '/',
    name: 'Home',
    component: () => import('@/view/home/index.vue'),
    meta: {
      title: '首页',
      requiresAuth: false
    }
  },
  {
    path: '/home',
    name: 'HomePage',
    component: () => import('@/view/home/index.vue'),
    meta: {
      title: '首页',
      requiresAuth: false
    }
  },
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/view/login/index.vue'),
    meta: {
      title: '用户登录',
      requiresAuth: false
    }
  },
  {
    path: '/oauth2/callback',
    name: 'OAuth2Callback',
    component: () => import('@/view/oauth2-callback/index.vue'),
    meta: {
      title: 'OAuth2登录处理',
      requiresAuth: false
    }
  },
  {
    path: '/register',
    name: 'Register',
    component: () => import('@/view/register/index.vue'),
    meta: {
      title: '用户注册',
      requiresAuth: false
    }
  },
  {
    path: '/forgot-password',
    name: 'ForgotPassword',
    component: () => import('@/view/forgot-password/index.vue'),
    meta: {
      title: '找回密码',
      requiresAuth: false
    }
  },
  {
    path: '/admin/login',
    name: 'AdminLogin',
    component: () => import('@/view/admin/login/index.vue'),
    meta: {
      title: '管理员登录',
      requiresAuth: false
    }
  },
  {
    path: '/init',
    name: 'SystemInit',
    component: () => import('@/view/init/index.vue'),
    meta: {
      title: '系统初始化',
      requiresAuth: false
    }
  },
  {
    path: '/user',
    name: 'User',
    component: Layout,
    redirect: '/user/dashboard',
    meta: {
      requiresAuth: true,
      roles: ['user', 'admin']
    },
    children: [
      {
        path: 'dashboard',
        name: 'UserDashboard',
        component: () => import('@/view/user/dashboard/index.vue'),
        meta: {
          title: '用户仪表盘',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'instances',
        name: 'UserInstances',
        component: () => import('@/view/user/instances/index.vue'),
        meta: {
          title: '我的实例',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'instances/:id',
        name: 'UserInstanceDetail',
        component: () => import('@/view/user/instances/detail.vue'),
        meta: {
          title: '实例详情',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'apply',
        name: 'UserApply',
        component: () => import('@/view/user/apply/index.vue'),
        meta: {
          title: '申请领取',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'tasks',
        name: 'UserTasks',
        component: () => import('@/view/user/tasks/index.vue'),
        meta: {
          title: '任务列表',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'profile',
        name: 'UserProfile',
        component: () => import('@/view/user/profile/index.vue'),
        meta: {
          title: '个人中心',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      }
    ]
  },
  {
    path: '/admin',
    name: 'Admin',
    component: Layout,
    redirect: '/admin/dashboard',
    meta: {
      requiresAuth: true,
      roles: ['admin']
    },
    children: [
      {
        path: 'dashboard',
        name: 'AdminDashboard',
        component: () => import('@/view/admin/dashboard/index.vue'),
        meta: {
          title: '管理员仪表盘',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'users',
        name: 'AdminUsers',
        component: () => import('@/view/admin/users/index.vue'),
        meta: {
          title: '用户管理',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'invite-codes',
        name: 'AdminInviteCodes',
        component: () => import('@/view/admin/invite-codes/index.vue'),
        meta: {
          title: '邀请码管理',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'providers',
        name: 'AdminProviders',
        component: () => import('@/view/admin/providers/index.vue'),
        meta: {
          title: '节点管理',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'tasks',
        name: 'AdminTasks',
        component: () => import('@/view/admin/tasks/index.vue'),
        meta: {
          title: '任务管理',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'instances',
        name: 'AdminInstances',
        component: () => import('@/view/admin/instances/index.vue'),
        meta: {
          title: '实例管理',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'port-mappings',
        name: 'AdminPortMappings',
        component: () => import('@/view/admin/portmapping/index.vue'),
        meta: {
          title: '端口映射管理',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'traffic',
        name: 'AdminTraffic',
        component: () => import('@/view/admin/traffic/index.vue'),
        meta: {
          title: '流量管理',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'system-images',
        name: 'AdminSystemImages',
        component: () => import('@/view/admin/system-images/index.vue'),
        meta: {
          title: '系统镜像',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'announcements',
        name: 'AdminAnnouncements',
        component: () => import('@/view/admin/announcements/index.vue'),
        meta: {
          title: '公告管理',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'config',
        name: 'AdminConfig',
        component: () => import('@/view/admin/config/index.vue'),
        meta: {
          title: '系统配置',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'performance',
        name: 'AdminPerformance',
        component: () => import('@/view/admin/performance/index.vue'),
        meta: {
          title: '性能监控',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'oauth2-providers',
        name: 'AdminOAuth2Providers',
        component: () => import('@/view/admin/oauth2/index.vue'),
        meta: {
          title: 'OAuth2管理',
          requiresAuth: true,
          roles: ['admin']
        }
      }
    ]
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'NotFound',
    component: () => import('@/view/404/index.vue'),
    meta: {
      title: '页面不存在',
      requiresAuth: false
    }
  }
]

const router = createRouter({
  history: createWebHashHistory(import.meta.env.BASE_URL || '/'),
  routes
})
setupRouterGuards(router)
export default router