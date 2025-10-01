<template>
  <!-- 添加style属性确保可见性，并根据收缩状态添加class -->
  <div
    class="sidebar-container"
    :class="{ 'is-collapse': isCollapse }"
    style="display: block; visibility: visible;"
  >
    <div class="sidebar-logo">
      <h1>OneClickVirt</h1>
      <el-button 
        class="collapse-btn" 
        :icon="isCollapse ? 'Expand' : 'Fold'" 
        size="small" 
        circle 
        @click="toggleCollapse" 
      />
    </div>
    <el-scrollbar wrap-class="scrollbar-wrapper">
      <el-menu
        :default-active="activeMenu"
        :collapse="isCollapse"
        :unique-opened="false"
        :collapse-transition="false"
        mode="vertical"
        background-color="#f0f9f4"
        text-color="#374151"
        active-text-color="#16a34a"
      >
        <!-- 首页链接 - 仅在未登录时显示 -->
        <el-menu-item
          v-if="!userStore.isLoggedIn"
          index="/home"
          @click="navigateTo('/home')"
        >
          <el-icon><HomeFilled /></el-icon>
          <span>首页</span>
        </el-menu-item>
        
        <!-- 动态生成的菜单项 -->
        <sidebar-item
          v-for="route in userRoutes"
          :key="route.path"
          :item="route"
          :base-path="route.path"
          :is-collapse="isCollapse"
        />
      </el-menu>
    </el-scrollbar>
  </div>
</template>

<script setup>
import { computed, onMounted, ref, watch, nextTick, inject } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUserStore } from '@/pinia/modules/user'
import { HomeFilled, Expand, Fold } from '@element-plus/icons-vue'
import SidebarItem from './SidebarItem.vue'

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()
const isCollapse = ref(false)

// 从父组件注入的方法
const toggleSidebarCollapse = inject('toggleSidebarCollapse', null)

const toggleCollapse = () => {
  isCollapse.value = !isCollapse.value
  // 通知父组件状态变化
  if (toggleSidebarCollapse) {
    toggleSidebarCollapse(isCollapse.value)
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
  const userType = userStore.userType
  console.log('侧边栏计算用户路由，当前用户类型:', userType)
  
  // 用户特定路由
  const userTypeRoutes = {
    // 普通用户路由
    user: [
      {
        path: '/user/dashboard',
        name: 'UserDashboard',
        meta: {
          title: '仪表盘',
          icon: 'Odometer'
        }
      },
      {
        path: '/user/instances',
        name: 'UserInstances',
        meta: {
          title: '我的实例',
          icon: 'Box'
        }
      },
      {
        path: '/user/apply',
        name: 'UserApply',
        meta: {
          title: '申请领取',
          icon: 'Plus'
        }
      },
      {
        path: '/user/tasks',
        name: 'UserTasks',
        meta: {
          title: '任务列表',
          icon: 'List'
        }
      },
      {
        path: '/user/profile',
        name: 'UserProfile',
        meta: {
          title: '个人中心',
          icon: 'User'
        }
      }
    ],
    // 管理员路由
    admin: [
      {
        path: '/admin/dashboard',
        name: 'AdminDashboard',
        meta: {
          title: '仪表盘',
          icon: 'Odometer'
        }
      },
      {
        path: '/admin/users',
        name: 'AdminUsers',
        meta: {
          title: '用户管理',
          icon: 'User'
        }
      },
      {
        path: '/admin/invite-codes',
        name: 'AdminInviteCodes',
        meta: {
          title: '邀请码管理',
          icon: 'Ticket'
        }
      },
      {
        path: '/admin/providers',
        name: 'AdminProviders',
        meta: {
          title: '提供商管理',
          icon: 'Monitor'
        }
      },
      {
        path: '/admin/instances',
        name: 'AdminInstances',
        meta: {
          title: '实例管理',
          icon: 'Box'
        }
      },
      {
        path: '/admin/tasks',
        name: 'AdminTasks',
        meta: {
          title: '任务管理',
          icon: 'List'
        }
      },
      {
        path: '/admin/port-mappings',
        name: 'AdminPortMappings',
        meta: {
          title: '端口映射管理',
          icon: 'Connection'
        }
      },
      {
        path: '/admin/config',
        name: 'AdminConfig',
        meta: {
          title: '系统配置',
          icon: 'Setting'
        }
      },
      {
        path: '/admin/announcements',
        name: 'AdminAnnouncements',
        meta: {
          title: '公告管理',
          icon: 'Bell'
        }
      },
      {
        path: '/admin/upload-config',
        name: 'AdminUploadConfig',
        meta: {
          title: '文件上传配置',
          icon: 'Upload'
        }
      },
      {
        path: '/admin/traffic',
        name: 'AdminTraffic',
        meta: {
          title: '流量管理',
          icon: 'TrendCharts'
        }
      },
      {
        path: '/admin/system-images',
        name: 'AdminSystemImages',
        meta: {
          title: '系统镜像',
          icon: 'Folder'
        }
      }
    ]
  }
  
  // 根据用户类型返回对应路由
  const routes = userTypeRoutes[userType] || []
  console.log('生成的用户路由数量:', routes.length, '路由详情:', JSON.stringify(routes))
  return routes
})

// 添加生命周期钩子，检查DOM渲染
onMounted(() => {
  console.log('侧边栏组件已挂载，组件ID:', Date.now())
  console.log('当前用户类型:', userStore.userType)
  console.log('用户登录状态:', userStore.isLoggedIn ? '已登录' : '未登录')
  
  // 确保组件在DOM中
  nextTick(() => {
    const el = document.querySelector('.sidebar-container')
    console.log('侧边栏容器元素:', el)
    if (el) {
      console.log('侧边栏内部HTML:', el.innerHTML.substring(0, 100) + '...')
    }
  })
})

// 监听用户类型变化
watch(() => userStore.userType, (newType, oldType) => {
  console.log('用户类型变化:', oldType, '->', newType)
  nextTick(() => {
    console.log('用户类型变化后，路由更新为:', userRoutes.value)
  })
}, { immediate: true })
</script>

<style lang="scss" scoped>
.sidebar-container {
  transition: width 0.28s;
  width: var(--sidebar-width);
  background-color: #f0f9f4; /* 浅绿色背景 */

  .sidebar-logo {
    height: var(--navbar-height);
    line-height: var(--navbar-height);
    background: #16a34a; /* 绿色背景 */
    text-align: center;
    overflow: hidden;
    display: flex;
    flex-direction: column;
    justify-content: center;
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
    background-color: #f0f9f4 !important;
  }

  /* 菜单项悬停效果 */
  :deep(.el-menu-item) {
    background-color: transparent !important;
    
    &:hover {
      background-color: #dcfce7 !important;
      color: #16a34a !important;
    }
    
    &.is-active {
      background-color: #bbf7d0 !important;
      color: #16a34a !important;
      border-right: 3px solid #16a34a;
    }
  }

  :deep(.el-sub-menu__title) {
    background-color: transparent !important;
    
    &:hover {
      background-color: #dcfce7 !important;
      color: #16a34a !important;
    }
  }

  // 收缩状态样式
  &.is-collapse {
    width: var(--sidebar-width-collapsed) !important;
    
    .sidebar-logo {
      h1 {
        opacity: 0;
      }
      
      .collapse-btn {
        right: 50%;
        transform: translate(50%, -50%);
      }
    }
  }
}
</style>