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
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { post, get } from '@/utils/request'
import { checkSystemInit } from '@/api/init'
import { containsUnsafeUsernameContent } from '@/utils/validate'

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
    console.log(t('init.debug.checkingStatus'), response)

    if (response && (response.code === 200) && response.data && response.data.needInit === false) {
      console.log(t('init.debug.alreadyInitialized'))
      ElMessage.info(t('init.messages.alreadyInitialized'))
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
  // 防止重复点击
  if (loading.value) {
    console.log('初始化正在进行中，忽略重复点击')
    return
  }
  
  try {
    // 验证所有表单
    const validations = [
      adminFormRef.value.validate()
    ]
    
    // 只在启用测试用户时验证用户表单
    if (initForm.user.enabled) {
      validations.push(userFormRef.value.validate())
    }
    
    // 如果是MySQL或MariaDB，需要验证数据库配置
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
      ElMessage.success(t('init.messages.initSuccess'))
      // 延长等待时间到4.5秒，确保后端数据库重新连接完成（后端需要2秒+处理时间）
      setTimeout(() => {
        router.push('/home')
      }, 4500)
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
  // 成功时不要在这里设置 loading.value = false，让页面保持loading状态直到跳转
}

onMounted(async () => {
  console.log(t('init.debug.pageMounted'))
  
  // 自动检测并设置数据库类型
  const detection = await detectDatabaseType()
  console.log(t('init.debug.detectedDbType'), detection)
  databaseForm.type = detection.type
  dbRecommendation.value = detection
  
  startPolling()
})

onUnmounted(() => {
  console.log(t('init.debug.pageUnmounted'))
  clearPolling()
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