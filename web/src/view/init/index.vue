<template>
  <div class="init-container">
    <div class="init-form">
      <div class="form-header">
        <h2>系统初始化</h2>
        <p>首次使用需要初始化系统</p>
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
            label="数据库配置"
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
                label="数据库类型"
                prop="type"
              >
                <el-radio-group
                  v-model="databaseForm.type"
                  @change="onDatabaseTypeChange"
                >
                  <el-radio label="mysql">
                    MySQL（推荐AMD64架构使用）
                  </el-radio>
                  <el-radio label="mariadb">
                    MariaDB（推荐ARM64架构使用）
                  </el-radio>
                </el-radio-group>
                <div class="database-type-hint">
                  <el-text
                    v-if="dbRecommendation"
                    size="small"
                    type="success"
                  >
                    {{ dbRecommendation.reason }} (架构: {{ dbRecommendation.architecture }})
                  </el-text>
                  <el-text
                    v-else
                    size="small"
                    type="info"
                  >
                    系统会根据架构自动选择合适的数据库类型，您也可以手动选择
                  </el-text>
                </div>
              </el-form-item>
              
              <!-- 数据库配置项（MySQL/MariaDB通用） -->
              <div
                v-if="databaseForm.type === 'mysql' || databaseForm.type === 'mariadb'"
                class="database-config"
              >
                <el-form-item
                  label="数据库地址"
                  prop="host"
                >
                  <el-input
                    v-model="databaseForm.host"
                    placeholder="127.0.0.1"
                  />
                </el-form-item>
                <el-form-item
                  label="数据库端口"
                  prop="port"
                >
                  <el-input
                    v-model="databaseForm.port"
                    placeholder="3306"
                  />
                </el-form-item>
                <el-form-item
                  label="数据库名称"
                  prop="database"
                >
                  <el-input
                    v-model="databaseForm.database"
                    placeholder="oneclickvirt"
                  />
                </el-form-item>
                <el-form-item
                  label="用户名"
                  prop="username"
                >
                  <el-input
                    v-model="databaseForm.username"
                    placeholder="root"
                  />
                </el-form-item>
                <el-form-item
                  label="密码"
                  prop="password"
                >
                  <el-input
                    v-model="databaseForm.password"
                    type="password"
                    placeholder="数据库密码"
                    show-password
                  />
                </el-form-item>
                
                <!-- 数据库连接测试 -->
                <el-form-item label=" ">
                  <el-button 
                    type="info" 
                    :loading="testingConnection"
                    @click="testDatabaseConnection"
                  >
                    测试数据库连接
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
            label="管理员设置"
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
                label="管理员用户名"
                prop="username"
              >
                <el-input
                  v-model="initForm.admin.username"
                  placeholder="请输入管理员用户名"
                  clearable
                />
              </el-form-item>
              <el-form-item
                label="管理员密码"
                prop="password"
              >
                <el-input
                  v-model="initForm.admin.password"
                  type="password"
                  placeholder="请输入管理员密码"
                  show-password
                  clearable
                />
                <div class="password-hint">
                  <el-text
                    size="small"
                    type="info"
                  >
                    密码需要至少8位，包含大写字母、小写字母、数字和特殊字符(!@#$%^&*等)
                  </el-text>
                </div>
              </el-form-item>
              <el-form-item
                label="确认密码"
                prop="confirmPassword"
              >
                <el-input
                  v-model="initForm.admin.confirmPassword"
                  type="password"
                  placeholder="请再次输入密码"
                  show-password
                  clearable
                />
              </el-form-item>
              <el-form-item
                label="管理员邮箱"
                prop="email"
              >
                <el-input
                  v-model="initForm.admin.email"
                  placeholder="请输入管理员邮箱"
                  clearable
                />
              </el-form-item>
            </el-form>
          </el-tab-pane>
          
          <!-- 普通用户设置标签页 -->
          <el-tab-pane
            label="普通用户设置"
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
                label="用户名"
                prop="username"
              >
                <el-input
                  v-model="initForm.user.username"
                  placeholder="请输入用户名"
                  clearable
                />
              </el-form-item>
              <el-form-item
                label="用户密码"
                prop="password"
              >
                <el-input
                  v-model="initForm.user.password"
                  type="password"
                  placeholder="请输入用户密码"
                  show-password
                  clearable
                />
                <div class="password-hint">
                  <el-text
                    size="small"
                    type="info"
                  >
                    密码需要至少8位，包含大写字母、小写字母、数字和特殊字符(!@#$%^&*等)
                  </el-text>
                </div>
              </el-form-item>
              <el-form-item
                label="确认密码"
                prop="confirmPassword"
              >
                <el-input
                  v-model="initForm.user.confirmPassword"
                  type="password"
                  placeholder="请再次输入密码"
                  show-password
                  clearable
                />
              </el-form-item>
              <el-form-item
                label="用户邮箱"
                prop="email"
              >
                <el-input
                  v-model="initForm.user.email"
                  placeholder="请输入用户邮箱"
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
          @click="fillDefaultData"
        >
          一键填入默认信息
        </el-button>
        <el-button
          type="primary"
          :loading="loading"
          @click="handleInit"
        >
          初始化系统
        </el-button>
      </div>

      <div class="init-info">
        <el-alert
          title="初始化说明"
          type="info"
          :closable="false"
          show-icon
        >
          <template #default>
            <p>系统初始化需要配置以下信息：</p>
            <ul>
              <li><strong>数据库配置：</strong>设置MySQL或MariaDB数据库连接信息</li>
              <li><strong>管理员设置：</strong>创建拥有系统最高权限的管理员账户</li>
              <li><strong>普通用户设置：</strong>创建具有基础功能使用权限的普通用户账户</li>
            </ul>
          </template>
        </el-alert>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { post, get } from '@/utils/request'
import { checkSystemInit } from '@/api/init'

const router = useRouter()
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
    callback(new Error('两次输入的密码不一致'))
  } else {
    callback()
  }
}

