<template>
  <div class="oauth2-config-container">
    <el-form
      ref="formRef"
      v-loading="loading"
      :model="formData"
      :rules="rules"
      label-width="150px"
      class="config-form"
    >
      <el-card
        class="oauth-card"
        shadow="never"
      >
        <template #header>
          <div class="card-header">
            <span>OAuth2 登录配置</span>
            <el-switch
              v-model="formData.enabled"
              active-text="启用"
              inactive-text="禁用"
            />
          </div>
        </template>
        <el-divider content-position="left">
          基础配置
        </el-divider>

        <el-form-item
          label="Client ID"
          prop="clientId"
        >
          <el-input
            v-model="formData.clientId"
            placeholder="请输入OAuth2 Client ID"
          />
        </el-form-item>

        <el-form-item
          label="Client Secret"
          prop="clientSecret"
        >
          <el-input
            v-model="formData.clientSecret"
            type="password"
            placeholder="请输入OAuth2 Client Secret"
            show-password
          />
        </el-form-item>

        <el-form-item
          label="Redirect URL"
          prop="redirectUrl"
        >
          <el-input
            v-model="formData.redirectUrl"
            placeholder="例如: http://localhost:8888/api/v1/auth/oauth2/callback"
          />
        </el-form-item>

        <el-divider content-position="left">
          OAuth2 端点配置
        </el-divider>

        <el-form-item
          label="授权地址"
          prop="authUrl"
        >
          <el-input
            v-model="formData.authUrl"
            placeholder="例如: https://connect.linux.do/oauth2/authorize"
          />
        </el-form-item>

        <el-form-item
          label="令牌地址"
          prop="tokenUrl"
        >
          <el-input
            v-model="formData.tokenUrl"
            placeholder="例如: https://connect.linux.do/oauth2/token"
          />
        </el-form-item>

        <el-form-item
          label="用户信息地址"
          prop="userinfoUrl"
        >
          <el-input
            v-model="formData.userinfoUrl"
            placeholder="例如: https://connect.linux.do/api/user"
          />
        </el-form-item>

        <el-form-item
          label="权限范围"
          prop="scopes"
        >
          <el-select
            v-model="formData.scopes"
            multiple
            filterable
            allow-create
            placeholder="请选择或输入权限范围"
            style="width: 100%"
          >
            <el-option
              label="read"
              value="read"
            />
            <el-option
              label="openid"
              value="openid"
            />
            <el-option
              label="profile"
              value="profile"
            />
            <el-option
              label="email"
              value="email"
            />
          </el-select>
        </el-form-item>

        <el-divider content-position="left">
          字段映射配置
        </el-divider>

        <el-form-item
          label="用户ID字段"
          prop="userIdField"
        >
          <el-input
            v-model="formData.userIdField"
            placeholder="例如: id"
          >
            <template #append>
              支持嵌套，如: user.id
            </template>
          </el-input>
        </el-form-item>

        <el-form-item
          label="用户名字段"
          prop="usernameField"
        >
          <el-input
            v-model="formData.usernameField"
            placeholder="例如: username"
          />
        </el-form-item>

        <el-form-item
          label="邮箱字段"
          prop="emailField"
        >
          <el-input
            v-model="formData.emailField"
            placeholder="例如: email"
          />
        </el-form-item>

        <el-form-item
          label="头像字段"
          prop="avatarField"
        >
          <el-input
            v-model="formData.avatarField"
            placeholder="例如: avatar_url"
          />
        </el-form-item>

        <el-form-item
          label="信任等级字段"
          prop="trustLevelField"
        >
          <el-input
            v-model="formData.trustLevelField"
            placeholder="例如: trust_level"
          />
        </el-form-item>

        <el-divider content-position="left">
          注册限制
        </el-divider>

        <el-form-item
          label="最大注册数"
          prop="maxRegistrations"
        >
          <el-input-number
            v-model="formData.maxRegistrations"
            :min="0"
            placeholder="0表示无限制"
            style="width: 100%"
          />
          <div class="form-item-tip">
            0表示无限制，大于0表示最多允许多少个OAuth2用户注册
          </div>
        </el-form-item>

        <el-form-item
          label="当前注册数"
        >
          <el-input-number
            v-model="formData.currentRegistrations"
            :min="0"
            disabled
            style="width: 100%"
          />
          <div class="form-item-tip">
            已通过OAuth2注册的用户数量
            <el-button
              type="danger"
              size="small"
              plain
              @click="resetRegistrationCount"
            >
              重置计数
            </el-button>
          </div>
        </el-form-item>

        <el-divider content-position="left">
          等级映射
        </el-divider>

        <el-form-item label="等级映射规则">
          <div class="level-mapping-container">
            <div
              v-for="(userLevel, trustLevel) in formData.levelMapping"
              :key="trustLevel"
              class="level-mapping-item"
            >
              <span class="mapping-label">Trust Level {{ trustLevel }}</span>
              <el-icon><Right /></el-icon>
              <el-select
                v-model="formData.levelMapping[trustLevel]"
                placeholder="选择系统等级"
              >
                <el-option
                  v-for="level in availableLevels"
                  :key="level"
                  :label="`等级 ${level}`"
                  :value="level"
                />
              </el-select>
            </div>
          </div>
          <div class="form-item-tip">
            将LinuxDo的trust_level映射到系统用户等级
          </div>
        </el-form-item>

        <el-form-item>
          <el-button
            type="primary"
            :loading="saving"
            @click="handleSave"
          >
            保存配置
          </el-button>
          <el-button @click="loadConfig">
            重置
          </el-button>
        </el-form-item>
      </el-card>
    </el-form>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Right } from '@element-plus/icons-vue'
