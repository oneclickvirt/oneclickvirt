<template>
  <div class="domain-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ t('user.domain.title') }}</span>
          <el-button type="primary" @click="handleCreate">
            <el-icon><Plus /></el-icon>
            {{ t('user.domain.addDomain') }}
          </el-button>
        </div>
      </template>

      <el-table :data="domains" v-loading="loading" stripe>
        <el-table-column prop="domainName" :label="t('user.domain.domainName')" />
        <el-table-column prop="instanceId" :label="t('user.domain.instanceId')" width="100" />
        <el-table-column prop="protocol" :label="t('user.domain.protocol')" width="100" />
        <el-table-column prop="internalPort" :label="t('user.domain.internalPort')" width="100" />
        <el-table-column prop="enableSsl" :label="t('user.domain.enableSsl')" width="80">
          <template #default="{ row }">
            <el-tag :type="row.enableSsl ? 'success' : 'info'" size="small">
              {{ row.enableSsl ? 'SSL' : 'HTTP' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="status" :label="t('user.domain.status')" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : row.status === 'error' ? 'danger' : 'warning'" size="small">
              {{ row.status === 'active' ? t('user.domain.statusActive') : row.status === 'error' ? t('user.domain.statusError') : t('user.domain.statusPending') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('user.domain.actions')" width="150" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="handleEdit(row)">
              <el-icon><Edit /></el-icon>
            </el-button>
            <el-button link type="danger" @click="handleDelete(row)">
              <el-icon><Delete /></el-icon>
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty v-if="!loading && domains.length === 0" :description="t('user.domain.noDomains')" />
    </el-card>

    <!-- 创建/编辑对话框 -->
    <el-dialog v-model="showDialog" :title="isEdit ? t('user.domain.edit') : t('user.domain.addDomain')" width="500px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="formRules" label-width="120px">
        <el-form-item :label="t('user.domain.domainName')" prop="domainName">
          <el-input v-model="form.domainName" placeholder="example.com" />
        </el-form-item>
        <el-form-item :label="t('user.domain.instanceId')" prop="instanceId">
          <el-input-number v-model="form.instanceId" :min="1" />
        </el-form-item>
        <el-form-item :label="t('user.domain.protocol')" prop="protocol">
          <el-select v-model="form.protocol">
            <el-option label="HTTP" value="http" />
            <el-option label="HTTPS" value="https" />
            <el-option label="TCP" value="tcp" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('user.domain.internalPort')" prop="internalPort">
          <el-input-number v-model="form.internalPort" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item :label="t('user.domain.enableSsl')" prop="enableSsl">
          <el-switch v-model="form.enableSsl" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showDialog = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Edit, Delete } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { getUserDomains, createUserDomain, updateUserDomain, deleteUserDomain } from '@/api/features'

const { t } = useI18n()

const domains = ref([])
const loading = ref(false)
const submitting = ref(false)
const showDialog = ref(false)
const isEdit = ref(false)
const editId = ref(null)
const formRef = ref(null)

const form = reactive({
  domainName: '',
  instanceId: null,
  protocol: 'http',
  internalPort: 80,
  enableSsl: false
})

const formRules = {
  domainName: [{ required: true, message: () => t('user.domain.domainRequired'), trigger: 'blur' }],
  instanceId: [{ required: true, message: () => t('user.domain.instanceIdRequired'), trigger: 'blur' }],
  internalPort: [{ required: true, message: () => t('user.domain.portRequired'), trigger: 'blur' }]
}

async function fetchData() {
  loading.value = true
  try {
    const res = await getUserDomains()
    if (res.code === 0 || res.code === 200) {
      domains.value = res.data || []
    }
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  isEdit.value = false
  editId.value = null
  Object.assign(form, { domainName: '', instanceId: null, protocol: 'http', internalPort: 80, enableSsl: false })
  showDialog.value = true
}

function handleEdit(row) {
  isEdit.value = true
  editId.value = row.id
  Object.assign(form, {
    domainName: row.domainName,
    instanceId: row.instanceId,
    protocol: row.protocol,
    internalPort: row.internalPort,
    enableSsl: row.enableSsl
  })
  showDialog.value = true
}

async function handleSubmit() {
  await formRef.value.validate()
  submitting.value = true
  try {
    if (isEdit.value) {
      await updateUserDomain(editId.value, form)
      ElMessage.success(t('user.domain.updateSuccess'))
    } else {
      await createUserDomain(form)
      ElMessage.success(t('user.domain.createSuccess'))
    }
    showDialog.value = false
    fetchData()
  } finally {
    submitting.value = false
  }
}

async function handleDelete(row) {
  await ElMessageBox.confirm(t('user.domain.confirmDelete'))
  await deleteUserDomain(row.id)
  ElMessage.success(t('user.domain.deleteSuccess'))
  fetchData()
}

onMounted(() => fetchData())
</script>

<style scoped>
.domain-container { padding: 20px; }
.card-header { display: flex; justify-content: space-between; align-items: center; }
</style>
