<template>
  <div class="login-container">
    <!-- 顶部栏 -->
    <header class="auth-header">
      <div class="header-content">
        <div class="logo">
          <img
            src="@/assets/images/logo.png"
            alt="OneClickVirt Logo"
            class="logo-image"
          >
          <h1>OneClickVirt</h1>
        </div>
        <nav class="nav-actions">
          <button
            class="nav-link language-btn"
            @click="switchLanguage"
          >
            <el-icon><Operation /></el-icon>
            {{ languageStore.currentLanguage === 'zh-CN' ? 'English' : '中文' }}
          </button>
          <router-link
            to="/"
            class="nav-link home-btn"
          >
            <el-icon><HomeFilled /></el-icon>
            {{ t('common.backToHome') }}
          </router-link>
        </nav>
      </div>
    </header>

    <div class="login-form">
      <div class="login-header">
        <h2>{{ t('login.title') }}</h2>
        <p>{{ t('login.subtitle') }}</p>
      </div>

      <el-form
        ref="loginFormRef"
        :model="loginForm"
        :rules="loginRules"
        label-width="0"
        size="large"
      >
        <el-form-item prop="username">
          <el-input
            v-model="loginForm.username"
            :placeholder="t('login.pleaseEnterUsername')"
            prefix-icon="User"
            clearable
          />
        </el-form-item>

        <el-form-item prop="password">
          <el-input
            v-model="loginForm.password"
            type="password"
            :placeholder="t('login.pleaseEnterPassword')"
            prefix-icon="Lock"
            show-password
            clearable
            @keyup.enter="handleLogin"
          />
        </el-form-item>

        <el-form-item prop="captcha">
          <div class="captcha-container">
            <el-input
              v-model="loginForm.captcha"
              :placeholder="t('login.pleaseEnterCaptcha')"
            />
            <div
              class="captcha-image"
              @click="refreshCaptcha"
            >
              <img
                v-if="captchaImage"
                :src="captchaImage"
                :alt="t('login.captchaAlt')"
              >
              <div
                v-else
                class="captcha-loading"
              >
                {{ t('common.loading') }}
              </div>
            </div>
          </div>
        </el-form-item>

        <div class="form-options">
          <el-checkbox v-model="loginForm.rememberMe">
            {{ t('login.rememberMe') }}
          </el-checkbox>
          <router-link
            to="/forgot-password"
            class="forgot-link"
          >
            {{ t('login.forgotPassword') }}
          </router-link>
        </div>

        <div class="form-actions">
          <el-button
            type="primary"
            :loading="loading"
            style="width: 100%;"
            @click="handleLogin"
          >
            {{ t('common.login') }}
          </el-button>
        </div>

        <div class="form-footer">
          <p>
            {{ t('login.noAccount') }} <router-link to="/register">
              {{ t('login.registerNow') }}
            </router-link>
          </p>
        </div>

        <div class="admin-login">
          <router-link
            to="/admin/login"
            class="admin-link"
          >
            {{ t('login.adminLogin') }}
          </router-link>
        </div>
      </el-form>

      <!-- OAuth2登录 -->
      <div
        v-if="oauth2Enabled && oauth2Providers.length > 0"
        class="oauth2-login"
      >
        <el-divider>{{ t('login.thirdPartyLogin') }}</el-divider>
        <div class="oauth2-providers">
          <el-button
            v-for="provider in oauth2Providers"
            :key="provider.id"
            class="oauth2-button"
            :loading="oauth2Loading"
            :disabled="oauth2Loading"
            @click="handleOAuth2Login(provider)"
          >
            <el-icon><Connection /></el-icon>
            {{ provider.displayName }}
          </el-button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useUserStore } from '@/pinia/modules/user'
import { getCaptcha } from '@/api/auth'
import { useErrorHandler } from '@/composables/useErrorHandler'
import { getPublicConfig } from '@/api/public'
import { getEnabledOAuth2Providers } from '@/api/oauth2'
import { Connection, Operation, HomeFilled } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useLanguageStore } from '@/pinia/modules/language'

