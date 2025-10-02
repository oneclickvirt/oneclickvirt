<template>
  <div class="profile-container">
    <!-- 加载状态 -->
    <div
      v-if="loading"
      class="loading-container"
    >
      <el-loading-directive />
      <div class="loading-text">
        加载个人信息中...
      </div>
    </div>
    
    <!-- 主要内容 -->
    <div v-else>
      <el-card class="profile-card">
        <template #header>
          <div class="card-header">
            <span>个人中心</span>
          </div>
        </template>

        <!-- 用户头像和基本信息 -->
        <div class="profile-header">
          <div class="avatar-section">
            <el-avatar
              :size="100"
              :src="userStore.getUserAvatar()"
            />
            <el-button
              type="primary"
              size="small"
              @click="showAvatarDialog = true"
            >
              更换头像
            </el-button>
          </div>
          <div class="user-info">
            <h2>{{ userStore.getUserDisplayName() }}</h2>
            <p class="username">
              @{{ userStore.user?.username }}
            </p>
            <el-tag :type="getUserTypeTagType()">
              >
              {{ userStore.getUserTypeText() }}
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
              label="基本信息"
              name="basic"
            >
              <el-form
                ref="profileFormRef"
                :model="profileForm"
                :rules="profileRules"
                label-width="100px"
                size="large"
              >
                <el-form-item label="用户名">
                  <el-input
                    v-model="profileForm.username"
                    disabled
                  />
                  <div class="form-tip">
                    用户名不可修改
                  </div>
                </el-form-item>

                <el-form-item
                  label="昵称"
                  prop="nickname"
                >
                  <el-input
                    v-model="profileForm.nickname"
                    placeholder="请输入昵称"
                    clearable
                  />
                </el-form-item>

                <el-form-item
                  label="邮箱"
                  prop="email"
                >
                  <el-input
                    v-model="profileForm.email"
                    placeholder="请输入邮箱地址"
                    clearable
                  />
                </el-form-item>

                <el-form-item
                  label="手机号"
                  prop="phone"
                >
                  <el-input
                    v-model="profileForm.phone"
                    placeholder="请输入手机号"
                    clearable
                  />
                </el-form-item>

                <el-form-item>
                  <el-button
                    type="primary"
                    :loading="updating"
                    @click="updateProfile"
                  >
                    保存修改
                  </el-button>
                  <el-button @click="resetForm">
                    重置
                  </el-button>
                </el-form-item>
              </el-form>
            </el-tab-pane>

            <!-- 密码管理标签页 -->
            <el-tab-pane
              label="密码管理"
              name="password"
            >
              <div class="password-section">
                <!-- 自动重置密码 -->
                <div class="password-reset-section">
                  <h3>自动重置密码</h3>
                  <div class="reset-intro">
                    <el-alert
                      title="密码自动重置"
                      type="warning"
                      :closable="false"
                      show-icon
                    >
                      <template #default>
                        <p>如果您忘记了当前密码，可以使用此功能自动生成一个新的安全密码。</p>
                        <p>新密码将显示在下方，并同时发送到您绑定的通信渠道。</p>
                        <p><strong>请确保您至少绑定了一个通信渠道，以便备份接收新密码。</strong></p>
                      </template>
                    </el-alert>
                  
                    <!-- 显示生成的新密码 -->
                    <div
                      v-if="generatedPassword"
                      class="generated-password"
                    >
                      <el-result
                        icon="success"
                        title="密码重置成功"
                        sub-title="已为您生成新密码，请复制并安全保管"
                      >
                        <template #extra>
                          <div style="margin: 20px 0;">
                            <el-text
                              type="info"
                              style="display: block; margin-bottom: 10px;"
                            >
                              新密码：
                            </el-text>
                            <el-input
                              v-model="generatedPassword"
                              readonly
                              style="width: 350px; font-family: monospace; font-size: 16px;"
                            >
                              <template #append>
                                <el-button @click="copyPassword">
                                  复制
                                </el-button>
                              </template>
                            </el-input>
                          </div>
                          <div style="margin: 20px 0;">
                            <el-text
                              size="small"
                              type="warning"
                            >
                              新密码已同时发送到您绑定的通信渠道，请妥善保管
                            </el-text>
                          </div>
                          <div style="margin-top: 20px;">
                            <el-button @click="closePasswordDialog">
                              关闭
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
                        重置密码
                      </el-button>
                    </div>
                  </div>
                </div>
              </div>
            </el-tab-pane>
          </el-tabs>
        </div>
      </el-card>

      <!-- 头像上传对话框 -->
      <el-dialog
        v-model="showAvatarDialog"
        title="更换头像"
        width="400px"
      >
        <el-upload
          class="avatar-uploader"
          action="/api/v1/upload/avatar"
          :headers="{ Authorization: `Bearer ${userStore.token}` }"
          :show-file-list="false"
          :on-success="handleAvatarSuccess"
          :before-upload="beforeAvatarUpload"
        >
          <img
            v-if="newAvatar"
            :src="newAvatar"
            class="avatar-preview"
          >
          <el-icon
            v-else
            class="avatar-uploader-icon"
          >
            <Plus />
          </el-icon>
        </el-upload>
        <template #footer>
          <el-button @click="showAvatarDialog = false">
            取消
          </el-button>
          <el-button
            type="primary"
            :disabled="!newAvatar"
            @click="confirmAvatar"
          >
            确认
          </el-button>
        </template>
      </el-dialog>
    </div> <!-- 结束主要内容区域 -->
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onActivated, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import { useUserStore } from '@/pinia/modules/user'
import { updateProfile as updateProfileApi, resetPassword } from '@/api/user'
import { validateImageFileSecure } from '@/utils/uploadValidator'

