<template>
  <div class="oauth2-providers-container">
    <!-- OAuth2 功能未启用提示 -->
    <el-alert
      v-if="!oauth2Enabled"
      :title="$t('admin.oauth2.notEnabled')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      <template #default>
        <div>
          {{ $t('admin.oauth2.notEnabledHint') }}
          <br>
          {{ $t('admin.oauth2.enableHint') }}
          <el-link
            type="primary"
            :underline="false"
            @click="goToConfig"
          >
            <strong>{{ $t('admin.oauth2.systemConfig') }}</strong>
          </el-link>
          {{ $t('admin.oauth2.enableHint2') }}
        </div>
      </template>
    </el-alert>

    <el-card
      shadow="never"
      class="providers-card"
    >
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.oauth2.title') }}</span>
          <el-button
            type="primary"
            size="default"
            @click="handleAdd"
          >
            <el-icon><Plus /></el-icon>
            {{ $t('admin.oauth2.addProvider') }}
          </el-button>
        </div>
      </template>

      <el-table
        v-loading="loading"
        :data="providers"
        class="providers-table"
        :row-style="{ height: '60px' }"
        :cell-style="{ padding: '12px 0' }"
        :header-cell-style="{ background: '#f5f7fa', padding: '14px 0', fontWeight: '600' }"
      >
        <el-table-column
          prop="id"
          label="ID"
          width="80"
          align="center"
        />
        <el-table-column
          prop="displayName"
          :label="$t('admin.oauth2.displayName')"
          min-width="140"
        />
        <el-table-column
          prop="name"
          :label="$t('admin.oauth2.identifierName')"
          min-width="140"
        />
        <el-table-column
          :label="$t('common.status')"
          width="100"
          align="center"
        >
          <template #default="{ row }">
            <el-tag
              :type="row.enabled ? 'success' : 'info'"
              size="default"
            >
              {{ row.enabled ? $t('common.enabled') : $t('common.disabled') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('admin.oauth2.registrationStats')"
          width="140"
          align="center"
        >
          <template #default="{ row }">
            <span v-if="row.maxRegistrations > 0">
              {{ row.currentRegistrations }} / {{ row.maxRegistrations }}
            </span>
            <span v-else>
              {{ row.totalUsers }} ({{ $t('admin.oauth2.unlimited') }})
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="clientId"
          label="Client ID"
          min-width="220"
          show-overflow-tooltip
        />
        <el-table-column
          prop="redirectUrl"
          :label="$t('admin.oauth2.callbackUrl')"
          min-width="200"
          show-overflow-tooltip
        />
        <el-table-column
          :label="$t('common.actions')"
          width="300"
          fixed="right"
          align="center"
        >
          <template #default="{ row }">
            <div class="action-buttons">
              <el-button
                size="small"
                @click="handleEdit(row)"
              >
                {{ $t('common.edit') }}
              </el-button>
              <el-button
                size="small"
                type="warning"
                @click="handleResetCount(row)"
              >
                {{ $t('admin.oauth2.resetCount') }}
              </el-button>
              <el-button
                size="small"
                type="danger"
                @click="handleDelete(row)"
              >
                {{ $t('common.delete') }}
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 添加/编辑对话框 -->
    <el-dialog
      v-model="dialogVisible"
      :title="dialogTitle"
      width="900px"
      :close-on-click-modal="false"
    >
      <template #header>
        <div class="dialog-header">
          <span>{{ dialogTitle }}</span>
          <div
            v-if="!isEdit"
            class="preset-buttons"
          >
            <el-button
              size="small"
              type="primary"
              @click="applyPreset('linuxdo')"
            >
              <el-icon><Connection /></el-icon>
              Linux.do
            </el-button>
            <el-button
              size="small"
              type="success"
              @click="applyPreset('idcflare')"
            >
              <el-icon><Connection /></el-icon>
              IDCFlare
            </el-button>
            <el-button
              size="small"
              @click="applyPreset('github')"
            >
              <el-icon><Connection /></el-icon>
              GitHub
            </el-button>
            <el-button
              size="small"
              type="info"
              @click="applyPreset('generic')"
            >
              <el-icon><Setting /></el-icon>
              {{ $t('admin.oauth2.genericOAuth2') }}
            </el-button>
          </div>
        </div>
      </template>
      <el-form
        ref="formRef"
        :model="formData"
        :rules="formRules"
        label-width="120px"
        class="oauth2-form"
      >
        <el-tabs
          v-model="activeTab"
          class="oauth2-tabs"
        >
          <el-tab-pane
            :label="$t('admin.oauth2.basicConfig')"
            name="basic"
          >
            <div class="form-section">
              <el-row :gutter="20">
                <el-col :span="12">
                  <el-form-item
                    :label="$t('admin.oauth2.displayName')"
                    prop="displayName"
                  >
                    <el-input
                      v-model="formData.displayName"
                      :placeholder="$t('admin.oauth2.displayNamePlaceholder')"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item
                    :label="$t('admin.oauth2.identifierName')"
                    prop="name"
                  >
                    <el-input
                      v-model="formData.name"
                      :placeholder="$t('admin.oauth2.identifierNamePlaceholder')"
                      :disabled="isEdit"
                    />
                  </el-form-item>
                </el-col>
              </el-row>

              <el-row :gutter="20">
                <el-col :span="12">
                  <el-form-item :label="$t('admin.oauth2.enableStatus')">
                    <el-switch
                      v-model="formData.enabled"
                      :active-text="$t('common.enable')"
                      :inactive-text="$t('common.disable')"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item
                    :label="$t('admin.oauth2.displayOrder')"
                    prop="sort"
                  >
                    <el-input-number
                      v-model="formData.sort"
                      :min="0"
                      :max="999"
                      :controls="false"
                      style="width: 100%"
                    />
                    <span class="form-tip">{{ $t('admin.oauth2.displayOrderHint') }}</span>
                  </el-form-item>
                </el-col>
              </el-row>

              <el-divider content-position="left">
                {{ $t('admin.oauth2.oauth2Credentials') }}
              </el-divider>

              <el-form-item
                label="Client ID"
                prop="clientId"
              >
                <el-input
                  v-model="formData.clientId"
                  placeholder="OAuth2 Client ID"
                />
              </el-form-item>

              <el-form-item
                label="Client Secret"
                prop="clientSecret"
              >
                <el-input
                  v-model="formData.clientSecret"
                  type="password"
                  :placeholder="isEdit ? $t('admin.oauth2.secretPlaceholderEdit') : 'OAuth2 Client Secret'"
                  show-password
                />
              </el-form-item>
            </div>
          </el-tab-pane>

          <el-tab-pane
            :label="$t('admin.oauth2.oauth2Endpoints')"
            name="endpoints"
          >
            <el-form-item
              label="回调地址"
              prop="redirectUrl"
            >
              <el-input
                v-model="formData.redirectUrl"
                placeholder="http://localhost:8888/api/v1/auth/oauth2/callback"
              />
            </el-form-item>

            <el-form-item
              label="授权地址"
              prop="authUrl"
            >
              <el-input
                v-model="formData.authUrl"
                placeholder="https://provider.com/oauth2/authorize"
              />
            </el-form-item>

            <el-form-item
              label="令牌地址"
              prop="tokenUrl"
            >
              <el-input
                v-model="formData.tokenUrl"
                placeholder="https://provider.com/oauth2/token"
              />
            </el-form-item>

            <el-form-item
              label="用户信息地址"
              prop="userInfoUrl"
            >
              <el-input
                v-model="formData.userInfoUrl"
                placeholder="https://provider.com/api/user"
              />
            </el-form-item>
          </el-tab-pane>

          <el-tab-pane
            label="字段映射"
            name="fields"
          >
            <el-alert
              type="info"
              :closable="false"
              style="margin-bottom: 20px"
            >
              <p>{{ $t('admin.oauth2.fieldMappingDesc') }}</p>
              <p>• {{ $t('admin.oauth2.requiredFields') }}</p>
              <p>• {{ $t('admin.oauth2.optionalFields') }}</p>
              <p>• {{ $t('admin.oauth2.nestedFieldsSupport') }}</p>
              <p>• {{ $t('admin.oauth2.defaultValuesInfo') }}</p>
            </el-alert>

            <el-form-item
              :label="$t('admin.oauth2.userIdField')"
              prop="userIdField"
            >
              <el-input
                v-model="formData.userIdField"
                :placeholder="$t('admin.oauth2.userIdFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item
              :label="$t('admin.oauth2.usernameField')"
              prop="usernameField"
            >
              <el-input
                v-model="formData.usernameField"
                :placeholder="$t('admin.oauth2.usernameFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.emailField')">
              <el-input
                v-model="formData.emailField"
                :placeholder="$t('admin.oauth2.emailFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.avatarField')">
              <el-input
                v-model="formData.avatarField"
                :placeholder="$t('admin.oauth2.avatarFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.nicknameField')">
              <el-input
                v-model="formData.nicknameField"
                :placeholder="$t('admin.oauth2.nicknameFieldPlaceholder')"
              />
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.trustLevelField')">
              <el-input
                v-model="formData.trustLevelField"
                :placeholder="$t('admin.oauth2.trustLevelFieldPlaceholder')"
              />
              <span class="form-tip">{{ $t('admin.oauth2.trustLevelFieldHint') }}</span>
            </el-form-item>
          </el-tab-pane>

          <el-tab-pane
            :label="$t('admin.oauth2.levelAndLimits')"
            name="level"
          >
            <el-form-item
              :label="$t('admin.oauth2.defaultUserLevel')"
              prop="defaultLevel"
            >
              <el-input-number
                v-model="formData.defaultLevel"
                :min="1"
                :max="10"
                :controls="false"
              />
              <span class="form-tip">{{ $t('admin.oauth2.defaultUserLevelHint') }}</span>
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.levelMappingConfig')">
              <div class="level-mapping">
                <div
                  v-for="(level, key) in formData.levelMapping"
                  :key="key"
                  class="mapping-item"
                >
                  <span>{{ $t('admin.oauth2.externalLevel') }} {{ key }} →</span>
                  <el-input-number
                    v-model="formData.levelMapping[key]"
                    :min="1"
                    :max="10"
                    :controls="false"
                    size="small"
                  />
                  <el-button
                    size="small"
                    type="danger"
                    text
                    @click="removeLevelMapping(key)"
                  >
                    {{ $t('common.delete') }}
                  </el-button>
                </div>
                <el-button
                  size="small"
                  @click="addLevelMapping"
                >
                  <el-icon><Plus /></el-icon>
                  {{ $t('admin.oauth2.addMapping') }}
                </el-button>
              </div>
              <span class="form-tip">{{ $t('admin.oauth2.levelMappingHint') }}</span>
            </el-form-item>

            <el-form-item :label="$t('admin.oauth2.registrationLimit')">
              <el-input-number
                v-model="formData.maxRegistrations"
                :min="0"
                :max="999999"
                :controls="false"
              />
              <span class="form-tip">{{ $t('admin.oauth2.registrationLimitHint') }}</span>
            </el-form-item>

            <el-form-item
              v-if="isEdit"
              :label="$t('admin.oauth2.currentRegistrations')"
            >
              <el-input-number
                v-model="formData.currentRegistrations"
                :controls="false"
                disabled
              />
            </el-form-item>
          </el-tab-pane>
        </el-tabs>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">
          {{ $t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="handleSubmit"
        >
          {{ $t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 添加等级映射对话框 -->
    <el-dialog
      v-model="mappingDialogVisible"
      :title="$t('admin.oauth2.addLevelMapping')"
      width="400px"
    >
      <el-form label-width="120px">
        <el-form-item :label="$t('admin.oauth2.externalLevelValue')">
          <el-input
            v-model="newMapping.externalLevel"
            :placeholder="$t('admin.oauth2.externalLevelPlaceholder')"
          />
        </el-form-item>
        <el-form-item :label="$t('admin.oauth2.systemUserLevel')">
          <el-input-number
            v-model="newMapping.systemLevel"
            :min="1"
            :max="10"
            :controls="false"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="mappingDialogVisible = false">
          {{ $t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          @click="confirmAddMapping"
        >
          {{ $t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Connection, Setting } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  getAllOAuth2Providers,
  createOAuth2Provider,
  updateOAuth2Provider,
  deleteOAuth2Provider,
  resetOAuth2RegistrationCount,
  getOAuth2Presets
} from '@/api/oauth2'
import { getAdminConfig } from '@/api/config'

const { t } = useI18n()
const router = useRouter()
const loading = ref(false)
const providers = ref([])
const dialogVisible = ref(false)
const dialogTitle = ref('')
const isEdit = ref(false)
const submitting = ref(false)
const activeTab = ref('basic')
const formRef = ref(null)
const oauth2Enabled = ref(true) // 默认为true，加载后更新

const mappingDialogVisible = ref(false)
const newMapping = reactive({
  externalLevel: '',
  systemLevel: 1
})

const formData = reactive({
  name: '',
  displayName: '',
  providerType: 'preset', // preset 或 generic
  enabled: true,
  clientId: '',
  clientSecret: '',
  redirectUrl: 'http://localhost:8888/api/v1/auth/oauth2/callback',
  authUrl: '',
  tokenUrl: '',
  userInfoUrl: '',
  userIdField: 'id',
  usernameField: 'username',
  emailField: 'email',
  avatarField: 'avatar',
  nicknameField: '',
  trustLevelField: '',
  maxRegistrations: 0,
  currentRegistrations: 0,
  levelMapping: {},
  defaultLevel: 1,
  sort: 0
})

const formRules = computed(() => ({
  name: [
    { required: true, message: t('admin.oauth2.validationName'), trigger: 'blur' }
  ],
  displayName: [
    { required: true, message: t('admin.oauth2.validationDisplayName'), trigger: 'blur' }
  ],
  clientId: [
    { required: true, message: t('admin.oauth2.validationClientId'), trigger: 'blur' }
  ],
  clientSecret: [
    { required: !isEdit.value, message: t('admin.oauth2.validationClientSecret'), trigger: 'blur' }
  ],
  redirectUrl: [
    { required: true, message: t('admin.oauth2.validationRedirectUrl'), trigger: 'blur' }
  ],
  authUrl: [
    { required: true, message: t('admin.oauth2.validationAuthUrl'), trigger: 'blur' }
  ],
  tokenUrl: [
    { required: true, message: t('admin.oauth2.validationTokenUrl'), trigger: 'blur' }
  ],
  userInfoUrl: [
    { required: true, message: t('admin.oauth2.validationUserInfoUrl'), trigger: 'blur' }
  ],
  userIdField: [
    { required: true, message: t('admin.oauth2.validationUserIdField'), trigger: 'blur' }
  ],
  usernameField: [
    { required: true, message: t('admin.oauth2.validationUsernameField'), trigger: 'blur' }
  ],
  defaultLevel: [
    { required: true, message: t('admin.oauth2.validationDefaultLevel'), trigger: 'blur' }
  ]
}))

onMounted(() => {
  loadProviders()
  loadSystemConfig()
})

const loadSystemConfig = async () => {
  try {
    const res = await getAdminConfig()
    if (res.data && res.data.auth) {
      oauth2Enabled.value = res.data.auth.enableOAuth2 || false
    }
  } catch (error) {
    console.error('加载系统配置失败:', error)
    // 加载失败时默认显示警告
    oauth2Enabled.value = false
  }
}

const goToConfig = () => {
  router.push('/admin/config')
}

const loadProviders = async () => {
  loading.value = true
  try {
    const res = await getAllOAuth2Providers()
    providers.value = res.data || []
  } catch (error) {
    ElMessage.error(t('admin.oauth2.loadProvidersFailed'))
  } finally {
    loading.value = false
  }
}

const resetForm = () => {
  Object.assign(formData, {
    name: '',
    displayName: '',
    providerType: 'preset',
    enabled: true,
    clientId: '',
    clientSecret: '',
    redirectUrl: 'http://localhost:8888/api/v1/auth/oauth2/callback',
    authUrl: '',
    tokenUrl: '',
    userInfoUrl: '',
    userIdField: 'id',
    usernameField: 'username',
    emailField: 'email',
    avatarField: 'avatar',
    nicknameField: '',
    trustLevelField: '',
    maxRegistrations: 0,
    currentRegistrations: 0,
    levelMapping: {},
    defaultLevel: 1,
    sort: 0
  })
  activeTab.value = 'basic'
}

// 应用预设配置
const applyPreset = async (presetName) => {
  try {
    const res = await getOAuth2Presets()
    const preset = res.data[presetName]
    
    if (!preset) {
      ElMessage.error(t('admin.oauth2.presetNotFound'))
      return
    }

    Object.assign(formData, {
      name: preset.name,
      displayName: preset.displayName,
      providerType: preset.providerType,
      authUrl: preset.authURL,
      tokenUrl: preset.tokenURL,
      userInfoUrl: preset.userInfoURL,
      userIdField: preset.userIDField,
      usernameField: preset.usernameField,
      emailField: preset.emailField,
      avatarField: preset.avatarField,
      nicknameField: preset.nicknameField || '',
      trustLevelField: preset.trustLevelField || '',
      levelMapping: preset.levelMapping || {},
      defaultLevel: preset.defaultLevel
    })
    
    ElMessage.success(t('admin.oauth2.presetApplied', { name: preset.displayName }))
  } catch (error) {
    console.error('Failed to load preset:', error)
    ElMessage.error(t('admin.oauth2.presetLoadFailed'))
  }
}

const handleAdd = () => {
  resetForm()
  isEdit.value = false
  dialogTitle.value = t('admin.oauth2.addProvider')
  dialogVisible.value = true
}

const handleEdit = (row) => {
  resetForm()
  
  // 解析levelMapping
  let levelMapping = {}
  try {
    if (row.levelMapping) {
      levelMapping = JSON.parse(row.levelMapping)
    }
  } catch (e) {
    console.error(t('admin.oauth2.parseLevelMappingFailed'), e)
  }

  Object.assign(formData, {
    id: row.id,
    name: row.name,
    displayName: row.displayName,
    providerType: row.providerType || 'preset',
    enabled: row.enabled,
    clientId: row.clientId,
    clientSecret: '', // 不回显密钥
    redirectUrl: row.redirectUrl,
    authUrl: row.authUrl,
    tokenUrl: row.tokenUrl,
    userInfoUrl: row.userInfoUrl,
    userIdField: row.userIdField || 'id',
    usernameField: row.usernameField || 'username',
    emailField: row.emailField || 'email',
    avatarField: row.avatarField || 'avatar',
    nicknameField: row.nicknameField || '',
    trustLevelField: row.trustLevelField || '',
    maxRegistrations: row.maxRegistrations || 0,
    currentRegistrations: row.currentRegistrations || 0,
    levelMapping: levelMapping,
    defaultLevel: row.defaultLevel || 1,
    sort: row.sort || 0
  })

  isEdit.value = true
  dialogTitle.value = t('admin.oauth2.editProvider')
  dialogVisible.value = true
}

const handleSubmit = async () => {
  if (!formRef.value) return

  await formRef.value.validate(async (valid) => {
    if (!valid) return

    submitting.value = true
    try {
      const data = {
        name: formData.name,
        displayName: formData.displayName,
        providerType: formData.providerType,
        enabled: formData.enabled,
        clientId: formData.clientId,
        redirectUrl: formData.redirectUrl,
        authUrl: formData.authUrl,
        tokenUrl: formData.tokenUrl,
        userInfoUrl: formData.userInfoUrl,
        userIdField: formData.userIdField,
        usernameField: formData.usernameField,
        emailField: formData.emailField,
        avatarField: formData.avatarField,
        nicknameField: formData.nicknameField,
        trustLevelField: formData.trustLevelField,
        maxRegistrations: formData.maxRegistrations,
        levelMapping: formData.levelMapping,
        defaultLevel: formData.defaultLevel,
        sort: formData.sort
      }

      // 只在创建或修改了密钥时才发送
      if (!isEdit.value || formData.clientSecret) {
        data.clientSecret = formData.clientSecret
      }

      if (isEdit.value) {
        await updateOAuth2Provider(formData.id, data)
        ElMessage.success(t('common.updateSuccess'))
      } else {
        await createOAuth2Provider(data)
        ElMessage.success(t('common.createSuccess'))
      }

      dialogVisible.value = false
      loadProviders()
    } catch (error) {
      ElMessage.error(error.response?.data?.message || t('common.operationFailed'))
    } finally {
      submitting.value = false
    }
  })
}

const handleDelete = async (row) => {
  try {
    await ElMessageBox.confirm(
      t('admin.oauth2.deleteConfirm', { name: row.displayName }),
      t('common.warning'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )

    await deleteOAuth2Provider(row.id)
    ElMessage.success(t('common.deleteSuccess'))
    loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(error.response?.data?.message || t('common.deleteFailed'))
    }
  }
}

const handleResetCount = async (row) => {
  try {
    await ElMessageBox.confirm(
      t('admin.oauth2.resetCountConfirm', { name: row.displayName }),
      t('common.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )

    await resetOAuth2RegistrationCount(row.id)
    ElMessage.success(t('admin.oauth2.resetSuccess'))
    loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(error.response?.data?.message || t('admin.oauth2.resetFailed'))
    }
  }
}

const addLevelMapping = () => {
  newMapping.externalLevel = ''
  newMapping.systemLevel = 1
  mappingDialogVisible.value = true
}

const confirmAddMapping = () => {
  if (!newMapping.externalLevel) {
    ElMessage.warning(t('admin.oauth2.enterExternalLevel'))
    return
  }

  formData.levelMapping[newMapping.externalLevel] = newMapping.systemLevel
  mappingDialogVisible.value = false
}

const removeLevelMapping = (key) => {
  delete formData.levelMapping[key]
}
</script>

<style scoped lang="scss">
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: #303133;
  }
}

.providers-table {
  width: 100%;
  
  .action-buttons {
    display: flex;
    gap: 10px;
    justify-content: center;
    flex-wrap: wrap;
    padding: 4px 0;
    
    .el-button {
      margin: 0 !important;
    }
  }
}

.dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  
  .preset-buttons {
    display: flex;
    gap: 10px;
  }
}

.oauth2-form {
  .oauth2-tabs {
    :deep(.el-tabs__content) {
      padding-top: 20px;
    }
  }

  .form-section {
    padding: 10px 0;
  }

  :deep(.el-form-item) {
    margin-bottom: 24px;
  }

  :deep(.el-divider) {
    margin: 30px 0 24px 0;
  }

  :deep(.el-input-number) {
    width: 100%;
  }
  
  :deep(.el-col) {
    .el-form-item {
      margin-right: 0;
    }
  }
}

.form-tip {
  display: block;
  margin-top: 4px;
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}

.level-mapping {
  .mapping-item {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 10px;

    span {
      min-width: 120px;
    }
  }
}
</style>
