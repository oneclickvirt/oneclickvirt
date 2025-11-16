<template>
  <div class="init-container">
    <div class="init-form">
      <div class="form-header">
        <h2>{{ t('init.title') }}</h2>
        <p>{{ t('init.subtitle') }}</p>
      </div>

      <!-- 统一的配置标签页 -->
      <div class="init-tabs">
        <el-tabs
          v-model="activeTab"
          type="border-card"
          @tab-click="handleTabClick"
        >
          <!-- 数据库配置标签页 -->
          <el-tab-pane
            :label="t('init.database.tabLabel')"
            name="database"
          >
            <el-form 
              ref="databaseFormRef" 
              :model="databaseForm" 
              :rules="databaseRules" 
              label-width="120px" 
              size="large"
            >
              <el-form-item
                :label="t('init.database.type')"
                prop="type"
              >
                <el-radio-group
                  v-model="databaseForm.type"
                  @change="onDatabaseTypeChange"
                >
                  <el-radio label="mysql">
                    {{ t('init.database.mysqlRecommended') }}
                  </el-radio>
                  <el-radio label="mariadb">
                    {{ t('init.database.mariadbRecommended') }}
                  </el-radio>
                </el-radio-group>
                <div class="database-type-hint">
                  <el-text
                    v-if="dbRecommendation"
                    size="small"
                    type="success"
                  >
                    {{ dbRecommendation.reason }} ({{ t('init.database.architecture') }}: {{ dbRecommendation.architecture }})
                  </el-text>
                  <el-text
                    v-else
                    size="small"
                    type="info"
                  >
                    {{ t('init.database.autoSelectHint') }}
                  </el-text>
                </div>
              </el-form-item>
              
              <!-- 数据库配置项（MySQL/MariaDB通用） -->
              <div
                v-if="databaseForm.type === 'mysql' || databaseForm.type === 'mariadb'"
                class="database-config"
              >
                <el-form-item
                  :label="t('init.database.host')"
                  prop="host"
                >
                  <el-input
                    v-model="databaseForm.host"
                    placeholder="127.0.0.1"
                  />
                </el-form-item>
                <el-form-item
                  :label="t('init.database.port')"
                  prop="port"
                >
                  <el-input
                    v-model="databaseForm.port"
                    placeholder="3306"
                  />
                </el-form-item>
                <el-form-item
                  :label="t('init.database.dbName')"
                  prop="database"
                >
                  <el-input
                    v-model="databaseForm.database"
                    placeholder="oneclickvirt"
                  />
                </el-form-item>
                <el-form-item
                  :label="t('init.database.username')"
                  prop="username"
                >
                  <el-input
                    v-model="databaseForm.username"
                    placeholder="root"
                  />
                </el-form-item>
                <el-form-item
                  :label="t('init.database.password')"
                  prop="password"
                >
                  <el-input
                    v-model="databaseForm.password"
                    type="password"
                    :placeholder="t('init.database.passwordPlaceholder')"
                    show-password
                  />
                </el-form-item>
                
                <!-- 数据库连接测试 -->
                <el-form-item>
                  <el-button 
                    type="info" 
                    :loading="testingConnection"
                    @click="testDatabaseConnection"
                  >
                    {{ t('init.database.testConnection') }}
                  </el-button>
                  <span
                    v-if="connectionTestResult"
                    :class="connectionTestResult.success ? 'test-success' : 'test-error'"
                  >
                    {{ connectionTestResult.message }}
                  </span>
                </el-form-item>
              </div>
            </el-form>
          </el-tab-pane>

          <!-- 管理员设置标签页 -->
          <el-tab-pane
            :label="t('init.admin.tabLabel')"
            name="admin"
          >
            <el-form
              ref="adminFormRef"
              :model="initForm.admin"
              :rules="adminRules"
              label-width="120px"
              size="large"
            >
              <el-form-item
                :label="t('init.admin.username')"
                prop="username"
              >
                <el-input
                  v-model="initForm.admin.username"
                  :placeholder="t('init.admin.usernamePlaceholder')"
                  clearable
                />
              </el-form-item>
              <el-form-item
                :label="t('init.admin.password')"
                prop="password"
              >
                <el-input
                  v-model="initForm.admin.password"
                  type="password"
                  :placeholder="t('init.admin.passwordPlaceholder')"
                  show-password
                  clearable
                />
                <div class="password-hint">
                  <el-text
                    size="small"
                    type="info"
                  >
                    {{ t('init.admin.passwordHint') }}
                  </el-text>
                </div>
              </el-form-item>
              <el-form-item
                :label="t('init.admin.confirmPassword')"
                prop="confirmPassword"
              >
                <el-input
                  v-model="initForm.admin.confirmPassword"
                  type="password"
                  :placeholder="t('init.admin.confirmPasswordPlaceholder')"
                  show-password
                  clearable
                />
              </el-form-item>
              <el-form-item
                :label="t('init.admin.email')"
                prop="email"
              >
                <el-input
                  v-model="initForm.admin.email"
                  :placeholder="t('init.admin.emailPlaceholder')"
                  clearable
                />
              </el-form-item>
            </el-form>
          </el-tab-pane>
          
          <!-- 普通用户设置标签页 -->
          <el-tab-pane
            :label="t('init.user.tabLabel')"
            name="user"
          >
            <el-form
              ref="userFormRef"
              :model="initForm.user"
              :rules="userRules"
              label-width="120px"
              size="large"
            >
              <el-form-item
                :label="t('init.user.username')"
                prop="username"
              >
                <el-input
                  v-model="initForm.user.username"
                  :placeholder="t('init.user.usernamePlaceholder')"
                  clearable
                />
              </el-form-item>
              <el-form-item
                :label="t('init.user.password')"
                prop="password"
              >
                <el-input
                  v-model="initForm.user.password"
                  type="password"
                  :placeholder="t('init.user.passwordPlaceholder')"
                  show-password
                  clearable
                />
                <div class="password-hint">
                  <el-text
                    size="small"
                    type="info"
                  >
                    {{ t('init.user.passwordHint') }}
                  </el-text>
                </div>
              </el-form-item>
              <el-form-item
                :label="t('init.user.confirmPassword')"
                prop="confirmPassword"
              >
                <el-input
                  v-model="initForm.user.confirmPassword"
                  type="password"
                  :placeholder="t('init.user.confirmPasswordPlaceholder')"
                  show-password
                  clearable
                />
              </el-form-item>
              <el-form-item
                :label="t('init.user.email')"
                prop="email"
              >
                <el-input
                  v-model="initForm.user.email"
                  :placeholder="t('init.user.emailPlaceholder')"
                  clearable
                />
              </el-form-item>
            </el-form>
          </el-tab-pane>
        </el-tabs>
      </div>

      <div class="action-buttons">
        <el-button
          type="info"
          style="width: 48%"
          @click="fillDefaultData"
        >
          {{ t('init.fillDefaults') }}
        </el-button>
        <el-button
          type="primary"
          :loading="loading"
          :disabled="loading || !isFormValid"
          style="width: 48%"
          @click="handleInit"
        >
          {{ t('init.initSystem') }}
        </el-button>
      </div>

      <div class="init-info">
        <el-alert
          :title="t('init.infoTitle')"
          type="info"
          :closable="false"
          show-icon
        >
          <template #default>
            <p>{{ t('init.infoDescription') }}</p>
            <ul>
              <li><strong>{{ t('init.database.tabLabel') }}：</strong>{{ t('init.infoDatabaseDesc') }}</li>
              <li><strong>{{ t('init.admin.tabLabel') }}：</strong>{{ t('init.infoAdminDesc') }}</li>
              <li><strong>{{ t('init.user.tabLabel') }}：</strong>{{ t('init.infoUserDesc') }}</li>
            </ul>
          </template>
        </el-alert>
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
    email: ''
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
    { min: 3, max: 20, message: t('init.validation.usernameLength'), trigger: 'blur' }
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
    { min: 3, max: 20, message: t('init.validation.usernameLength'), trigger: 'blur' }
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
  
  // 检查普通用户表单
  const userValid = initForm.user.username && 
                    initForm.user.password && 
                    initForm.user.confirmPassword && 
                    initForm.user.email &&
                    initForm.user.password === initForm.user.confirmPassword
  
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

    if (response && response.code === 0 && response.data && response.data.needInit === false) {
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

const handleTabClick = (tab) => {
  activeTab.value = tab.name
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
    if (response && response.code === 0 && response.data) {
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
    
    if (response.code === 0 || response.code === 200) {
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
      adminFormRef.value.validate(),
      userFormRef.value.validate()
    ]
    
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
        email: initForm.user.email
      },
      database: databaseForm
    }

    const response = await post('/v1/public/init', requestData)

    if (response.code === 0 || response.code === 200) {
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
  // 注意：成功时不要在这里设置 loading.value = false，让页面保持loading状态直到跳转
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
  background: #f8fffe;
  padding: 20px;
  position: relative;
  overflow: hidden;
}

.init-container::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: linear-gradient(135deg, rgba(34, 197, 94, 0.05) 0%, rgba(34, 197, 94, 0.1) 100%);
  z-index: 1;
}

