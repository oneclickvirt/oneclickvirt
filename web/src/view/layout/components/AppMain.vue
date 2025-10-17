<template>
  <section class="app-main">
    <router-view v-slot="{ Component, route }">
      <transition
        name="fade-transform"
        mode="out-in"
        @before-enter="onBeforeEnter"
        @after-enter="onAfterEnter"
      >
        <!-- 移除keep-alive，确保组件每次都重新挂载，提供强制刷新机制 -->
        <component 
          :is="Component" 
          :key="`${route.fullPath}-${refreshKey}`"
        />
      </transition>
    </router-view>
  </section>
</template>

<script setup>
import { nextTick, ref } from 'vue'

const refreshKey = ref(0)

// 页面进入前
const onBeforeEnter = () => {
  // 加载状态指示
  const appMain = document.querySelector('.app-main')
  if (appMain) {
    appMain.style.opacity = '0.7'
  }
}

// 页面进入后
const onAfterEnter = () => {
  // 移除加载状态
  const appMain = document.querySelector('.app-main')
  if (appMain) {
    appMain.style.opacity = '1'
  }
  
  // 确保组件完全加载后触发数据刷新
  nextTick(() => {
    window.dispatchEvent(new CustomEvent('page-loaded'))
  })
}

// 暴露刷新方法给父组件
defineExpose({
  forceRefresh: () => {
    refreshKey.value++
  }
})
</script>

<style lang="scss" scoped>
.app-main {
  min-height: calc(100vh - 50px);
  width: 100%;
  position: relative;
  overflow: hidden;
  padding: var(--spacing-lg);
  background-color: var(--background-white);
}

.fade-transform-enter-active,
.fade-transform-leave-active {
  transition: all var(--transition-normal);
}

.fade-transform-enter-from {
  opacity: 0;
  transform: translateX(30px);
}

.fade-transform-leave-to {
  opacity: 0;
  transform: translateX(-30px);
}
</style>