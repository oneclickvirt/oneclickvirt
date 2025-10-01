<template>
  <div class="oauth2-callback-container">
    <el-card class="callback-card">
      <div v-if="loading" class="loading-container">
        <el-icon class="is-loading" :size="50">
          <Loading />
        </el-icon>
        <p class="loading-text">正在处理登录信息...</p>
      </div>
      
      <div v-else-if="error" class="error-container">
        <el-icon :size="50" color="#f56c6c">
          <CircleClose />
        </el-icon>
        <p class="error-text">{{ errorMessage }}</p>
        <el-button type="primary" @click="goToLogin">返回登录</el-button>
      </div>
      
      <div v-else class="success-container">
        <el-icon :size="50" color="#67c23a">
          <CircleCheck />
        </el-icon>
        <p class="success-text">登录成功！正在跳转...</p>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Loading, CircleClose, CircleCheck } from '@element-plus/icons-vue'
import { useUserStore } from '@/pinia/modules/user'

const router = useRouter()
const userStore = useUserStore()

const loading = ref(true)
const error = ref(false)
const errorMessage = ref('')

onMounted(async () => {
  try {
    // 从URL获取token和用户信息
    const urlParams = new URLSearchParams(window.location.search)
    const token = urlParams.get('token')
    const username = urlParams.get('user')
    
    if (!token) {
      throw new Error('未获取到登录凭证')
    }
    
    // 保存token到localStorage
    localStorage.setItem('token', token)
    
    // 获取完整的用户信息
    await userStore.GetUserInfo()
    
    // 显示成功消息
    ElMessage.success(`欢迎回来，${username || '用户'}！`)
    
    loading.value = false
    
    // 延迟跳转，让用户看到成功提示
    setTimeout(() => {
      // 根据用户类型跳转到相应页面
      if (userStore.userInfo.userType === 'admin') {
        router.push('/admin')
      } else {
        router.push('/user')
      }
    }, 1000)
    
  } catch (err) {
    console.error('OAuth2回调处理失败:', err)
    loading.value = false
    error.value = true
    errorMessage.value = err.message || '登录处理失败，请重试'
    ElMessage.error(errorMessage.value)
  }
})

const goToLogin = () => {
  router.push('/login')
}
</script>

<style scoped lang="scss">
.oauth2-callback-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  
  .callback-card {
    width: 400px;
    text-align: center;
    
    .loading-container,
    .error-container,
    .success-container {
      padding: 40px 20px;
      
      .el-icon {
        margin-bottom: 20px;
      }
      
      p {
        font-size: 16px;
        margin: 20px 0;
        color: #606266;
      }
      
      .loading-text {
        color: #409eff;
      }
      
      .error-text {
        color: #f56c6c;
      }
      
      .success-text {
        color: #67c23a;
      }
    }
    
    .el-button {
      margin-top: 20px;
    }
  }
}
</style>
