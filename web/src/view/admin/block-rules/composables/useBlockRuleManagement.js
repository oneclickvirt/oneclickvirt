import { ref, reactive, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { blockRulesApi, getAllInstances } from '@/api/admin'

export function useBlockRuleManagement() {
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
    } catch (err) {
      ElMessage.error(err?.message || t('common.loadFailed'))
    } finally {
      loadingRules.value = false
    }
  }

  async function fetchApplications() {
    loadingApps.value = true
    try {
      const res = await blockRulesApi.getApplications()
      applications.value = res?.data || []
    } catch (err) {
      ElMessage.error(err?.message || t('common.loadFailed'))
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
      ElMessage.error(err?.message || t('common.operationFailed'))
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
    } catch (err) {
      if (err === 'cancel' || err?.action === 'cancel' || err?.action === 'close') return
      ElMessage.error(err?.message || t('common.operationFailed'))
    }
  }

  async function handleToggleEnabled(row, val) {
    try {
      await blockRulesApi.updateRule(row.id, { enabled: val })
      row.enabled = val
    } catch (err) {
      ElMessage.error(err?.message || t('common.operationFailed'))
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
      ElMessage.error(err?.message || t('common.operationFailed'))
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
    } catch (err) {
      if (err === 'cancel' || err?.action === 'cancel' || err?.action === 'close') return
      ElMessage.error(err?.message || t('common.operationFailed'))
    }
  }

  onMounted(() => {
    fetchRules()
    fetchApplications()
    fetchAgentProviders()
  })

  return {
    rules,
    applications,
    providerOptions,
    instanceOptions,
    selectedRules,
    selectedApps,
    loadingRules,
    loadingApps,
    loadingInstances,
    submitting,
    showRuleDialog,
    showApplyDialog,
    isEdit,
    editId,
    ruleFormRef,
    applyFormRef,
    instanceProviderFilter,
    categories,
    scopeOptions,
    ipVersionOptions,
    categoryPresets,
    ruleForm,
    applyForm,
    ruleFormRules,
    applyFormRules,
    parseStrings,
    formatDate,
    categoryTagType,
    statusTagType,
    fetchRules,
    fetchApplications,
    fetchAgentProviders,
    fetchInstancesForProvider,
    handleScopeChange,
    handleRuleSelectionChange,
    handleAppSelectionChange,
    resetRuleForm,
    handleCreateRule,
    handleEditRule,
    handleSubmitRule,
    handleDeleteRule,
    handleToggleEnabled,
    handleApplyRules,
    handleRemoveApplications
  }
}
