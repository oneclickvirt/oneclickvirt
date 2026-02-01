<template>
  <div class="profile-container">
    <!-- 加载状态 -->
    <div
      v-if="loading"
      class="loading-container"
    >
      <el-loading-directive />
      <div class="loading-text">
        {{ t('user.profile.loadingProfile') }}
      </div>
    </div>
    
    <!-- 主要内容 -->
    <div v-else>
      <el-card class="profile-card">
        <template #header>
          <div class="card-header">
            <span>{{ t('user.profile.title') }}</span>
          </div>
        </template>

        <!-- 用户头像和基本信息 -->
        <div class="profile-header">
          <div class="avatar-section">
            <el-avatar
              :size="100"
              :src="userStore.getUserAvatar()"
            />
          </div>
          <div class="user-info">
            <h2>{{ userStore.getUserDisplayName() }}</h2>
            <p class="username">
              @{{ userStore.user?.username }}
            </p>
            <el-tag :type="getUserTypeTagType()">
              >
              {{ getUserTypeText() }}
            </el-tag>
          </div>
        </div>

        <!-- 标签页内容 -->
        <div class="profile-content">
          <el-divider />
        
          <el-tabs
            v-model="activeTab"
            type="card"
            class="profile-tabs"
          >
            <!-- 基本信息标签页 -->
            <el-tab-pane
              :label="t('user.profile.basicInfo')"
              name="basic"
            >
              <el-form
                ref="profileFormRef"
                :model="profileForm"
                :rules="profileRules"
                label-width="100px"
                size="large"
              >
                <el-form-item :label="t('user.profile.username')">
                  <el-input
                    v-model="profileForm.username"
                    disabled
                  />
                  <div class="form-tip">
                    {{ t('user.profile.usernameCannotChange') }}
                  </div>
                </el-form-item>

                <el-form-item
                  :label="t('user.profile.nickname')"
                  prop="nickname"
                >
                  <el-input
                    v-model="profileForm.nickname"
                    :placeholder="t('user.profile.pleaseEnterNickname')"
                    clearable
                  />
                </el-form-item>

                <el-form-item
                  :label="t('user.profile.email')"
                  prop="email"
                >
                  <el-input
                    v-model="profileForm.email"
                    :placeholder="t('user.profile.pleaseEnterEmail')"
                    clearable
                  />
                </el-form-item>

                <el-form-item
                  :label="t('user.profile.phone')"
                  prop="phone"
                >
                  <el-input
                    v-model="profileForm.phone"
                    :placeholder="t('user.profile.pleaseEnterPhone')"
                    clearable
                  />
                </el-form-item>

                <el-form-item>
                  <el-button
                    type="primary"
                    :loading="updating"
                    @click="updateProfile"
                  >
                    {{ t('user.profile.saveChanges') }}
                  </el-button>
                  <el-button @click="resetForm">
                    {{ t('common.reset') }}
                  </el-button>
                </el-form-item>
              </el-form>
            </el-tab-pane>

            <!-- 密码管理标签页 -->
            <el-tab-pane
              :label="t('user.profile.passwordManagement')"
              name="password"
            >
              <div class="password-section">
                <!-- 自动重置密码 -->
                <div class="password-reset-section">
                  <h3>{{ t('user.profile.autoResetPassword') }}</h3>
                  <div class="reset-intro">
                    <el-alert
                      :title="t('user.profile.passwordAutoReset')"
                      type="warning"
                      :closable="false"
                      show-icon
                    >
                      <template #default>
                        <p>{{ t('user.profile.autoResetDescription1') }}</p>
                        <p>{{ t('user.profile.autoResetDescription2') }}</p>
                        <p><strong>{{ t('user.profile.autoResetDescription3') }}</strong></p>
                      </template>
                    </el-alert>
                  
                    <!-- 显示生成的新密码 -->
                    <div
                      v-if="generatedPassword"
                      class="generated-password"
                    >
                      <el-result
                        icon="success"
                        :title="t('user.profile.passwordResetSuccess')"
                        :sub-title="t('user.profile.newPasswordGenerated')"
                      >
                        <template #extra>
                          <div style="margin: 20px 0;">
                            <el-text
                              type="info"
                              style="display: block; margin-bottom: 10px;"
                            >
                              {{ t('user.profile.newPassword') }}：
                            </el-text>
                            <el-input
                              v-model="generatedPassword"
                              readonly
                              style="width: 350px; font-family: monospace; font-size: 16px;"
                            >
                              <template #append>
                                <el-button @click="copyPassword">
                                  {{ t('common.copy') }}
                                </el-button>
                              </template>
                            </el-input>
                          </div>
                          <div style="margin: 20px 0;">
                            <el-text
                              size="small"
                              type="warning"
                            >
                              {{ t('user.profile.passwordSentToChannel') }}
                            </el-text>
                          </div>
                          <div style="margin-top: 20px;">
                            <el-button @click="closePasswordDialog">
                              {{ t('common.close') }}
                            </el-button>
                          </div>
                        </template>
                      </el-result>
                    </div>
                  
                    <!-- 重置密码按钮 -->
                    <div
                      v-else
                      style="margin-top: 20px;"
                    >
                      <el-button
                        type="danger"
                        :loading="resetPasswordLoading"
                        @click="confirmPasswordReset"
                      >
                        {{ t('user.profile.resetPassword') }}
                      </el-button>
                    </div>
                  </div>
                </div>
              </div>
            </el-tab-pane>
          </el-tabs>
        </div>
      </el-card>
    </div> <!-- 结束主要内容区域 -->
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onActivated, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '@/pinia/modules/user'
import { updateProfile as updateProfileApi, resetPassword } from '@/api/user'

