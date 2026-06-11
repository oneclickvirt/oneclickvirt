<template>
  <div class="snapshots-tab">
    <div class="toolbar">
      <el-button type="primary" :loading="submitting" @click="openCreateDialog">
        {{ t('user.instanceDetail.createSnapshot') }}
      </el-button>
      <el-button :loading="loading" @click="loadSnapshots">
        {{ t('user.instanceDetail.refresh') }}
      </el-button>
    </div>

    <el-table v-loading="loading" :data="snapshots" border>
      <el-table-column prop="name" :label="t('user.instanceDetail.snapshotName')" min-width="160" />
      <el-table-column prop="description" :label="t('user.instanceDetail.description')" min-width="180" show-overflow-tooltip />
      <el-table-column prop="status" :label="t('user.instanceDetail.status')" width="110">
        <template #default="{ row }">
          <el-tag :type="snapshotStatusType(row.status)">{{ translateStatus(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="source" :label="t('user.instanceDetail.source')" width="100" />
      <el-table-column prop="createdAt" :label="t('user.instanceDetail.createdAt')" width="180">
        <template #default="{ row }">{{ formatDate(row.createdAt) }}</template>
      </el-table-column>
      <el-table-column :label="t('user.instanceDetail.actions')" width="290" fixed="right">
        <template #default="{ row }">
          <el-button size="small" type="warning" :disabled="row.status !== 'available'" @click="restoreSnapshot(row)">
            {{ t('user.instanceDetail.restoreSnapshot') }}
          </el-button>
          <el-button size="small" @click="downloadSnapshot(row)">
            {{ t('user.instanceDetail.downloadSnapshot') }}
          </el-button>
          <el-button size="small" type="danger" @click="deleteSnapshot(row)">
            {{ t('user.instanceDetail.delete') }}
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-empty v-if="!loading && !snapshots.length" :description="t('user.instanceDetail.noSnapshots')" />

    <el-pagination
      v-model:current-page="pagination.page"
      v-model:page-size="pagination.pageSize"
      :total="pagination.total"
      :page-sizes="[10, 20, 50]"
      layout="total, sizes, prev, pager, next"
      class="pagination"
      @size-change="loadSnapshots"
      @current-change="loadSnapshots"
    />

    <el-dialog v-model="createDialogVisible" :title="t('user.instanceDetail.createSnapshot')" width="520px">
      <el-form :model="createForm" label-width="110px">
        <el-form-item :label="t('user.instanceDetail.snapshotName')">
          <el-input v-model="createForm.name" :placeholder="t('user.instanceDetail.autoSnapshotName')" />
        </el-form-item>
        <el-form-item :label="t('user.instanceDetail.description')">
          <el-input v-model="createForm.description" type="textarea" :rows="3" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="createSnapshot">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  createUserInstanceSnapshot,
  deleteUserSnapshot,
  downloadUserSnapshot,
  getUserInstanceSnapshots,
  restoreUserSnapshot
} from '@/api/user'

const props = defineProps({
  instanceId: {
    type: [Number, String],
    required: true
  }
})

const { t, locale } = useI18n()
const loading = ref(false)
const submitting = ref(false)
const snapshots = ref([])
const pagination = reactive({ page: 1, pageSize: 10, total: 0 })
const createDialogVisible = ref(false)
const createForm = reactive({ name: '', description: '' })

const loadSnapshots = async () => {
  if (!props.instanceId) return
  loading.value = true
  try {
    const res = await getUserInstanceSnapshots(props.instanceId, { page: pagination.page, pageSize: pagination.pageSize })
    snapshots.value = res.data?.list || []
    pagination.total = res.data?.total || 0
  } catch (error) {
    ElMessage.error(error.message || t('user.instanceDetail.loadSnapshotsFailed'))
  } finally {
    loading.value = false
  }
}

const openCreateDialog = () => {
  createForm.name = ''
  createForm.description = ''
  createDialogVisible.value = true
}

const createSnapshot = async () => {
  submitting.value = true
  try {
    await createUserInstanceSnapshot(props.instanceId, { ...createForm })
    ElMessage.success(t('user.instanceDetail.createSnapshotSubmitted'))
    createDialogVisible.value = false
    await loadSnapshots()
  } catch (error) {
    ElMessage.error(error.message || t('user.instanceDetail.createSnapshotFailed'))
  } finally {
    submitting.value = false
  }
}

const restoreSnapshot = async (row) => {
  try {
    await ElMessageBox.confirm(t('user.instanceDetail.restoreSnapshotConfirm', { name: row.name }), t('user.instanceDetail.confirmOperation'), { type: 'warning' })
    await restoreUserSnapshot(row.id)
    ElMessage.success(t('user.instanceDetail.restoreSnapshotSubmitted'))
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.message || t('user.instanceDetail.restoreSnapshotFailed'))
  }
}

const deleteSnapshot = async (row) => {
  try {
    await ElMessageBox.confirm(t('user.instanceDetail.deleteSnapshotConfirm', { name: row.name }), t('user.instanceDetail.confirmOperation'), { type: 'warning' })
    await deleteUserSnapshot(row.id)
    ElMessage.success(t('user.instanceDetail.deleteSuccess'))
    await loadSnapshots()
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.message || t('user.instanceDetail.deleteFailed'))
  }
}

const downloadSnapshot = async (row) => {
  try {
    const res = await downloadUserSnapshot(row.id)
    saveBlob(res.data, filenameFromResponse(res) || `snapshot-${row.instanceName || props.instanceId}-${row.name}.json`)
  } catch (error) {
    ElMessage.error(error.message || t('user.instanceDetail.downloadSnapshotFailed'))
  }
}

const snapshotStatusType = (status) => {
  if (status === 'available') return 'success'
  if (status === 'failed') return 'danger'
  return 'warning'
}

const translateStatus = (status) => {
  const keyMap = { creating: 'snapshotCreating', available: 'snapshotAvailable', failed: 'snapshotFailed' }
  return keyMap[status] ? t(`user.instanceDetail.${keyMap[status]}`) : status
}

const formatDate = (date) => {
  if (!date) return '-'
  return new Date(date).toLocaleString(locale.value)
}

const filenameFromResponse = (response) => {
  const disposition = response?.headers?.['content-disposition'] || ''
  const match = disposition.match(/filename="?([^";]+)"?/i)
  return match ? decodeURIComponent(match[1]) : ''
}

const saveBlob = (blob, filename) => {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

watch(() => props.instanceId, () => {
  pagination.page = 1
  loadSnapshots()
})

onMounted(loadSnapshots)
</script>

<style scoped>
.snapshots-tab {
  min-height: 220px;
}
.toolbar {
  display: flex;
  gap: 12px;
  align-items: center;
  margin-bottom: 16px;
  flex-wrap: wrap;
}
.pagination {
  margin-top: 16px;
  justify-content: flex-end;
}
</style>
