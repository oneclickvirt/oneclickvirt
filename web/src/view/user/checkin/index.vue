<template>
  <div class="checkin-container">
    <el-card>
      <template #header>
        <span>{{ t('user.checkin.title') }}</span>
      </template>

      <!-- 签到操作 -->
      <el-form label-width="120px" style="max-width: 500px; margin-bottom: 30px;">
        <el-form-item :label="t('user.checkin.selectInstance')">
          <el-select v-model="selectedInstanceId" :placeholder="t('user.checkin.selectInstance')">
            <el-option v-for="inst in instances" :key="inst.id" :label="inst.name" :value="inst.id" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="getCode" :disabled="!selectedInstanceId" :loading="gettingCode">
            {{ t('user.checkin.getCode') }}
          </el-button>
        </el-form-item>
        <el-form-item v-if="verificationCode" :label="t('user.checkin.inputCode')">
          <el-input v-model="inputCode" style="width: 200px; margin-right: 10px;" />
          <el-button type="success" @click="doCheckin" :loading="checkingIn">
            {{ t('user.checkin.checkin') }}
          </el-button>
        </el-form-item>
        <el-form-item v-if="verificationCode">
          <el-tag type="info">{{ verificationCode }}</el-tag>
        </el-form-item>
      </el-form>

      <!-- 签到记录 -->
      <el-divider />
      <h3>{{ t('user.checkin.records') }}</h3>
      <el-table :data="records" v-loading="loadingRecords" stripe>
        <el-table-column prop="instanceId" :label="t('user.checkin.instanceName')" width="120" />
        <el-table-column prop="renewalDays" :label="t('user.checkin.renewalDays')" width="100" />
        <el-table-column :label="t('user.checkin.oldExpireAt')" width="180">
          <template #default="{ row }">{{ formatDate(row.oldExpireAt) }}</template>
        </el-table-column>
        <el-table-column :label="t('user.checkin.newExpireAt')" width="180">
          <template #default="{ row }">{{ formatDate(row.newExpireAt) }}</template>
        </el-table-column>
        <el-table-column :label="t('user.checkin.checkinTime')" width="180">
          <template #default="{ row }">{{ formatDate(row.createdAt) }}</template>
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
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { generateCheckinCode, doCheckin as doCheckinApi, getCheckinRecords } from '@/api/features'
import { getUserInstances } from '@/api/user'

const { t } = useI18n()

const instances = ref([])
const selectedInstanceId = ref(null)
const verificationCode = ref('')
const inputCode = ref('')
const gettingCode = ref(false)
const checkingIn = ref(false)
const records = ref([])
const loadingRecords = ref(false)
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)

function formatDate(dateStr) {
  if (!dateStr) return '-'
  return new Date(dateStr).toLocaleString()
}

async function fetchInstances() {
  try {
    const res = await getUserInstances({ page: 1, pageSize: 100 })
    if (res.code === 0 || res.code === 200) {
      instances.value = res.data?.list || res.data || []
    }
  } catch { /* ignore */ }
}

async function getCode() {
  gettingCode.value = true
  try {
    const res = await generateCheckinCode(selectedInstanceId.value)
    if (res.code === 0 || res.code === 200) {
      verificationCode.value = res.data.code
      ElMessage.success(t('user.checkin.codeSent'))
    }
  } finally {
    gettingCode.value = false
  }
}

async function doCheckin() {
  checkingIn.value = true
  try {
    const res = await doCheckinApi({ instanceId: selectedInstanceId.value, code: inputCode.value })
    if (res.code === 0 || res.code === 200) {
      ElMessage.success(t('user.checkin.checkinSuccess'))
      verificationCode.value = ''
      inputCode.value = ''
      fetchRecords()
    }
  } finally {
    checkingIn.value = false
  }
}

async function fetchRecords() {
  loadingRecords.value = true
  try {
    const res = await getCheckinRecords({ page: page.value, pageSize: pageSize.value })
    if (res.code === 0 || res.code === 200) {
      records.value = res.data?.list || []
      total.value = res.data?.total || 0
    }
  } finally {
    loadingRecords.value = false
  }
}

function handlePageChange(p) {
  page.value = p
  fetchRecords()
}

onMounted(() => {
  fetchInstances()
  fetchRecords()
})
</script>

<style scoped>
.checkin-container { padding: 20px; }
</style>