const router = useRouter()
const userStore = useUserStore()
const { t, locale } = useI18n()
const { executeAsync, handleSubmit } = useErrorHandler()
const languageStore = useLanguageStore()

const loginFormRef = ref()
const loading = ref(false)
const captchaImage = ref('')
const captchaId = ref('')
const oauth2Enabled = ref(false)
const oauth2Providers = ref([])
const oauth2Loading = ref(false) // OAuth2登录防重复点击

const loginForm = reactive({
  username: '',
  password: '',
  captcha: '',
  rememberMe: false,
  userType: 'user',
  loginType: 'password'
})

const loginRules = computed(() => ({
  username: [
    { required: true, message: t('validation.usernameRequired'), trigger: 'blur' }
  ],
  password: [
    { required: true, message: t('validation.passwordRequired'), trigger: 'blur' }
  ],
  captcha: [
    { required: true, message: t('validation.captchaRequired'), trigger: 'blur' }
  ]
}))

const handleLogin = async () => {
  if (!loginFormRef.value) return
  
  // 防止重复提交
  if (loading.value) return

  await loginFormRef.value.validate(async (valid) => {
    if (!valid) return
    
    // 再次检查loading状态，防止表单验证期间的重复点击
    if (loading.value) return
    
    loading.value = true
    
    try {
      const result = await handleSubmit(async () => {
        return await userStore.userLogin({
          ...loginForm,
          captchaId: captchaId.value
        })
      }, {
        successMessage: t('login.loginSuccess'),
        showLoading: false // 使用组件自己的loading
      })

      if (result.success) {
        // 根据用户类型和视图模式跳转
        const userType = userStore.userType
        const viewMode = userStore.viewMode || userType
        
        console.log('登录成功，用户类型:', userType, '视图模式:', viewMode)
        
        // 只有管理员可以访问管理员界面
        if (userType === 'admin' && viewMode === 'admin') {
          router.push('/admin/dashboard')
        } else {
          // 普通用户或管理员的用户视图
          router.push('/user/dashboard')
        }
      } else {
        refreshCaptcha() // 登录失败刷新验证码
      }
    } finally {
      loading.value = false
    }
  })
}

const refreshCaptcha = async () => {
  await executeAsync(async () => {
    const response = await getCaptcha()
    captchaImage.value = response.data.imageData
    captchaId.value = response.data.captchaId
    loginForm.captcha = ''
  }, {
    showError: false, // 静默处理验证码错误
    showLoading: false
  })
}

// OAuth2登录
const handleOAuth2Login = (provider) => {
  // 防止重复点击
  if (oauth2Loading.value) return
  
  oauth2Loading.value = true
  
  // 跳转到后端的OAuth2登录接口，使用provider_id参数
  window.location.href = `/api/v1/auth/oauth2/login?provider_id=${provider.id}`
  
  // 页面跳转后loading状态会自动重置，这里不需要手动重置
}

// 检查OAuth2配置并加载提供商列表
const checkOAuth2Config = async () => {
  try {
    // 获取OAuth2全局开关状态
    const configResponse = await getPublicConfig()
    oauth2Enabled.value = configResponse.data?.oauth2Enabled || false
    
    // 如果启用了OAuth2，加载提供商列表
    if (oauth2Enabled.value) {
      const providersResponse = await getEnabledOAuth2Providers()
      oauth2Providers.value = providersResponse.data || []
    }
  } catch (error) {
    console.error(t('login.getOAuth2ConfigFailed'), error)
  }
}

// 切换语言
const switchLanguage = () => {
  const newLang = languageStore.toggleLanguage()
  locale.value = newLang
  ElMessage.success(t('navbar.languageSwitched'))
}

onMounted(() => {
  refreshCaptcha()
  checkOAuth2Config()
})
</script>

<style scoped>
.login-container {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  background-color: #f5f7fa;
}

.login-container::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: linear-gradient(135deg, #74b9ff 0%, #0984e3 100%);
  background-size: cover;
  opacity: 0.1;
  z-index: -1;
}

/* 顶部栏样式 */
.auth-header {
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(20px);
  box-shadow: 0 2px 20px rgba(22, 163, 74, 0.1);
  border-bottom: 1px solid rgba(22, 163, 74, 0.1);
}

