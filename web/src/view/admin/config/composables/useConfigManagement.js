import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox, ElNotification } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getAdminConfig, updateAdminConfig } from '@/api/config'
import { getInstanceTypePermissions, updateInstanceTypePermissions } from '@/api/admin'
import { useLanguageStore } from '@/pinia/modules/language'
import { useSiteStore } from '@/pinia/modules/site'
import { useFeatureStore } from '@/pinia/modules/feature'
import { DEFAULT_QUOTA_LEVEL_LIMITS, normalizeLevelLimits, formatLevelLimitsForBackend, getSortedLevelKeys } from '@/utils/levels'

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
      levelLimits: normalizeLevelLimits(DEFAULT_QUOTA_LEVEL_LIMITS, DEFAULT_QUOTA_LEVEL_LIMITS)
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
            config.value.quota.levelLimits = normalizeLevelLimits(response.data.quota.levelLimits, DEFAULT_QUOTA_LEVEL_LIMITS)
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
    const configuredLevels = getSortedLevelKeys(config.value.quota.levelLimits)
    if (!configuredLevels.includes(Number(config.value.quota.defaultLevel))) {
      ElMessage.error(t('admin.config.defaultLevelMissing'))
      return
    }
    for (const level of configuredLevels) {
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
        configToSave.quota.levelLimits = formatLevelLimitsForBackend(configToSave.quota.levelLimits)
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
