<template>
  <div
    class="sidebar-container"
    :class="{ 
      'is-collapse': isCollapse && !isMobile,
      'mobile': isMobile
    }"
  >
    <div class="sidebar-logo">
      <img
        v-show="!isCollapse || isMobile"
        :src="siteStore.logoSrc"
        alt="Logo"
        class="sidebar-logo-img"
      >
      <h1 v-show="!isCollapse || isMobile">
        {{ siteStore.displaySiteName }}
      </h1>
      <el-button 
        v-if="!isMobile"
        class="collapse-btn" 
        :icon="isCollapse ? Expand : Fold" 
        size="small" 
        circle 
        @click="toggleCollapse" 
      />
    </div>
    <el-scrollbar wrap-class="scrollbar-wrapper">
      <el-menu
        :default-active="activeMenu"
        :collapse="isCollapse && !isMobile"
        :unique-opened="false"
        :collapse-transition="false"
        mode="vertical"
        active-text-color="#16a34a"
        @select="handleMenuSelect"
      >
        <!-- 首页链接 - 仅在未登录时显示 -->
        <el-menu-item
          v-if="!userStore.isLoggedIn"
          index="/home"
        >
          <el-icon><HomeFilled /></el-icon>
          <template #title>
            {{ t('navbar.home') }}
          </template>
        </el-menu-item>
        
        <!-- 动态生成的菜单项 -->
        <sidebar-item
          v-for="route in userRoutes"
          :key="route.path"
          :item="route"
          :base-path="route.path"
          :is-collapse="isCollapse && !isMobile"
        />
      </el-menu>
    </el-scrollbar>
  </div>
</template>

<script setup>
import { computed, onMounted, watch, nextTick, inject, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useUserStore } from '@/pinia/modules/user'
import { HomeFilled, Expand, Fold } from '@element-plus/icons-vue'
import SidebarItem from './SidebarItem.vue'
import { useSiteStore } from '@/pinia/modules/site'
import { useFeatureStore } from '@/pinia/modules/feature'

const route = useRoute()
const router = useRouter()
const { t, locale } = useI18n()
const userStore = useUserStore()
const siteStore = useSiteStore()
const featureStore = useFeatureStore()

// 从父组件注入的状态和方法
const toggleSidebarCollapse = inject('toggleSidebarCollapse', null)
const sidebarCollapsed = inject('sidebarCollapsed', computed(() => false))
const isMobile = inject('isMobile', ref(false))
const closeSidebar = inject('closeSidebar', null)
const isCollapse = computed(() => sidebarCollapsed.value)

const toggleCollapse = () => {
  if (toggleSidebarCollapse) {
    toggleSidebarCollapse(!isCollapse.value)
  }
}

// 移动端点击菜单后关闭侧边栏
const handleMenuSelect = () => {
  if (isMobile.value && closeSidebar) {
    closeSidebar()
  }
}

// 获取当前活动菜单
const activeMenu = computed(() => {
  return route.path
})

// 导航函数
const navigateTo = (path) => {
  router.push(path)
}

