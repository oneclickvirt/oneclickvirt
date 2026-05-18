<template>
  <div class="api-tokens-page">
    <el-card shadow="never">
      <template #header>
        <div class="card-header">
          <span>{{ t('user.apiTokens.title') }}</span>
          <el-button type="primary" :icon="Plus" @click="showCreateDialog = true">
            {{ t('user.apiTokens.createToken') }}
          </el-button>
        </div>
      </template>

      <el-alert
        :title="t('user.apiTokens.usageNote')"
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom: 16px"
      />
      <el-alert
        :title="t('user.apiTokens.maxTokensWarning')"
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 16px"
      />

      <div v-if="loading" class="loading-container">
        <el-skeleton :rows="3" animated />
      </div>

      <el-empty
        v-else-if="tokens.length === 0"
        :description="t('user.apiTokens.noTokens')"
      >
        <el-button type="primary" @click="showCreateDialog = true">
          {{ t('user.apiTokens.createNewToken') }}
        </el-button>
      </el-empty>

      <div v-else>
        <div class="tokens-summary">
          {{ t('user.apiTokens.totalTokens', { count: tokens.length }) }}
        </div>
        <el-table :data="tokens" style="width: 100%" stripe>
          <el-table-column :label="t('user.apiTokens.tokenName')" prop="name" min-width="120" />
          <el-table-column :label="t('user.apiTokens.tokenPrefix')" prop="tokenPrefix" width="150">
            <template #default="{ row }">
              <el-tag type="info" size="small">{{ row.tokenPrefix }}...</el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('user.apiTokens.useCount')" prop="useCount" width="90" />
          <el-table-column :label="t('user.apiTokens.createdAt')" width="160">
            <template #default="{ row }">
              {{ formatDate(row.createdAt) }}
            </template>
          </el-table-column>
          <el-table-column :label="t('user.apiTokens.expiresAt')" width="160">
            <template #default="{ row }">
              <span v-if="!row.expiresAt">{{ t('user.apiTokens.never') }}</span>
              <span v-else>{{ formatDate(row.expiresAt) }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('user.apiTokens.lastUsedAt')" width="160">
            <template #default="{ row }">
              <span v-if="!row.lastUsedAt">{{ t('user.apiTokens.unused') }}</span>
              <span v-else>{{ formatDate(row.lastUsedAt) }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.actions')" width="100" fixed="right">
            <template #default="{ row }">
              <el-popconfirm
                :title="t('user.apiTokens.deleteConfirm')"
                @confirm="handleDelete(row.id)"
              >
                <template #reference>
                  <el-button type="danger" size="small" :icon="Delete" />
                </template>
              </el-popconfirm>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </el-card>

    <!-- 创建 Token 对话框 -->
    <el-dialog
      v-model="showCreateDialog"
      :title="t('user.apiTokens.createToken')"
      width="480px"
      @close="resetCreateForm"
    >
      <el-form ref="createFormRef" :model="createForm" :rules="createRules" label-width="100px">
        <el-form-item :label="t('user.apiTokens.tokenName')" prop="name">
          <el-input
            v-model="createForm.name"
            :placeholder="t('user.apiTokens.tokenNamePlaceholder')"
            maxlength="64"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('user.apiTokens.expireDays')" prop="expireDays">
          <el-input-number
            v-model="createForm.expireDays"
            :min="0"
            :max="3650"
            style="width: 100%"
          />
          <div class="form-hint">{{ t('user.apiTokens.expireDaysHint') }}</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showCreateDialog = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="creating" @click="handleCreate">
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 新建 Token 展示对话框 -->
    <el-dialog
      v-model="showTokenDialog"
      :title="t('user.apiTokens.createSuccess')"
      width="560px"
      :close-on-click-modal="false"
      :close-on-press-escape="false"
    >
      <el-alert
        :title="t('user.apiTokens.copyTokenWarning')"
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 16px"
      />
      <div class="token-display">
        <el-input
          :value="newToken"
          readonly
          type="textarea"
          :rows="3"
          resize="none"
        />
        <el-button
          type="primary"
          :icon="CopyDocument"
          style="margin-top: 8px; width: 100%"
          @click="copyToken"
        >
          {{ t('common.copy') }}
        </el-button>
      </div>
      <template #footer>
        <el-button type="primary" @click="showTokenDialog = false; loadTokens()">
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Plus, Delete, CopyDocument } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { createApiToken, getApiTokenList, deleteApiToken } from '@/api/auth'

const { t } = useI18n()

const tokens = ref([])
const loading = ref(false)
const creating = ref(false)
const showCreateDialog = ref(false)
const showTokenDialog = ref(false)
const newToken = ref('')
const createFormRef = ref(null)

const createForm = ref({
  name: '',
  expireDays: 0
})

const createRules = {
  name: [
    { required: true, message: () => t('user.apiTokens.tokenNameRequired'), trigger: 'blur' }
  ]
}

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

const loadTokens = async () => {
  loading.value = true
  try {
    const res = await getApiTokenList()
    tokens.value = res.data?.items || []
  } catch {
    ElMessage.error(t('common.operationFailed'))
  } finally {
    loading.value = false
  }
}

const resetCreateForm = () => {
  createForm.value = { name: '', expireDays: 0 }
  createFormRef.value?.clearValidate()
}

const handleCreate = async () => {
  if (!createFormRef.value) return
  const valid = await createFormRef.value.validate().catch(() => false)
  if (!valid) return

  creating.value = true
  try {
    const res = await createApiToken({
      name: createForm.value.name,
      expireDays: createForm.value.expireDays || 0
    })
    const token = res.data?.token || res.data?.Token || ''
    newToken.value = token
    showCreateDialog.value = false
    showTokenDialog.value = true
    resetCreateForm()
  } catch {
    ElMessage.error(t('user.apiTokens.createFailed'))
  } finally {
    creating.value = false
  }
}

const handleDelete = async (id) => {
  try {
    await deleteApiToken(id)
    ElMessage.success(t('user.apiTokens.deleteSuccess'))
    await loadTokens()
  } catch {
    ElMessage.error(t('user.apiTokens.deleteFailed'))
  }
}

const copyToken = async () => {
  try {
    await navigator.clipboard.writeText(newToken.value)
    ElMessage.success(t('user.apiTokens.copiedSuccess'))
  } catch {
    // fallback
    const el = document.createElement('textarea')
    el.value = newToken.value
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    document.body.removeChild(el)
    ElMessage.success(t('user.apiTokens.copiedSuccess'))
  }
}

onMounted(() => {
  loadTokens()
})
</script>

<style scoped>
.api-tokens-page {
  padding: 16px;
}
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.loading-container {
  padding: 20px;
}
.tokens-summary {
  margin-bottom: 12px;
  color: #606266;
  font-size: 14px;
}
.token-display {
  padding: 8px 0;
}
.form-hint {
  font-size: 12px;
  color: #909399;
  margin-top: 4px;
}
</style>
