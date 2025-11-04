<template>
  <div class="forgot-password-container">
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

    <div class="forgot-password-form">
      <div v-if="!emailSent">
        <h2>{{ t('forgotPassword.title') }}</h2>
        <p>{{ t('forgotPassword.subtitle') }}</p>

        <el-form
          ref="forgotFormRef"
          :model="forgotForm"
          :rules="forgotRules"
          label-width="0"
          size="large"
        >
          <el-form-item prop="email">
            <el-input
              v-model="forgotForm.email"
              :placeholder="t('forgotPassword.pleaseEnterEmail')"
              prefix-icon="Message"
            />
          </el-form-item>

          <el-form-item prop="captcha">
            <div class="captcha-container">
              <el-input
                v-model="forgotForm.captcha"
                :placeholder="t('login.pleaseEnterCaptcha')"
                style="width: 60%"
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

          <el-form-item>
            <el-button
              type="primary"
              :loading="loading"
              style="width: 100%;"
              @click="handleForgotPassword"
            >
              {{ t('forgotPassword.sendResetLink') }}
            </el-button>
          </el-form-item>

          <div class="form-footer">
            <router-link to="/login">
              {{ t('forgotPassword.backToLogin') }}
            </router-link>
          </div>
        </el-form>
      </div>

      <div
        v-else
        class="success-message"
      >
        <el-result
          icon="success"
          :title="t('forgotPassword.emailSent')"
          :sub-title="t('forgotPassword.checkEmail')"
        >
          <template #extra>
            <el-button
              type="primary"
              @click="goToLogin"
            >
              {{ t('forgotPassword.backToLogin') }}
            </el-button>
          </template>
        </el-result>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { forgotPassword } from '@/api/auth'
import { getCaptcha } from '@/api/auth'
import { Operation, HomeFilled } from '@element-plus/icons-vue'
import { useLanguageStore } from '@/pinia/modules/language'

const router = useRouter()
const { t, locale } = useI18n()
const languageStore = useLanguageStore()
const forgotFormRef = ref()
const loading = ref(false)
const emailSent = ref(false)
const captchaImage = ref('')
const captchaId = ref('')

const forgotForm = reactive({
  email: '',
  captcha: ''
})

const forgotRules = computed(() => ({
  email: [
    { required: true, message: t('validation.emailRequired'), trigger: 'blur' },
    { type: 'email', message: t('validation.emailFormat'), trigger: 'blur' }
  ],
  captcha: [
    { required: true, message: t('validation.captchaRequired'), trigger: 'blur' }
  ]
}))

const handleForgotPassword = async () => {
  if (!forgotFormRef.value) return

  await forgotFormRef.value.validate(async (valid) => {
    if (!valid) return

    loading.value = true
    try {
      const response = await forgotPassword({
        email: forgotForm.email,
        captcha: forgotForm.captcha,
        captchaId: captchaId.value
      })

      if (response.code === 0 || response.code === 200) {
        emailSent.value = true
      }
    } catch (error) {
      console.error(t('forgotPassword.resetFailed'), error)
      ElMessage.error(t('forgotPassword.resetFailed'))
      refreshCaptcha()
    } finally {
      loading.value = false
    }
  })
}

const refreshCaptcha = async () => {
  try {
    const response = await getCaptcha()
    if (response.code === 0 || response.code === 200) {
      captchaImage.value = response.data.imageData
      captchaId.value = response.data.captchaId
      forgotForm.captcha = ''
    }
  } catch (error) {
    console.error(t('login.captchaFailed'), error)
  }
}

const goToLogin = () => {
  router.push('/login')
}

// 切换语言
const switchLanguage = () => {
  const newLang = languageStore.toggleLanguage()
  locale.value = newLang
  ElMessage.success(t('navbar.languageSwitched'))
}

onMounted(() => {
  refreshCaptcha()
})
</script>

<style scoped>
.forgot-password-container {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  background-color: #f5f7fa;
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

.forgot-password-form {
  margin: auto;
  margin-top: 60px;
  margin-bottom: 60px;
  width: 400px;
  padding: 40px;
  background-color: #fff;
  border-radius: 8px;
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
}

.forgot-password-form h2 {
  font-size: 24px;
  color: #303133;
  margin-bottom: 10px;
  text-align: center;
}

.forgot-password-form p {
  font-size: 14px;
  color: #909399;
  margin-bottom: 30px;
  text-align: center;
}

.form-footer {
  text-align: center;
  margin-top: 20px;
}

.form-footer a {
  color: #409eff;
  text-decoration: none;
}

.success-message {
  text-align: center;
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

@media (max-width: 768px) {
  .forgot-password-form {
    width: 90%;
    padding: 20px;
  }
}
</style>