<template>
  <div class="admin-login-container">
    <div class="login-form">
      <div class="login-header">
        <h2>管理员登录</h2>
        <p>请输入管理员账号和密码</p>
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
            placeholder="请输入管理员用户名"
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

        <el-form-item>
          <el-button
            type="primary"
            :loading="loading"
            style="width: 100%;"
            @click="handleLogin"
          >
            登录
          </el-button>
        </el-form-item>

        <div class="form-footer">
          <router-link
            to="/login"
            class="back-link"
          >
            返回用户登录
          </router-link>
        </div>
      </el-form>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '@/pinia/modules/user'
import { ElMessage } from 'element-plus'
import { useErrorHandler } from '@/composables/useErrorHandler'

import { getCaptcha } from '@/api/auth'

const router = useRouter()
const userStore = useUserStore()
const { executeAsync, handleSubmit } = useErrorHandler()

const loginFormRef = ref()
const loading = ref(false)
const captchaImage = ref('')
const captchaId = ref('')

const loginForm = reactive({
  username: '',
  password: '',
  captcha: '',
  userType: 'admin',
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
      return await userStore.adminLogin({
        ...loginForm,
        captchaId: captchaId.value
      })
    }, {
      successMessage: '登录成功',
      showLoading: false // 使用组件自己的loading
    })

    if (result.success) {
      router.push('/admin/dashboard')
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

onMounted(() => {
  refreshCaptcha()
})
</script>

<style scoped>
.admin-login-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background-color: #f5f7fa;
}

.admin-login-container::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
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

.form-footer {
  text-align: center;
  margin-top: 20px;
  font-size: 14px;
  color: #909399;
  width: 100%;
}

.login-form :deep(.el-button) {
  width: 100% !important;
  height: 45px;
}

.back-link {
  color: #909399;
  text-decoration: none;
  margin: 0 5px;
}

.back-link:hover {
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

@media (max-width: 768px) {
  .login-form {
    width: 90%;
    padding: 20px;
  }
}
</style>