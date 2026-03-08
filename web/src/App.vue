<template>
  <div id="app">
    <router-view />
    <GlobalSSHManager />
  </div>
</template>

<script setup>
import { onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { checkSystemInit } from '@/api/init'
import GlobalSSHManager from '@/components/GlobalSSHManager.vue'
import { useSiteStore } from '@/pinia/modules/site'

const router = useRouter()
const siteStore = useSiteStore()

// 动态更新浏览器 tab favicon
function updateFavicon(url) {
  let link = document.querySelector("link[rel~='icon']")
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }
  link.href = url || '/logo.ico'
}

const checkInitStatus = async () => {
  try {
    const response = await checkSystemInit()
    console.log('App启动时检查初始化状态:', response)
    if (response && response.code === 0 && response.data && response.data.needInit === true) {
      console.log('系统需要初始化，强制跳转到初始化页面')
      // 强制跳转到初始化页面
      router.replace('/init')
    }
  } catch (error) {
    console.error('App启动时检查系统初始化状态失败:', error)
    console.warn('初始化检查失败，应用继续正常启动')
    // 不阻塞应用启动
  }
}

onMounted(async () => {
  // 获取站点配置（包含自定义 logo）
  await siteStore.fetchSiteConfig()
  // 初始化 favicon
  if (siteStore.logoURL) {
    updateFavicon(siteStore.logoURL)
  }
  // 应用启动时检查初始化状态
  checkInitStatus()
})

// 当 logo URL 变化时更新 favicon
watch(() => siteStore.logoURL, (newURL) => {
  if (newURL) {
    updateFavicon(newURL)
  }
})

// 当站点名称变化时更新浏览器 tab 标题
watch(() => siteStore.displaySiteName, (name) => {
  document.title = name
}, { immediate: true })
</script>

<style>
#app {
  font-family: Avenir, Helvetica, Arial, sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  color: var(--text-color-primary);
}

* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

html, body {
  height: 100%;
  background-color: var(--bg-color-primary);
}

a {
  text-decoration: none;
  color: var(--primary-color);
  transition: var(--transition-all);
}

a:hover {
  color: var(--primary-color-dark);
}

h1, h2, h3, h4, h5, h6 {
  color: var(--text-color-primary);
  margin-top: 0;
}

p {
  color: var(--text-color-secondary);
  line-height: 1.6;
}

.container {
  max-width: var(--content-max-width);
  margin: 0 auto;
  padding: 0 var(--spacing-md);
}

.el-button--primary {
  background-color: var(--primary-color);
  border-color: var(--primary-color);
}

.el-button--primary:hover,
.el-button--primary:focus {
  background-color: var(--primary-color-dark);
  border-color: var(--primary-color-dark);
}

.el-card {
  border-radius: var(--border-radius-medium);
  border-color: var(--border-color);
  transition: var(--transition-all);
}

.el-card:hover {
  box-shadow: var(--box-shadow-hover);
  border-color: var(--border-color-hover);
}
</style>