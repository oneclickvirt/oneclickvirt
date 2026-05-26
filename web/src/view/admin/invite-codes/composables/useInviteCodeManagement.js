import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { copyToClipboard } from '@/utils/clipboard'
import { getInviteCodes, createInviteCode, generateInviteCodes, deleteInviteCode, batchDeleteInviteCodes, exportInviteCodes } from '@/api/admin'

export function useInviteCodeManagement() {
  const { t } = useI18n()

  const inviteCodes = ref([])
  const loading = ref(false)
  const showCreateDialog = ref(false)
  const showGenerateDialog = ref(false)
  const showExportDialog = ref(false)
  const createLoading = ref(false)
  const generateLoading = ref(false)
  const createFormRef = ref()
  const generateFormRef = ref()
  const selectedCodes = ref([])
  const exportedCodes = ref('')

  // 筛选表单
  const filterForm = reactive({
    isUsed: null,
    status: 0
  })

  // 分页
  const currentPage = ref(1)
  const pageSize = ref(10)
  const total = ref(0)

  // 创建自定义邀请码表单
  const createForm = reactive({
    code: '',
    maxUses: 1,
    expiresAt: '',
    description: ''
  })

  // 创建表单验证规则
  const createRules = {
    code: [
      { required: true, message: t('admin.inviteCodes.codeRequired'), trigger: 'blur' },
      { min: 3, max: 50, message: t('admin.inviteCodes.codeLengthError'), trigger: 'blur' },
      { pattern: /^[0-9A-Z]+$/, message: t('admin.inviteCodes.codeFormatError'), trigger: 'blur' }
    ],
    maxUses: [
      { required: true, message: t('admin.inviteCodes.maxUsesRequired'), trigger: 'blur' }
    ]
  }

  // 生成邀请码表单
  const generateForm = reactive({
    count: 1,
    maxUses: 1,
    expiresAt: '',
    description: ''
  })

  // 表单验证规则
  const generateRules = {
    count: [
      { required: true, message: t('admin.inviteCodes.countRequired'), trigger: 'blur' }
    ],
    maxUses: [
      { required: true, message: t('admin.inviteCodes.maxUsesRequired'), trigger: 'blur' }
    ]
  }

  const loadInviteCodes = async () => {
    loading.value = true
    try {
      const params = {
        page: currentPage.value,
        pageSize: pageSize.value
      }

      if (filterForm.isUsed !== null) {
        params.isUsed = filterForm.isUsed
      }
      if (filterForm.status !== 0) {
        params.status = filterForm.status
      }

      const response = await getInviteCodes(params)
      inviteCodes.value = response.data.list || []
      total.value = response.data.total || 0
    } catch (error) {
      ElMessage.error(error?.message || t('admin.inviteCodes.loadFailed'))
    } finally {
      loading.value = false
    }
  }

  const handleFilterChange = () => {
    currentPage.value = 1
    loadInviteCodes()
  }

  const handleSelectionChange = (selection) => {
    selectedCodes.value = selection
  }

  const handleBatchExport = async () => {
    if (selectedCodes.value.length === 0) {
      ElMessage.warning(t('admin.inviteCodes.selectToExport'))
      return
    }

    try {
      const ids = selectedCodes.value.map(item => item.id)
      const response = await exportInviteCodes({ ids })
      exportedCodes.value = response.data.join('\n')
      showExportDialog.value = true
    } catch (error) {
      ElMessage.error(error?.message || t('admin.inviteCodes.exportFailed'))
    }
  }

  const handleBatchDelete = async () => {
    if (selectedCodes.value.length === 0) {
      ElMessage.warning(t('admin.inviteCodes.selectToDelete'))
      return
    }

    try {
      await ElMessageBox.confirm(
        t('admin.inviteCodes.batchDeleteConfirm', { count: selectedCodes.value.length }),
        t('admin.inviteCodes.batchDeleteTitle'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning',
        }
      )

      const ids = selectedCodes.value.map(item => item.id)
      await batchDeleteInviteCodes({ ids })
      ElMessage.success(t('admin.inviteCodes.batchDeleteSuccess'))
      await loadInviteCodes()
    } catch (error) {
      if (error !== 'cancel' && error?.action !== 'cancel' && error?.action !== 'close') {
        ElMessage.error(error?.message || t('admin.inviteCodes.batchDeleteFailed'))
      }
    }
  }

  const copyExportedCodes = async () => {
    if (!exportedCodes.value) {
      ElMessage.warning(t('admin.inviteCodes.nothingToCopy'))
      return
    }
    await copyToClipboard(exportedCodes.value, t('admin.inviteCodes.copiedToClipboard'))
  }

  const cancelCreate = () => {
    showCreateDialog.value = false
    createFormRef.value?.resetFields()
    Object.assign(createForm, {
      code: '',
      maxUses: 1,
      expiresAt: '',
      description: ''
    })
  }

  const handleCreateDialogClose = (done) => {
    const isFormDirty = !!(createForm.code || createForm.description)
    if (isFormDirty) {
      ElMessageBox.confirm(
        t('common.unsavedChangesConfirm'),
        t('common.unsavedChanges'),
        {
          confirmButtonText: t('common.discardChanges'),
          cancelButtonText: t('common.cancel'),
          type: 'warning'
        }
      ).then(() => {
        if (typeof done === 'function') done()
        cancelCreate()
      }).catch(() => {})
    } else {
      if (typeof done === 'function') done()
      cancelCreate()
    }
  }

  const submitCreate = async () => {
    try {
      await createFormRef.value.validate()
      createLoading.value = true

      const data = {
        code: createForm.code,
        count: 1,
        maxUses: createForm.maxUses,
        expiresAt: createForm.expiresAt || '',
        remark: createForm.description
      }

      await createInviteCode(data)
      ElMessage.success(t('admin.inviteCodes.createSuccess'))
      cancelCreate()
      await loadInviteCodes()
    } catch (error) {
      ElMessage.error(error?.message || t('admin.inviteCodes.createFailed'))
    } finally {
      createLoading.value = false
    }
  }

  const cancelGenerate = () => {
    showGenerateDialog.value = false
    generateFormRef.value?.resetFields()
    Object.assign(generateForm, {
      count: 1,
      maxUses: 1,
      expiresAt: '',
      description: ''
    })
  }

  const submitGenerate = async () => {
    try {
      await generateFormRef.value.validate()
      generateLoading.value = true

      const data = {
        count: generateForm.count,
        maxUses: generateForm.maxUses,
        expiresAt: generateForm.expiresAt || '',
        remark: generateForm.description
      }

      await generateInviteCodes(data)
      ElMessage.success(t('admin.inviteCodes.generateSuccess'))
      cancelGenerate()
      await loadInviteCodes()
    } catch (error) {
      ElMessage.error(error?.message || t('admin.inviteCodes.generateFailed'))
    } finally {
      generateLoading.value = false
    }
  }

  const deleteCode = async (id) => {
    try {
      await ElMessageBox.confirm(
        t('admin.inviteCodes.deleteConfirm'),
        t('admin.inviteCodes.deleteTitle'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning',
        }
      )

      await deleteInviteCode(id)
      ElMessage.success(t('admin.inviteCodes.deleteSuccess'))
      await loadInviteCodes()
    } catch (error) {
      if (error !== 'cancel' && error?.action !== 'cancel' && error?.action !== 'close') {
        ElMessage.error(error?.message || t('admin.inviteCodes.deleteFailed'))
      }
    }
  }

  const handleSizeChange = (newSize) => {
    pageSize.value = newSize
    currentPage.value = 1
    loadInviteCodes()
  }

  const handleCurrentChange = (newPage) => {
    currentPage.value = newPage
    loadInviteCodes()
  }

  onMounted(() => {
    loadInviteCodes()
  })

  return {
    inviteCodes,
    loading,
    showCreateDialog,
    showGenerateDialog,
    showExportDialog,
    createLoading,
    generateLoading,
    createFormRef,
    generateFormRef,
    selectedCodes,
    exportedCodes,
    filterForm,
    currentPage,
    pageSize,
    total,
    createForm,
    createRules,
    generateForm,
    generateRules,
    loadInviteCodes,
    handleFilterChange,
    handleSelectionChange,
    handleBatchExport,
    handleBatchDelete,
    copyExportedCodes,
    cancelCreate,
    handleCreateDialogClose,
    submitCreate,
    cancelGenerate,
    submitGenerate,
    deleteCode,
    handleSizeChange,
    handleCurrentChange
  }
}
