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
            <span>{{ t('admin.config.oauth2LoginConfig') }}</span>
            <el-switch
              v-model="formData.enabled"
              :active-text="t('common.enabled')"
              :inactive-text="t('common.disabled')"
            />
          </div>
        </template>
        <el-divider content-position="left">
          {{ t('admin.config.oauth2BasicConfig') }}
        </el-divider>

        <el-form-item
          :label="t('admin.config.oauth2ClientIdLabel')"
          prop="clientId"
        >
          <el-input
            v-model="formData.clientId"
            :placeholder="t('admin.config.oauth2ClientIdPlaceholder')"
          />
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2ClientSecretLabel')"
          prop="clientSecret"
        >
          <el-input
            v-model="formData.clientSecret"
            type="password"
            :placeholder="t('admin.config.oauth2ClientSecretPlaceholder')"
            show-password
          />
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2RedirectUrlLabel')"
          prop="redirectUrl"
        >
          <el-input
            v-model="formData.redirectUrl"
            :placeholder="t('admin.config.oauth2RedirectUrlPlaceholder')"
          />
        </el-form-item>

        <el-divider content-position="left">
          {{ t('admin.config.oauth2EndpointConfig') }}
        </el-divider>

        <el-form-item
          :label="t('admin.config.oauth2AuthUrlLabel')"
          prop="authUrl"
        >
          <el-input
            v-model="formData.authUrl"
            :placeholder="t('admin.config.oauth2AuthUrlPlaceholder')"
          />
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2TokenUrlLabel')"
          prop="tokenUrl"
        >
          <el-input
            v-model="formData.tokenUrl"
            :placeholder="t('admin.config.oauth2TokenUrlPlaceholder')"
          />
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2UserInfoUrlLabel')"
          prop="userinfoUrl"
        >
          <el-input
            v-model="formData.userinfoUrl"
            :placeholder="t('admin.config.oauth2UserInfoUrlPlaceholder')"
          />
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2ScopesLabel')"
          prop="scopes"
        >
          <el-select
            v-model="formData.scopes"
            multiple
            filterable
            allow-create
            :placeholder="t('admin.config.oauth2ScopesPlaceholder')"
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
          {{ t('admin.config.oauth2FieldMapping') }}
        </el-divider>

        <el-form-item
          :label="t('admin.config.oauth2UserIdFieldLabel')"
          prop="userIdField"
        >
          <el-input
            v-model="formData.userIdField"
            :placeholder="t('admin.config.oauth2UserIdFieldPlaceholder')"
          >
            <template #append>
              {{ t('admin.config.oauth2UserIdFieldAppend') }}
            </template>
          </el-input>
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2UsernameFieldLabel')"
          prop="usernameField"
        >
          <el-input
            v-model="formData.usernameField"
            :placeholder="t('admin.config.oauth2UsernameFieldPlaceholder')"
          />
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2EmailFieldLabel')"
          prop="emailField"
        >
          <el-input
            v-model="formData.emailField"
            :placeholder="t('admin.config.oauth2EmailFieldPlaceholder')"
          />
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2AvatarFieldLabel')"
          prop="avatarField"
        >
          <el-input
            v-model="formData.avatarField"
            :placeholder="t('admin.config.oauth2AvatarFieldPlaceholder')"
          />
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2TrustLevelFieldLabel')"
          prop="trustLevelField"
        >
          <el-input
            v-model="formData.trustLevelField"
            :placeholder="t('admin.config.oauth2TrustLevelFieldPlaceholder')"
          />
        </el-form-item>

        <el-divider content-position="left">
          {{ t('admin.config.oauth2RegistrationLimit') }}
        </el-divider>

        <el-form-item
          :label="t('admin.config.oauth2MaxRegistrations')"
          prop="maxRegistrations"
        >
          <el-input-number
            v-model="formData.maxRegistrations"
            :min="0"
            :controls="false"
            :placeholder="t('admin.config.oauth2MaxRegistrationsPlaceholder')"
            style="width: 100%"
          />
          <div class="form-item-tip">
            {{ t('admin.config.oauth2MaxRegistrationsHint') }}
          </div>
        </el-form-item>

        <el-form-item
          :label="t('admin.config.oauth2CurrentRegistrations')"
        >
          <el-input-number
            v-model="formData.currentRegistrations"
            :min="0"
            :controls="false"
            disabled
            style="width: 100%"
          />
          <div class="form-item-tip">
            {{ t('admin.config.oauth2CurrentRegistrationsHint') }}
            <el-button
              type="danger"
              size="small"
              plain
              @click="resetRegistrationCount"
            >
              {{ t('admin.config.oauth2ResetCount') }}
            </el-button>
          </div>
        </el-form-item>

        <el-divider content-position="left">
          {{ t('admin.config.oauth2LevelMapping') }}
        </el-divider>

        <el-form-item :label="t('admin.config.oauth2LevelMapping')">
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
                :placeholder="t('admin.config.oauth2SelectLevel')"
              >
                <el-option
                  v-for="level in availableLevels"
                  :key="level"
                  :label="t('admin.config.oauth2LevelLabel', { level })"
                  :value="level"
                />
              </el-select>
            </div>
          </div>
          <div class="form-item-tip">
            {{ t('admin.config.oauth2LevelMappingHint') }}
          </div>
        </el-form-item>

        <el-form-item>
          <el-button
            type="primary"
            :loading="saving"
            @click="handleSave"
          >
            {{ t('admin.config.oauth2SaveConfig') }}
          </el-button>
          <el-button @click="loadConfig">
            {{ t('admin.config.oauth2ResetConfig') }}
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
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const providerId = ref(null)

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
    { required: true, message: () => t('admin.oauth2.validationClientId'), trigger: 'blur' }
  ],
  clientSecret: [
    { required: true, message: () => t('admin.oauth2.validationClientSecret'), trigger: 'blur' }
  ],
  redirectUrl: [
    { required: true, message: () => t('admin.oauth2.validationRedirectUrl'), trigger: 'blur' }
  ],
  authUrl: [
    { required: true, message: () => t('admin.oauth2.validationAuthUrl'), trigger: 'blur' }
  ],
  tokenUrl: [
    { required: true, message: () => t('admin.oauth2.validationTokenUrl'), trigger: 'blur' }
  ],
  userinfoUrl: [
    { required: true, message: () => t('admin.oauth2.validationUserInfoUrl'), trigger: 'blur' }
  ]
})

