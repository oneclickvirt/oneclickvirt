import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  getAllOAuth2Providers,
  createOAuth2Provider,
  updateOAuth2Provider,
  deleteOAuth2Provider,
  resetOAuth2RegistrationCount,
  getOAuth2Presets
} from '@/api/oauth2'
import { getAdminConfig } from '@/api/config'

export default function useOAuth2() {
  const { t } = useI18n()
  const router = useRouter()
  const loading = ref(false)
  const providers = ref([])
  const dialogVisible = ref(false)
  const dialogTitle = ref('')
  const isEdit = ref(false)
  const submitting = ref(false)
  const activeTab = ref('basic')
  const formRef = ref(null)
  const oauth2Enabled = ref(true) // 默认为true，加载后更新

  const mappingDialogVisible = ref(false)
  const selectedPreset = ref('')
  const newMapping = reactive({
    externalLevel: '',
    systemLevel: 1
  })

  const formData = reactive({
    name: '',
    displayName: '',
    providerType: 'preset', // preset 或 generic
    enabled: true,
    clientId: '',
    clientSecret: '',
    redirectUrl: 'http://localhost:8888/api/v1/auth/oauth2/callback',
    authUrl: '',
    tokenUrl: '',
    userInfoUrl: '',
    userIdField: 'id',
    usernameField: 'username',
    emailField: 'email',
    avatarField: 'avatar',
    nicknameField: '',
    trustLevelField: '',
    maxRegistrations: 0,
    currentRegistrations: 0,
    levelMapping: {},
    defaultLevel: 1,
    sort: 0
  })

  const formRules = computed(() => ({
    name: [
      { required: true, message: t('admin.oauth2.validationName'), trigger: 'blur' }
    ],
    displayName: [
      { required: true, message: t('admin.oauth2.validationDisplayName'), trigger: 'blur' }
    ],
    clientId: [
      { required: true, message: t('admin.oauth2.validationClientId'), trigger: 'blur' }
    ],
    clientSecret: [
      { required: !isEdit.value, message: t('admin.oauth2.validationClientSecret'), trigger: 'blur' }
    ],
    redirectUrl: [
      { required: true, message: t('admin.oauth2.validationRedirectUrl'), trigger: 'blur' }
    ],
    authUrl: [
      { required: true, message: t('admin.oauth2.validationAuthUrl'), trigger: 'blur' }
    ],
    tokenUrl: [
      { required: true, message: t('admin.oauth2.validationTokenUrl'), trigger: 'blur' }
    ],
    userInfoUrl: [
      { required: true, message: t('admin.oauth2.validationUserInfoUrl'), trigger: 'blur' }
    ],
    userIdField: [
      { required: true, message: t('admin.oauth2.validationUserIdField'), trigger: 'blur' }
    ],
    usernameField: [
      { required: true, message: t('admin.oauth2.validationUsernameField'), trigger: 'blur' }
    ],
    defaultLevel: [
      { required: true, message: t('admin.oauth2.validationDefaultLevel'), trigger: 'blur' }
    ]
  }))

  onMounted(() => {
    loadProviders()
    loadSystemConfig()
  })

  const loadSystemConfig = async () => {
    try {
      const res = await getAdminConfig()
      if (res.data && res.data.auth) {
        oauth2Enabled.value = res.data.auth.enableOAuth2 || false
      }
    } catch (error) {
      console.error('加载系统配置失败:', error)
      // 加载失败时默认显示警告
      oauth2Enabled.value = false
    }
  }

  const goToConfig = () => {
    router.push('/admin/config')
  }

  const loadProviders = async () => {
    loading.value = true
    try {
      const res = await getAllOAuth2Providers()
      providers.value = res.data || []
    } catch (error) {
      ElMessage.error(t('admin.oauth2.loadProvidersFailed'))
    } finally {
      loading.value = false
    }
  }

  const resetForm = () => {
    Object.assign(formData, {
      name: '',
      displayName: '',
      providerType: 'preset',
      enabled: true,
      clientId: '',
      clientSecret: '',
      redirectUrl: 'http://localhost:8888/api/v1/auth/oauth2/callback',
      authUrl: '',
      tokenUrl: '',
      userInfoUrl: '',
      userIdField: 'id',
      usernameField: 'username',
      emailField: 'email',
      avatarField: 'avatar',
      nicknameField: '',
      trustLevelField: '',
      maxRegistrations: 0,
      currentRegistrations: 0,
      levelMapping: {},
      defaultLevel: 1,
      sort: 0
    })
    activeTab.value = 'basic'
  }

  // 应用预设配置
  const applyPreset = async (presetName) => {
    try {
      const res = await getOAuth2Presets()
      const preset = res.data[presetName]
      
      if (!preset) {
        ElMessage.error(t('admin.oauth2.presetNotFound'))
        return
      }

      // 先重置表单，确保清空所有字段
      resetForm()
      
      // 然后应用预设配置
      Object.assign(formData, {
        name: preset.Name || preset.name || '',
        displayName: preset.DisplayName || preset.displayName || '',
        providerType: preset.ProviderType || preset.providerType || 'preset',
        authUrl: preset.AuthURL || preset.authURL || '',
        tokenUrl: preset.TokenURL || preset.tokenURL || '',
        userInfoUrl: preset.UserInfoURL || preset.userInfoURL || '',
        userIdField: preset.UserIDField || preset.userIDField || 'id',
        usernameField: preset.UsernameField || preset.usernameField || 'username',
        emailField: preset.EmailField || preset.emailField || 'email',
        avatarField: preset.AvatarField || preset.avatarField || 'avatar',
        nicknameField: preset.NicknameField || preset.nicknameField || '',
        trustLevelField: preset.TrustLevelField || preset.trustLevelField || '',
        levelMapping: preset.LevelMapping || preset.levelMapping || {},
        defaultLevel: preset.DefaultLevel || preset.defaultLevel || 1
      })
      
      ElMessage.success(t('admin.oauth2.presetApplied', { name: formData.displayName }))
    } catch (error) {
      console.error('Failed to load preset:', error)
      ElMessage.error(t('admin.oauth2.presetLoadFailed'))
    }
  }

  // 处理预设选择变更
  const handlePresetChange = (presetName) => {
    if (!presetName || presetName === 'custom') {
      resetForm()
      return
    }
    applyPreset(presetName)
  }

  const handleAdd = () => {
    resetForm()
    selectedPreset.value = ''
    isEdit.value = false
    dialogTitle.value = t('admin.oauth2.addProvider')
    dialogVisible.value = true
  }

  const handleEdit = (row) => {
    resetForm()
    
    // 解析levelMapping
    let levelMapping = {}
    try {
      if (row.levelMapping) {
        levelMapping = JSON.parse(row.levelMapping)
      }
    } catch (e) {
      console.error(t('admin.oauth2.parseLevelMappingFailed'), e)
    }

    Object.assign(formData, {
      id: row.id,
      name: row.name,
      displayName: row.displayName,
      providerType: row.providerType || 'preset',
      enabled: row.enabled,
      clientId: row.clientId,
      clientSecret: '', // 不回显密钥
      redirectUrl: row.redirectUrl,
      authUrl: row.authUrl,
      tokenUrl: row.tokenUrl,
      userInfoUrl: row.userInfoUrl,
      userIdField: row.userIdField || 'id',
      usernameField: row.usernameField || 'username',
      emailField: row.emailField || 'email',
      avatarField: row.avatarField || 'avatar',
      nicknameField: row.nicknameField || '',
      trustLevelField: row.trustLevelField || '',
      maxRegistrations: row.maxRegistrations || 0,
      currentRegistrations: row.currentRegistrations || 0,
      levelMapping: levelMapping,
      defaultLevel: row.defaultLevel || 1,
      sort: row.sort || 0
    })

    isEdit.value = true
    dialogTitle.value = t('admin.oauth2.editProvider')
    dialogVisible.value = true
  }

  const handleSubmit = async () => {
    if (!formRef.value) return

    await formRef.value.validate(async (valid) => {
      if (!valid) return

      submitting.value = true
      try {
        const data = {
          name: formData.name,
          displayName: formData.displayName,
          providerType: formData.providerType,
          enabled: formData.enabled,
          clientId: formData.clientId,
          redirectUrl: formData.redirectUrl,
          authUrl: formData.authUrl,
          tokenUrl: formData.tokenUrl,
          userInfoUrl: formData.userInfoUrl,
          userIdField: formData.userIdField,
          usernameField: formData.usernameField,
          emailField: formData.emailField,
          avatarField: formData.avatarField,
          nicknameField: formData.nicknameField,
          trustLevelField: formData.trustLevelField,
          maxRegistrations: formData.maxRegistrations,
          levelMapping: formData.levelMapping,
          defaultLevel: formData.defaultLevel,
          sort: formData.sort
        }

        // 只在创建或修改了密钥时才发送
        if (!isEdit.value || formData.clientSecret) {
          data.clientSecret = formData.clientSecret
        }

        if (isEdit.value) {
          await updateOAuth2Provider(formData.id, data)
          ElMessage.success(t('common.updateSuccess'))
        } else {
          await createOAuth2Provider(data)
          ElMessage.success(t('common.createSuccess'))
        }

        dialogVisible.value = false
        loadProviders()
      } catch (error) {
        ElMessage.error(error.response?.data?.message || t('common.operationFailed'))
      } finally {
        submitting.value = false
      }
    })
  }

  const handleDelete = async (row) => {
    try {
      await ElMessageBox.confirm(
        t('admin.oauth2.deleteConfirm', { name: row.displayName }),
        t('common.warning'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        }
      )

      await deleteOAuth2Provider(row.id)
      ElMessage.success(t('common.deleteSuccess'))
      loadProviders()
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(error.response?.data?.message || t('common.deleteFailed'))
      }
    }
  }

  const handleResetCount = async (row) => {
    try {
      await ElMessageBox.confirm(
        t('admin.oauth2.resetCountConfirm', { name: row.displayName }),
        t('common.confirm'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        }
      )

      await resetOAuth2RegistrationCount(row.id)
      ElMessage.success(t('admin.oauth2.resetSuccess'))
      loadProviders()
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(error.response?.data?.message || t('admin.oauth2.resetFailed'))
      }
    }
  }

  const addLevelMapping = () => {
    newMapping.externalLevel = ''
    newMapping.systemLevel = 1
    mappingDialogVisible.value = true
  }

  const confirmAddMapping = () => {
    if (!newMapping.externalLevel) {
      ElMessage.warning(t('admin.oauth2.enterExternalLevel'))
      return
    }

    formData.levelMapping[newMapping.externalLevel] = newMapping.systemLevel
    mappingDialogVisible.value = false
  }

  const removeLevelMapping = (key) => {
    delete formData.levelMapping[key]
  }

  return {
    loading, providers,
    dialogVisible, dialogTitle, isEdit, submitting, activeTab, formRef, oauth2Enabled,
    mappingDialogVisible, selectedPreset, newMapping,
    formData, formRules,
    goToConfig, loadProviders,
    handlePresetChange, handleAdd, handleEdit, handleSubmit,
    handleDelete, handleResetCount,
    addLevelMapping, confirmAddMapping, removeLevelMapping
  }
}
