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

const app = createApp(App)
app.config.productionTip = false

for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

const pinia = createPinia()
app.use(ElementPlus).use(pinia).use(router)

// 初始化用户状态监控器
initUserStatusMonitor()

app.mount('#app')

export default app