const validateUserConfirmPassword = (rule, value, callback) => {
  if (value !== initForm.user.password) {
    callback(new Error('两次输入的密码不一致'))
  } else {
    callback()
  }
}

const validatePassword = (rule, value, callback) => {
  if (!value) {
    callback(new Error('请输入密码'))
    return
  }
  
  if (value.length < 8) {
    callback(new Error('密码长度至少8位'))
    return
  }
  
  if (!/[A-Z]/.test(value)) {
    callback(new Error('密码必须包含大写字母'))
    return
  }
  
  if (!/[a-z]/.test(value)) {
    callback(new Error('密码必须包含小写字母'))
    return
  }
  
  if (!/[0-9]/.test(value)) {
    callback(new Error('密码必须包含数字'))
    return
  }
  
  if (!/[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(value)) {
    callback(new Error('密码必须包含特殊字符(!@#$%^&*等)'))
    return
  }
  
  callback()
}

const adminRules = {
  username: [
    { required: true, message: '请输入管理员用户名', trigger: 'blur' },
    { min: 3, max: 20, message: '用户名长度在 3 到 20 个字符', trigger: 'blur' }
  ],
  password: [
    { required: true, message: '请输入管理员密码', trigger: 'blur' },
    { validator: validatePassword, trigger: 'blur' }
  ],
  confirmPassword: [
    { required: true, message: '请确认密码', trigger: 'blur' },
    { validator: validateAdminConfirmPassword, trigger: 'blur' }
  ],
  email: [
    { required: true, message: '请输入管理员邮箱', trigger: 'blur' },
    { type: 'email', message: '请输入正确的邮箱格式', trigger: 'blur' }
  ]
}

const userRules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { min: 3, max: 20, message: '用户名长度在 3 到 20 个字符', trigger: 'blur' }
  ],
  password: [
    { required: true, message: '请输入用户密码', trigger: 'blur' },
    { validator: validatePassword, trigger: 'blur' }
  ],
  confirmPassword: [
    { required: true, message: '请确认密码', trigger: 'blur' },
    { validator: validateUserConfirmPassword, trigger: 'blur' }
  ],
  email: [
    { required: true, message: '请输入用户邮箱', trigger: 'blur' },
    { type: 'email', message: '请输入正确的邮箱格式', trigger: 'blur' }
  ]
}

// 数据库配置验证规则
const databaseRules = {
  type: [
    { required: true, message: '请选择数据库类型', trigger: 'change' }
  ],
  host: [
    { required: true, message: '请输入数据库地址', trigger: 'blur' }
  ],
  port: [
    { required: true, message: '请输入数据库端口', trigger: 'blur' },
    { pattern: /^\d+$/, message: '端口必须为数字', trigger: 'blur' }
  ],
  database: [
    { required: true, message: '请输入数据库名称', trigger: 'blur' }
  ],
  username: [
    { required: true, message: '请输入数据库用户名', trigger: 'blur' }
  ]
}

// 创建管理员用户表单验证规则

