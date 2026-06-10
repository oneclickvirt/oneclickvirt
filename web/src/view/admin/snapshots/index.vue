<template>
  <div class="snapshot-page">
    <el-card class="summary-card">
      <template #header>
        <div class="card-header">
          <span>{{ t('admin.snapshots.title') }}</span>
          <el-button type="primary" :loading="loading" @click="loadAll">{{ t('admin.snapshots.refresh') }}</el-button>
        </div>
      </template>
      <el-row :gutter="16">
        <el-col :xs="24" :sm="8" :md="6">
          <el-statistic :title="t('admin.snapshots.totalSnapshots')" :value="overview.total || 0" />
        </el-col>
        <el-col :xs="24" :sm="8" :md="6">
          <el-statistic :title="t('admin.snapshots.availableSnapshots')" :value="overview.available || 0" />
        </el-col>
        <el-col :xs="24" :sm="8" :md="6">
          <el-statistic :title="t('admin.snapshots.failedSnapshots')" :value="overview.failed || 0" />
        </el-col>
        <el-col :xs="24" :sm="8" :md="6">
          <el-statistic :title="t('admin.snapshots.schedules')" :value="overview.schedules || 0" />
        </el-col>
      </el-row>
    </el-card>

    <el-card class="content-card">
      <el-tabs v-model="activeTab" @tab-change="handleTabChange">
        <el-tab-pane :label="t('admin.snapshots.overview')" name="snapshots">
          <div class="toolbar">
            <el-input v-model.number="snapshotFilter.instanceId" :placeholder="t('admin.snapshots.instanceId')" clearable class="toolbar-input" />
            <el-select v-model="snapshotFilter.status" :placeholder="t('admin.snapshots.status')" clearable class="toolbar-input">
              <el-option :label="t('admin.snapshots.creating')" value="creating" />
              <el-option :label="t('admin.snapshots.available')" value="available" />
              <el-option :label="t('admin.snapshots.failed')" value="failed" />
            </el-select>
            <el-select v-model="snapshotFilter.providerType" :placeholder="t('admin.snapshots.provider')" clearable class="toolbar-input">
              <el-option label="Proxmox" value="proxmox" />
              <el-option label="LXD" value="lxd" />
              <el-option label="Incus" value="incus" />
              <el-option label="QEMU/Libvirt" value="qemu" />
              <el-option label="KubeVirt" value="kubevirt" />
              <el-option label="Docker" value="docker" />
              <el-option label="Podman" value="podman" />
            </el-select>
            <el-button type="primary" @click="loadSnapshots">{{ t('admin.snapshots.query') }}</el-button>
            <el-button @click="resetSnapshotFilter">{{ t('admin.snapshots.reset') }}</el-button>
            <el-button type="success" @click="openCreateDialog">{{ t('admin.snapshots.createSnapshot') }}</el-button>
          </div>
          <el-table v-loading="loading" :data="snapshots" border>
            <el-table-column prop="id" label="ID" width="80" />
            <el-table-column prop="name" :label="t('admin.snapshots.snapshotName')" min-width="160" />
            <el-table-column prop="instanceName" :label="t('admin.snapshots.instance')" min-width="160" />
            <el-table-column prop="providerType" :label="t('admin.snapshots.provider')" width="120" />
            <el-table-column prop="instanceType" :label="t('admin.snapshots.type')" width="100" />
            <el-table-column prop="source" :label="t('admin.snapshots.source')" width="100" />
            <el-table-column prop="status" :label="t('admin.snapshots.status')" width="110">
              <template #default="{ row }">
                <el-tag :type="snapshotStatusType(row.status)">{{ translateStatus(row.status) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="createdAt" :label="t('admin.snapshots.createTime')" width="180">
              <template #default="{ row }">{{ formatDate(row.createdAt) }}</template>
            </el-table-column>
            <el-table-column :label="t('admin.snapshots.actions')" width="210" fixed="right">
              <template #default="{ row }">
                <el-button size="small" type="warning" :disabled="row.status !== 'available'" @click="restoreSnapshot(row)">{{ t('admin.snapshots.restore') }}</el-button>
                <el-button size="small" type="danger" @click="deleteSnapshot(row)">{{ t('admin.snapshots.delete') }}</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-pagination
            v-model:current-page="snapshotPagination.page"
            v-model:page-size="snapshotPagination.pageSize"
            :total="snapshotPagination.total"
            :page-sizes="[10, 20, 50, 100]"
            layout="total, sizes, prev, pager, next"
            class="pagination"
            @size-change="loadSnapshots"
            @current-change="loadSnapshots"
          />
        </el-tab-pane>

        <el-tab-pane :label="t('admin.snapshots.scheduleSnapshots')" name="schedules">
          <div class="toolbar">
            <el-button type="success" @click="openScheduleDialog">{{ t('admin.snapshots.newSchedule') }}</el-button>
            <el-button @click="loadSchedules">{{ t('admin.snapshots.refresh') }}</el-button>
          </div>
          <el-table v-loading="loading" :data="schedules" border>
            <el-table-column prop="id" label="ID" width="80" />
            <el-table-column prop="name" :label="t('admin.snapshots.scheduleName')" min-width="160" />
            <el-table-column prop="instanceName" :label="t('admin.snapshots.instance')" min-width="160" />
            <el-table-column prop="intervalHours" :label="t('admin.snapshots.intervalHours')" width="120" />
            <el-table-column prop="retentionDays" :label="t('admin.snapshots.retentionDays')" width="110" />
            <el-table-column prop="maxSnapshots" :label="t('admin.snapshots.maxSnapshots')" width="110" />
            <el-table-column prop="enabled" :label="t('admin.snapshots.enabled')" width="90">
              <template #default="{ row }">
                <el-switch v-model="row.enabled" @change="toggleSchedule(row)" />
              </template>
            </el-table-column>
            <el-table-column prop="nextRunAt" :label="t('admin.snapshots.nextRunAt')" width="180">
              <template #default="{ row }">{{ formatDate(row.nextRunAt) }}</template>
            </el-table-column>
            <el-table-column prop="lastError" :label="t('admin.snapshots.lastError')" min-width="180" show-overflow-tooltip />
            <el-table-column :label="t('admin.snapshots.actions')" width="110" fixed="right">
              <template #default="{ row }">
                <el-button size="small" type="danger" @click="deleteSchedule(row)">{{ t('admin.snapshots.delete') }}</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-pagination
            v-model:current-page="schedulePagination.page"
            v-model:page-size="schedulePagination.pageSize"
            :total="schedulePagination.total"
            :page-sizes="[10, 20, 50, 100]"
            layout="total, sizes, prev, pager, next"
            class="pagination"
            @size-change="loadSchedules"
            @current-change="loadSchedules"
          />
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <el-dialog v-model="createDialogVisible" :title="t('admin.snapshots.createSnapshot')" width="520px">
      <el-form :model="createForm" label-width="100px">
        <el-form-item :label="t('admin.snapshots.instanceId')" required>
          <el-input-number v-model="createForm.instanceId" :min="1" controls-position="right" />
        </el-form-item>
        <el-form-item :label="t('admin.snapshots.snapshotName')">
          <el-input v-model="createForm.name" :placeholder="t('admin.snapshots.autoNamePlaceholder')" />
        </el-form-item>
        <el-form-item :label="t('admin.snapshots.description')">
          <el-input v-model="createForm.description" type="textarea" :rows="3" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="createSnapshot">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="scheduleDialogVisible" :title="t('admin.snapshots.addSchedule')" width="560px">
      <el-form :model="scheduleForm" label-width="120px">
        <el-form-item :label="t('admin.snapshots.instanceId')" required>
          <el-input-number v-model="scheduleForm.instanceId" :min="1" controls-position="right" />
        </el-form-item>
        <el-form-item :label="t('admin.snapshots.scheduleName')" required>
          <el-input v-model="scheduleForm.name" />
        </el-form-item>
        <el-form-item :label="t('admin.snapshots.intervalHours')">
          <el-input-number v-model="scheduleForm.intervalHours" :min="1" :max="720" controls-position="right" />
        </el-form-item>
        <el-form-item :label="t('admin.snapshots.retentionDays')">
          <el-input-number v-model="scheduleForm.retentionDays" :min="1" :max="365" controls-position="right" />
        </el-form-item>
        <el-form-item :label="t('admin.snapshots.maxSnapshots')">
          <el-input-number v-model="scheduleForm.maxSnapshots" :min="1" :max="100" controls-position="right" />
        </el-form-item>
        <el-form-item :label="t('admin.snapshots.enabled')">
          <el-switch v-model="scheduleForm.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="scheduleDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="createSchedule">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { snapshotApi } from '@/api/admin'

const { t, locale } = useI18n()

const activeTab = ref('snapshots')
const loading = ref(false)
const submitting = ref(false)
const overview = reactive({ total: 0, available: 0, failed: 0, schedules: 0 })
const snapshots = ref([])
const schedules = ref([])
const snapshotPagination = reactive({ page: 1, pageSize: 20, total: 0 })
const schedulePagination = reactive({ page: 1, pageSize: 20, total: 0 })
const snapshotFilter = reactive({ instanceId: null, providerType: '', status: '' })
const createDialogVisible = ref(false)
const scheduleDialogVisible = ref(false)
const createForm = reactive({ instanceId: null, name: '', description: '' })
const scheduleForm = reactive({ instanceId: null, name: '', intervalHours: 24, retentionDays: 7, maxSnapshots: 3, enabled: true })

const loadOverview = async () => {
  const res = await snapshotApi.overview()
  Object.assign(overview, res.data || {})
}

const loadSnapshots = async () => {
  loading.value = true
  try {
    const params = {
      page: snapshotPagination.page,
      pageSize: snapshotPagination.pageSize,
      status: snapshotFilter.status || undefined,
      providerType: snapshotFilter.providerType || undefined,
      instanceId: snapshotFilter.instanceId || undefined
    }
    const res = await snapshotApi.list(params)
    snapshots.value = res.data?.list || []
    snapshotPagination.total = res.data?.total || 0
  } catch (error) {
    ElMessage.error(error.message || t('admin.snapshots.loadSnapshotsFailed'))
  } finally {
    loading.value = false
  }
}

const loadSchedules = async () => {
  loading.value = true
  try {
    const res = await snapshotApi.schedules({ page: schedulePagination.page, pageSize: schedulePagination.pageSize })
    schedules.value = res.data?.list || []
    schedulePagination.total = res.data?.total || 0
  } catch (error) {
    ElMessage.error(error.message || t('admin.snapshots.loadSchedulesFailed'))
  } finally {
    loading.value = false
  }
}

const loadAll = async () => {
  loading.value = true
  try {
    await Promise.all([loadOverview(), loadSnapshots(), loadSchedules()])
  } finally {
    loading.value = false
  }
}

const handleTabChange = (tab) => {
  if (tab === 'schedules') loadSchedules()
  else loadSnapshots()
}

const resetSnapshotFilter = () => {
  Object.assign(snapshotFilter, { instanceId: null, providerType: '', status: '' })
  snapshotPagination.page = 1
  loadSnapshots()
}

const openCreateDialog = () => {
  Object.assign(createForm, { instanceId: snapshotFilter.instanceId || null, name: '', description: '' })
  createDialogVisible.value = true
}

const createSnapshot = async () => {
  if (!createForm.instanceId) {
    ElMessage.warning(t('admin.snapshots.fillInstanceId'))
    return
  }
  submitting.value = true
  try {
    await snapshotApi.create(createForm.instanceId, { name: createForm.name, description: createForm.description })
    ElMessage.success(t('admin.snapshots.createSnapshotSuccess'))
    createDialogVisible.value = false
    await Promise.all([loadOverview(), loadSnapshots()])
  } catch (error) {
    ElMessage.error(error.message || t('admin.snapshots.createSnapshotFailed'))
  } finally {
    submitting.value = false
  }
}

const restoreSnapshot = async (row) => {
  try {
    await ElMessageBox.confirm(t('admin.snapshots.restoreConfirm', { name: row.name }), t('admin.snapshots.restoreTitle'), { type: 'warning' })
    await snapshotApi.restore(row.id)
    ElMessage.success(t('admin.snapshots.restoreSubmitted'))
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.message || t('admin.snapshots.restoreFailed'))
  }
}

const deleteSnapshot = async (row) => {
  try {
    await ElMessageBox.confirm(t('admin.snapshots.deleteSnapshotConfirm', { name: row.name }), t('admin.snapshots.deleteTitle'), { type: 'warning' })
    await snapshotApi.delete(row.id)
    ElMessage.success(t('admin.snapshots.deleteSuccess'))
    await Promise.all([loadOverview(), loadSnapshots()])
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.message || t('admin.snapshots.deleteFailed'))
  }
}