const loadConfig = async () => {
  loading.value = true
  try {
    const response = await getOAuth2Config()
    if ((response.code === 200) && response.data) {
      // Response is a providers array; use the first provider as the config
      const providers = Array.isArray(response.data) ? response.data : []
      if (providers.length > 0) {
        const p = providers[0]
        providerId.value = p.id
        Object.assign(formData, p)
      } else {
        providerId.value = null
      }
      
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
    ElMessage.error(t('admin.config.oauth2LoadFailed'))
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

      const response = await updateOAuth2Config(providerId.value, data)
      if ((response.code === 200)) {
        ElMessage.success(t('admin.config.oauth2SaveSuccess'))
        await loadConfig()
      } else {
        ElMessage.error(response.message || t('admin.config.oauth2SaveFailed'))
      }
    } catch (error) {
      ElMessage.error(t('admin.config.oauth2SaveFailed'))
      console.error(error)
    } finally {
      saving.value = false
    }
  })
}

const resetRegistrationCount = async () => {
  try {
    await ElMessageBox.confirm(
      t('admin.config.oauth2ResetCountConfirm'),
      t('common.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )

    const response = await resetOAuth2RegistrationCount(providerId.value)
    if ((response.code === 200)) {
      ElMessage.success(t('admin.config.oauth2ResetCountSuccess'))
      await loadConfig()
    } else {
      ElMessage.error(response.message || t('admin.config.oauth2ResetCountFailed'))
    }
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.config.oauth2ResetCountFailed'))
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

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: var(--text-color-primary);
  }
}

.form-item-tip {
  font-size: 12px;
  color: var(--text-color-secondary);
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
  background: var(--neutral-bg);
  border-radius: 4px;
}

.mapping-label {
  min-width: 120px;
  font-weight: 500;
}

:deep(.el-divider__text) {
  font-weight: 600;
  color: var(--text-color-primary);
}
</style>
