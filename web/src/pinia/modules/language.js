import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useLanguageStore = defineStore('language', () => {
  const currentLanguage = ref(localStorage.getItem('language') || 'zh-CN')

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
    setLanguage,
    toggleLanguage,
    getLanguageLabel,
    getCurrentLanguageLabel
  }
})
