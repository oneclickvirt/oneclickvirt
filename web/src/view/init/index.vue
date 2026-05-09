<template>
  <div class="init-container">
    <div class="init-bg-pattern" />
    <div class="init-card">
      <!-- Header -->
      <div class="init-header">
        <div class="init-logo">
          <svg viewBox="0 0 24 24" width="40" height="40" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 2L2 7l10 5 10-5-10-5z" />
            <path d="M2 17l10 5 10-5" />
            <path d="M2 12l10 5 10-5" />
          </svg>
        </div>
        <h1>{{ t('init.title') }}</h1>
        <p>{{ t('init.subtitle') }}</p>
      </div>

      <!-- Progress Panel (shown after submit) -->
      <InitProgressPanel
        v-if="showProgress"
        :status="progressStatus"
        :steps="progressSteps"
        :percent="progressPercent"
        :bar-status="progressBarStatus"
        :tag-type="progressTagType"
        :status-text="progressStatusText"
        @retry="retryInit"
        @go-home="goHome"
      />

      <!-- Form Panel (hidden after submit) -->
      <template v-if="!showProgress">
        <!-- Steps indicator -->
        <el-steps :active="stepIndex" finish-status="success" align-center class="init-steps">
          <el-step :title="t('init.database.tabLabel')" />
          <el-step :title="t('init.admin.tabLabel')" />
          <el-step :title="t('init.user.tabLabel')" />
        </el-steps>

        <!-- Step 1: Database -->
        <div v-show="activeTab === 'database'" class="step-content">
          <el-form 
            ref="databaseFormRef" 
            :model="databaseForm" 
            :rules="databaseRules" 
            label-width="140px" 
            label-position="top"
            size="large"
          >
            <el-form-item :label="t('init.database.type')" prop="type">
              <el-radio-group v-model="databaseForm.type" @change="onDatabaseTypeChange">
                <el-radio-button label="mysql">MySQL</el-radio-button>
                <el-radio-button label="mariadb">MariaDB</el-radio-button>
              </el-radio-group>
              <div class="field-hint">
                <el-text v-if="dbRecommendation" size="small" type="success">
                  {{ dbRecommendation.reason }} ({{ t('init.database.architecture') }}: {{ dbRecommendation.architecture }})
                </el-text>
                <el-text v-else size="small" type="info">
                  {{ t('init.database.autoSelectHint') }}
                </el-text>
              </div>
            </el-form-item>

            <el-row :gutter="16">
              <el-col :span="16">
                <el-form-item :label="t('init.database.host')" prop="host">
                  <el-input v-model="databaseForm.host" placeholder="127.0.0.1" />
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item :label="t('init.database.port')" prop="port">
                  <el-input v-model="databaseForm.port" placeholder="3306" />
                </el-form-item>
              </el-col>
            </el-row>

            <el-form-item :label="t('init.database.dbName')" prop="database">
              <el-input v-model="databaseForm.database" placeholder="oneclickvirt" />
            </el-form-item>

            <el-row :gutter="16">
              <el-col :span="12">
                <el-form-item :label="t('init.database.username')" prop="username">
                  <el-input v-model="databaseForm.username" placeholder="root" />
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item :label="t('init.database.password')" prop="password">
                  <el-input v-model="databaseForm.password" type="password" :placeholder="t('init.database.passwordPlaceholder')" show-password />
                </el-form-item>
              </el-col>
            </el-row>

            <el-form-item>
              <el-button type="info" plain :loading="testingConnection" @click="testDatabaseConnection">
                {{ t('init.database.testConnection') }}
              </el-button>
              <span v-if="connectionTestResult" :class="connectionTestResult.success ? 'test-success' : 'test-error'">
                {{ connectionTestResult.message }}
              </span>
            </el-form-item>
          </el-form>
        </div>

        <!-- Step 2: Admin -->
        <div v-show="activeTab === 'admin'" class="step-content">
          <el-form
            ref="adminFormRef"
            :model="initForm.admin"
            :rules="adminRules"
            label-position="top"
            size="large"
          >
            <el-form-item :label="t('init.admin.username')" prop="username">
              <el-input v-model="initForm.admin.username" :placeholder="t('init.admin.usernamePlaceholder')" clearable prefix-icon="User" />
            </el-form-item>
            <el-form-item :label="t('init.admin.password')" prop="password">
              <el-input v-model="initForm.admin.password" type="password" :placeholder="t('init.admin.passwordPlaceholder')" show-password clearable prefix-icon="Lock" />
              <div class="field-hint">
                <el-text size="small" type="info">{{ t('init.admin.passwordHint') }}</el-text>
              </div>
            </el-form-item>
            <el-form-item :label="t('init.admin.confirmPassword')" prop="confirmPassword">
              <el-input v-model="initForm.admin.confirmPassword" type="password" :placeholder="t('init.admin.confirmPasswordPlaceholder')" show-password clearable prefix-icon="Lock" />
            </el-form-item>
            <el-form-item :label="t('init.admin.email')" prop="email">
              <el-input v-model="initForm.admin.email" :placeholder="t('init.admin.emailPlaceholder')" clearable prefix-icon="Message" />
            </el-form-item>
          </el-form>
        </div>

        <!-- Step 3: User -->
        <div v-show="activeTab === 'user'" class="step-content">
          <el-form
            ref="userFormRef"
            :model="initForm.user"
            :rules="userRules"
            label-position="top"
            size="large"
          >
            <el-form-item :label="t('init.user.enableStatus')">
              <div class="enable-toggle">
                <el-switch
                  v-model="initForm.user.enabled"
                  :active-text="t('common.enabled')"
                  :inactive-text="t('common.disabled')"
                />
                <el-text size="small" type="warning" class="enable-hint">{{ t('init.user.enableHint') }}</el-text>
              </div>
            </el-form-item>
            <template v-if="initForm.user.enabled">
              <el-form-item :label="t('init.user.username')" prop="username">
                <el-input v-model="initForm.user.username" :placeholder="t('init.user.usernamePlaceholder')" clearable prefix-icon="User" />
              </el-form-item>
              <el-form-item :label="t('init.user.password')" prop="password">
                <el-input v-model="initForm.user.password" type="password" :placeholder="t('init.user.passwordPlaceholder')" show-password clearable prefix-icon="Lock" />
                <div class="field-hint">
                  <el-text size="small" type="info">{{ t('init.user.passwordHint') }}</el-text>
                </div>
              </el-form-item>
              <el-form-item :label="t('init.user.confirmPassword')" prop="confirmPassword">
                <el-input v-model="initForm.user.confirmPassword" type="password" :placeholder="t('init.user.confirmPasswordPlaceholder')" show-password clearable prefix-icon="Lock" />
              </el-form-item>
              <el-form-item :label="t('init.user.email')" prop="email">
                <el-input v-model="initForm.user.email" :placeholder="t('init.user.emailPlaceholder')" clearable prefix-icon="Message" />
              </el-form-item>
            </template>
          </el-form>
        </div>

        <!-- Navigation buttons -->
        <div class="init-actions">
          <el-button v-if="activeTab !== 'database'" @click="prevStep" size="large">
            {{ t('common.back') }}
          </el-button>
          <el-button type="info" plain @click="fillDefaultData" size="large">
            {{ t('init.fillDefaults') }}
          </el-button>
          <div style="flex: 1" />
          <el-button v-if="activeTab !== 'user'" type="primary" @click="nextStep" size="large">
            {{ t('init.nextStep') }}
          </el-button>
          <el-button
            v-else
            type="primary"
            :loading="loading"
            :disabled="loading || !isFormValid"
            @click="handleInit"
            size="large"
          >
            {{ t('init.initSystem') }}
          </el-button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { post, get } from '@/utils/request'
