import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox, ElNotification } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getAdminConfig, updateAdminConfig } from '@/api/config'
import { getInstanceTypePermissions, updateInstanceTypePermissions } from '@/api/admin'
import { useLanguageStore } from '@/pinia/modules/language'
import { useSiteStore } from '@/pinia/modules/site'
import { useFeatureStore } from '@/pinia/modules/feature'

export function useConfigManagement() {
  const { t, locale } = useI18n()
  const languageStore = useLanguageStore()
  const siteStore = useSiteStore()
  const featureStore = useFeatureStore()

  const activeTab = ref('auth')

  const config = ref({
    auth: {
      enableEmail: false,
      enableTelegram: false,
      enableQQ: false,
      enableOAuth2: false,
      enablePublicRegistration: false,
      enableKYC: false,
      enableDomain: false,
      enableCheckin: false,
      emailSMTPHost: '',
      emailSMTPPort: 587,
      emailUsername: '',
      emailPassword: '',
      telegramBotToken: '',
      qqAppID: '',
      qqAppKey: ''
    },
    quota: {
      defaultLevel: 1,
      levelLimits: {
        1: { maxInstances: 1, maxResources: { cpu: 1, memory: 512, disk: 1024, bandwidth: 100 }, maxTraffic: 102400, expiryDays: 0 },
        2: { maxInstances: 3, maxResources: { cpu: 2, memory: 1024, disk: 2048, bandwidth: 200 }, maxTraffic: 204800, expiryDays: 0 },
        3: { maxInstances: 5, maxResources: { cpu: 4, memory: 2048, disk: 4096, bandwidth: 500 }, maxTraffic: 409600, expiryDays: 0 },
        4: { maxInstances: 10, maxResources: { cpu: 8, memory: 4096, disk: 8192, bandwidth: 1000 }, maxTraffic: 819200, expiryDays: 0 },
        5: { maxInstances: 20, maxResources: { cpu: 16, memory: 8192, disk: 16384, bandwidth: 2000 }, maxTraffic: 1638400, expiryDays: 0 }
      }
    },
    inviteCode: {
      enabled: false
    },
    other: {
      maxAvatarSize: 2,
      defaultLanguage: '',
      logoURL: '',
      siteName: ''
    },
    captcha: {
      enabled: false
    },
    kyc: {
      method: 'manual',
      requireRealName: false,
      restrictCreateInstance: false,
      restrictRedeemCode: false,
      restrictDomainBind: false,
      alipayAppId: '',
      alipayPrivateKey: '',
      alipayPublicKey: ''
    }
  })

  const instanceTypePermissions = ref({
    minLevelForContainer: 1,
    minLevelForVM: 3,
    minLevelForDeleteContainer: 1,
    minLevelForDeleteVM: 2,
    minLevelForResetContainer: 1,
    minLevelForResetVM: 2
  })

  const loading = ref(false)
  const systemConfigLanguage = ref('')

  const loadConfig = async () => {
    loading.value = true
    try {
      const response = await getAdminConfig()
      if ((response.code === 200) && response.data) {
        if (response.data.auth) {
          config.value.auth = { ...config.value.auth, ...response.data.auth }
        }
        if (response.data.inviteCode) {
          config.value.inviteCode = { ...config.value.inviteCode, ...response.data.inviteCode }
        }
        if (response.data.other) {
          config.value.other = { ...config.value.other, ...response.data.other }
          systemConfigLanguage.value = config.value.other.defaultLanguage || ''
        }
        if (response.data.captcha) {
          config.value.captcha = { ...config.value.captcha, ...response.data.captcha }
        }
        if (response.data.kyc) {
          config.value.kyc = { ...config.value.kyc, ...response.data.kyc }
        }
        if (response.data.quota) {
          if (response.data.quota.defaultLevel != null) {
            config.value.quota.defaultLevel = response.data.quota.defaultLevel
          }
          if (response.data.quota.levelLimits) {
            config.value.quota.levelLimits = {}
            for (let level = 1; level <= 5; level++) {
              const levelKey = String(level)
              if (response.data.quota.levelLimits[levelKey]) {
                const limitData = response.data.quota.levelLimits[levelKey]
                config.value.quota.levelLimits[level] = {
                  maxInstances: limitData['max-instances'] ?? (level * 2),
                  maxResources: {
                    cpu: limitData['max-resources']?.cpu ?? (level * 2),
                    memory: limitData['max-resources']?.memory ?? (1024 * Math.pow(2, level - 1)),
                    disk: limitData['max-resources']?.disk ?? (10240 * Math.pow(2, level - 1)),
                    bandwidth: limitData['max-resources']?.bandwidth ?? (10 * level)
                  },
                  maxTraffic: limitData['max-traffic'] ?? (1024 * level),
                  expiryDays: limitData['expiry-days'] ?? 0
                }
              } else {
                config.value.quota.levelLimits[level] = {
                  maxInstances: level * 2,
                  maxResources: {
                    cpu: level * 2,
                    memory: 1024 * Math.pow(2, level - 1),
                    disk: 10240 * Math.pow(2, level - 1),
                    bandwidth: 10 * level
                  },
                  maxTraffic: 1024 * level,
                  expiryDays: 0
                }
              }
            }
          }
        }
      }
    } catch (error) {
      ElMessage.error(t('admin.config.loadConfigFailed'))
    } finally {
      loading.value = false
    }
  }

  const loadInstanceTypePermissions = async () => {
    try {
      const response = await getInstanceTypePermissions()
      if ((response.code === 200) && response.data) {
        instanceTypePermissions.value = {
          minLevelForContainer: response.data.minLevelForContainer || 1,
          minLevelForVM: response.data.minLevelForVM || 3,
          minLevelForDeleteContainer: response.data.minLevelForDeleteContainer || 1,
          minLevelForDeleteVM: response.data.minLevelForDeleteVM || 2,
          minLevelForResetContainer: response.data.minLevelForResetContainer || 1,
          minLevelForResetVM: response.data.minLevelForResetVM || 2
        }
      }
    } catch (error) {
      ElMessage.error(t('admin.config.loadPermissionsFailed'))
    }
  }

  const saveConfig = async () => {
    for (let level = 1; level <= 5; level++) {
      const limit = config.value.quota.levelLimits[level]
      if (!limit) { ElMessage.error(t('admin.config.levelConfigEmpty', { level })); return }
      if (!limit.maxInstances || limit.maxInstances <= 0) { ElMessage.error(t('admin.config.maxInstancesInvalid', { level })); return }
      if (!limit.maxTraffic || limit.maxTraffic <= 0) { ElMessage.error(t('admin.config.trafficLimitInvalid', { level })); return }
      if (!limit.maxResources) { ElMessage.error(t('admin.config.resourceConfigEmpty', { level })); return }
      if (!limit.maxResources.cpu || limit.maxResources.cpu <= 0) { ElMessage.error(t('admin.config.maxCPUInvalid', { level })); return }
      if (!limit.maxResources.memory || limit.maxResources.memory <= 0) { ElMessage.error(t('admin.config.maxMemoryInvalid', { level })); return }
      if (!limit.maxResources.disk || limit.maxResources.disk <= 0) { ElMessage.error(t('admin.config.maxDiskInvalid', { level })); return }
      if (!limit.maxResources.bandwidth || limit.maxResources.bandwidth <= 0) { ElMessage.error(t('admin.config.maxBandwidthInvalid', { level })); return }
    }

    loading.value = true
    try {
      const oldLanguage = systemConfigLanguage.value
      const newLanguage = config.value.other.defaultLanguage
      const languageChanged = oldLanguage !== newLanguage

      const configToSave = JSON.parse(JSON.stringify(config.value))
      if (configToSave.quota && configToSave.quota.levelLimits) {
        const convertedLimits = {}
        Object.keys(configToSave.quota.levelLimits).forEach(level => {
          const limit = configToSave.quota.levelLimits[level]
          convertedLimits[level] = {
            'max-instances': limit.maxInstances,
            'max-resources': { cpu: limit.maxResources.cpu, memory: limit.maxResources.memory, disk: limit.maxResources.disk, bandwidth: limit.maxResources.bandwidth },
            'max-traffic': limit.maxTraffic,
            'expiry-days': limit.expiryDays ?? 30
          }
        })
        configToSave.quota.levelLimits = convertedLimits
      }

      await updateAdminConfig(configToSave)
      await updateInstanceTypePermissions(instanceTypePermissions.value)

      ElMessage.success(t('admin.config.saveSuccess'))
      await siteStore.refresh()
      await featureStore.refresh()

      if (languageChanged) {
        const effectiveLanguage = languageStore.forceApplySystemLanguage(newLanguage)
        locale.value = effectiveLanguage
        ElNotification({ title: t('common.success'), message: t('admin.config.languageChangedRefreshing'), type: 'success', duration: 2000 })
        setTimeout(() => { window.location.reload() }, 2000)
      } else {
        await loadConfig()
        await loadInstanceTypePermissions()
      }
    } catch (error) {
      ElMessage.error(t('admin.config.saveFailed', { error: error.message || t('common.unknownError') }))
    } finally {
      loading.value = false
    }
  }

  const resetConfig = async () => {
    await loadConfig()
    await loadInstanceTypePermissions()
    ElMessage.success(t('admin.config.configReset'))
  }

  onMounted(() => {
    loadConfig()
    loadInstanceTypePermissions()
  })

  return {
    activeTab, config, instanceTypePermissions, loading,
    saveConfig, resetConfig, t
  }
}
