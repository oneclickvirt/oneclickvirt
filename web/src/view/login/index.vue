<template>
  <div class="login-container">
    <div class="login-form">
      <div class="login-header">
        <h2>用户登录</h2>
        <p>欢迎回来，请登录您的账号</p>
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
            placeholder="请输入用户名"
            prefix-icon="User"
            clearable
          />
        </el-form-item>

        <el-form-item prop="password">
          <el-input
            v-model="loginForm.password"
            type="password"
            placeholder="请输入密码"
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
              placeholder="请输入验证码"
              style="width: 60%"
            />
            <div
              class="captcha-image"
              @click="refreshCaptcha"
            >
              <img
                v-if="captchaImage"
                :src="captchaImage"
                alt="验证码"
              >
              <div
                v-else
                class="captcha-loading"
              >
                加载中...
              </div>
            </div>
          </div>
        </el-form-item>

        <div class="form-options">
          <el-checkbox v-model="loginForm.rememberMe">
            记住我
          </el-checkbox>
          <router-link
            to="/forgot-password"
            class="forgot-link"
          >
            忘记密码?
          </router-link>
        </div>

        <div class="form-actions">
          <el-button
            type="primary"
            :loading="loading"
            style="width: 100%;"
            @click="handleLogin"
          >
            登录
          </el-button>
        </div>

        <div class="form-footer">
          <p>
            还没有账号? <router-link to="/register">
              立即注册
            </router-link>
          </p>
        </div>

        <div class="admin-login">
          <router-link
            to="/admin/login"
            class="admin-link"
          >
            管理员登录
          </router-link>
        </div>
      </el-form>

      <!-- OAuth2登录 -->
      <div
        v-if="oauth2Enabled && oauth2Providers.length > 0"
        class="oauth2-login"
      >
        <el-divider>或使用第三方登录或注册</el-divider>
        <div class="oauth2-providers">
          <el-button
            v-for="provider in oauth2Providers"
            :key="provider.id"
            class="oauth2-button"
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
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '@/pinia/modules/user'
import { getCaptcha } from '@/api/auth'
import { useErrorHandler } from '@/composables/useErrorHandler'
import { getPublicConfig } from '@/api/public'
import { getEnabledOAuth2Providers } from '@/api/oauth2'
import { Connection } from '@element-plus/icons-vue'

const router = useRouter()
const userStore = useUserStore()
const { executeAsync, handleSubmit } = useErrorHandler()

const loginFormRef = ref()
const loading = ref(false)
const captchaImage = ref('')
const captchaId = ref('')
const oauth2Enabled = ref(false)
const oauth2Providers = ref([])

const loginForm = reactive({
  username: '',
  password: '',
  captcha: '',
  rememberMe: false,
  userType: 'user',
  loginType: 'password'
})

const loginRules = reactive({
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' }
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' }
  ],
  captcha: [
    { required: true, message: '请输入验证码', trigger: 'blur' }
  ]
})

const handleLogin = async () => {
  if (!loginFormRef.value) return

  await loginFormRef.value.validate(async (valid) => {
    if (!valid) return

    const result = await handleSubmit(async () => {
      return await userStore.userLogin({
        ...loginForm,
        captchaId: captchaId.value
      })
    }, {
      successMessage: '登录成功',
      showLoading: false // 使用组件自己的loading
    })

    if (result.success) {
      router.push('/user/dashboard')
    } else {
      refreshCaptcha() // 登录失败刷新验证码
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
  // 跳转到后端的OAuth2登录接口，使用provider_id参数
  window.location.href = `/api/v1/auth/oauth2/login?provider_id=${provider.id}`
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
    console.error('获取OAuth2配置失败:', error)
  }
}

onMounted(() => {
  refreshCaptcha()
  checkOAuth2Config()
})
</script>

<style scoped>
.login-container {
  display: flex;
  justify-content: center;
  align-items: center;
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

.login-form {
  width: 400px;
  padding: 40px;
  background-color: #fff;
  border-radius: 8px;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
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
}

.forgot-link {
  color: #409eff;
  text-decoration: none;
}

.form-actions {
  margin-bottom: 20px;
}

.form-footer {
  text-align: center;
  margin-bottom: 20px;
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
}

.captcha-image {
  width: 38%;
  height: 40px;
  border: 1px solid #dcdfe6;
  border-radius: 4px;
  overflow: hidden;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
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
  margin: 20px 0;
}

.oauth2-providers {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.oauth2-button {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid #dcdfe6;
  background: white;
  color: #606266;
}

.oauth2-button:hover {
  border-color: #409eff;
  color: #409eff;
}

@media (max-width: 768px) {
  .login-form {
    width: 90%;
    padding: 20px;
  }
}
</style>