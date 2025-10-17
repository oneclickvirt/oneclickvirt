<template>
  <div class="oauth2-providers-container">
    <!-- OAuth2 功能未启用提示 -->
    <el-alert
      v-if="!oauth2Enabled"
      title="OAuth2 功能未启用"
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      <template #default>
        <div>
          当前 OAuth2 登录功能未启用。您可以在此管理 OAuth2 提供商配置，但这些配置不会在登录页面生效。
          <br>
          如需启用，请前往
          <el-link type="primary" @click="goToConfig" :underline="false">
            <strong>系统配置</strong>
          </el-link>
          页面开启 OAuth2 功能。
        </div>
      </template>
    </el-alert>

    <el-card shadow="never" class="providers-card">
      <template #header>
        <div class="card-header">
          <span class="card-title">OAuth2管理</span>
          <el-button
            type="primary"
            size="default"
            @click="handleAdd"
          >
            <el-icon><Plus /></el-icon>
            添加提供商
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
          label="显示名称"
          min-width="140"
        />
        <el-table-column
          prop="name"
          label="标识名称"
          min-width="140"
        />
        <el-table-column
          label="状态"
          width="100"
          align="center"
        >
          <template #default="{ row }">
            <el-tag
              :type="row.enabled ? 'success' : 'info'"
              size="default"
            >
              {{ row.enabled ? '已启用' : '已禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          label="注册统计"
          width="140"
          align="center"
        >
          <template #default="{ row }">
            <span v-if="row.maxRegistrations > 0">
              {{ row.currentRegistrations }} / {{ row.maxRegistrations }}
            </span>
            <span v-else>
              {{ row.totalUsers }} (无限制)
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
          label="回调地址"
          min-width="200"
          show-overflow-tooltip
        />
        <el-table-column
          label="操作"
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
                编辑
              </el-button>
              <el-button
                size="small"
                type="warning"
                @click="handleResetCount(row)"
              >
                重置计数
              </el-button>
              <el-button
                size="small"
                type="danger"
                @click="handleDelete(row)"
              >
                删除
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
          <div v-if="!isEdit" class="preset-buttons">
            <el-button
              size="small"
              type="primary"
              @click="applyLinuxDoPreset"
            >
              <el-icon><Connection /></el-icon>
              Linux.do 预设
            </el-button>
            <el-button
              size="small"
              type="success"
              @click="applyIDCFlarePreset"
            >
              <el-icon><Connection /></el-icon>
              IDCFlare 预设
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
        <el-tabs v-model="activeTab" class="oauth2-tabs">
          <el-tab-pane
            label="基础配置"
            name="basic"
          >
            <div class="form-section">
              <el-row :gutter="20">
                <el-col :span="12">
                  <el-form-item
                    label="显示名称"
                    prop="displayName"
                  >
                    <el-input
                      v-model="formData.displayName"
                      placeholder="例如: Linux.do"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item
                    label="标识名称"
                    prop="name"
                  >
                    <el-input
                      v-model="formData.name"
                      placeholder="例如: linuxdo (唯一标识，不可重复)"
                      :disabled="isEdit"
                    />
                  </el-form-item>
                </el-col>
              </el-row>

              <el-row :gutter="20">
                <el-col :span="12">
                  <el-form-item label="启用状态">
                    <el-switch
                      v-model="formData.enabled"
                      active-text="启用"
                      inactive-text="禁用"
                    />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item
                    label="显示顺序"
                    prop="sort"
                  >
                    <el-input-number
                      v-model="formData.sort"
                      :min="0"
                      :max="999"
                      :controls-position="right"
                      style="width: 100%"
                    />
                    <span class="form-tip">数字越小越靠前</span>
                  </el-form-item>
                </el-col>
              </el-row>

              <el-divider content-position="left">OAuth2 凭证</el-divider>

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
                  :placeholder="isEdit ? '留空表示不修改原有Secret' : 'OAuth2 Client Secret'"
                  show-password
                />
              </el-form-item>
            </div>
          </el-tab-pane>

          <el-tab-pane
            label="OAuth2端点"
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
              <p>字段映射说明：</p>
              <p>• 必需字段：userIdField（用户ID）、usernameField（用户名）</p>
              <p>• 可选字段：emailField、avatarField、nicknameField、trustLevelField</p>
              <p>• 支持嵌套字段，使用点号分隔，例如：user.profile.name</p>
              <p>• 如果提供商不返回某些字段，系统会自动使用默认值</p>
            </el-alert>

            <el-form-item
              label="用户ID字段"
              prop="userIdField"
            >
              <el-input
                v-model="formData.userIdField"
                placeholder="默认: id"
              />
            </el-form-item>

            <el-form-item
              label="用户名字段"
              prop="usernameField"
            >
              <el-input
                v-model="formData.usernameField"
                placeholder="默认: username"
              />
            </el-form-item>

            <el-form-item label="邮箱字段">
              <el-input
                v-model="formData.emailField"
                placeholder="默认: email"
              />
            </el-form-item>

            <el-form-item label="头像字段">
              <el-input
                v-model="formData.avatarField"
                placeholder="默认: avatar"
              />
            </el-form-item>

            <el-form-item label="昵称字段">
              <el-input
                v-model="formData.nicknameField"
                placeholder="可选，例如: name 或 nickname"
              />
            </el-form-item>

            <el-form-item label="信任等级字段">
              <el-input
                v-model="formData.trustLevelField"
                placeholder="可选，例如: trust_level"
              />
              <span class="form-tip">用于等级映射，如LinuxDo的trust_level</span>
            </el-form-item>
          </el-tab-pane>

          <el-tab-pane
            label="等级与限制"
            name="level"
          >
            <el-form-item
              label="默认用户等级"
              prop="defaultLevel"
            >
              <el-input-number
                v-model="formData.defaultLevel"
                :min="1"
                :max="10"
              />
              <span class="form-tip">当无法提取等级或无映射时使用此等级</span>
            </el-form-item>

            <el-form-item label="等级映射配置">
              <div class="level-mapping">
                <div
                  v-for="(level, key) in formData.levelMapping"
                  :key="key"
                  class="mapping-item"
                >
                  <span>外部等级 {{ key }} →</span>
                  <el-input-number
                    v-model="formData.levelMapping[key]"
                    :min="1"
                    :max="10"
                    size="small"
                  />
                  <el-button
                    size="small"
                    type="danger"
                    text
                    @click="removeLevelMapping(key)"
                  >
                    删除
                  </el-button>
                </div>
                <el-button
                  size="small"
                  @click="addLevelMapping"
                >
                  <el-icon><Plus /></el-icon>
                  添加映射
                </el-button>
              </div>
              <span class="form-tip">将外部提供商的等级映射到系统用户等级</span>
            </el-form-item>

            <el-form-item label="注册数量限制">
              <el-input-number
                v-model="formData.maxRegistrations"
                :min="0"
                :max="999999"
              />
              <span class="form-tip">0 表示无限制</span>
            </el-form-item>

            <el-form-item
              v-if="isEdit"
              label="当前注册数"
            >
              <el-input-number
                v-model="formData.currentRegistrations"
                disabled
              />
            </el-form-item>
          </el-tab-pane>
        </el-tabs>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">
          取消
        </el-button>
        <el-button
          type="primary"
          @click="handleSubmit"
          :loading="submitting"
        >
          确定
        </el-button>
      </template>
    </el-dialog>

    <!-- 添加等级映射对话框 -->
    <el-dialog
      v-model="mappingDialogVisible"
      title="添加等级映射"
      width="400px"
    >
      <el-form label-width="120px">
        <el-form-item label="外部等级值">
          <el-input
            v-model="newMapping.externalLevel"
            placeholder="例如: 0, 1, 2..."
          />
        </el-form-item>
        <el-form-item label="系统用户等级">
          <el-input-number
            v-model="newMapping.systemLevel"
            :min="1"
            :max="10"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="mappingDialogVisible = false">
          取消
        </el-button>
        <el-button
          type="primary"
          @click="confirmAddMapping"
        >
          确定
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Connection } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import {
  getAllOAuth2Providers,
  createOAuth2Provider,
  updateOAuth2Provider,
  deleteOAuth2Provider,
  resetOAuth2RegistrationCount
} from '@/api/oauth2'
import { getAdminConfig } from '@/api/config'

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
    { required: true, message: '请输入标识名称', trigger: 'blur' }
  ],
  displayName: [
    { required: true, message: '请输入显示名称', trigger: 'blur' }
  ],
  clientId: [
    { required: true, message: '请输入Client ID', trigger: 'blur' }
  ],
  clientSecret: [
    { required: !isEdit.value, message: '请输入Client Secret', trigger: 'blur' }
  ],
  redirectUrl: [
    { required: true, message: '请输入回调地址', trigger: 'blur' }
  ],
  authUrl: [
    { required: true, message: '请输入授权地址', trigger: 'blur' }
  ],
  tokenUrl: [
    { required: true, message: '请输入令牌地址', trigger: 'blur' }
  ],
  userInfoUrl: [
    { required: true, message: '请输入用户信息地址', trigger: 'blur' }
  ],
  userIdField: [
    { required: true, message: '请输入用户ID字段', trigger: 'blur' }
  ],
  usernameField: [
    { required: true, message: '请输入用户名字段', trigger: 'blur' }
  ],
  defaultLevel: [
    { required: true, message: '请设置默认等级', trigger: 'blur' }
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
    ElMessage.error('加载提供商列表失败')
  } finally {
    loading.value = false
  }
}

