<template>
  <div
    class="app-wrapper"
    :class="{ 'mobile': isMobile }"
  >
    <!-- 顶部栏公告 -->
    <TopbarAnnouncement />
    
    <!-- 移动端遮罩层 -->
    <div
      v-if="isMobile && sidebar.opened"
      class="drawer-bg"
      @click="closeSidebar"
    />
    
    <!-- 侧边栏 -->
    <component
      :is="Sidebar"
      :key="userStore.userType"
      class="sidebar-container"
      :class="{ 
        'is-collapse': isCollapse && !isMobile,
        'mobile': isMobile,
        'hidden': isMobile && !sidebar.opened
      }"
    />
    
    <!-- 主容器 -->
    <div
      class="main-container"
      :class="{ 
        'main-container-collapsed': isCollapse && !isMobile,
        'mobile': isMobile
      }"
    >
      <div
        class="fixed-header"
        :class="{ 
          'fixed-header-collapsed': isCollapse && !isMobile,
          'mobile': isMobile
        }"
      >
        <navbar @toggle-sidebar="toggleSidebar" />
      </div>
      <app-main />
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, nextTick, provide } from 'vue'
import { Navbar, Sidebar, AppMain } from './components'
import { useUserStore } from '@/pinia/modules/user'
import TopbarAnnouncement from '@/components/TopbarAnnouncement.vue'

const userStore = useUserStore()
const isMobile = ref(false)
const sidebar = ref({
  opened: true
})
const isCollapse = ref(false)

// 检测设备类型
const checkDevice = () => {
  const width = window.innerWidth
  isMobile.value = width < 768
  
  // 移动端默认关闭侧边栏
  if (isMobile.value) {
    sidebar.value.opened = false
    isCollapse.value = false
  } else {
    sidebar.value.opened = true
    // 平板端默认收缩
    if (width >= 768 && width < 1024) {
      isCollapse.value = true
    }
  }
}

// 切换侧边栏
const toggleSidebar = () => {
  if (isMobile.value) {
    sidebar.value.opened = !sidebar.value.opened
  } else {
    isCollapse.value = !isCollapse.value
    if (toggleSidebarCollapse) {
      toggleSidebarCollapse(isCollapse.value)
    }
  }
}

// 关闭侧边栏（移动端）
const closeSidebar = () => {
  sidebar.value.opened = false
}

// 提供给子组件的方法
const toggleSidebarCollapse = (collapsed) => {
  if (!isMobile.value) {
    isCollapse.value = collapsed
  }
}

// 提供收缩状态和移动端状态给子组件
provide('toggleSidebarCollapse', toggleSidebarCollapse)
provide('isMobile', computed(() => isMobile.value))
provide('sidebarOpened', computed(() => sidebar.value.opened))
provide('closeSidebar', closeSidebar)

onMounted(() => {
  checkDevice()
  window.addEventListener('resize', checkDevice)
  
  nextTick(() => {
    const sidebarEl = document.querySelector('.sidebar-container')
    if (!sidebarEl || sidebarEl.children.length === 0) {
      userStore.$patch({ userType: userStore.userType })
    }
  })
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', checkDevice)
})
</script>

<style lang="scss" scoped>
.app-wrapper {
  position: relative;
  height: 100%;
  width: 100%;
  background-color: var(--bg-color-primary);

  &.mobile {
    overflow-x: hidden;
  }
}

.drawer-bg {
  background: rgba(0, 0, 0, 0.3);
  width: 100%;
  top: 0;
  height: 100%;
  position: fixed;
  z-index: var(--z-drawer-bg);
}

.fixed-header {
  position: fixed;
  top: 0;
  right: 0;
  z-index: var(--z-navbar);
  width: calc(100% - var(--sidebar-width));
  transition: width 0.28s;
  background-color: var(--bg-color-secondary);
  box-shadow: var(--box-shadow-light);
  border-bottom: 1px solid var(--border-color);
  
  &.fixed-header-collapsed {
    width: calc(100% - var(--sidebar-width-collapsed));
  }
  
  &.mobile {
    width: 100%;
  }
}

.sidebar-container {
  transition: transform 0.28s, width 0.28s;
  width: var(--sidebar-width);
  background-color: var(--bg-color-sidebar);
  height: 100%;
  position: fixed;
  font-size: 0px;
  top: 0;
  bottom: 0;
  left: 0;
  z-index: var(--z-sidebar);
  overflow: hidden;
  box-shadow: 2px 0 6px rgba(0, 0, 0, 0.1);
  
  &.is-collapse {
    width: var(--sidebar-width-collapsed);
  }
  
  &.mobile {
    width: var(--sidebar-width);
    transform: translateX(0);
    
    &.hidden {
      transform: translateX(-100%);
    }
  }
}

.main-container {
  min-height: 100%;
  transition: margin-left 0.28s;
  margin-left: var(--sidebar-width);
  position: relative;
  padding-top: var(--navbar-height);
  display: flex;
  flex-direction: column;
  
  &.main-container-collapsed {
    margin-left: var(--sidebar-width-collapsed);
  }
  
  &.mobile {
    margin-left: 0;
    width: 100%;
  }
}

/* 平板端适配 */
@media (max-width: 1024px) and (min-width: 768px) {
  .sidebar-container:not(.mobile) {
    width: var(--sidebar-width-collapsed);
  }
  
  .main-container:not(.mobile) {
    margin-left: var(--sidebar-width-collapsed);
  }
  
  .fixed-header:not(.mobile) {
    width: calc(100% - var(--sidebar-width-collapsed));
  }
}

/* 移动端适配 */
@media (max-width: 768px) {
  .sidebar-container {
    width: var(--sidebar-width);
  }
  
  .main-container {
    margin-left: 0;
  }
  
  .fixed-header {
    width: 100%;
  }
}
</style>