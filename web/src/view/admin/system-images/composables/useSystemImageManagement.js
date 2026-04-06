import { ref, reactive, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { systemImageApi } from '@/api/admin'
import { 
  getOperatingSystemsByCategory, 
  getDisplayName 
} from '@/utils/operating-systems'

export function useSystemImageManagement() {
  const { t, locale } = useI18n()

  const loading = ref(false)
  const submitting = ref(false)
  const dialogVisible = ref(false)
  const selectedRows = ref([])
  const tableData = ref([])

  const searchForm = reactive({ search: '', providerType: '', instanceType: '', architecture: '', osType: '', status: '' })
  const pagination = reactive({ page: 1, pageSize: 10, total: 0 })

  const form = reactive({
    name: '', providerType: '', instanceType: '', architecture: '',
    url: '', checksum: '', size: null, description: '',
    osType: '', osVersion: '', tags: '',
    minMemoryMB: null, minDiskMB: null, useCdn: true
  })

  const formRef = ref()
  const isEdit = ref(false)
  const editId = ref(null)
  const groupedOperatingSystems = ref(getOperatingSystemsByCategory())

  const dialogTitle = computed(() => isEdit.value ? t('admin.systemImages.editImage') : t('admin.systemImages.addImage'))

  const rules = {
    name: [{ required: true, message: t('admin.systemImages.imageNameRequired'), trigger: 'blur' }],
    providerType: [{ required: true, message: t('admin.systemImages.providerTypeRequired'), trigger: 'change' }],
    instanceType: [{ required: true, message: t('admin.systemImages.instanceTypeRequired'), trigger: 'change' }],
    architecture: [{ required: true, message: t('admin.systemImages.architectureRequired'), trigger: 'change' }],
    url: [
      { required: true, message: t('admin.systemImages.urlRequired'), trigger: 'blur' },
      { type: 'url', message: t('admin.systemImages.urlInvalid'), trigger: 'blur' }
    ],
    minMemoryMB: [
      { required: true, message: t('admin.systemImages.minMemoryRequired'), trigger: 'blur' },
      { type: 'number', min: 1, message: t('admin.systemImages.minMemoryInvalid'), trigger: 'blur' }
    ],
    minDiskMB: [
      { required: true, message: t('admin.systemImages.minDiskRequired'), trigger: 'blur' },
      { type: 'number', min: 1, message: t('admin.systemImages.minDiskInvalid'), trigger: 'blur' }
    ]
  }

  const fetchData = async () => {
    loading.value = true
    try {
      const params = { page: pagination.page, pageSize: pagination.pageSize, ...searchForm }
      const response = await systemImageApi.getList(params)
      if (response.code === 0 || response.code === 200) {
        tableData.value = response.data.list || []
        pagination.total = response.data.total || 0
      }
    } catch (error) {
      ElMessage.error(t('admin.systemImages.loadFailed') + ': ' + error.message)
    } finally { loading.value = false }
  }

  const handleSearch = () => { pagination.page = 1; fetchData() }
  const handleReset = () => { Object.assign(searchForm, { search: '', providerType: '', instanceType: '', architecture: '', osType: '', status: '' }); handleSearch() }
  const handleSelectionChange = (selection) => { selectedRows.value = selection }

  const handleCreate = () => { isEdit.value = false; editId.value = null; resetForm(); dialogVisible.value = true }

  const handleEdit = (row) => {
    isEdit.value = true; editId.value = row.id
    Object.assign(form, {
      name: row.name, providerType: row.providerType, instanceType: row.instanceType,
      architecture: row.architecture, url: row.url, checksum: row.checksum || '',
      size: row.size || null, description: row.description || '',
      osType: row.osType || '', osVersion: row.osVersion || '', tags: row.tags || '',
      minMemoryMB: row.minMemoryMB || null, minDiskMB: row.minDiskMB || null,
      useCdn: row.useCdn !== undefined ? row.useCdn : true
    })
    dialogVisible.value = true
  }

  const handleSubmit = async () => {
    if (!formRef.value) return
    try {
      await formRef.value.validate()
      submitting.value = true
      const data = { ...form }
      if (isEdit.value) { await systemImageApi.update(editId.value, data); ElMessage.success(t('admin.systemImages.updateSuccess')) }
      else { await systemImageApi.create(data); ElMessage.success(t('admin.systemImages.createSuccess')) }
      dialogVisible.value = false; fetchData()
    } catch (error) { if (error.message) ElMessage.error(error.message || t('common.operationFailed')) }
    finally { submitting.value = false }
  }

  const handleDelete = async (row) => {
    try {
      await ElMessageBox.confirm(t('admin.systemImages.deleteConfirm', { name: row.name }), t('admin.systemImages.warning'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      await systemImageApi.delete(row.id); ElMessage.success(t('admin.systemImages.deleteSuccess')); fetchData()
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.systemImages.deleteFailed') + ': ' + error.message) }
  }

  const handleToggleStatus = async (row) => {
    const newStatus = row.status === 'active' ? 'inactive' : 'active'
    const action = newStatus === 'active' ? t('admin.systemImages.activate') : t('common.disable')
    try {
      await ElMessageBox.confirm(t('admin.systemImages.toggleStatusConfirm', { action, name: row.name }), t('admin.systemImages.confirm'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      await systemImageApi.update(row.id, { status: newStatus }); ElMessage.success(t('admin.systemImages.toggleStatusSuccess', { action })); fetchData()
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.systemImages.toggleStatusFailed', { action }) + ': ' + error.message) }
  }

  const handleBatchDelete = async () => {
    try {
      await ElMessageBox.confirm(t('admin.systemImages.batchDeleteConfirm', { count: selectedRows.value.length }), t('admin.systemImages.warning'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      const ids = selectedRows.value.map(row => row.id)
      await systemImageApi.batchDelete({ ids }); ElMessage.success(t('admin.systemImages.batchDeleteSuccess')); selectedRows.value = []; fetchData()
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.systemImages.batchDeleteFailed') + ': ' + error.message) }
  }

  const handleBatchStatus = async (status) => {
    const action = status === 'active' ? t('admin.systemImages.activate') : t('common.disable')
    try {
      await ElMessageBox.confirm(t('admin.systemImages.batchStatusConfirm', { action, count: selectedRows.value.length }), t('admin.systemImages.confirm'), { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' })
      const ids = selectedRows.value.map(row => row.id)
      await systemImageApi.batchUpdateStatus({ ids, status }); ElMessage.success(t('admin.systemImages.batchStatusSuccess', { action })); selectedRows.value = []; fetchData()
    } catch (error) { if (error !== 'cancel') ElMessage.error(t('admin.systemImages.batchStatusFailed', { action }) + ': ' + error.message) }
  }

  const handleSizeChange = (size) => { pagination.pageSize = size; pagination.page = 1; fetchData() }
  const handleCurrentChange = (page) => { pagination.page = page; fetchData() }
  const handleDialogClose = () => { dialogVisible.value = false; resetForm() }

  const resetForm = () => {
    if (formRef.value) formRef.value.resetFields()
    Object.assign(form, {
      name: '', providerType: '', instanceType: '', architecture: '',
      url: '', checksum: '', size: null, description: '',
      osType: '', osVersion: '', tags: '',
      minMemoryMB: null, minDiskMB: null, useCdn: true
    })
  }

  const handleProviderTypeChange = () => {
    if (['docker', 'podman', 'containerd'].includes(form.providerType) && form.instanceType === 'vm') form.instanceType = ''
    if (['qemu', 'kubevirt'].includes(form.providerType) && form.instanceType === 'container') form.instanceType = ''
  }

  const handleInstanceTypeChange = () => {}
  const handleOsTypeChange = () => { form.osVersion = '' }

  const getUrlHint = () => {
    if (!form.providerType || !form.instanceType) return ''
    if (form.providerType === 'proxmox' && form.instanceType === 'vm') return t('admin.systemImages.urlHintProxmoxVM')
    if (form.providerType === 'lxd' || form.providerType === 'incus') return t('admin.systemImages.urlHintLxdIncus')
    if (['docker', 'podman', 'containerd'].includes(form.providerType) && form.instanceType === 'container') return t('admin.systemImages.urlHintContainerTarGz', { provider: form.providerType.charAt(0).toUpperCase() + form.providerType.slice(1) })
    return ''
  }

  const getProviderTypeName = (type) => {
    const names = { proxmox: 'ProxmoxVE', lxd: 'LXD', incus: 'Incus', docker: 'Docker', podman: 'Podman', containerd: 'Containerd', qemu: 'QEMU/KVM', kubevirt: 'KubeVirt' }
    return names[type] || type
  }

  const getProviderTypeColor = (type) => {
    const colors = { proxmox: 'primary', lxd: 'success', incus: 'warning', docker: 'info', podman: 'info', containerd: 'info', qemu: 'danger', kubevirt: 'danger' }
    return colors[type] || ''
  }

  const truncateUrl = (url) => { if (!url) return ''; return url.length > 50 ? url.substring(0, 50) + '...' : url }

  const formatFileSize = (bytes) => {
    if (!bytes || bytes === 0) return '-'
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(1024))
    return Math.round(bytes / Math.pow(1024, i) * 100) / 100 + ' ' + sizes[i]
  }

  const formatDateTime = (dateTime) => {
    if (!dateTime) return '-'
    return new Date(dateTime).toLocaleString(locale.value)
  }

  return {
    loading, submitting, dialogVisible, selectedRows, tableData,
    searchForm, pagination, form, formRef, isEdit, editId,
    groupedOperatingSystems, dialogTitle, rules,
    fetchData, handleSearch, handleReset, handleSelectionChange,
    handleCreate, handleEdit, handleSubmit, handleDelete,
    handleToggleStatus, handleBatchDelete, handleBatchStatus,
    handleSizeChange, handleCurrentChange, handleDialogClose,
    handleProviderTypeChange, handleInstanceTypeChange, handleOsTypeChange,
    getUrlHint, getProviderTypeName, getProviderTypeColor,
    truncateUrl, formatFileSize, formatDateTime,
    getDisplayName,
    t
  }
}