const { t } = useI18n()
const userStore = useUserStore()

// 当前活动标签页
const activeTab = ref('basic')

// 表单引用
const profileFormRef = ref()

// 加载状态
const loading = ref(true)
const updating = ref(false)
const resetPasswordLoading = ref(false)

// 密码重置相关
const generatedPassword = ref('')

// 个人信息表单
const profileForm = reactive({
  username: '',
  nickname: '',
  email: '',
  phone: ''
})

const profileRules = reactive({
  nickname: [
    { required: true, message: '请输入昵称', trigger: 'blur' },
    { min: 2, max: 20, message: '昵称长度在 2 到 20 个字符', trigger: 'blur' }
  ],
  email: [
    { type: 'email', message: '请输入正确的邮箱格式', trigger: 'blur' }
  ],
  phone: [
    { pattern: /^1[3-9]\d{9}$/, message: '请输入正确的手机号格式', trigger: 'blur' }
  ]
})

const getUserTypeTagType = () => {
  switch (userStore.userType) {
    case 'admin':
      return 'danger'
    default:
      return 'primary'
  }
}

const getUserTypeText = () => {
  switch (userStore.userType) {
    case 'admin':
      return t('common.admin')
    case 'user':
      return t('common.normalUser')
    default:
      return t('common.unknown')
  }
}

const initForm = () => {
  if (userStore.user) {
    profileForm.username = userStore.user.username
    profileForm.nickname = userStore.user.nickname || ''
    profileForm.email = userStore.user.email || ''
    profileForm.phone = userStore.user.phone || ''
  }
}

const updateProfile = async () => {
  if (!profileFormRef.value) return
  
  await profileFormRef.value.validate(async (valid) => {
    if (!valid) return
    
    updating.value = true
    try {
      const response = await updateProfileApi(profileForm)
      if (response.code === 0 || response.code === 200) {
        ElMessage.success(t('user.profile.updateSuccess'))
        await userStore.fetchUserInfo()
      } else {
        ElMessage.error(response.msg || t('user.profile.updateFailed'))
      }
    } catch (error) {
      ElMessage.error(t('user.profile.updateFailedRetry'))
    } finally {
      updating.value = false
    }
  })
}

const resetForm = () => {
  initForm()
}