import { getOAuth2Config, updateOAuth2Config, resetOAuth2RegistrationCount } from '@/api/config'

const formRef = ref()
const loading = ref(false)
const saving = ref(false)
const availableLevels = [1, 2, 3, 4, 5]

const formData = reactive({
  enabled: false,
  clientId: '',
  clientSecret: '',
  redirectUrl: '',
  authUrl: '',
  tokenUrl: '',
  userinfoUrl: '',
  scopes: ['read', 'openid'],
  userIdField: 'id',
  usernameField: 'username',
  emailField: 'email',
  avatarField: 'avatar_url',
  trustLevelField: 'trust_level',
  maxRegistrations: 0,
  currentRegistrations: 0,
  levelMapping: {
    0: 1,
    1: 1,
    2: 1,
    3: 1,
    4: 1
  }
})

const rules = reactive({
  clientId: [
    { required: true, message: '请输入Client ID', trigger: 'blur' }
  ],
  clientSecret: [
    { required: true, message: '请输入Client Secret', trigger: 'blur' }
  ],
  redirectUrl: [
    { required: true, message: '请输入Redirect URL', trigger: 'blur' }
  ],
  authUrl: [
    { required: true, message: '请输入授权地址', trigger: 'blur' }
  ],
  tokenUrl: [
    { required: true, message: '请输入令牌地址', trigger: 'blur' }
  ],
  userinfoUrl: [
    { required: true, message: '请输入用户信息地址', trigger: 'blur' }
  ]
})

const loadConfig = async () => {
  loading.value = true
  try {
    const response = await getOAuth2Config()
    if (response.code === 0 && response.data) {
      Object.assign(formData, response.data)
      
      // 确保levelMapping是对象
      if (!formData.levelMapping || typeof formData.levelMapping !== 'object') {
        formData.levelMapping = {
          0: 1,
          1: 1,
          2: 1,
          3: 1,
          4: 1
        }
      }
    }
  } catch (error) {
    ElMessage.error('加载OAuth2配置失败')
    console.error(error)
  } finally {
    loading.value = false
  }
}

const handleSave = async () => {
  if (!formRef.value) return

  await formRef.value.validate(async (valid) => {
    if (!valid) return

    saving.value = true
    try {
      // 转换levelMapping的键为整数
      const levelMappingInt = {}
      Object.keys(formData.levelMapping).forEach(key => {
        levelMappingInt[parseInt(key)] = formData.levelMapping[key]
      })

      const data = {
        ...formData,
        levelMapping: levelMappingInt
      }

      const response = await updateOAuth2Config(data)
      if (response.code === 0) {
        ElMessage.success('OAuth2配置保存成功')
        await loadConfig()
      } else {
        ElMessage.error(response.message || '保存失败')
      }
    } catch (error) {
      ElMessage.error('保存OAuth2配置失败')
      console.error(error)
    } finally {
      saving.value = false
    }
  })
}

const resetRegistrationCount = async () => {
  try {
    await ElMessageBox.confirm(
      '确定要重置OAuth2注册计数吗？此操作不可撤销。',
      '警告',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    const response = await resetOAuth2RegistrationCount()
    if (response.code === 0) {
      ElMessage.success('注册计数已重置')
      await loadConfig()
    } else {
      ElMessage.error(response.message || '重置失败')
    }
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('重置失败')
      console.error(error)
    }
  }
}

onMounted(() => {
  loadConfig()
})
</script>

<style scoped>
.oauth2-config-container {
  padding: 0;
}

.oauth-card {
  margin-bottom: 20px;
}

.config-form {
  padding: 20px 0;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.form-item-tip {
  font-size: 12px;
  color: #909399;
  margin-top: 5px;
  display: flex;
  align-items: center;
  gap: 10px;
}

.level-mapping-container {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.level-mapping-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px;
  background: #f5f7fa;
  border-radius: 4px;
}

.mapping-label {
  min-width: 120px;
  font-weight: 500;
}

:deep(.el-divider__text) {
  font-weight: 600;
  color: #303133;
}
</style>
