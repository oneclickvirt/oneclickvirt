import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN'
import enUS from './locales/en-US'

const messages = {
  'zh-CN': zhCN,
  'en-US': enUS
}

// 导出一个函数用于创建 i18n 实例，以便在获取系统配置后设置正确的语言
export const createI18nInstance = (locale = 'zh-CN') => {
  return createI18n({
    legacy: false,
    locale: locale,
    fallbackLocale: 'zh-CN',
    messages
  })
}

// 默认导出一个临时实例，稍后会在 main.js 中重新初始化
const i18n = createI18nInstance('zh-CN')

export default i18n
