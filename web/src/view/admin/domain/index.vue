<template>
  <div class="domain-mgmt-container">
    <el-tabs v-model="activeTab">
      <!-- 域名绑定列表标签页 -->
      <el-tab-pane :label="t('admin.domain.allDomains')" name="domains">
        <el-card>
          <el-table :data="domains" v-loading="loading" stripe>
            <el-table-column prop="id" label="ID" width="60" />
            <el-table-column prop="userId" :label="t('admin.domain.userId')" width="80" />
            <el-table-column prop="instanceId" :label="t('admin.domain.instanceId')" width="80" />
            <el-table-column prop="domainName" :label="t('admin.domain.domainName')" />
            <el-table-column prop="internalIP" :label="t('admin.domain.internalIP')" />
            <el-table-column prop="internalPort" :label="t('admin.domain.internalPort')" width="100" />
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
      </el-tab-pane>

      <!-- 节点域名配置标签页 -->
      <el-tab-pane :label="t('admin.domain.providerConfig')" name="config">
        <el-card>
          <el-table :data="providers" v-loading="configLoading" stripe>
            <el-table-column prop="id" label="ID" width="60" />
            <el-table-column prop="name" :label="t('admin.domain.providerName')" />
            <el-table-column prop="type" :label="t('admin.domain.providerType')" width="100" />
            <el-table-column :label="t('admin.domain.domainBindingEnabled')" width="120">
              <template #default="{ row }">
                <el-tag :type="getProviderConfig(row.id)?.enabled ? 'success' : 'info'" size="small">
                  {{ getProviderConfig(row.id)?.enabled ? t('common.enabled') : t('common.disabled') }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column :label="t('admin.domain.maxDomainsPerUser')" width="120">
              <template #default="{ row }">
                {{ getProviderConfig(row.id)?.maxDomainsPerUser ?? 3 }}
              </template>
            </el-table-column>
            <el-table-column :label="t('admin.domain.allowedSuffixes')" min-width="150">
              <template #default="{ row }">
                <span>{{ getProviderConfig(row.id)?.allowedSuffixes || t('admin.domain.noSuffixLimit') }}</span>
              </template>
            </el-table-column>
            <el-table-column :label="t('admin.domain.actions')" width="100" fixed="right">
              <template #default="{ row }">
                <el-button link type="primary" @click="handleEditConfig(row)">
                  <el-icon><Edit /></el-icon>
                </el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <!-- 域名配置编辑对话框 -->
    <el-dialog v-model="showConfigDialog" :title="t('admin.domain.editConfig')" width="520px" destroy-on-close>
      <el-form ref="configFormRef" :model="configForm" label-width="150px">
        <el-form-item :label="t('admin.domain.enabled')">
          <el-switch v-model="configForm.enabled" />
        </el-form-item>
        <el-form-item :label="t('admin.domain.maxDomainsPerUser')">
          <el-input-number v-model="configForm.maxDomainsPerUser" :min="1" :max="100" style="width: 100%" />
        </el-form-item>
        <el-form-item :label="t('admin.domain.dnsType')">
          <el-select v-model="configForm.dnsType" style="width: 100%">
            <el-option label="Hosts" value="hosts" />
            <el-option label="Nginx" value="nginx" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('admin.domain.allowedSuffixes')">
          <el-input v-model="configForm.allowedSuffixes" :placeholder="t('admin.domain.allowedSuffixesPlaceholder')" />
          <div style="font-size: 12px; color: #909399; margin-top: 2px;">{{ t('admin.domain.allowedSuffixesTip') }}</div>
        </el-form-item>
        <el-form-item :label="t('admin.domain.nginxConfigPath')">
          <el-input v-model="configForm.nginxConfigPath" placeholder="/etc/nginx/conf.d" />
        </el-form-item>
        <el-form-item :label="t('admin.domain.nginxReloadCmd')">
          <el-input v-model="configForm.nginxReloadCmd" placeholder="systemctl reload nginx" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showConfigDialog = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="savingConfig" @click="handleSaveConfig">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, Edit } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { adminGetDomains, adminDeleteDomain, getDomainConfig, updateDomainConfig } from '@/api/features'
import { getProviderList } from '@/api/admin'

const { t } = useI18n()

const activeTab = ref('domains')
const domains = ref([])
const loading = ref(false)
const providers = ref([])
const providerConfigs = ref({})
const configLoading = ref(false)
const showConfigDialog = ref(false)
const savingConfig = ref(false)
const configFormRef = ref(null)
const editingProviderId = ref(null)

const configForm = reactive({
  enabled: false,
  maxDomainsPerUser: 3,
  dnsType: 'hosts',
  dnsConfigPath: '',
  nginxConfigPath: '/etc/nginx/conf.d',
  nginxReloadCmd: 'systemctl reload nginx',
  allowedSuffixes: ''
})

function getProviderConfig(providerId) {
  return providerConfigs.value[providerId] || null
}

async function fetchData() {
  loading.value = true
  try {
    const res = await adminGetDomains()
    if (res.code === 200) {
      domains.value = res.data || []
    }
  } finally {
    loading.value = false
  }
}

async function fetchProviders() {
  configLoading.value = true
  try {
    const res = await getProviderList({ page: 1, pageSize: 999 })
    if (res.code === 200) {
      providers.value = res.data?.list || []
      // Fetch domain config for each provider
      const configs = {}
      await Promise.all(providers.value.map(async (p) => {
        try {
          const r = await getDomainConfig(p.id)
          if (r.code === 200) {
            configs[p.id] = r.data
          }
        } catch (_) {
          // provider has no config yet
        }
      }))
      providerConfigs.value = configs
    }
  } finally {
    configLoading.value = false
  }
}

function handleEditConfig(provider) {
  editingProviderId.value = provider.id
  const existing = getProviderConfig(provider.id)
  Object.assign(configForm, {
    enabled: existing?.enabled ?? false,
    maxDomainsPerUser: existing?.maxDomainsPerUser ?? 3,
    dnsType: existing?.dnsType ?? 'hosts',
    dnsConfigPath: existing?.dnsConfigPath ?? '',
    nginxConfigPath: existing?.nginxConfigPath ?? '/etc/nginx/conf.d',
    nginxReloadCmd: existing?.nginxReloadCmd ?? 'systemctl reload nginx',
    allowedSuffixes: existing?.allowedSuffixes ?? ''
  })
  showConfigDialog.value = true
}

async function handleSaveConfig() {
  savingConfig.value = true
  try {
    await updateDomainConfig(editingProviderId.value, { ...configForm })
    ElMessage.success(t('admin.domain.configUpdated'))
    showConfigDialog.value = false
    await fetchProviders()
  } catch (e) {
    ElMessage.error(e?.message || t('admin.domain.saveFailed'))
  } finally {
    savingConfig.value = false
  }
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(t('admin.domain.confirmDelete'))
    await adminDeleteDomain(row.id)
    ElMessage.success(t('admin.domain.deleteSuccess'))
    fetchData()
  } catch (e) {
    if (e !== 'cancel') {
      ElMessage.error(e?.message || t('admin.domain.deleteFailed'))
    }
  }
}

onMounted(() => {
  fetchData()
  fetchProviders()
})
</script>

<style scoped>
.domain-mgmt-container { padding: 20px; }
</style>
