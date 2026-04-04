import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getRegisterConfig } from '@/api/config'

export const useFeatureStore = defineStore('feature', () => {
  const kycEnabled = ref(false)
  const domainEnabled = ref(false)
  const oauth2Enabled = ref(false)
  const loaded = ref(false)

  async function fetchFeatureFlags() {
    try {
      const res = await getRegisterConfig()
      if (res.code === 0 && res.data) {
        kycEnabled.value = !!res.data.kycEnabled
        domainEnabled.value = !!res.data.domainEnabled
        oauth2Enabled.value = !!res.data.oauth2Enabled
      }
    } catch (e) {
      console.warn('Failed to fetch feature flags:', e)
    } finally {
      loaded.value = true
    }
  }

  async function refresh() {
    loaded.value = false
    await fetchFeatureFlags()
  }

  return { kycEnabled, domainEnabled, oauth2Enabled, loaded, fetchFeatureFlags, refresh }
})
