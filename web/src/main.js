import '@/assets/styles/variables.css'
import './style/main.scss'
import './style/dialog-override.css'
import 'element-plus/theme-chalk/dark/css-vars.css'
import '@fortawesome/fontawesome-free/css/all.css'
import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'
import router from './router'
import { createPinia } from 'pinia'
import App from './App.vue'
import { initUserStatusMonitor } from '@/utils/userStatusMonitor'
import i18n from './i18n'
import { getPublicSystemConfig } from '@/api/public'
import { useLanguageStore } from '@/pinia/modules/language'

const app = createApp(App)
app.config.productionTip = false

for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

const pinia = createPinia()
app.use(ElementPlus).use(pinia).use(i18n).use(router)

// 初始化语言设置
const initLanguage = async () => {
  const languageStore = useLanguageStore()
  
  try {
    // 尝试获取系统配置的默认语言
    const response = await getPublicSystemConfig()
    // 检查响应数据
    if (response && response.data) {
      const defaultLang = response.data.default_language
      // 只有当后端明确返回了语言设置时才设置（包括空字符串）
      // 如果API返回空对象（没有default_language字段），将使用浏览器语言检测
      if (defaultLang !== undefined) {
        languageStore.setSystemConfigLanguage(defaultLang)
      }
    }
  } catch (error) {
    console.warn('获取系统语言配置失败，将使用浏览器语言检测:', error)
  }
  
  // 初始化语言（会根据优先级自动选择：用户设置 > 系统配置 > 浏览器语言）
  const effectiveLanguage = languageStore.initLanguage()
  i18n.global.locale.value = effectiveLanguage
}

// 初始化语言设置后再挂载应用
initLanguage().then(() => {
  // 初始化用户状态监控器
  initUserStatusMonitor()
  
  app.mount('#app')
})

export default app
