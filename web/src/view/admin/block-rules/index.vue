<template>
  <div class="block-rules-container">
    <!-- Rules Tab -->
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ t('admin.blockRules.rules') }}</span>
          <div>
            <el-button
              type="primary"
              @click="handleCreateRule"
            >
              <el-icon><Plus /></el-icon>
              {{ t('admin.blockRules.addRule') }}
            </el-button>
            <el-button
              type="success"
              :disabled="selectedRules.length === 0"
              @click="showApplyDialog = true"
            >
              {{ t('admin.blockRules.applyRules') }}
            </el-button>
          </div>
        </div>
      </template>

      <el-table
        v-loading="loadingRules"
        :data="rules"
        stripe
        @selection-change="handleRuleSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
        />
        <el-table-column
          prop="name"
          :label="t('admin.blockRules.ruleName')"
          min-width="150"
        />
        <el-table-column
          prop="category"
          :label="t('admin.blockRules.category')"
          width="120"
        >
          <template #default="{ row }">
            <el-tag
              :type="categoryTagType(row.category)"
              size="small"
            >
              {{ t(`admin.blockRules.categories.${row.category}`) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="description"
          :label="t('admin.blockRules.description')"
          min-width="200"
          show-overflow-tooltip
        />
        <el-table-column
          :label="t('admin.blockRules.strings')"
          width="120"
        >
          <template #default="{ row }">
            {{ parseStrings(row.strings).length }} {{ t('admin.blockRules.strings') }}
          </template>
        </el-table-column>
        <el-table-column
          :label="t('admin.blockRules.enabled')"
          width="100"
        >
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              @change="(val) => handleToggleEnabled(row, val)"
            />
          </template>
        </el-table-column>
        <el-table-column
          :label="t('admin.blockRules.builtin')"
          width="80"
        >
          <template #default="{ row }">
            <el-tag
              v-if="row.is_builtin"
              type="info"
              size="small"
            >
              {{ t('admin.blockRules.builtin') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('admin.blockRules.actions')"
          width="160"
          fixed="right"
        >
          <template #default="{ row }">
            <el-button
              link
              type="primary"
              @click="handleEditRule(row)"
            >
              <el-icon><Edit /></el-icon>
            </el-button>
            <el-button
              link
              type="danger"
              @click="handleDeleteRule(row)"
            >
              <el-icon><Delete /></el-icon>
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- Applications Card -->
    <el-card style="margin-top: 16px;">
      <template #header>
        <div class="card-header">
          <span>{{ t('admin.blockRules.applications') }}</span>
          <el-button
            type="danger"
            :disabled="selectedApps.length === 0"
            @click="handleRemoveApplications"
          >
            {{ t('admin.blockRules.removeApplications') }}
          </el-button>
        </div>
      </template>

      <el-table
        v-loading="loadingApps"
        :data="applications"
        stripe
        @selection-change="handleAppSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
        />
        <el-table-column
          prop="rule_id"
          :label="t('admin.blockRules.ruleId')"
          width="90"
        />
        <el-table-column
          prop="scope"
          :label="t('admin.blockRules.scope')"
          width="100"
        >
          <template #default="{ row }">
            <el-tag size="small">
              {{ t(`admin.blockRules.scopes.${row.scope}`) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="target_name"
          :label="t('admin.blockRules.targetName')"
          min-width="150"
        />
        <el-table-column
          prop="status"
          :label="t('admin.blockRules.status')"
          width="100"
        >
          <template #default="{ row }">
            <el-tag
              :type="statusTagType(row.status)"
              size="small"
            >
              {{ t(`admin.blockRules.statuses.${row.status}`) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="ip_version"
          :label="t('admin.blockRules.ipVersion')"
          width="120"
        >
          <template #default="{ row }">
            <el-tag
              size="small"
              type="info"
            >
              {{ t(`admin.blockRules.ipVersions.${row.ip_version || 'both'}`) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="created_at"
          :label="t('admin.blockRules.createdAt')"
          width="180"
        >
          <template #default="{ row }">
            {{ formatDate(row.created_at) }}
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- Create/Edit Rule Dialog -->
    <el-dialog
      v-model="showRuleDialog"
      :title="isEdit ? t('admin.blockRules.editRule') : t('admin.blockRules.addRule')"
      width="600px"
      destroy-on-close
    >
      <el-form
        ref="ruleFormRef"
        :model="ruleForm"
        :rules="ruleFormRules"
        label-width="120px"
      >
        <el-form-item
          :label="t('admin.blockRules.ruleName')"
          prop="name"
        >
          <el-input v-model="ruleForm.name" />
        </el-form-item>
        <el-form-item
          :label="t('admin.blockRules.category')"
          prop="category"
        >
          <el-select
            v-model="ruleForm.category"
            style="width: 100%;"
          >
            <el-option
              v-for="cat in categories"
              :key="cat"
              :label="t(`admin.blockRules.categories.${cat}`)"
              :value="cat"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('admin.blockRules.description')">
          <el-input
            v-model="ruleForm.description"
            type="textarea"
            :rows="2"
          />
        </el-form-item>
        <el-form-item
          :label="t('admin.blockRules.strings')"
          prop="stringsText"
        >
          <el-input
            v-model="ruleForm.stringsText"
            type="textarea"
            :rows="8"
            :placeholder="t('admin.blockRules.stringsPlaceholder')"
          />
        </el-form-item>
        <el-form-item :label="t('admin.blockRules.enabled')">
          <el-switch v-model="ruleForm.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showRuleDialog = false">
          {{ t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="handleSubmitRule"
        >
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- Apply Rules Dialog -->
    <el-dialog
      v-model="showApplyDialog"
      :title="t('admin.blockRules.applyRules')"
      width="600px"
      destroy-on-close
    >
      <el-form
        ref="applyFormRef"
        :model="applyForm"
        :rules="applyFormRules"
        label-width="120px"
      >
        <el-form-item :label="t('admin.blockRules.selectRules')">
          <div>
            <el-tag
              v-for="rule in selectedRules"
              :key="rule.id"
              style="margin: 2px;"
              size="small"
            >
              {{ rule.name }}
            </el-tag>
          </div>
        </el-form-item>
        <el-form-item
          :label="t('admin.blockRules.scope')"
          prop="scope"
        >
          <el-select
            v-model="applyForm.scope"
            style="width: 100%;"
            @change="handleScopeChange"
          >
            <el-option
              v-for="s in scopeOptions"
              :key="s"
              :label="t(`admin.blockRules.scopes.${s}`)"
              :value="s"
            />
          </el-select>
        </el-form-item>
        <el-form-item
          v-if="applyForm.scope === 'provider'"
          :label="t('admin.blockRules.selectTargets')"
          prop="target_ids"
        >
          <el-select
            v-model="applyForm.target_ids"
            multiple
            filterable
            style="width: 100%;"
          >
            <el-option
              v-for="p in providerOptions"
              :key="p.id"
              :label="p.name || `Provider #${p.id}`"
              :value="p.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item
          v-if="applyForm.scope === 'instance'"
          :label="t('admin.blockRules.selectTargets')"
          prop="target_ids"
        >
          <div style="width: 100%;">
            <el-select
              v-model="instanceProviderFilter"
              :placeholder="t('admin.blockRules.filterByProvider')"
              clearable
              style="width: 100%; margin-bottom: 8px;"
              @change="fetchInstancesForProvider"
            >
              <el-option
                v-for="p in providerOptions"
                :key="p.id"
                :label="p.name || `Provider #${p.id}`"
                :value="p.id"
              />
            </el-select>
            <el-select
              v-model="applyForm.target_ids"
              multiple
              filterable
              :loading="loadingInstances"
              style="width: 100%;"
            >
              <el-option
                v-for="inst in instanceOptions"
                :key="inst.id"
                :label="`${inst.name} (${inst.status})`"
                :value="inst.id"
              />
            </el-select>
          </div>
        </el-form-item>
        <el-form-item
          :label="t('admin.blockRules.ipVersion')"
          prop="ip_version"
        >
          <el-select
            v-model="applyForm.ip_version"
            style="width: 100%;"
          >
            <el-option
              v-for="v in ipVersionOptions"
              :key="v"
              :label="t(`admin.blockRules.ipVersions.${v}`)"
              :value="v"
            />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showApplyDialog = false">
          {{ t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="handleApplyRules"
        >
          {{ t('admin.blockRules.applyRules') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Edit, Delete } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { blockRulesApi, getAllInstances } from '@/api/admin'

const { t } = useI18n()

const rules = ref([])
const applications = ref([])
const providerOptions = ref([])
const instanceOptions = ref([])
const selectedRules = ref([])
const selectedApps = ref([])
const loadingRules = ref(false)
const loadingApps = ref(false)
const loadingInstances = ref(false)
const submitting = ref(false)
const showRuleDialog = ref(false)
const showApplyDialog = ref(false)
const isEdit = ref(false)
const editId = ref(null)
const ruleFormRef = ref(null)
const applyFormRef = ref(null)
const instanceProviderFilter = ref(null)

const categories = ['mining', 'bt', 'speedtest', 'custom']
const scopeOptions = ['global', 'provider', 'instance']
const ipVersionOptions = ['both', 'ipv4', 'ipv6']

// Preset strings for each category
const categoryPresets = {
  mining: [
    'ethermine.com', 'ethermine.org', 'antpool.one', 'antpool.com', 'pool.bar',
    'c3pool', 'xmrig.com', 'blackcat.host', 'minexmr.com', 'supportxmr.com',
    'monerohash.com', 'hashvault.pro', 'xmrpool.eu', 'minergate.com',
    'webminepool.com', 'nanopool.org', '2miners.com', 'f2pool.com',
    'sparkpool.com', 'nicehash.com', 'prohashing.com', 'coinhive.com',
    'coinimp.com', 'cryptoloot.pro', 'xmrig', 'xmr-stak', 'cpuminer',
    'cgminer', 'ethminer', 'stratum+tcp', 'stratum+ssl', 'stratum+http',
    'stratum', 'raw.githubusercontent.com/xmrig', 'github.com/xmrig'
  ],
  bt: [
    'BitTorrent', 'BitTorrent protocol', 'BitTorrent protocol\\x13',
    'magnet:', '.torrent', 'd1:ad2:id20', 'd1:rd2:id20',
    'ut_metadata', 'ut_pex', 'lt_metadata', 'lt_donthave',
    'qBittorrent', 'Transmission', 'Deluge', 'aria2', 'libtorrent',
    'uTorrent', 'BiglyBT', 'Vuze', 'xunlei', 'Thunder', 'XLLiveUD'
  ],
  speedtest: [
    'speedtest', 'fast.com', 'speedtest.net', 'speedtest.com', 'speedtest.cn',
    'ookla.com', 'speedtestcustom.com', 'ovo.speedtestcustom.com',
    'speed.cloudflare.com', 'test.ustc.edu.cn', '10000.gd.cn',
    'db.laomoe.com', 'jiyou.cloud', 'mirrors.ustc.edu.cn',
    'mirrors.tuna.tsinghua.edu.cn', 'mirrors.aliyun.com',
    '.speed', '.speed.', '/speedtest', '/speed-test'
  ]
}

const ruleForm = reactive({
  name: '',
  category: 'custom',
  description: '',
  stringsText: '',
  enabled: true
})

const applyForm = reactive({
  scope: 'global',
  target_ids: [],
  ip_version: 'both'
})

const ruleFormRules = {
  name: [{ required: true, message: () => t('admin.blockRules.ruleNameRequired'), trigger: 'blur' }],
  category: [{ required: true, message: () => t('admin.blockRules.categoryRequired'), trigger: 'change' }],
  stringsText: [{ required: true, message: () => t('admin.blockRules.stringsRequired'), trigger: 'blur' }]
}

const applyFormRules = {
  scope: [{ required: true, message: () => t('admin.blockRules.scopeRequired'), trigger: 'change' }]
}

function parseStrings(str) {
  try { return JSON.parse(str) || [] } catch { return [] }
}

function formatDate(dateStr) {
  if (!dateStr) return ''
  return new Date(dateStr).toLocaleString()
}

function categoryTagType(cat) {
  const map = { mining: 'danger', bt: 'warning', speedtest: 'info', custom: '' }
  return map[cat] || ''
}

function statusTagType(status) {
  const map = { pending: 'warning', applied: 'success', failed: 'danger', removed: 'info' }
  return map[status] || ''
}

async function fetchRules() {
  loadingRules.value = true
  try {
    const res = await blockRulesApi.getRules()
    rules.value = res?.data || []
  } finally {
    loadingRules.value = false
  }
}

async function fetchApplications() {
  loadingApps.value = true
  try {
    const res = await blockRulesApi.getApplications()
    applications.value = res?.data || []
  } finally {
    loadingApps.value = false
  }
}

async function fetchAgentProviders() {
  try {
    const res = await blockRulesApi.getAgentProviders()
    const providers = res?.data || []
    providerOptions.value = providers.map(p => ({
      id: typeof p === 'object' ? p.id : p,
      name: typeof p === 'object' ? p.name : `Provider #${p}`
    }))
  } catch { /* ignore */ }
}

async function fetchInstancesForProvider(providerId) {
  applyForm.target_ids = []
  if (!providerId) {
    instanceOptions.value = []
    return
  }
  loadingInstances.value = true
  try {
    const res = await getAllInstances({ provider_id: providerId, page: 1, pageSize: 500 })
    instanceOptions.value = res?.data?.list || res?.data || []
  } catch {
    instanceOptions.value = []
  } finally {
    loadingInstances.value = false
  }
}

function handleScopeChange() {
  applyForm.target_ids = []
  instanceProviderFilter.value = null
  instanceOptions.value = []
}

function handleRuleSelectionChange(selection) {
  selectedRules.value = selection
}

function handleAppSelectionChange(selection) {
  selectedApps.value = selection
}

function resetRuleForm() {
  ruleForm.name = ''
  ruleForm.category = 'custom'
  ruleForm.description = ''
  ruleForm.stringsText = ''
  ruleForm.enabled = true
}

// Auto-fill preset strings when category changes during creation
watch(() => ruleForm.category, (newCategory) => {
  if (!isEdit.value && categoryPresets[newCategory]) {
    ruleForm.stringsText = categoryPresets[newCategory].join('\n')
  }
})

function handleCreateRule() {
  isEdit.value = false
  editId.value = null
  resetRuleForm()
  showRuleDialog.value = true
}

function handleEditRule(row) {
  isEdit.value = true
  editId.value = row.id
  ruleForm.name = row.name
  ruleForm.category = row.category
  ruleForm.description = row.description
  ruleForm.stringsText = parseStrings(row.strings).join('\n')
  ruleForm.enabled = row.enabled
  showRuleDialog.value = true
}

async function handleSubmitRule() {
  try {
    await ruleFormRef.value.validate()
  } catch { return }

  const strings = ruleForm.stringsText.split('\n').map(s => s.trim()).filter(Boolean)
  const data = {
    name: ruleForm.name,
    category: ruleForm.category,
    description: ruleForm.description,
    strings,
    enabled: ruleForm.enabled
  }

  submitting.value = true
  try {
    if (isEdit.value) {
      await blockRulesApi.updateRule(editId.value, data)
      ElMessage.success(t('admin.blockRules.updateSuccess'))
    } else {
      await blockRulesApi.createRule(data)
      ElMessage.success(t('admin.blockRules.createSuccess'))
    }
    showRuleDialog.value = false
    fetchRules()
  } catch (err) {
    ElMessage.error(err.message || err.response?.data?.msg || t('common.operationFailed'))
  } finally {
    submitting.value = false
  }
}

async function handleDeleteRule(row) {
  try {
    await ElMessageBox.confirm(t('admin.blockRules.confirmDelete'), {
      type: 'warning'
    })
    await blockRulesApi.deleteRule(row.id)
    ElMessage.success(t('admin.blockRules.deleteSuccess'))
    fetchRules()
    fetchApplications()
  } catch { /* cancelled */ }
}

async function handleToggleEnabled(row, val) {
  try {
    await blockRulesApi.updateRule(row.id, { enabled: val })
    row.enabled = val
  } catch (err) {
    ElMessage.error(err.message || err.response?.data?.msg || t('common.operationFailed'))
  }
}

async function handleApplyRules() {
  if (applyForm.scope !== 'global' && applyForm.target_ids.length === 0) {
    ElMessage.warning(t('admin.blockRules.selectTargets'))
    return
  }
  submitting.value = true
  try {
    await blockRulesApi.applyRules({
      rule_ids: selectedRules.value.map(r => r.id),
      scope: applyForm.scope,
      target_ids: applyForm.scope === 'global' ? [] : applyForm.target_ids,
      ip_version: applyForm.ip_version
    })
    ElMessage.success(t('admin.blockRules.applySuccess'))
    showApplyDialog.value = false
    fetchApplications()
  } catch (err) {
    ElMessage.error(err.message || err.response?.data?.msg || t('common.operationFailed'))
  } finally {
    submitting.value = false
  }
}

async function handleRemoveApplications() {
  try {
    await ElMessageBox.confirm(t('admin.blockRules.confirmRemove'), {
      type: 'warning'
    })
    await blockRulesApi.removeApplications({
      application_ids: selectedApps.value.map(a => a.id)
    })
    ElMessage.success(t('admin.blockRules.removeSuccess'))
    fetchApplications()
  } catch { /* cancelled */ }
}

onMounted(() => {
  fetchRules()
  fetchApplications()
  fetchAgentProviders()
})
</script>

<style scoped>
.block-rules-container {
  padding: 20px;
}
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
</style>