.init-form {
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(10px);
  padding: 50px 45px;
  border-radius: 16px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.08);
  width: 100%;
  max-width: 520px;
  border: 1px solid rgba(34, 197, 94, 0.1);
  position: relative;
  z-index: 2;
}

.form-header {
  text-align: center;
  margin-bottom: 40px;
}

.form-header h2 {
  color: #1f2937;
  margin-bottom: 12px;
  font-weight: 700;
  font-size: 32px;
}

.form-header p {
  color: #6b7280;
  margin: 0;
  font-size: 16px;
  line-height: 1.5;
}

.user-type-tabs {
  margin-bottom: 30px;
}

.init-tabs {
  margin-bottom: 30px;
}

.init-tabs :deep(.el-tabs__content) {
  padding: 25px 0;
}

.mysql-config {
  margin-top: 20px;
  padding-top: 20px;
  border-top: 1px solid #e5e7eb;
}

.action-buttons {
  display: flex;
  justify-content: space-between;
  margin-bottom: 30px;
  gap: 15px;
}

.init-info {
  margin-top: 30px;
}

.init-info ul {
  margin: 15px 0 0 0;
  padding-left: 20px;
}

.init-info li {
  margin: 8px 0;
  color: #6b7280;
  line-height: 1.5;
}

.password-hint {
  margin-top: 5px;
}

