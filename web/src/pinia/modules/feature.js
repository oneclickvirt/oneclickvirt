import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getRegisterConfig } from '@/api/config'

export const useFeatureStore = defineStore('feature', () => {
  const kycEnabled = ref(false)
  const kycMethod = ref('manual')
  const domainEnabled = ref(false)
  const oauth2Enabled = ref(false)
  const checkinEnabled = ref(false)
  const loaded = ref(false)

  async function fetchFeatureFlags() {
    try {
      const res = await getRegisterConfig()
      if (res.code === 0 && res.data) {
        kycEnabled.value = !!res.data.kycEnabled
        kycMethod.value = res.data.kycMethod || 'manual'
        domainEnabled.value = !!res.data.domainEnabled
        oauth2Enabled.value = !!res.data.oauth2Enabled
        checkinEnabled.value = !!res.data.checkinEnabled
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

  return { kycEnabled, kycMethod, domainEnabled, oauth2Enabled, checkinEnabled, loaded, fetchFeatureFlags, refresh }
})
