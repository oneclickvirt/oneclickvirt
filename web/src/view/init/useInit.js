import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { post, get } from '@/utils/request'
import { checkSystemInit, getInitProgress } from '@/api/init'
import { containsUnsafeUsernameContent } from '@/utils/validate'
import { resetInitCache } from '@/router/guards'

export default function useInit() {
  const router = useRouter()
  const { t } = useI18n()
  const adminFormRef = ref()
  const userFormRef = ref()
  const databaseFormRef = ref()
  const loading = ref(false)
  const testingConnection = ref(false)
  const connectionTestResult = ref(null)
  const pollingTimer = ref(null)
  const activeTab = ref('database')
  const dbRecommendation = ref(null)

  // Progress tracking
  const showProgress = ref(false)
  const progressStatus = ref('idle')   // idle | in_progress | success | failed
  const progressSteps = ref([])
  const progressPercent = computed(() => {
    if (!progressSteps.value.length) return 0
    const done = progressSteps.value.filter(s => s.status === 'success').length
    return Math.round((done / progressSteps.value.length) * 100)
  })
  const progressBarStatus = computed(() => {
    if (progressStatus.value === 'success') return 'success'
    if (progressStatus.value === 'failed') return 'exception'
    return undefined
  })
  const progressTagType = computed(() => {
    if (progressStatus.value === 'success') return 'success'
    if (progressStatus.value === 'failed') return 'danger'
    if (progressStatus.value === 'in_progress') return 'warning'
    return 'info'
  })
  const progressStatusText = computed(() => {
    const map = {
      idle: t('init.progress.statusIdle'),
      in_progress: t('init.progress.statusInProgress'),
      success: t('init.progress.statusSuccess'),
      failed: t('init.progress.statusFailed'),
    }
    return map[progressStatus.value] || progressStatus.value
  })

  const steps = ['database', 'admin', 'user']
  const stepIndex = computed(() => steps.indexOf(activeTab.value))

  const nextStep = () => {
    const idx = steps.indexOf(activeTab.value)
    if (idx < steps.length - 1) {
      activeTab.value = steps[idx + 1]
    }
  }

  const prevStep = () => {
    const idx = steps.indexOf(activeTab.value)
    if (idx > 0) {
      activeTab.value = steps[idx - 1]
    }
  }

  // 数据库配置表单
  const databaseForm = reactive({
    type: 'mysql',
    host: '127.0.0.1',
    port: '3306',
    database: 'oneclickvirt',
    username: 'root',
    password: ''
  })

  const initForm = reactive({
    admin: {
      username: '',
      password: '',
      confirmPassword: '',
      email: ''
    },
    user: {
      username: '',
      password: '',
      confirmPassword: '',
      email: '',
      enabled: false
    }
  })

  const validateAdminConfirmPassword = (rule, value, callback) => {
    if (value !== initForm.admin.password) {
      callback(new Error(t('init.validation.passwordMismatch')))
    } else {
      callback()
    }
  }

  const validateUserConfirmPassword = (rule, value, callback) => {
    if (value !== initForm.user.password) {
      callback(new Error(t('init.validation.passwordMismatch')))
    } else {
      callback()
    }
  }

  const validateUsernameSafety = (rule, value, callback) => {
    if (!value) {
      callback()
      return
    }

    if (containsUnsafeUsernameContent(value)) {
      callback(new Error(t('validation.usernameUnsafe')))
      return
    }

    callback()
  }

  const validatePassword = (rule, value, callback) => {
    if (!value) {
      callback(new Error(t('init.validation.passwordRequired')))
      return
    }
    
    if (value.length < 8) {
      callback(new Error(t('init.validation.passwordMinLength')))
      return
    }
    
    if (!/[A-Z]/.test(value)) {
      callback(new Error(t('init.validation.passwordUppercase')))
      return
    }
    
    if (!/[a-z]/.test(value)) {
      callback(new Error(t('init.validation.passwordLowercase')))
      return
    }
    
    if (!/[0-9]/.test(value)) {
      callback(new Error(t('init.validation.passwordNumber')))
      return
    }
    
    if (!/[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(value)) {
      callback(new Error(t('init.validation.passwordSpecialChar')))
      return
    }
    
    callback()
  }

  const adminRules = {
    username: [
      { required: true, message: t('init.validation.adminUsernameRequired'), trigger: 'blur' },
      { min: 3, max: 20, message: t('init.validation.usernameLength'), trigger: 'blur' },
      { validator: validateUsernameSafety, trigger: 'blur' }
    ],
    password: [
      { required: true, message: t('init.validation.adminPasswordRequired'), trigger: 'blur' },
      { validator: validatePassword, trigger: 'blur' }
    ],
    confirmPassword: [
      { required: true, message: t('init.validation.confirmPasswordRequired'), trigger: 'blur' },
      { validator: validateAdminConfirmPassword, trigger: 'blur' }
    ],
    email: [
      { required: true, message: t('init.validation.adminEmailRequired'), trigger: 'blur' },
      { type: 'email', message: t('init.validation.emailFormat'), trigger: 'blur' }
    ]
  }

  const userRules = {
    username: [
      { required: true, message: t('init.validation.userUsernameRequired'), trigger: 'blur' },
      { min: 3, max: 20, message: t('init.validation.usernameLength'), trigger: 'blur' },
      { validator: validateUsernameSafety, trigger: 'blur' }
    ],
    password: [
      { required: true, message: t('init.validation.userPasswordRequired'), trigger: 'blur' },
      { validator: validatePassword, trigger: 'blur' }
    ],
    confirmPassword: [
      { required: true, message: t('init.validation.confirmPasswordRequired'), trigger: 'blur' },
      { validator: validateUserConfirmPassword, trigger: 'blur' }
    ],
    email: [
      { required: true, message: t('init.validation.userEmailRequired'), trigger: 'blur' },
      { type: 'email', message: t('init.validation.emailFormat'), trigger: 'blur' }
    ]
  }

  // 数据库配置验证规则
  const databaseRules = {
    type: [
      { required: true, message: t('init.validation.dbTypeRequired'), trigger: 'change' }
    ],
    host: [
      { required: true, message: t('init.validation.dbHostRequired'), trigger: 'blur' }
    ],
    port: [
      { required: true, message: t('init.validation.dbPortRequired'), trigger: 'blur' },
      { pattern: /^\d+$/, message: t('init.validation.dbPortNumber'), trigger: 'blur' }
    ],
    database: [
      { required: true, message: t('init.validation.dbNameRequired'), trigger: 'blur' }
    ],
    username: [
      { required: true, message: t('init.validation.dbUsernameRequired'), trigger: 'blur' }
    ]
  }

  // 计算属性：检查表单是否填写完整
  const isFormValid = computed(() => {
    // 检查管理员表单
    const adminValid = initForm.admin.username && 
                       initForm.admin.password && 
                       initForm.admin.confirmPassword && 
                       initForm.admin.email &&
                       initForm.admin.password === initForm.admin.confirmPassword
    
    // 检查普通用户表单（禁用时跳过验证）
    const userValid = !initForm.user.enabled || (
                      initForm.user.username && 
                      initForm.user.password && 
                      initForm.user.confirmPassword && 
                      initForm.user.email &&
                      initForm.user.password === initForm.user.confirmPassword)
    
    // 检查数据库配置
    const dbValid = databaseForm.type && 
                    databaseForm.host && 
                    databaseForm.port && 
                    databaseForm.database && 
                    databaseForm.username
    
    return adminValid && userValid && dbValid
  })

  const checkInitStatus = async () => {
    try {
      const response = await checkSystemInit()
      if (response && (response.code === 200) && response.data && response.data.needInit === false) {
        clearPolling()
        resetInitCache()
        router.push('/home')
      }
    } catch (error) {
      console.error(t('init.debug.checkStatusFailed'), error)
    }
  }

  const startPolling = () => {
    checkInitStatus()
    pollingTimer.value = setInterval(() => {
      checkInitStatus()
    }, 6000)
  }

  const clearPolling = () => {
    if (pollingTimer.value) {
      clearInterval(pollingTimer.value)
      pollingTimer.value = null
    }
  }

  // 进度轮询
  let progressTimer = null

  const startProgressPolling = () => {
    stopProgressPolling()
    progressTimer = setInterval(async () => {
      try {
        const res = await getInitProgress()
        if (res && res.code === 200 && res.data) {
          const data = res.data
          progressStatus.value = data.status || 'idle'
          if (Array.isArray(data.steps)) {
            progressSteps.value = data.steps
          }
          if (data.status === 'success') {
            stopProgressPolling()
            ElMessage.success(t('init.progress.successMsg'))
            resetInitCache()
            setTimeout(() => router.push('/home'), 1500)
          } else if (data.status === 'failed') {
            stopProgressPolling()
          }
        }
      } catch (e) {
        // 后端重启中，连接可能短暂中断，忽略并继续轮询
      }
    }, 1500)
  }

  const stopProgressPolling = () => {
    if (progressTimer) {
      clearInterval(progressTimer)
      progressTimer = null
    }
  }

  const retryInit = () => {
    showProgress.value = false
    progressStatus.value = 'idle'
    progressSteps.value = []
    loading.value = false
    startPolling()
  }

  const goHome = () => {
    resetInitCache()
    router.push('/home')
  }

  // 数据库类型变化处理
  const onDatabaseTypeChange = (type) => {
    // 根据数据库类型调整默认端口
    if (type === 'mysql' || type === 'mariadb') {
      databaseForm.port = '3306'
    }
  }

  // 自动检测数据库类型
  const detectDatabaseType = async () => {
    try {
      // 尝试从后端API获取推荐的数据库类型
      const response = await get('/v1/public/recommended-db-type')
      if (response && (response.code === 200) && response.data) {
        return {
          type: response.data.recommendedType,
          reason: response.data.reason,
          architecture: response.data.architecture
        }
      }
    } catch (error) {
      console.warn(t('init.debug.recommendedDbFailed'), error)
    }
    
    // 如果API调用失败，回退到客户端检测
    const userAgent = navigator.userAgent.toLowerCase()
    const platform = navigator.platform.toLowerCase()
    
    // 简单的架构检测逻辑
    if (platform.includes('arm') || platform.includes('aarch64')) {
      return {
        type: 'mariadb',
        reason: t('init.debug.armRecommendMariadb'),
        architecture: 'ARM64'
      }
    } else if (platform.includes('x86') || platform.includes('intel') || platform.includes('amd64')) {
      return {
        type: 'mysql', 
        reason: t('init.debug.amdRecommendMysql'),
        architecture: 'AMD64'
      }
    }
    
    // 默认使用MySQL
    return {
      type: 'mysql',
      reason: t('init.debug.defaultMysql'),
      architecture: 'Unknown'
    }
  }

  const fillDefaultData = () => {
    // 填入默认数据
    initForm.admin.username = 'admin'
    initForm.admin.password = 'Admin123!@#'
    initForm.admin.confirmPassword = 'Admin123!@#'
    initForm.admin.email = 'admin@spiritlhl.net'
    initForm.user.username = 'testuser'
    initForm.user.password = 'TestUser123!@#'
    initForm.user.confirmPassword = 'TestUser123!@#'
    initForm.user.email = 'user@spiritlhl.net'
    initForm.user.enabled = false
    ElMessage.success(t('init.messages.defaultsFilled'))
  }

  const testDatabaseConnection = async () => {
    try {
      // 先验证数据库表单
      if (!databaseFormRef.value) {
        ElMessage.error(t('init.messages.formNotReady'))
        return
      }
      
      await databaseFormRef.value.validate()
      
      testingConnection.value = true
      connectionTestResult.value = null
      
      // 发送测试连接请求
      const testData = {
        type: databaseForm.type,
        host: databaseForm.host,
        port: databaseForm.port,
        database: databaseForm.database,
        username: databaseForm.username,
        password: databaseForm.password
      }
      
      const response = await post('/v1/public/test-db-connection', testData)
      
      if (response.code === 200) {
        connectionTestResult.value = {
          success: true,
          message: '✅ ' + t('init.messages.dbConnSuccess')
        }
        ElMessage.success(t('init.messages.dbTestSuccess'))
      } else {
        connectionTestResult.value = {
          success: false,
          message: '❌ ' + (response.msg || t('init.messages.dbConnFailed'))
        }
        ElMessage.error(response.msg || t('init.messages.dbTestFailed'))
      }
    } catch (error) {
      console.error(t('init.messages.dbTestFailed') + ':', error)
      connectionTestResult.value = {
        success: false,
        message: '❌ ' + (error.response?.data?.msg || error.message || t('init.messages.dbTestFailed'))
      }
      ElMessage.error(error.response?.data?.msg || error.message || t('init.messages.dbTestFailed'))
    } finally {
      testingConnection.value = false
    }
  }

  const handleInit = async () => {
    if (loading.value) return
    
    try {
      const validations = [adminFormRef.value.validate()]
      if (initForm.user.enabled) {
        validations.push(userFormRef.value.validate())
      }
      if (databaseForm.type === 'mysql' || databaseForm.type === 'mariadb') {
        validations.push(databaseFormRef.value.validate())
      }
      await Promise.all(validations)
      
      loading.value = true
      clearPolling()

      const requestData = {
        admin: {
          username: initForm.admin.username,
          password: initForm.admin.password,
          email: initForm.admin.email
        },
        user: {
          username: initForm.user.username,
          password: initForm.user.password,
          email: initForm.user.email,
          enabled: initForm.user.enabled
        },
        database: databaseForm
      }

      const response = await post('/v1/public/init', requestData)

      if (response.code === 200) {
        // 切换到进度面板
        showProgress.value = true
        progressStatus.value = 'in_progress'
        loading.value = false
        startProgressPolling()
      } else {
        ElMessage.error(response.msg || t('init.messages.initFailed'))
        loading.value = false
        startPolling()
      }
    } catch (error) {
      console.error(t('init.messages.initFailed') + ':', error)
      ElMessage.error(t('init.messages.initRetry'))
      loading.value = false
      startPolling()
    }
  }

  onMounted(async () => {
    // 自动检测并设置数据库类型
    const detection = await detectDatabaseType()
    databaseForm.type = detection.type
    dbRecommendation.value = detection
    
    startPolling()
  })

  onUnmounted(() => {
    clearPolling()
    stopProgressPolling()
  })

  return {
    adminFormRef, userFormRef, databaseFormRef,
    loading, testingConnection, connectionTestResult,
    activeTab, dbRecommendation,
    showProgress, progressStatus, progressSteps,
    progressPercent, progressBarStatus, progressTagType, progressStatusText,
    stepIndex,
    databaseForm, initForm,
    adminRules, userRules, databaseRules,
    isFormValid,
    nextStep, prevStep,
    onDatabaseTypeChange, fillDefaultData,
    testDatabaseConnection, handleInit,
    retryInit, goHome
  }
}