// 根据用户类型获取对应的路由
const userRoutes = computed(() => {
  // 使用 viewMode 来决定显示哪个视图的菜单
  // 管理员(含normal_admin)可以切换视图，普通用户只能看到用户视图
  const viewMode = userStore.currentViewMode || userStore.userType
  // normal_admin 也使用 admin 路由集，但会过滤掉超管专属项
  const effectiveMode = (viewMode === 'normal_admin' || viewMode === 'admin') ? 'admin' : viewMode
  
  // 强制依赖 locale，确保语言切换时重新计算
  locale.value
  
  // 用户特定路由
  const userTypeRoutes = {
    // 普通用户路由
    user: [
      {
        path: '/user/dashboard',
        name: 'UserDashboard',
        meta: {
          title: t('sidebar.dashboard'),
          icon: 'Odometer'
        }
      },
      {
        path: '/user/instances',
        name: 'UserInstances',
        meta: {
          title: t('sidebar.myInstances'),
          icon: 'Box'
        }
      },
      {
        path: '/user/apply',
        name: 'UserApply',
        meta: {
          title: t('sidebar.apply'),
          icon: 'Plus'
        }
      },
      {
        path: '/user/tasks',
        name: 'UserTasks',
        meta: {
          title: t('sidebar.taskList'),
          icon: 'List'
        }
      },
      {
        path: '/user/profile',
        name: 'UserProfile',
        meta: {
          title: t('sidebar.personalCenter'),
          icon: 'User'
        }
      },
      {
        path: '/user/domain',
        name: 'UserDomain',
        meta: {
          title: t('sidebar.domainBinding'),
          icon: 'Link'
        }
      },
      {
        path: '/user/kyc',
        name: 'UserKYC',
        meta: {
          title: t('sidebar.kycVerification'),
          icon: 'Postcard'
        }
      },
      {
        path: '/user/checkin',
        name: 'UserCheckin',
        meta: {
          title: t('sidebar.checkinRenewal'),
          icon: 'Calendar'
        }
      },
      {
        path: '/user/api-tokens',
        name: 'UserApiTokens',
        meta: {
          title: t('sidebar.apiTokenManagement'),
          icon: 'Key'
        }
      }
    ],
    // 管理员路由
    admin: [
      {
        path: '/admin/dashboard',
        name: 'AdminDashboard',
        meta: {
          title: t('sidebar.dashboard'),
          icon: 'Odometer'
        }
      },
      {
        path: '/admin/users',
        name: 'AdminUsers',
        meta: {
          title: t('sidebar.userManagement'),
          icon: 'User'
        }
      },
      {
        path: '/admin/invite-codes',
        name: 'AdminInviteCodes',
        meta: {
          title: t('sidebar.inviteCodeManagement'),
          icon: 'Ticket'
        }
      },
      {
        path: '/admin/redemption-codes',
        name: 'AdminRedemptionCodes',
        meta: {
          title: t('sidebar.redemptionCodeManagement'),
          icon: 'Discount'
        }
      },
      {
        path: '/admin/providers',
        name: 'AdminProviders',
        meta: {
          title: t('sidebar.providerManagement'),
          icon: 'Monitor'
        }
      },
      {
        path: '/admin/group',
        name: 'AdminGroup',
        meta: {
          title: t('sidebar.groupManagement'),
          icon: 'Collection'
        }
      },
      {
        path: '/admin/tasks',
        name: 'AdminTasks',
        meta: {
          title: t('sidebar.taskManagement'),
          icon: 'List'
        }
      },
      {
        path: '/admin/instances',
        name: 'AdminInstances',
        meta: {
          title: t('sidebar.instanceManagement'),
          icon: 'Box'
        }
      },
      {
        path: '/admin/traffic',
        name: 'AdminTraffic',
        meta: {
          title: t('sidebar.trafficManagement'),
          icon: 'TrendCharts'
        }
      },
      {
        path: '/admin/port-mappings',
        name: 'AdminPortMappings',
        meta: {
          title: t('sidebar.portManagement'),
          icon: 'Connection'
        }
      },
      {
        path: '/admin/system-images',
        name: 'AdminSystemImages',
        meta: {
          title: t('sidebar.systemImages'),
          icon: 'Folder'
        }
      },
      {
        path: '/admin/snapshots',
        name: 'AdminSnapshots',
        meta: {
          title: t('sidebar.snapshotManagement'),
          icon: 'Camera'
        }
      },
      {
        path: '/admin/block-rules',
        name: 'AdminBlockRules',
        meta: {
          title: t('sidebar.blockRulesManagement'),
          icon: 'Lock'
        }
      },
      {
        path: '/admin/domain',
        name: 'AdminDomain',
        meta: {
          title: t('sidebar.domainManagement'),
          icon: 'Link'
        }
      },
      {
        path: '/admin/kyc',
        name: 'AdminKYC',
        meta: {
          title: t('sidebar.kycManagement'),
          icon: 'Postcard'
        }
      },
      {
        path: '/admin/api-tokens',
        name: 'AdminApiTokens',
        meta: {
          title: t('sidebar.adminApiTokenManagement'),
          icon: 'Key'
        }
      },
      {
        path: '/admin/announcements',
        name: 'AdminAnnouncements',
        meta: {
          title: t('sidebar.announcementManagement'),
          icon: 'Bell'
        }
      },
      {
        path: '/admin/oauth2-providers',
        name: 'AdminOAuth2Providers',
        meta: {
          title: 'OAuth2',
          icon: 'Connection'
        }
      },
      {
        path: '/admin/config',
        name: 'AdminConfig',
        meta: {
          title: t('sidebar.systemConfiguration'),
          icon: 'Setting'
        }
      },
      {
        path: '/admin/performance',
        name: 'AdminPerformance',
        meta: {
          title: t('sidebar.performanceMonitoring'),
          icon: 'Histogram'
        }
      },
      {
        path: '/admin/logs',
        name: 'AdminLogs',
        meta: {
          title: t('sidebar.logViewer'),
          icon: 'Document'
        }
      }
    ]
  }
  
  // 根据视图模式返回对应路由
  const routes = userTypeRoutes[effectiveMode] || []
  
  // 超级管理员专属路由名称集（normal_admin 不可见）
  const superAdminOnlyRoutes = new Set([
    'AdminUsers', 'AdminConfig', 'AdminPerformance', 'AdminLogs', 'AdminOAuth2Providers',
    'AdminInviteCodes', 'AdminAnnouncements', 'AdminSystemImages', 'AdminApiTokens'
  ])
  
  // 判断是否为普通管理员
  const isNormalAdmin = userStore.userType === 'normal_admin'
  
  // 根据功能开关和角色过滤路由
  const filteredRoutes = routes.filter(route => {
    if (['UserKYC', 'AdminKYC'].includes(route.name) && !featureStore.kycEnabled) return false
    if (['UserDomain', 'AdminDomain'].includes(route.name) && !featureStore.domainEnabled) return false
    if (['UserCheckin'].includes(route.name) && !featureStore.checkinEnabled) return false
    if (isNormalAdmin && superAdminOnlyRoutes.has(route.name)) return false
    return true
  })
  
  return filteredRoutes
})