const checkInitStatus = async () => {
  try {
    const response = await checkSystemInit()
    console.log('检查初始化状态:', response)

    if (response && response.code === 0 && response.data && response.data.needInit === false) {
      console.log('系统已初始化，跳转到首页')
      ElMessage.info('系统已完成初始化，正在跳转到首页...')
      clearPolling()
      router.push('/home')
    }
  } catch (error) {
    console.error('检查初始化状态失败:', error)
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
  console.log('数据库类型变更为:', type)
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
      console.log('服务器推荐的数据库类型:', response.data)
      return {
        type: response.data.recommendedType,
        reason: response.data.reason,
        architecture: response.data.architecture
      }
    }
  } catch (error) {
    console.warn('获取推荐数据库类型失败，使用客户端检测:', error)
  }
  
  // 如果API调用失败，回退到客户端检测
  const userAgent = navigator.userAgent.toLowerCase()
  const platform = navigator.platform.toLowerCase()
  
  // 简单的架构检测逻辑
  if (platform.includes('arm') || platform.includes('aarch64')) {
    return {
      type: 'mariadb',
      reason: 'ARM64架构推荐使用MariaDB',
      architecture: 'ARM64'
    }
  } else if (platform.includes('x86') || platform.includes('intel') || platform.includes('amd64')) {
    return {
      type: 'mysql', 
      reason: 'AMD64架构推荐使用MySQL',
      architecture: 'AMD64'
    }
  }
  
  // 默认使用MySQL
  return {
    type: 'mysql',
    reason: '默认推荐使用MySQL',
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
  ElMessage.success('已填入默认信息')
}

const testDatabaseConnection = async () => {
  try {
    // 先验证数据库表单
    if (!databaseFormRef.value) {
      ElMessage.error('数据库配置表单未准备好')
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
        message: '✅ 数据库连接成功'
      }
      ElMessage.success('数据库连接测试成功')
    } else {
      connectionTestResult.value = {
        success: false,
        message: '❌ ' + (response.msg || '数据库连接失败')
      }
      ElMessage.error(response.msg || '数据库连接测试失败')
    }
  } catch (error) {
    console.error('数据库连接测试失败:', error)
    connectionTestResult.value = {
      success: false,
      message: '❌ ' + (error.response?.data?.msg || error.message || '数据库连接测试失败')
    }
    ElMessage.error(error.response?.data?.msg || error.message || '数据库连接测试失败')
  } finally {
    testingConnection.value = false
  }
}

const handleInit = async () => {
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
      ElMessage.success('系统初始化成功！正在跳转到首页...')
      setTimeout(() => {
        router.push('/home')
      }, 1500)
    } else {
      ElMessage.error(response.msg || '系统初始化失败')
      startPolling()
    }
  } catch (error) {
    console.error('初始化失败:', error)
    ElMessage.error('系统初始化失败，请重试')
    startPolling()
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  console.log('Init页面已挂载，启动轮询检查')
  
  // 自动检测并设置数据库类型
  const detection = await detectDatabaseType()
  console.log('检测到的数据库类型:', detection)
  databaseForm.type = detection.type
  dbRecommendation.value = detection
  
  startPolling()
})

onUnmounted(() => {
  console.log('Init页面卸载，清除轮询')
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

:deep(.el-form) {
  width: 100%;
}

:deep(.el-form-item__content) {
  width: 100%;
}

:deep(.el-input) {
  width: 100%;
}

.init-tabs :deep(.el-tabs__content) {
  padding: 20px 0;
}

:deep(.el-tabs--border-card) {
  border-radius: 12px;
  border: 1px solid rgba(229, 231, 235, 0.8);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.02);
}

:deep(.el-tab-pane) {
  width: 100%;
}

.mysql-config {
  margin-top: 20px;
  padding-top: 20px;
  border-top: 1px solid #e5e7eb;
}

.action-buttons {
  display: flex;
  justify-content: space-between;
  align-items: stretch;
  margin-bottom: 30px;
  gap: 16px;
  width: 100%;
}

.action-buttons :deep(.el-button) {
  flex: 1;
  height: 50px;
  min-width: 0;
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
  margin-bottom: 24px;
  display: flex;
  align-items: flex-start;
}

:deep(.el-form-item__label) {
  color: #374151;
  font-weight: 500;
  font-size: 15px;
  line-height: 1.5;
  flex-shrink: 0;
  width: 120px !important;
}

:deep(.el-form-item__content) {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

:deep(.el-input) {
  width: 100%;
  border-radius: 12px;
}

:deep(.el-radio-group) {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

:deep(.el-input__wrapper) {
  background: rgba(255, 255, 255, 0.8);
  border: 2px solid rgba(229, 231, 235, 0.8);
  border-radius: 12px;
  transition: all 0.3s ease;
  padding: 12px 16px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.02);
  width: 100%;
  box-sizing: border-box;
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

:deep(.el-form-item .el-button--info) {
  height: 40px;
  padding: 0 20px;
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

  .action-buttons :deep(.el-button) {
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