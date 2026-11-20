import { defineStore } from 'pinia'
import { ref } from 'vue'

export function normalizeLanguage(language) {
  if (typeof language !== 'string') return ''
  switch (language.toLowerCase()) {
    case 'zh':
    case 'zh-cn':
      return 'zh-CN'
    case 'en':
    case 'en-us':
      return 'en-US'
    default:
      return ''
  }
}

export const useLanguageStore = defineStore('language', () => {
  const currentLanguage = ref('zh-CN')
  let systemLanguage = ''
  let systemLanguageKey = null

  const getEffectiveLanguage = () => {
    try {
      const savedLanguage = normalizeLanguage(localStorage.getItem('language'))
      const savedKey = localStorage.getItem('language-system-default')
      if (savedLanguage && (systemLanguageKey === null || savedKey === systemLanguageKey ||
        (savedKey === null && !systemLanguage))) {
        return savedLanguage
      }
    } catch {
      // Browsers can block access to local storage.
    }
    const browserLanguage = navigator.language || navigator.userLanguage
    return systemLanguage || (!browserLanguage || /^zh(?:-|$)/i.test(browserLanguage) ? 'zh-CN' : 'en-US')
  }

  const setSystemConfigLanguage = (language, revision) => {
    systemLanguage = normalizeLanguage(language)
    // A language choice belongs to the system default in effect when it was made.
    systemLanguageKey = JSON.stringify([systemLanguage, revision || ''])
  }

  const initLanguage = () => {
    currentLanguage.value = getEffectiveLanguage()
    return currentLanguage.value
  }

  const setLanguage = (lang) => {
    currentLanguage.value = lang
    try {
      localStorage.setItem('language', lang)
      if (systemLanguageKey === null) {
        localStorage.removeItem('language-system-default')
      } else {
        localStorage.setItem('language-system-default', systemLanguageKey)
      }
    } catch {
      // The current page can still change language.
    }
  }

  const toggleLanguage = () => {
    const newLang = currentLanguage.value === 'zh-CN' ? 'en-US' : 'zh-CN'
    setLanguage(newLang)
    return newLang
  }

  return {
    currentLanguage,
    toggleLanguage,
    setSystemConfigLanguage,
    initLanguage
  }
})
