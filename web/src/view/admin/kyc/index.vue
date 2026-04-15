<template>
  <div class="kyc-mgmt-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ t('admin.kyc.title') }}</span>
          <el-radio-group v-model="statusFilter" @change="fetchData" size="small">
            <el-radio-button value="">{{ t('admin.kyc.filterAll') }}</el-radio-button>
            <el-radio-button value="pending">{{ t('admin.kyc.filterPending') }}</el-radio-button>
            <el-radio-button value="approved">{{ t('admin.kyc.filterApproved') }}</el-radio-button>
            <el-radio-button value="rejected">{{ t('admin.kyc.filterRejected') }}</el-radio-button>
          </el-radio-group>
        </div>
      </template>

      <el-table :data="records" v-loading="loading" stripe>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="userId" :label="t('admin.kyc.userId')" width="80" />
        <el-table-column prop="realName" :label="t('admin.kyc.realName')" />
        <el-table-column prop="method" :label="t('admin.kyc.method')" width="100" />
        <el-table-column prop="status" :label="t('admin.kyc.status')" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'approved' ? 'success' : row.status === 'rejected' ? 'danger' : 'warning'" size="small">
              {{ row.status === 'approved' ? t('admin.kyc.statusApproved') : row.status === 'rejected' ? t('admin.kyc.statusRejected') : t('admin.kyc.statusPending') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('admin.kyc.createdAt')" width="180">
          <template #default="{ row }">{{ formatDate(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column :label="t('admin.kyc.review')" width="200" fixed="right">
          <template #default="{ row }">
            <template v-if="row.status === 'pending'">
              <el-button type="success" size="small" @click="handleReview(row, true)">
                {{ t('admin.kyc.approve') }}
              </el-button>
              <el-button type="danger" size="small" @click="handleReview(row, false)">
                {{ t('admin.kyc.reject') }}
              </el-button>
            </template>
            <span v-else>-</span>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        v-if="total > pageSize"
        style="margin-top: 16px; justify-content: flex-end;"
        :current-page="page"
        :page-size="pageSize"
        :total="total"
        layout="total, prev, pager, next"
        @current-change="handlePageChange"
      />
    </el-card>

    <!-- 拒绝原因对话框 -->
    <el-dialog v-model="showRejectDialog" :title="t('admin.kyc.rejectReason')" width="400px" destroy-on-close>
      <el-input v-model="rejectReason" type="textarea" :rows="3" />
      <template #footer>
        <el-button @click="showRejectDialog = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="confirmReject">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { adminGetKYCList, adminReviewKYC } from '@/api/features'

const { t } = useI18n()

const records = ref([])
const loading = ref(false)
const submitting = ref(false)
const statusFilter = ref('')
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)
const showRejectDialog = ref(false)
const rejectReason = ref('')
const rejectingRecord = ref(null)

function formatDate(dateStr) {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

async function fetchData() {
  loading.value = true
  try {
    const res = await adminGetKYCList({ status: statusFilter.value, page: page.value, pageSize: pageSize.value })
    if (res.code === 200) {
      records.value = res.data?.list || []
      total.value = res.data?.total || 0
    }
  } finally {
    loading.value = false
  }
}

async function handleReview(row, approved) {
  if (approved) {
    submitting.value = true
    try {
      await adminReviewKYC(row.id, { approved: true, rejectReason: '' })
      ElMessage.success(t('admin.kyc.approveSuccess'))
      fetchData()
    } finally {
      submitting.value = false
    }
  } else {
    rejectingRecord.value = row
    rejectReason.value = ''
    showRejectDialog.value = true
  }
}

async function confirmReject() {
  submitting.value = true
  try {
    await adminReviewKYC(rejectingRecord.value.id, { approved: false, rejectReason: rejectReason.value })
    ElMessage.success(t('admin.kyc.rejectSuccess'))
    showRejectDialog.value = false
    fetchData()
  } finally {
    submitting.value = false
  }
}

function handlePageChange(p) {
  page.value = p
  fetchData()
}

onMounted(() => fetchData())
</script>

<style scoped>
.kyc-mgmt-container { padding: 20px; }
.card-header { display: flex; justify-content: space-between; align-items: center; }
</style>
