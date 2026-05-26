// composables/useAnnouncementManagement.js - announcements/index.vue 的逻辑
import { ref, reactive, nextTick, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { getAnnouncements, createAnnouncement, updateAnnouncement, deleteAnnouncement, batchDeleteAnnouncements, batchUpdateAnnouncementStatus } from '@/api/admin'

export function useAnnouncementManagement() {
  const { t, locale } = useI18n()

  const announcements = ref([])
  const showAddDialog = ref(false)
  const loading = ref(false)
  const submitting = ref(false)
  const isEditing = ref(false)
  const formRef = ref()

  // 批量操作相关
  const selectedRows = ref([])
  const batchUpdating = ref(false)

  // 筛选条件
  const filterType = ref('')
  const filterStatus = ref(null)  // 改为null初始值
  const filterTitle = ref('')

  // 时间字段
  const startTime = ref('')
  const endTime = ref('')

  // 表单数据
  const form = ref({
    id: null,
    title: '',
    content: '',
    type: 'homepage',
    priority: 0,
    isSticky: false,
    status: 1
  })

  // 表单验证规则
  const rules = reactive({
    title: [
      { required: true, message: t('admin.announcements.titleRequired'), trigger: 'blur' }
    ],
    type: [
      { required: true, message: t('admin.announcements.typeRequired'), trigger: 'change' }
    ],
    content: [
      { required: true, message: t('admin.announcements.contentRequired'), trigger: 'blur' }
    ]
  })

  // 富文本编辑器配置
  const editorOptions = {
    placeholder: t('admin.announcements.editorPlaceholder'),
    modules: {
      toolbar: [
        ['bold', 'italic', 'underline', 'strike'],
        ['blockquote', 'code-block'],
        [{ 'header': 1 }, { 'header': 2 }],
        [{ 'list': 'ordered'}, { 'list': 'bullet' }],
        [{ 'script': 'sub'}, { 'script': 'super' }],
        [{ 'indent': '-1'}, { 'indent': '+1' }],
        [{ 'direction': 'rtl' }],
        [{ 'size': ['small', false, 'large', 'huge'] }],
        [{ 'header': [1, 2, 3, 4, 5, 6, false] }],
        [{ 'color': [] }, { 'background': [] }],
        [{ 'font': [] }],
        [{ 'align': [] }],
        ['clean'],
        ['link', 'image']
      ]
    }
  }

  // 格式化日期
  const formatDate = (dateString) => {
    return new Date(dateString).toLocaleString(locale.value)
  }

  // 处理富文本内容变化
  const handleContentChange = (content) => {
    form.value.content = content
  }

  // 加载公告列表
  const loadAnnouncements = async () => {
    loading.value = true
    try {
      const params = {
        page: 1,
        pageSize: 50  // 设置较大的pageSize以显示更多公告
      }
      
      // 类型过滤
      if (filterType.value) {
        params.type = filterType.value
      }
      
      // 状态过滤 - 逻辑：只有当明确选择了状态值时才传递参数
      if (filterStatus.value !== null && filterStatus.value !== undefined) {
        params.status = filterStatus.value
      }
      // 不传递status参数时，后端会获取所有状态的数据
      
      // 标题搜索
      if (filterTitle.value) {
        params.title = filterTitle.value
      }
      
      const response = await getAnnouncements(params)
      announcements.value = response.data.list || []
    } catch (error) {
      ElMessage.error(t('admin.announcements.loadAnnouncementsFailed'))
      console.error('加载公告列表失败:', error)
    } finally {
      loading.value = false
    }
  }

  // 公告
  const addAnnouncement = () => {
    // 先重置表单，确保清空之前的数据
    resetForm()
    // 确保富文本编辑器内容被清空
    form.value.content = ''
    isEditing.value = false
    showAddDialog.value = true
    
    // 下一个tick确保DOM更新后再清空验证状态
    nextTick(() => {
      if (formRef.value) {
        formRef.value.clearValidate()
      }
    })
  }

  // 编辑公告
  const editAnnouncement = (announcement) => {
    form.value = { 
      id: announcement.id,
      title: announcement.title,
      content: announcement.contentHtml || announcement.content,
      type: announcement.type,
      priority: announcement.priority,
      isSticky: announcement.isSticky,
      status: announcement.status
    }
    
    // 设置时间
    startTime.value = announcement.startTime ? new Date(announcement.startTime).toISOString().slice(0, 19).replace('T', ' ') : ''
    endTime.value = announcement.endTime ? new Date(announcement.endTime).toISOString().slice(0, 19).replace('T', ' ') : ''
    
    isEditing.value = true
    showAddDialog.value = true
  }

  // 删除公告
  const deleteAnnouncementHandler = async (id) => {
    try {
      await ElMessageBox.confirm(t('admin.announcements.confirmDelete'), t('admin.announcements.deleteTitle'), {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      })
      
      await deleteAnnouncement(id)
      ElMessage.success(t('message.deleteSuccess'))
      await loadAnnouncements()
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t('message.deleteFailed'))
      }
    }
  }

  // 保存公告
  const saveAnnouncement = async () => {
    if (!formRef.value) return
    
    try {
      await formRef.value.validate()
    } catch (error) {
      return
    }

    submitting.value = true
    try {
      const data = {
        title: form.value.title,
        content: form.value.content, // 这里既存储富文本也存储HTML
        contentHtml: form.value.content, // 富文本编辑器返回的就是HTML
        type: form.value.type,
        priority: form.value.priority,
        isSticky: form.value.isSticky
      }
      
      if (isEditing.value) {
        data.status = form.value.status
        if (startTime.value) data.startTime = startTime.value
        if (endTime.value) data.endTime = endTime.value
        
        await updateAnnouncement(form.value.id, data)
        ElMessage.success(t('message.updateSuccess'))
      } else {
        await createAnnouncement(data)
        ElMessage.success(t('message.createSuccess'))
      }
      
      showAddDialog.value = false
      await loadAnnouncements()
      // 确保对话框关闭后重置表单
      resetForm()
    } catch (error) {
      ElMessage.error(error?.message || (isEditing.value ? t('message.updateFailed') : t('message.createFailed')))
    } finally {
      submitting.value = false
    }
  }

  // 重置表单
  const resetForm = () => {
    form.value = { 
      id: null, 
      title: '', 
      content: '', 
      type: 'homepage',
      priority: 0,
      isSticky: false,
      status: 1
    }
    startTime.value = ''
    endTime.value = ''
    isEditing.value = false
    
    // 清空表单验证状态
    if (formRef.value) {
      formRef.value.clearValidate()
    }
  }

  // 关闭对话框
  const handleDialogClose = () => {
    // 重置表单数据
    resetForm()
    showAddDialog.value = false
  }

  // 选择变化处理
  const handleSelectionChange = (selection) => {
    selectedRows.value = selection
  }

  // 批量删除
  const handleBatchDelete = async () => {
    if (selectedRows.value.length === 0) {
      ElMessage.warning(t('admin.announcements.batchDelete'))
      return
    }
    
    try {
      await ElMessageBox.confirm(
        `${t('admin.announcements.confirmBatchDelete')} ${selectedRows.value.length} ${t('admin.announcements.items')}`,
        t('admin.announcements.batchDeleteTitle'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning',
        }
      )
      
      const ids = selectedRows.value.map(row => row.id)
      await batchDeleteAnnouncements(ids)
      ElMessage.success(t('message.deleteSuccess'))
      selectedRows.value = []
      await loadAnnouncements()
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t('message.deleteFailed'))
      }
    }
  }

  // 批量切换状态
  const handleBatchToggleStatus = async () => {
    if (selectedRows.value.length === 0) {
      ElMessage.warning(t('admin.announcements.batchToggleStatus'))
      return
    }

    // 确定统一的状态：如果选中的所有公告都是启用状态，则全部禁用；否则全部启用
    const allEnabled = selectedRows.value.every(row => row.status === 1)
    const newStatus = allEnabled ? 0 : 1
    const statusText = newStatus === 1 ? t('common.enable') : t('common.disable')
    
    try {
      await ElMessageBox.confirm(
        `${t('admin.announcements.confirmBatchToggle')} ${selectedRows.value.length} ${t('admin.announcements.items')}${statusText}${t('common.question')}`,
        t('admin.announcements.batchToggleTitle'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning',
        }
      )
      
      batchUpdating.value = true
      const ids = selectedRows.value.map(row => row.id)
      await batchUpdateAnnouncementStatus(ids, newStatus)
      ElMessage.success(t('admin.announcements.batchToggleSuccess'))
      selectedRows.value = []
      await loadAnnouncements()
    } catch (error) {
      console.error('批量状态切换失败:', error)
      if (error !== 'cancel') {
        ElMessage.error(t('admin.announcements.batchToggleFailed'))
      }
    } finally {
      batchUpdating.value = false
    }
  }

  // 切换单个公告状态
  const toggleAnnouncementStatus = async (announcement) => {
    const newStatus = announcement.status === 1 ? 0 : 1
    const statusText = newStatus === 1 ? t('common.enable') : t('common.disable')
    
    try {
      await ElMessageBox.confirm(
        `${t('admin.announcements.confirmToggle')}${statusText}${t('admin.announcements.thisAnnouncement')}${t('common.question')}`,
        t('admin.announcements.toggleStatusTitle'),
        {
          confirmButtonText: t('common.confirm'),
          cancelButtonText: t('common.cancel'),
          type: 'warning',
        }
      )
      
      await updateAnnouncement(announcement.id, { status: newStatus })
      ElMessage.success(t('admin.announcements.toggleSuccess'))
      await loadAnnouncements()
    } catch (error) {
      if (error !== 'cancel') {
        ElMessage.error(t('admin.announcements.toggleFailed'))
      }
    }
  }

  // 重置筛选条件
  const resetFilters = () => {
    filterType.value = ''
    filterStatus.value = null  // 改为null
    filterTitle.value = ''
    loadAnnouncements()
  }

  onMounted(() => {
    loadAnnouncements()
  })

  return {
    announcements,
    showAddDialog,
    loading,
    submitting,
    isEditing,
    formRef,
    selectedRows,
    batchUpdating,
    filterType,
    filterStatus,
    filterTitle,
    startTime,
    endTime,
    form,
    rules,
    editorOptions,
    formatDate,
    handleContentChange,
    loadAnnouncements,
    addAnnouncement,
    editAnnouncement,
    deleteAnnouncementHandler,
    saveAnnouncement,
    resetForm,
    handleDialogClose,
    handleSelectionChange,
    handleBatchDelete,
    handleBatchToggleStatus,
    toggleAnnouncementStatus,
    resetFilters
  }
}
