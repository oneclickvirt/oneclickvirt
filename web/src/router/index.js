import { createRouter, createWebHashHistory } from 'vue-router'
import { setupRouterGuards } from './guards'
import Layout from '@/view/layout/index.vue'

const routes = [
  {
    path: '/',
    name: 'Home',
    component: () => import('@/view/home/index.vue'),
    meta: {
      title: 'sidebar.dashboard',
      requiresAuth: false
    }
  },
  {
    path: '/home',
    name: 'HomePage',
    component: () => import('@/view/home/index.vue'),
    meta: {
      title: 'sidebar.dashboard',
      requiresAuth: false
    }
  },
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/view/login/index.vue'),
    meta: {
      title: 'login.title',
      requiresAuth: false
    }
  },
  {
    path: '/oauth2/callback',
    name: 'OAuth2Callback',
    component: () => import('@/view/oauth2-callback/index.vue'),
    meta: {
      title: 'oauth2Callback.title',
      requiresAuth: false
    }
  },
  {
    path: '/register',
    name: 'Register',
    component: () => import('@/view/register/index.vue'),
    meta: {
      title: 'register.title',
      requiresAuth: false
    }
  },
  {
    path: '/forgot-password',
    name: 'ForgotPassword',
    component: () => import('@/view/forgot-password/index.vue'),
    meta: {
      title: 'forgotPassword.title',
      requiresAuth: false
    }
  },
  {
    path: '/admin/login',
    name: 'AdminLogin',
    component: () => import('@/view/admin/login/index.vue'),
    meta: {
      title: 'adminLogin.title',
      requiresAuth: false
    }
  },
  {
    path: '/init',
    name: 'SystemInit',
    component: () => import('@/view/init/index.vue'),
    meta: {
      title: 'init.title',
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
          title: 'sidebar.dashboard',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'instances',
        name: 'UserInstances',
        component: () => import('@/view/user/instances/index.vue'),
        meta: {
          title: 'sidebar.myInstances',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'instances/:id',
        name: 'UserInstanceDetail',
        component: () => import('@/view/user/instances/detail.vue'),
        meta: {
          title: 'common.instanceDetail',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'apply',
        name: 'UserApply',
        component: () => import('@/view/user/apply/index.vue'),
        meta: {
          title: 'sidebar.apply',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'tasks',
        name: 'UserTasks',
        component: () => import('@/view/user/tasks/index.vue'),
        meta: {
          title: 'sidebar.taskList',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'profile',
        name: 'UserProfile',
        component: () => import('@/view/user/profile/index.vue'),
        meta: {
          title: 'sidebar.personalCenter',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'domain',
        name: 'UserDomain',
        component: () => import('@/view/user/domain/index.vue'),
        meta: {
          title: 'sidebar.domainBinding',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'kyc',
        name: 'UserKYC',
        component: () => import('@/view/user/kyc/index.vue'),
        meta: {
          title: 'sidebar.kycVerification',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'checkin',
        name: 'UserCheckin',
        component: () => import('@/view/user/checkin/index.vue'),
        meta: {
          title: 'sidebar.checkinRenewal',
          requiresAuth: true,
          roles: ['user', 'admin']
        }
      },
      {
        path: 'api-tokens',
        name: 'UserApiTokens',
        component: () => import('@/view/user/api-tokens/index.vue'),
        meta: {
          title: 'sidebar.apiTokenManagement',
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
          title: 'sidebar.dashboard',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'users',
        name: 'AdminUsers',
        component: () => import('@/view/admin/users/index.vue'),
        meta: {
          title: 'sidebar.userManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'invite-codes',
        name: 'AdminInviteCodes',
        component: () => import('@/view/admin/invite-codes/index.vue'),
        meta: {
          title: 'sidebar.inviteCodeManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'redemption-codes',
        name: 'AdminRedemptionCodes',
        component: () => import('@/view/admin/redemption-codes/index.vue'),
        meta: {
          title: 'sidebar.redemptionCodeManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'providers',
        name: 'AdminProviders',
        component: () => import('@/view/admin/providers/index.vue'),
        meta: {
          title: 'sidebar.providerManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'group',
        name: 'AdminGroup',
        component: () => import('@/view/admin/group/index.vue'),
        meta: {
          title: 'sidebar.groupManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'tasks',
        name: 'AdminTasks',
        component: () => import('@/view/admin/tasks/index.vue'),
        meta: {
          title: 'sidebar.taskManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'instances',
        name: 'AdminInstances',
        component: () => import('@/view/admin/instances/index.vue'),
        meta: {
          title: 'sidebar.instanceManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'port-mappings',
        name: 'AdminPortMappings',
        component: () => import('@/view/admin/portmapping/index.vue'),
        meta: {
          title: 'sidebar.portManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'traffic',
        name: 'AdminTraffic',
        component: () => import('@/view/admin/traffic/index.vue'),
        meta: {
          title: 'sidebar.trafficManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'system-images',
        name: 'AdminSystemImages',
        component: () => import('@/view/admin/system-images/index.vue'),
        meta: {
          title: 'sidebar.systemImages',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'announcements',
        name: 'AdminAnnouncements',
        component: () => import('@/view/admin/announcements/index.vue'),
        meta: {
          title: 'sidebar.announcementManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'block-rules',
        name: 'AdminBlockRules',
        component: () => import('@/view/admin/block-rules/index.vue'),
        meta: {
          title: 'sidebar.blockRulesManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'config',
        name: 'AdminConfig',
        component: () => import('@/view/admin/config/index.vue'),
        meta: {
          title: 'sidebar.systemConfiguration',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'performance',
        name: 'AdminPerformance',
        component: () => import('@/view/admin/performance/index.vue'),
        meta: {
          title: 'sidebar.performanceMonitoring',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'logs',
        name: 'AdminLogs',
        component: () => import('@/view/admin/logs/index.vue'),
        meta: {
          title: 'sidebar.logViewer',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'oauth2-providers',
        name: 'AdminOAuth2Providers',
        component: () => import('@/view/admin/oauth2/index.vue'),
        meta: {
          title: 'sidebar.oauth2Management',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'domain',
        name: 'AdminDomain',
        component: () => import('@/view/admin/domain/index.vue'),
        meta: {
          title: 'sidebar.domainManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'kyc',
        name: 'AdminKYC',
        component: () => import('@/view/admin/kyc/index.vue'),
        meta: {
          title: 'sidebar.kycManagement',
          requiresAuth: true,
          roles: ['admin']
        }
      },
      {
        path: 'api-tokens',
        name: 'AdminApiTokens',
        component: () => import('@/view/admin/api-tokens/index.vue'),
        meta: {
          title: 'sidebar.adminApiTokenManagement',
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
      title: 'notFound.title',
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