const openScheduleDialog = () => {
  Object.assign(scheduleForm, { instanceId: null, name: '', intervalHours: 24, retentionDays: 7, maxSnapshots: 3, enabled: true })
  scheduleDialogVisible.value = true
}

const createSchedule = async () => {
  if (!scheduleForm.instanceId || !scheduleForm.name) {
    ElMessage.warning(t('admin.snapshots.fillScheduleRequired'))
    return
  }
  submitting.value = true
  try {
    await snapshotApi.createSchedule({ ...scheduleForm })
    ElMessage.success(t('admin.snapshots.createScheduleSuccess'))
    scheduleDialogVisible.value = false
    await Promise.all([loadOverview(), loadSchedules()])
  } catch (error) {
    ElMessage.error(error.message || t('admin.snapshots.createScheduleFailed'))
  } finally {
    submitting.value = false
  }
}

const toggleSchedule = async (row) => {
  try {
    await snapshotApi.updateSchedule(row.id, { enabled: row.enabled })
    ElMessage.success(t('admin.snapshots.updateSuccess'))
  } catch (error) {
    row.enabled = !row.enabled
    ElMessage.error(error.message || t('admin.snapshots.updateFailed'))
  }
}

const deleteSchedule = async (row) => {
  try {
    await ElMessageBox.confirm(t('admin.snapshots.deleteScheduleConfirm', { name: row.name }), t('admin.snapshots.deleteTitle'), { type: 'warning' })
    await snapshotApi.deleteSchedule(row.id)
    ElMessage.success(t('admin.snapshots.deleteSuccess'))
    await Promise.all([loadOverview(), loadSchedules()])
  } catch (error) {
    if (error !== 'cancel') ElMessage.error(error.message || t('admin.snapshots.deleteFailed'))
  }
}

const snapshotStatusType = (status) => {
  if (status === 'available') return 'success'
  if (status === 'failed') return 'danger'
  return 'warning'
}

const translateStatus = (status) => {
  const keyMap = { creating: 'creating', available: 'available', failed: 'failed' }
  return keyMap[status] ? t(`admin.snapshots.${keyMap[status]}`) : status
}

const formatDate = (date) => {
  if (!date) return '-'
  return new Date(date).toLocaleString(locale.value)
}

onMounted(loadAll)
</script>

<style scoped>
.snapshot-page {
  padding: 20px;
}
.summary-card,
.content-card {
  margin-bottom: 16px;
}
.card-header,
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.toolbar {
  justify-content: flex-start;
  margin-bottom: 16px;
}
.toolbar-input {
  width: 180px;
}
.pagination {
  margin-top: 16px;
  justify-content: flex-end;
}
</style>