:deep(.el-tabs__header) {
  margin-bottom: 25px;
}

:deep(.el-tabs__nav-wrap::after) {
  background-color: rgba(34, 197, 94, 0.1);
}

:deep(.el-tabs__active-bar) {
  background-color: #16a34a;
}

:deep(.el-tabs__item) {
  color: #6b7280;
  font-weight: 500;
}

:deep(.el-tabs__item.is-active) {
  color: #16a34a;
  font-weight: 600;
}

:deep(.el-button--info) {
  background: #6b7280;
  border-color: #6b7280;
  border-radius: 12px;
  height: 50px;
  font-size: 16px;
  font-weight: 600;
  transition: all 0.3s ease;
}

:deep(.el-button--info:hover) {
  background: #4b5563;
  border-color: #4b5563;
  transform: translateY(-1px);
}

:deep(.el-form-item) {
  margin-bottom: 25px;
}

:deep(.el-form-item__label) {
  color: #374151;
  font-weight: 500;
  font-size: 15px;
}

:deep(.el-input) {
  border-radius: 12px;
}

:deep(.el-input__wrapper) {
  background: rgba(255, 255, 255, 0.8);
  border: 2px solid rgba(229, 231, 235, 0.8);
  border-radius: 12px;
  transition: all 0.3s ease;
  padding: 12px 16px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.02);
}

:deep(.el-input__wrapper:hover) {
  border-color: rgba(34, 197, 94, 0.3);
  background: white;
}

:deep(.el-input__wrapper.is-focus) {
  border-color: #16a34a;
  background: white;
  box-shadow: 0 0 0 3px rgba(34, 197, 94, 0.1);
}

:deep(.el-input__inner) {
  color: #374151;
  font-size: 15px;
  font-weight: 500;
}

:deep(.el-button--primary) {
  background: #16a34a;
  border-color: #16a34a;
  border-radius: 12px;
  height: 50px;
  font-size: 16px;
  font-weight: 600;
  transition: all 0.3s ease;
  box-shadow: 0 2px 8px rgba(34, 197, 94, 0.25);
  position: relative;
  overflow: hidden;
}

:deep(.el-button--primary:hover) {
  background: #15803d;
  border-color: #15803d;
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(34, 197, 94, 0.35);
}

:deep(.el-button--primary:active) {
  transform: translateY(0);
}

:deep(.el-alert--info) {
  background: rgba(34, 197, 94, 0.05);
  border: 1px solid rgba(34, 197, 94, 0.15);
  border-radius: 12px;
  padding: 20px;
}

:deep(.el-alert__icon) {
  color: #16a34a;
}

:deep(.el-alert__title) {
  color: #374151;
  font-weight: 600;
  font-size: 15px;
}

:deep(.el-alert__content) {
  color: #6b7280;
  font-size: 14px;
  line-height: 1.6;
}

.password-hint {
  margin-top: 5px;
  font-size: 12px;
  line-height: 1.4;
}

.database-type-hint {
  margin-top: 8px;
  font-size: 12px;
}

.test-success {
  color: #67c23a;
  margin-left: 10px;
  font-size: 14px;
}

.test-error {
  color: #f56c6c;
  margin-left: 10px;
  font-size: 14px;
}

@media (max-width: 768px) {
  .init-form {
    padding: 35px 25px;
    margin: 0 10px;
  }

  .form-header h2 {
    font-size: 26px;
  }

  :deep(.el-form-item__label) {
    font-size: 14px;
  }

  :deep(.el-button--primary) {
    height: 45px;
    font-size: 15px;
  }
}

@media (max-width: 480px) {
  .init-container {
    padding: 15px;
  }

  .init-form {
    padding: 30px 20px;
  }

  .form-header h2 {
    font-size: 24px;
  }
}
</style>