const resetForm = () => {
  Object.assign(formData, {
    name: '',
    displayName: '',
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

// Linux.do 预设
const applyLinuxDoPreset = () => {
  Object.assign(formData, {
    name: 'linuxdo',
    displayName: 'Linux.do',
    authUrl: 'https://connect.linux.do/oauth2/authorize',
    tokenUrl: 'https://connect.linux.do/oauth2/token',
    userInfoUrl: 'https://connect.linux.do/api/user',
    userIdField: 'id',
    usernameField: 'username',
    emailField: 'email',
    avatarField: 'avatar_url',
    nicknameField: 'name',
    trustLevelField: 'trust_level',
    defaultLevel: 1
  })
  ElMessage.success('已应用 Linux.do 预设配置，请填写 Client ID 和 Client Secret')
}

// IDCFlare 预设
const applyIDCFlarePreset = () => {
  Object.assign(formData, {
    name: 'idcflare',
    displayName: 'IDCFlare',
    authUrl: 'https://connect.idcflare.com/oauth2/authorize',
    tokenUrl: 'https://connect.idcflare.com/oauth2/token',
    userInfoUrl: 'https://connect.idcflare.com/api/user',
    userIdField: 'id',
    usernameField: 'username',
    emailField: 'email',
    avatarField: 'avatar_url',
    nicknameField: 'name',
    trustLevelField: 'trust_level',
    defaultLevel: 1
  })
  ElMessage.success('已应用 IDCFlare 预设配置，请填写 Client ID 和 Client Secret')
}

const handleAdd = () => {
  resetForm()
  isEdit.value = false
  dialogTitle.value = '添加OAuth2提供商'
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
    console.error('解析levelMapping失败:', e)
  }

  Object.assign(formData, {
    id: row.id,
    name: row.name,
    displayName: row.displayName,
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
  dialogTitle.value = '编辑OAuth2提供商'
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
        ElMessage.success('更新成功')
      } else {
        await createOAuth2Provider(data)
        ElMessage.success('创建成功')
      }

      dialogVisible.value = false
      loadProviders()
    } catch (error) {
      ElMessage.error(error.response?.data?.message || '操作失败')
    } finally {
      submitting.value = false
    }
  })
}

const handleDelete = async (row) => {
  try {
    await ElMessageBox.confirm(
      `确定要删除提供商 "${row.displayName}" 吗？如果有用户正在使用此提供商，将无法删除。`,
      '警告',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    await deleteOAuth2Provider(row.id)
    ElMessage.success('删除成功')
    loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(error.response?.data?.message || '删除失败')
    }
  }
}

const handleResetCount = async (row) => {
  try {
    await ElMessageBox.confirm(
      `确定要重置提供商 "${row.displayName}" 的注册计数吗？`,
      '确认',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )

    await resetOAuth2RegistrationCount(row.id)
    ElMessage.success('重置成功')
    loadProviders()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(error.response?.data?.message || '重置失败')
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
    ElMessage.warning('请输入外部等级值')
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
.oauth2-providers-container {
  padding: 24px;
  
  .providers-card {
    :deep(.el-card__header) {
      padding: 20px 24px;
      border-bottom: 1px solid #ebeef5;
    }
    
    :deep(.el-card__body) {
      padding: 24px;
    }
  }
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  .card-title {
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