// 生命周期钩子，检查DOM渲染
onMounted(() => {
  // 确保组件在DOM中
  nextTick(() => {
    document.querySelector('.sidebar-container')
  })
})

// 监听用户类型变化
watch(() => userStore.userType, () => {
  nextTick(() => {
    userRoutes.value
  })
}, { immediate: true })
</script>

<style lang="scss" scoped>
.sidebar-container {
  transition: width 0.28s;
  width: var(--sidebar-width);
  background-color: var(--bg-color-sidebar-light);

  .sidebar-logo {
    height: var(--navbar-height);
    line-height: var(--navbar-height);
    background: #16a34a; /* 绿色背景 */
    text-align: center;
    overflow: hidden;
    display: flex;
    flex-direction: row;
    align-items: center;
    justify-content: flex-start;
    padding: 0 var(--spacing-md);
    position: relative;

    h1 {
      color: #ffffff; /* 白色文字 */
      font-weight: var(--font-weight-semibold);
      font-size: var(--font-size-md);
      font-family: Avenir, Helvetica Neue, Arial, Helvetica, sans-serif;
      margin: 0;
      transition: opacity 0.28s;
    }
    
    .sidebar-logo-img {
      height: 28px;
      width: 28px;
      border-radius: 4px;
      object-fit: contain;
      flex-shrink: 0;
      margin-right: 8px;
    }
    
    span {
      font-size: var(--font-size-xs);
      color: #dcfce7; /* 浅绿色文字 */
    }

    .collapse-btn {
      position: absolute;
      top: 50%;
      right: 10px;
      transform: translateY(-50%);
      color: #dcfce7; /* 浅绿色 */
      background: transparent;
      border: none;
      transition: all 0.28s;
      
      &:hover {
        color: #ffffff; /* 悬停时白色 */
      }
    }
  }

  .scrollbar-wrapper {
    overflow-x: hidden !important;
  }

  .el-scrollbar__bar.is-vertical {
    right: 0px;
  }

  .el-scrollbar {
    height: calc(100% - var(--navbar-height));
  }

  .is-horizontal {
    display: none;
  }

  a {
    display: inline-block;
    width: 100%;
    overflow: hidden;
  }

  .svg-icon {
    margin-right: 16px;
  }

  .sub-el-icon {
    margin-right: 12px;
    margin-left: -2px;
  }

  .el-menu {
    border: none;
    height: 100%;
    background-color: var(--bg-color-sidebar-light) !important;
  }

  /* 菜单项悬停效果 */
  :deep(.el-menu-item) {
    background-color: transparent !important;
    
    &:hover {
      background-color: var(--bg-color-hover) !important;
      color: #16a34a !important;
    }
    
    &.is-active {
      background-color: var(--bg-color-active) !important;
      color: #16a34a !important;
      border-right: 3px solid #16a34a;
    }
  }

  :deep(.el-sub-menu__title) {
    background-color: transparent !important;
    
    &:hover {
      background-color: var(--bg-color-hover) !important;
      color: #16a34a !important;
    }
  }

  // 收缩状态样式
  &.is-collapse {
    width: var(--sidebar-width-collapsed);

    :deep(.el-menu) {
      width: var(--sidebar-width-collapsed) !important;
    }

    :deep(.el-menu-item),
    :deep(.el-sub-menu__title) {
      justify-content: center;
      padding-left: 0 !important;
      padding-right: 0 !important;
    }

    :deep(.el-menu-item .el-menu-tooltip__trigger),
    :deep(.el-sub-menu__title .el-menu-tooltip__trigger) {
      justify-content: center;
    }

    :deep(.menu-title),
    :deep(.el-sub-menu__icon-arrow),
    :deep(.el-menu-item > span:not(.el-icon)),
    :deep(.el-sub-menu__title > span:not(.el-icon)) {
      display: none !important;
      width: 0 !important;
      min-width: 0 !important;
      overflow: hidden !important;
    }

    :deep(.menu-item) {
      justify-content: center;
      width: var(--sidebar-width-collapsed);
    }

    :deep(.menu-icon),
    :deep(.el-icon) {
      margin-right: 0 !important;
    }

    .sidebar-logo {
      .collapse-btn {
        right: 50%;
        transform: translate(50%, -50%);
      }
    }
  }
  
  // 移动端样式
  &.mobile {
    width: var(--sidebar-width);
    
    .sidebar-logo {
      .collapse-btn {
        display: none;
      }
    }
  }
}

/* 移动端适配 */
@media (max-width: 768px) {
  .sidebar-container {
    .sidebar-logo {
      h1 {
        font-size: var(--font-size-base);
      }
    }
    
    :deep(.el-menu-item),
    :deep(.el-sub-menu__title) {
      height: 48px;
      line-height: 48px;
    }
  }
}
</style>
