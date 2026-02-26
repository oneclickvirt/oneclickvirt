import { defineStore } from 'pinia'
import { ref } from 'vue'

// 判断系统是否偏好暗色
const systemPrefersDark = () =>
  window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches

// 读取优先级：用户手动选择（localStorage）> 系统偏好
const resolveInitialDark = () => {
  const saved = localStorage.getItem('theme')
  if (saved === 'dark') return true
  if (saved === 'light') return false
  // 未设置过，跟随系统
  return systemPrefersDark()
}

export const useThemeStore = defineStore('theme', () => {
  const isDark = ref(resolveInitialDark())

  const applyTheme = (dark) => {
    if (dark) {
      document.documentElement.classList.add('dark')
    } else {
      document.documentElement.classList.remove('dark')
    }
  }

  const initTheme = () => {
    applyTheme(isDark.value)

    // 监听系统主题变化：只有在用户没有手动选择时才自动跟随
    if (window.matchMedia) {
      const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
      const handleSystemChange = (e) => {
        if (!localStorage.getItem('theme')) {
          isDark.value = e.matches
          applyTheme(isDark.value)
        }
      }
      // 兼容旧版 Safari（不支持 addEventListener）
      if (mediaQuery.addEventListener) {
        mediaQuery.addEventListener('change', handleSystemChange)
      } else {
        mediaQuery.addListener(handleSystemChange)
      }
    }
  }

  const toggleTheme = () => {
    isDark.value = !isDark.value
    localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
    applyTheme(isDark.value)
    return isDark.value
  }

  return { isDark, initTheme, toggleTheme }
})
