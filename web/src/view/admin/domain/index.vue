<template>
  <div class="domain-mgmt-container">
    <el-card>
      <template #header>
        <span>{{ t('admin.domain.title') }}</span>
      </template>

      <el-table :data="domains" v-loading="loading" stripe>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="userId" :label="t('admin.domain.userId')" width="80" />
        <el-table-column prop="instanceId" :label="t('admin.domain.instanceId')" width="80" />
        <el-table-column prop="domainName" :label="t('admin.domain.domainName')" />
        <el-table-column prop="status" :label="t('admin.domain.status')" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : row.status === 'error' ? 'danger' : 'warning'" size="small">
              {{ row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.domain.actions')" width="100" fixed="right">
          <template #default="{ row }">
            <el-button link type="danger" @click="handleDelete(row)">
              <el-icon><Delete /></el-icon>
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { adminGetDomains, adminDeleteDomain } from '@/api/features'

const { t } = useI18n()

const domains = ref([])
const loading = ref(false)

async function fetchData() {
  loading.value = true
  try {
    const res = await adminGetDomains()
    if (res.code === 0 || res.code === 200) {
      domains.value = res.data || []
    }
  } finally {
    loading.value = false
  }
}

async function handleDelete(row) {
  await ElMessageBox.confirm(t('admin.domain.confirmDelete'))
  await adminDeleteDomain(row.id)
  ElMessage.success(t('admin.domain.deleteSuccess'))
  fetchData()
}

onMounted(() => fetchData())
</script>

<style scoped>
.domain-mgmt-container { padding: 20px; }
</style>