import { checkSystemInit, getInitProgress } from '@/api/init'
import { containsUnsafeUsernameContent } from '@/utils/validate'
import InitProgressPanel from './components/InitProgressPanel.vue'

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

// 创建管理员用户表单验证规则

const checkInitStatus = async () => {
  try {
    const response = await checkSystemInit()
    if (response && (response.code === 200) && response.data && response.data.needInit === false) {
      clearPolling()
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
  router.push('/home')
}

// 数据库类型变化处理
const onDatabaseTypeChange = (type) => {
  console.log(t('init.debug.dbTypeChanged'), type)
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
      console.log(t('init.debug.serverRecommendedDb'), response.data)
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
</script>

<style scoped>
.init-container {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #f0fdf4 0%, #ecfdf5 50%, #f0f9ff 100%);
  padding: 24px;
  position: relative;
  overflow: hidden;
}

.init-bg-pattern {
  position: absolute;
  inset: 0;
  background-image:
    radial-gradient(circle at 20% 30%, rgba(34, 197, 94, 0.08) 0%, transparent 50%),
    radial-gradient(circle at 80% 70%, rgba(59, 130, 246, 0.06) 0%, transparent 50%);
  z-index: 0;
}

.init-card {
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(20px);
  padding: 48px 40px 36px;
  border-radius: 20px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.06), 0 1px 2px rgba(0, 0, 0, 0.04);
  width: 100%;
  max-width: 620px;
  border: 1px solid rgba(34, 197, 94, 0.12);
  position: relative;
  z-index: 1;
}

.init-header {
  text-align: center;
  margin-bottom: 32px;
}

.init-logo {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 64px;
  border-radius: 16px;
  background: linear-gradient(135deg, #16a34a, #22c55e);
  color: white;
  margin-bottom: 16px;
  box-shadow: 0 4px 12px rgba(34, 197, 94, 0.3);
}

.init-header h1 {
  font-size: 28px;
  font-weight: 700;
  color: #111827;
  margin: 0 0 8px;
}

.init-header p {
  font-size: 15px;
  color: #6b7280;
  margin: 0;
}

.init-steps {
  margin-bottom: 32px;
}

.step-content {
  min-height: 280px;
}

.init-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 24px;
  padding-top: 24px;
  border-top: 1px solid #f3f4f6;
}

.field-hint {
  margin-top: 6px;
}

.enable-toggle {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.enable-hint {
  line-height: 1.4;
}

.test-success {
  color: #16a34a;
  margin-left: 12px;
  font-size: 14px;
}

.test-error {
  color: #ef4444;
  margin-left: 12px;
  font-size: 14px;
}

:deep(.el-steps) {
  padding: 0 20px;
}

:deep(.el-step__title.is-finish) {
  color: #16a34a;
}

:deep(.el-step__head.is-finish) {
  color: #16a34a;
  border-color: #16a34a;
}

:deep(.el-step__head.is-process) {
  color: #16a34a;
  border-color: #16a34a;
}

:deep(.el-step__title.is-process) {
  color: #16a34a;
  font-weight: 600;
}

:deep(.el-form-item__label) {
  font-weight: 500;
  color: #374151;
}

:deep(.el-input__wrapper) {
  border-radius: 10px;
  box-shadow: 0 0 0 1px #e5e7eb inset;
  transition: all 0.2s;
}

:deep(.el-input__wrapper:hover) {
  box-shadow: 0 0 0 1px rgba(34, 197, 94, 0.4) inset;
}

:deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 2px rgba(34, 197, 94, 0.25) inset;
}

:deep(.el-button--primary) {
  background: #16a34a;
  border-color: #16a34a;
  border-radius: 10px;
}

:deep(.el-button--primary:hover) {
  background: #15803d;
  border-color: #15803d;
}

:deep(.el-radio-button__inner) {
  border-radius: 8px;
}

@media (max-width: 640px) {
  .init-card {
    padding: 32px 20px 24px;
    border-radius: 16px;
  }

  .init-header h1 {
    font-size: 24px;
  }

  .init-actions {
    flex-wrap: wrap;
  }
}
</style>