// 确认密码重置
const confirmPasswordReset = async () => {
  try {
    await ElMessageBox.confirm(
      t('user.profile.passwordResetConfirm'),
      t('common.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    await resetUserPassword()
  } catch {
    // 用户取消操作
  }
}

// 重置密码
const resetUserPassword = async () => {
  resetPasswordLoading.value = true
  try {
    const response = await resetPassword()
    if (response.code === 0 || response.code === 200) {
      // 获取返回的新密码
      if (response.data && response.data.newPassword) {
        generatedPassword.value = response.data.newPassword
        ElMessage.success(t('user.profile.passwordResetSuccessWithMessage'))
      } else {
        ElMessage.success(response.msg || t('user.profile.passwordResetSuccessDefault'))
      }
    } else {
      ElMessage.error(response.msg || t('user.profile.passwordResetFailed'))
    }
  } catch (error) {
    console.error('Password reset error:', error)
    ElMessage.error(t('user.profile.passwordResetFailedRetry'))
  } finally {
    resetPasswordLoading.value = false
  }
}

// 复制密码到剪贴板
const copyPassword = async () => {
  if (!generatedPassword.value) {
    ElMessage.warning(t('user.profile.noPasswordToCopy'))
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(generatedPassword.value)
      ElMessage.success(t('user.profile.passwordCopied'))
      return
    }
    
    // 降级方案：使用传统的 document.execCommand
    const textArea = document.createElement('textarea')
    textArea.value = generatedPassword.value
    textArea.style.position = 'fixed'
    textArea.style.left = '-999999px'
    textArea.style.top = '-999999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()
    
    try {
      // @ts-ignore - execCommand 已废弃但作为降级方案仍需使用
      const successful = document.execCommand('copy')
      if (successful) {
        ElMessage.success(t('user.profile.passwordCopied'))
      } else {
        throw new Error('execCommand failed')
      }
    } finally {
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    ElMessage.error(t('user.profile.copyFailed'))
  }
}

// 关闭密码对话框
const closePasswordDialog = () => {
  generatedPassword.value = ''
}

onMounted(() => {
  // 强制页面刷新监听器
  window.addEventListener('force-page-refresh', handleForceRefresh)
  
  loading.value = true
  try {
    initForm()
  } finally {
    loading.value = false
  }
})

// 使用 onActivated 确保每次页面激活时都重新加载数据
onActivated(() => {
  loading.value = true
  try {
    initForm()
  } finally {
    loading.value = false
  }
})

// 处理强制刷新事件
const handleForceRefresh = (event) => {
  if (event.detail && event.detail.path === '/user/profile') {
    loading.value = true
    try {
      initForm()
    } finally {
      loading.value = false
    }
  }
}

onUnmounted(() => {
  // 清理事件监听器
  window.removeEventListener('force-page-refresh', handleForceRefresh)
})
</script>

<style scoped>
.loading-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 400px;
  color: #666;
}

.loading-text {
  margin-top: 16px;
  font-size: 14px;
}

.profile-container {
  padding: 20px;
  max-width: 800px;
  margin: 0 auto;
}

.profile-card {
  box-shadow: 0 2px 12px 0 rgba(0, 0, 0, 0.1);
}

.card-header {
  font-size: 18px;
  font-weight: 600;
  color: #333;
}

.profile-content {
  padding: 20px;
}

.profile-header {
  display: flex;
  align-items: center;
  margin-bottom: 30px;
}

.avatar-section {
  text-align: center;
  margin-right: 30px;
}

.avatar-section .el-button {
  margin-top: 10px;
}

.user-info h2 {
  margin: 0 0 10px 0;
  color: #333;
  font-size: 24px;
}

.username {
  margin: 0 0 10px 0;
  color: #666;
  font-size: 14px;
}

.form-tip {
  font-size: 12px;
  color: #999;
  margin-top: 5px;
}

.password-section h3 {
  margin: 0 0 20px 0;
  color: #333;
  font-size: 16px;
  font-weight: 600;
}

.avatar-uploader {
  text-align: center;
}

.avatar-uploader .el-upload {
  border: 1px dashed #d9d9d9;
  border-radius: 6px;
  cursor: pointer;
  position: relative;
  overflow: hidden;
  transition: 0.2s;
}

.avatar-uploader .el-upload:hover {
  border-color: #409eff;
}

.avatar-uploader-icon {
  font-size: 28px;
  color: #8c939d;
  width: 178px;
  height: 178px;
  line-height: 178px;
  text-align: center;
}

.avatar-preview {
  width: 178px;
  height: 178px;
  display: block;
}

.password-hint {
  margin-top: 5px;
  font-size: 12px;
  line-height: 1.4;
}

.password-reset-section {
  margin-bottom: 30px;
}

.reset-intro {
  margin-top: 15px;
}

.generated-password {
  margin-top: 20px;
  text-align: center;
}

.profile-tabs {
  margin-top: 20px;
}
</style>
