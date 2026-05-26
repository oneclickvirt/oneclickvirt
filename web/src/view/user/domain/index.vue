<template>
  <div class="domain-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ t('user.domain.title') }}</span>
          <el-button
            type="primary"
            @click="handleCreate"
          >
            {{ t('user.domain.addDomain') }}
          </el-button>
        </div>
      </template>

      <el-table
        v-loading="loading"
        :data="domains"
        stripe
      >
        <el-table-column
          prop="domainName"
          :label="t('user.domain.domainName')"
        />
        <el-table-column
          prop="instanceId"
          :label="t('user.domain.instanceId')"
          width="100"
        />
        <el-table-column
          prop="protocol"
          :label="t('user.domain.protocol')"
          width="100"
        />
        <el-table-column
          prop="internalIP"
          :label="t('user.domain.internalIp')"
        />
        <el-table-column
          prop="internalPort"
          :label="t('user.domain.internalPort')"
          width="100"
        />
        <el-table-column
          prop="enableSSL"
          :label="t('user.domain.enableSsl')"
          width="100"
        >
          <template #default="{ row }">
            <el-tag
              :type="row.enableSSL ? (row.hasCert ? 'success' : 'warning') : 'info'"
              size="small"
            >
              {{ row.enableSSL ? (row.hasCert ? 'SSL' : t('user.domain.noCert')) : 'HTTP' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          :label="t('user.domain.status')"
          width="100"
        >
          <template #default="{ row }">
            <el-tag
              :type="row.status === 'active' ? 'success' : row.status === 'error' ? 'danger' : 'warning'"
              size="small"
            >
              {{ row.status === 'active' ? t('user.domain.statusActive') : row.status === 'error' ? t('user.domain.statusError') : t('user.domain.statusPending') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('user.domain.actions')"
          width="150"
          fixed="right"
        >
          <template #default="{ row }">
            <el-button
              link
              type="primary"
              @click="handleEdit(row)"
            >
              <el-icon><Edit /></el-icon>
            </el-button>
            <el-button
              link
              type="danger"
              @click="handleDelete(row)"
            >
              <el-icon><Delete /></el-icon>
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty
        v-if="!loading && domains.length === 0"
        :description="t('user.domain.noDomains')"
      />
    </el-card>

    <!-- 创建/编辑对话框 -->
    <el-dialog
      v-model="showDialog"
      :title="isEdit ? t('user.domain.edit') : t('user.domain.addDomain')"
      width="560px"
      destroy-on-close
    >
      <!-- DNS 绑定说明 -->
      <el-alert
        v-if="!isEdit"
        type="info"
        :closable="false"
        style="margin-bottom: 16px;"
      >
        <template #title>
          <span>{{ t('user.domain.dnsGuideTitle') }}</span>
        </template>
        <div style="margin-top: 6px; line-height: 1.7;">
          <div>{{ t('user.domain.dnsStep1') }}</div>
          <div>{{ t('user.domain.dnsStep2') }}</div>
          <div>{{ t('user.domain.dnsStep3') }}</div>
        </div>
      </el-alert>

      <el-form
        ref="formRef"
        :model="form"
        :rules="formRules"
        label-width="130px"
      >
        <el-form-item
          :label="t('user.domain.domainName')"
          prop="domainName"
        >
          <el-input
            v-model="form.domainName"
            placeholder="app.example.com"
            :disabled="isEdit"
          />
        </el-form-item>
        <el-form-item
          v-if="!isEdit"
          :label="t('user.domain.instanceId')"
          prop="instanceId"
        >
          <el-select
            v-model="form.instanceId"
            :placeholder="t('user.domain.selectInstance')"
            filterable
            style="width: 100%"
            @change="onInstanceChange"
          >
            <el-option
              v-for="inst in userInstances"
              :key="inst.id"
              :value="inst.id"
              :label="`#${inst.id} - ${inst.name || ''}`"
            />
          </el-select>
        </el-form-item>
        <el-form-item
          v-if="!isEdit && selectedInstancePublicIP"
          :label="t('user.domain.nodeIpLabel')"
        >
          <el-input
            :model-value="selectedInstancePublicIP"
            readonly
          />
          <div class="form-tip">
            {{ t('user.domain.nodeIpTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="t('user.domain.internalIp')"
          prop="internalIP"
        >
          <el-input
            v-model="form.internalIP"
            placeholder="172.17.0.2"
          />
          <div class="form-tip">
            {{ t('user.domain.internalIpTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="t('user.domain.internalPort')"
          prop="internalPort"
        >
          <el-input-number
            v-model="form.internalPort"
            :min="1"
            :max="65535"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item
          :label="t('user.domain.protocol')"
          prop="protocol"
        >
          <el-select
            v-model="form.protocol"
            style="width: 100%"
          >
            <el-option
              label="HTTP"
              value="http"
            />
            <el-option
              label="HTTPS"
              value="https"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('user.domain.enableSsl')">
          <el-switch v-model="form.enableSSL" />
          <span
            class="form-tip"
            style="margin-left: 8px;"
          >{{ t('user.domain.enableSslTip') }}</span>
        </el-form-item>
        <template v-if="form.enableSSL">
          <el-form-item :label="t('user.domain.sslCert')">
            <el-input
              v-model="form.sslCertContent"
              type="textarea"
              :rows="4"
              :placeholder="t('user.domain.sslCertPlaceholder')"
            />
            <div class="form-tip">
              {{ t('user.domain.sslCertTip') }}
            </div>
          </el-form-item>
          <el-form-item :label="t('user.domain.sslKey')">
            <el-input
              v-model="form.sslKeyContent"
              type="textarea"
              :rows="4"
              :placeholder="t('user.domain.sslKeyPlaceholder')"
            />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="showDialog = false">
          {{ t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="handleSubmit"
        >
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Edit, Delete } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { getUserDomains, createUserDomain, updateUserDomain, deleteUserDomain } from '@/api/features'
import { getUserInstances } from '@/api/user'

const { t } = useI18n()

const domains = ref([])
const loading = ref(false)
const submitting = ref(false)
const showDialog = ref(false)
const isEdit = ref(false)
const editId = ref(null)
const formRef = ref(null)
const userInstances = ref([])

const form = reactive({
  domainName: '',
  instanceId: null,
  protocol: 'http',
  internalIP: '',
  internalPort: 80,
  enableSSL: false,
  sslCertContent: '',
  sslKeyContent: ''
})

const formRules = {
  domainName: [{ required: true, message: () => t('user.domain.domainRequired'), trigger: 'blur' }],
  instanceId: [{ required: true, message: () => t('user.domain.instanceIdRequired'), trigger: 'change' }],
  internalIP: [{ required: true, message: () => t('user.domain.internalIPRequired'), trigger: 'blur' }],
  internalPort: [{ required: true, message: () => t('user.domain.portRequired'), trigger: 'blur' }]
}

const selectedInstancePublicIP = computed(() => {
  if (!form.instanceId) return ''
  const inst = userInstances.value.find(i => i.id === form.instanceId)
  return inst?.publicIP || inst?.publicIp || ''
})

function onInstanceChange(instanceId) {
  const inst = userInstances.value.find(i => i.id === instanceId)
  if (inst) {
    if (inst.privateIP || inst.privateIp) {
      form.internalIP = inst.privateIP || inst.privateIp
    }
  }
}

async function fetchInstances() {
  try {
    const res = await getUserInstances({ page: 1, pageSize: 999 })
    userInstances.value = res.data?.list || res.data?.data || res.data || []
  } catch (_) {
    // ignore
  }
}

async function fetchData() {
  loading.value = true
  try {
    const res = await getUserDomains()
    if (res.code === 200) {
      domains.value = res.data || []
    }
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  isEdit.value = false
  editId.value = null
  Object.assign(form, { domainName: '', instanceId: null, protocol: 'http', internalIP: '', internalPort: 80, enableSSL: false, sslCertContent: '', sslKeyContent: '' })
  showDialog.value = true
}

function handleEdit(row) {
  isEdit.value = true
  editId.value = row.id
  Object.assign(form, {
    domainName: row.domainName,
    instanceId: row.instanceId,
    protocol: row.protocol || 'http',
    internalIP: row.internalIP || '',
    internalPort: row.internalPort,
    enableSSL: row.enableSSL || false,
    sslCertContent: '',
    sslKeyContent: ''
  })
  showDialog.value = true
}

async function handleSubmit() {
  try {
    await formRef.value.validate()
  } catch (_) {
    return
  }
  submitting.value = true
  try {
    if (isEdit.value) {
      await updateUserDomain(editId.value, {
        internalIP: form.internalIP,
        internalPort: form.internalPort,
        protocol: form.protocol,
        enableSSL: form.enableSSL,
        sslCertContent: form.sslCertContent,
        sslKeyContent: form.sslKeyContent
      })
      ElMessage.success(t('user.domain.updateSuccess'))
    } else {
      await createUserDomain({
        domainName: form.domainName,
        instanceId: form.instanceId,
        protocol: form.protocol,
        internalIP: form.internalIP,
        internalPort: form.internalPort,
        enableSSL: form.enableSSL,
        sslCertContent: form.sslCertContent,
        sslKeyContent: form.sslKeyContent
      })
      ElMessage.success(t('user.domain.createSuccess'))
    }
    showDialog.value = false
    fetchData()
  } catch (error) {
    ElMessage.error(error?.message || (isEdit.value ? t('user.domain.updateFailed') : t('user.domain.createFailed')))
  } finally {
    submitting.value = false
  }
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(t('user.domain.confirmDelete'))
  } catch (_) {
    return
  }
  try {
    await deleteUserDomain(row.id)
    ElMessage.success(t('user.domain.deleteSuccess'))
    fetchData()
  } catch (error) {
    ElMessage.error(error?.message || t('user.domain.deleteFailed'))
  }
}

onMounted(() => {
  fetchData()
  fetchInstances()
})
</script>

<style scoped>
.domain-container { padding: 20px; }
.card-header { display: flex; justify-content: space-between; align-items: center; }
.form-tip { font-size: 12px; color: #909399; line-height: 1.4; margin-top: 2px; }
</style>