.header-content {
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 24px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  height: 70px;
}

.logo {
  display: flex;
  align-items: center;
  gap: 12px;
}

.logo-image {
  width: 48px;
  height: 48px;
  object-fit: contain;
}

.logo h1 {
  font-size: 28px;
  color: #16a34a;
  margin: 0;
  font-weight: 700;
  background: linear-gradient(135deg, #16a34a, #22c55e);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

.nav-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.nav-link {
  text-decoration: none;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 12px 24px;
  border-radius: 25px;
  border: 1px solid #e5e7eb;
  background: transparent;
  color: #374151;
  font-size: 16px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.3s ease;
}

.nav-link:hover {
  background: rgba(22, 163, 74, 0.1);
  color: #16a34a;
  transform: translateY(-2px);
}

.nav-link.home-btn {
  background: linear-gradient(135deg, #16a34a, #22c55e);
  color: white;
  border: none;
  box-shadow: 0 4px 15px rgba(22, 163, 74, 0.3);
}

.nav-link.home-btn:hover {
  background: linear-gradient(135deg, #15803d, #16a34a);
  transform: translateY(-2px);
  box-shadow: 0 6px 20px rgba(22, 163, 74, 0.4);
}

.login-form {
  margin: auto;
  margin-top: 60px;
  margin-bottom: 60px;
  width: 400px;
  padding: 40px;
  background-color: #fff;
  border-radius: 8px;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
}

.login-form :deep(.el-form) {
  width: 100%;
}

.login-form :deep(.el-form-item) {
  width: 100%;
  margin-bottom: 20px;
}

.login-form :deep(.el-form-item__content) {
  width: 100%;
  line-height: normal;
}

.login-form :deep(.el-input) {
  width: 100%;
}

.login-form :deep(.el-input__wrapper) {
  width: 100%;
  box-sizing: border-box;
}

.login-header {
  text-align: center;
  margin-bottom: 30px;
}

.login-header h2 {
  font-size: 24px;
  color: #303133;
  margin-bottom: 10px;
}

.login-header p {
  font-size: 14px;
  color: #909399;
}

.form-options {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
  width: 100%;
}

.forgot-link {
  color: #409eff;
  text-decoration: none;
}

.form-actions {
  margin-bottom: 20px;
  width: 100%;
}

.form-actions .el-button {
  width: 100% !important;
  height: 45px;
}

.form-footer {
  text-align: center;
  margin-bottom: 20px;
  width: 100%;
}

.form-footer a {
  color: #409eff;
  text-decoration: none;
}

.admin-login {
  text-align: center;
  font-size: 14px;
  color: #909399;
}

.admin-link {
  color: #909399;
  text-decoration: none;
  margin: 0 5px;
}

.admin-link:hover {
  color: #409eff;
}

.captcha-container {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  width: 100%;
}

.captcha-container .el-input {
  flex: 1;
}

.captcha-image {
  width: 120px;
  height: 40px;
  border: 1px solid #dcdfe6;
  border-radius: 4px;
  overflow: hidden;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.captcha-image img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.captcha-loading {
  font-size: 12px;
  color: #909399;
}

.oauth2-login {
  margin: 20px 0 0 0;
  width: 100%;
  padding: 0;
}

.oauth2-login :deep(.el-divider) {
  margin: 20px 0;
}

.oauth2-providers {
  display: flex;
  flex-direction: column;
  gap: 10px;
  width: 100%;
  padding: 0;
  margin: 0;
}

.oauth2-button {
  width: 100% !important;
  height: 45px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid #dcdfe6;
  background: white;
  color: #606266;
  margin: 0 !important;
  padding: 0 20px !important;
  box-sizing: border-box;
}

.oauth2-button:hover {
  border-color: #409eff;
  color: #409eff;
}

.oauth2-providers :deep(.el-button) {
  width: 100% !important;
  margin: 0 !important;
}

@media (max-width: 768px) {
  .login-form {
    width: 90%;
    padding: 20px;
  }
}
</style>