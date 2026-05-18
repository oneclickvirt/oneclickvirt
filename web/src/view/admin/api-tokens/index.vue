<template>
  <div class="api-tokens-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ t('admin.apiTokens.title') }}</span>
        </div>
      </template>

      <!-- 搜索栏 -->
      <div class="toolbar">
        <el-input
          v-model="keyword"
          :placeholder="t('admin.apiTokens.searchPlaceholder')"
          style="width: 280px;"
          clearable
          @keyup.enter="handleSearch"
          @clear="handleSearch"
        >
          <template #prefix>
            <el-icon><Search /></el-icon>
          </template>
        </el-input>
        <el-button type="primary" style="margin-left: 10px;" @click="handleSearch">
          {{ t('common.search') }}
        </el-button>
        <el-button style="margin-left: 10px;" @click="resetFilters">
          {{ t('common.reset') }}
        </el-button>
        <el-popconfirm
          v-if="selectedIds.length > 0"
          :title="t('admin.apiTokens.batchDeleteConfirm', { count: selectedIds.length })"
          @confirm="handleBatchDelete"
        >
          <template #reference>
            <el-button type="danger" style="margin-left: 10px;">
              {{ t('admin.apiTokens.batchDelete') }}（{{ selectedIds.length }}）
            </el-button>
          </template>
        </el-popconfirm>
      </div>

      <!-- 表格 -->
      <el-table
        v-loading="loading"
        :data="tokens"
        style="width: 100%; margin-top: 16px;"
        stripe
        @selection-change="handleSelectionChange"
      >
        <el-table-column type="selection" width="50" />
        <el-table-column :label="t('admin.apiTokens.userId')" prop="userId" width="80" />
        <el-table-column :label="t('admin.apiTokens.username')" prop="username" min-width="120" />
        <el-table-column :label="t('admin.apiTokens.userType')" prop="userType" width="120">
          <template #default="{ row }">
            <el-tag
              :type="row.userType === 'admin' ? 'danger' : row.userType === 'normal_admin' ? 'warning' : 'info'"
              size="small"
            >
              {{ formatUserType(row.userType) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.apiTokens.tokenName')" prop="name" min-width="120" />
        <el-table-column :label="t('admin.apiTokens.tokenPrefix')" prop="tokenPrefix" width="140">
          <template #default="{ row }">
            <el-tag type="info" size="small">{{ row.tokenPrefix }}...</el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.apiTokens.useCount')" prop="useCount" width="90" />
        <el-table-column :label="t('admin.apiTokens.createdAt')" width="160">
          <template #default="{ row }">
            {{ formatDate(row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.apiTokens.expiresAt')" width="160">
          <template #default="{ row }">
            <span v-if="!row.expiresAt">{{ t('admin.apiTokens.never') }}</span>
            <span v-else>{{ formatDate(row.expiresAt) }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.apiTokens.lastUsedAt')" width="160">
          <template #default="{ row }">
            <span v-if="!row.lastUsedAt">{{ t('admin.apiTokens.unused') }}</span>
            <span v-else>{{ formatDate(row.lastUsedAt) }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.actions')" width="100" fixed="right">
          <template #default="{ row }">
            <el-popconfirm
              :title="t('admin.apiTokens.deleteConfirm')"
              @confirm="handleDelete(row.id)"
            >
              <template #reference>
                <el-button type="danger" size="small" :icon="Delete" />
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="pagination-container">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handleSizeChange"
          @current-change="handlePageChange"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Delete, Search } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { adminGetApiTokenList, adminDeleteApiToken, adminBatchDeleteApiTokens } from '@/api/admin'

const { t } = useI18n()

const tokens = ref([])
const loading = ref(false)
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(10)
const keyword = ref('')
const selectedIds = ref([])

const formatDate = (dateStr) => {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

const formatUserType = (userType) => {
  if (userType === 'admin') return t('admin.apiTokens.userTypeAdmin')
  if (userType === 'normal_admin') return t('admin.apiTokens.userTypeNormalAdmin')
  return t('admin.apiTokens.userTypeUser')
}

const loadTokens = async () => {
  loading.value = true
  try {
    const res = await adminGetApiTokenList({
      page: currentPage.value,
      pageSize: pageSize.value,
      keyword: keyword.value
    })
    tokens.value = res.data?.items || []
    total.value = res.data?.total || 0
  } catch {
    ElMessage.error(t('common.operationFailed'))
  } finally {
    loading.value = false
  }
}

const handleSearch = () => {
  currentPage.value = 1
  loadTokens()
}

const resetFilters = () => {
  keyword.value = ''
  currentPage.value = 1
  loadTokens()
}

const handleSelectionChange = (selection) => {
  selectedIds.value = selection.map((row) => row.id)
}

const handleDelete = async (id) => {
  try {
    await adminDeleteApiToken(id)
    ElMessage.success(t('admin.apiTokens.deleteSuccess'))
    await loadTokens()
  } catch {
    ElMessage.error(t('admin.apiTokens.deleteFailed'))
  }
}

const handleBatchDelete = async () => {
  if (selectedIds.value.length === 0) return
  try {
    await adminBatchDeleteApiTokens(selectedIds.value)
    ElMessage.success(t('admin.apiTokens.batchDeleteSuccess'))
    selectedIds.value = []
    await loadTokens()
  } catch {
    ElMessage.error(t('admin.apiTokens.batchDeleteFailed'))
  }
}

const handlePageChange = (page) => {
  currentPage.value = page
  loadTokens()
}

const handleSizeChange = (size) => {
  pageSize.value = size
  currentPage.value = 1
  loadTokens()
}

onMounted(() => {
  loadTokens()
})
</script>

<style scoped>
.api-tokens-container {
  padding: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.toolbar {
  display: flex;
  align-items: center;
}

.pagination-container {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