const userStore = useUserStore()

// 当前活动标签页
const activeTab = ref('basic')

// 表单引用
const profileFormRef = ref()

// 加载状态
const loading = ref(true)
const updating = ref(false)
const resetPasswordLoading = ref(false)

// 头像相关
const showAvatarDialog = ref(false)
const newAvatar = ref('')

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
    { type: 'email', message: '请输入正确的邮箱地址', trigger: 'blur' }
  ],
  phone: [
    { pattern: /^1[3-9]\d{9}$/, message: '请输入正确的手机号', trigger: 'blur' }
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
        ElMessage.success('个人信息更新成功')
        await userStore.fetchUserInfo()
      } else {
        ElMessage.error(response.msg || '更新失败')
      }
    } catch (error) {
      ElMessage.error('更新失败，请重试')
    } finally {
      updating.value = false
    }
  })
}

const resetForm = () => {
  initForm()
}

const beforeAvatarUpload = async (file) => {
  try {
    const validation = await validateImageFileSecure(file, {
      maxSize: 2 * 1024 * 1024, // 2MB
      allowedTypes: ['image/jpeg', 'image/png', 'image/webp'],
      showError: true
    })
    
    if (!validation.valid) {
      console.error('头像验证失败:', validation.errors)
    }
    
    return validation.valid
  } catch (error) {
    console.error('头像验证异常:', error)
    ElMessage.error('文件验证失败')
    return false
  }
}

const handleAvatarSuccess = (response) => {
  if (response.code === 0 || response.code === 200) {
    newAvatar.value = response.data.url
  } else {
    ElMessage.error('头像上传失败')
  }
}

const confirmAvatar = () => {
  ElMessage.success('头像更新成功')
  showAvatarDialog.value = false
  newAvatar.value = ''
}

// 确认密码重置
const confirmPasswordReset = async () => {
  try {
    await ElMessageBox.confirm(
      '确定要重置密码吗？新密码将自动生成并发送到您绑定的通信渠道。',
      '确认重置密码',
      {
        confirmButtonText: '确认重置',
        cancelButtonText: '取消',
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
        ElMessage.success('密码重置成功，新密码已显示在下方并发送到您的通信渠道')
      } else {
        ElMessage.success(response.msg || '密码重置成功，新密码已发送到您绑定的通信渠道')
      }
    } else {
      ElMessage.error(response.msg || '密码重置失败')
    }
  } catch (error) {
    console.error('Password reset error:', error)
    ElMessage.error('密码重置失败，请重试')
  } finally {
    resetPasswordLoading.value = false
  }
}

// 复制密码到剪贴板
const copyPassword = async () => {
  if (!generatedPassword.value) {
    ElMessage.warning('没有可复制的密码')
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(generatedPassword.value)
      ElMessage.success('密码已复制到剪贴板')
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
        ElMessage.success('密码已复制到剪贴板')
      } else {
        throw new Error('execCommand failed')
      }
    } finally {
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    ElMessage.error('复制失败，请手动复制')
  }
}

// 关闭密码对话框
const closePasswordDialog = () => {
  generatedPassword.value = ''
}

onMounted(async () => {
  // 添加强制页面刷新监听器
  window.addEventListener('force-page-refresh', handleForceRefresh)
  
  loading.value = true
  try {
    await initForm()
  } finally {
    loading.value = false
  }
})

// 使用 onActivated 确保每次页面激活时都重新加载数据
onActivated(async () => {
  loading.value = true
  try {
    await initForm()
  } finally {
    loading.value = false
  }
})

// 处理强制刷新事件
const handleForceRefresh = async (event) => {
  if (event.detail && event.detail.path === '/user/profile') {
    loading.value = true
    try {
      await initForm()
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

.generated-password {
  margin-top: 20px;
  padding: 20px;
  border: 1px solid #e4e7ed;
  border-radius: 8px;
  background-color: #f9f9f9;
}

.password-reset-section h3 {
  margin-bottom: 15px;
  color: #333;
}

.reset-intro {
  margin-bottom: 20px;
}
</style>