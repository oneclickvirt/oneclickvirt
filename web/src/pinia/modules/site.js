import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getPublicSystemConfig } from '@/api/public'

export const useSiteStore = defineStore('site', () => {
  // 自定义 Logo URL（从后端读取，空字符串表示使用默认）
  const logoURL = ref('')
  // 自定义网站名称（空表示使用默认 OneClickVirt）
  const siteName = ref('')
  // 是否已经初始化
  const initialized = ref(false)

  // 默认 Logo 资源路径（用于 img src 属性）
  const defaultLogoSrc = new URL('@/assets/images/logo.png', import.meta.url).href

  // 计算最终使用的 logo src
  const logoSrc = computed(() => {
    return logoURL.value && logoURL.value.trim() !== '' ? logoURL.value.trim() : defaultLogoSrc
  })

  // 计算最终显示的网站名称（空时默认 OneClickVirt）
  const displaySiteName = computed(() => {
    return siteName.value && siteName.value.trim() !== '' ? siteName.value.trim() : 'OneClickVirt'
  })

  // 从后端获取站点配置
  async function fetchSiteConfig() {
    if (initialized.value) return
    try {
      const res = await getPublicSystemConfig()
      if (res && res.code === 0 && res.data) {
        if (res.data.logo_url) {
          logoURL.value = res.data.logo_url
        }
        if (res.data.site_name) {
          siteName.value = res.data.site_name
        }
      }
    } catch (e) {
      // 静默失败，使用默认配置
    } finally {
      initialized.value = true
    }
  }

  // 强制刷新（管理员保存配置后调用）
  function refresh() {
    initialized.value = false
    return fetchSiteConfig()
  }

  return { logoURL, logoSrc, siteName, displaySiteName, fetchSiteConfig, refresh }
})
