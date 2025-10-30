import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useLanguageStore = defineStore('language', () => {
  const currentLanguage = ref(localStorage.getItem('language') || '')
  const systemConfigLanguage = ref('') // 系统配置的默认语言

  // 检测浏览器语言
  const detectBrowserLanguage = () => {
    const browserLang = navigator.language || navigator.userLanguage
    
    // 检测是否为中文
    if (browserLang.toLowerCase().includes('zh')) {
      return 'zh-CN'
    }
    
    // 非中文时显示英文
    return 'en-US'
  }

  // 获取应该使用的语言（优先级：localStorage > 系统配置 > 浏览器语言 > 默认中文）
  const getEffectiveLanguage = () => {
    // 1. 如果用户手动设置过语言（localStorage有值），优先使用
    const storedLang = localStorage.getItem('language')
    if (storedLang) {
      return storedLang
    }

    // 2. 如果系统配置了默认语言，使用系统配置
    if (systemConfigLanguage.value) {
      return systemConfigLanguage.value
    }

    // 3. 尝试检测浏览器语言
    try {
      return detectBrowserLanguage()
    } catch (e) {
      console.warn('检测浏览器语言失败:', e)
      // 4. 检测失败时默认显示中文
      return 'zh-CN'
    }
  }

  // 设置系统配置的默认语言
  const setSystemConfigLanguage = (lang) => {
    systemConfigLanguage.value = lang
    // 如果用户没有手动设置过语言，则使用系统配置
    if (!localStorage.getItem('language')) {
      currentLanguage.value = getEffectiveLanguage()
    }
  }

  // 初始化语言
  const initLanguage = () => {
    currentLanguage.value = getEffectiveLanguage()
    return currentLanguage.value
  }

  const setLanguage = (lang) => {
    currentLanguage.value = lang
    localStorage.setItem('language', lang)
  }

  const toggleLanguage = () => {
    const newLang = currentLanguage.value === 'zh-CN' ? 'en-US' : 'zh-CN'
    setLanguage(newLang)
    return newLang
  }

  const getLanguageLabel = (lang) => {
    return lang === 'zh-CN' ? '中文' : 'English'
  }

  const getCurrentLanguageLabel = () => {
    return getLanguageLabel(currentLanguage.value)
  }

  return {
    currentLanguage,
    systemConfigLanguage,
    setLanguage,
    toggleLanguage,
    getLanguageLabel,
    getCurrentLanguageLabel,
    detectBrowserLanguage,
    getEffectiveLanguage,
    setSystemConfigLanguage,
    initLanguage
  }
})
