<template>
  <div class="announcements-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>公告管理</span>
          <div class="header-actions">
            <el-button 
              v-if="selectedRows.length > 0" 
              type="danger" 
              :disabled="selectedRows.length === 0"
              @click="handleBatchDelete"
            >
              批量删除 ({{ selectedRows.length }})
            </el-button>
            <el-button 
              v-if="selectedRows.length > 0" 
              type="warning" 
              :disabled="selectedRows.length === 0"
              :loading="batchUpdating"
              @click="handleBatchToggleStatus"
            >
              批量切换状态 ({{ selectedRows.length }})
            </el-button>
            <el-button
              type="primary"
              @click="addAnnouncement"
            >
              添加公告
            </el-button>
          </div>
        </div>
      </template>
      
      <!-- 筛选条件 -->
      <div class="filter-container">
        <el-row
          :gutter="20"
          style="margin-bottom: 20px;"
        >
          <el-col :span="6">
            <el-select
              v-model="filterType"
              placeholder="选择公告类型"
              clearable
              @change="loadAnnouncements"
            >
              <el-option
                label="全部"
                value=""
              />
              <el-option
                label="首页公告"
                value="homepage"
              />
              <el-option
                label="顶部栏公告"
                value="topbar"
              />
            </el-select>
          </el-col>
          <el-col :span="6">
            <el-select
              v-model="filterStatus"
              placeholder="选择状态"
              clearable
              @change="loadAnnouncements"
            >
              <el-option
                label="全部"
                :value="null"
              />
              <el-option
                label="启用"
                :value="1"
              />
              <el-option
                label="禁用"
                :value="0"
              />
            </el-select>
          </el-col>
          <el-col :span="6">
            <el-input 
              v-model="filterTitle" 
              placeholder="搜索标题" 
              clearable 
              @clear="loadAnnouncements"
              @keyup.enter="loadAnnouncements"
            >
              <template #append>
                <el-button
                  icon="Search"
                  @click="loadAnnouncements"
                />
              </template>
            </el-input>
          </el-col>
          <el-col :span="6">
            <el-button @click="resetFilters">
              重置筛选
            </el-button>
          </el-col>
        </el-row>
      </div>
      
      <el-table 
        v-loading="loading" 
        :data="announcements" 
        style="width: 100%"
        @selection-change="handleSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
        />
        <el-table-column
          prop="title"
          label="标题"
          width="200"
        />
        <el-table-column
          prop="type"
          label="类型"
          width="120"
        >
          <template #default="scope">
            <el-tag :type="scope.row.type === 'homepage' ? 'success' : 'warning'">
              {{ scope.row.type === 'homepage' ? '首页公告' : '顶部栏公告' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="priority"
          label="优先级"
          width="80"
        />
        <el-table-column
          prop="isSticky"
          label="置顶"
          width="80"
        >
          <template #default="scope">
            <el-tag
              :type="scope.row.isSticky ? 'danger' : 'info'"
              size="small"
            >
              {{ scope.row.isSticky ? '是' : '否' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          label="状态"
          width="80"
        >
          <template #default="scope">
            <el-tag
              :type="scope.row.status === 1 ? 'success' : 'danger'"
              size="small"
            >
              {{ scope.row.status === 1 ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="content"
          label="内容"
          show-overflow-tooltip
        />
        <el-table-column
          prop="createdAt"
          label="创建时间"
          width="160"
        >
          <template #default="scope">
            {{ formatDate(scope.row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          label="操作"
          width="200"
          fixed="right"
        >
          <template #default="scope">
            <el-button
              size="small"
              @click="editAnnouncement(scope.row)"
            >
              编辑
            </el-button>
            <el-button 
              size="small" 
              :type="scope.row.status === 1 ? 'warning' : 'success'"
              @click="toggleAnnouncementStatus(scope.row)"
            >
              {{ scope.row.status === 1 ? '禁用' : '启用' }}
            </el-button>
            <el-button
              size="small"
              type="danger"
              @click="deleteAnnouncementHandler(scope.row.id)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 添加/编辑公告对话框 -->
    <el-dialog 
      v-model="showAddDialog" 
      :title="isEditing ? '编辑公告' : '添加公告'" 
      width="800px"
      :append-to-body="true"
      class="announcement-dialog"
      :close-on-click-modal="false"
      :close-on-press-escape="false"
      @close="handleDialogClose"
    >
      <el-form
        ref="formRef"
        :model="form"
        label-width="100px"
        :rules="rules"
      >
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              label="公告标题"
              prop="title"
            >
              <el-input
                v-model="form.title"
                placeholder="请输入公告标题"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              label="公告类型"
              prop="type"
            >
              <el-select
                v-model="form.type"
                placeholder="请选择公告类型"
                style="width: 100%"
              >
                <el-option
                  label="首页公告"
                  value="homepage"
                />
                <el-option
                  label="顶部栏公告"
                  value="topbar"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        
        <el-row :gutter="20">
          <el-col :span="8">
            <el-form-item label="优先级">
              <el-input-number
                v-model="form.priority"
                :min="0"
                :max="100"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item label="是否置顶">
              <el-switch v-model="form.isSticky" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item
              v-if="isEditing"
              label="状态"
            >
              <el-select
                v-model="form.status"
                style="width: 100%"
              >
                <el-option
                  label="启用"
                  :value="1"
                />
                <el-option
                  label="禁用"
                  :value="0"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        
        <el-form-item
          label="公告内容"
          prop="content"
        >
          <QuillEditor
            v-model:content="form.content"
            content-type="html"
            theme="snow"
            style="height: 300px;"
            :options="editorOptions"
            @update:content="handleContentChange"
          />
        </el-form-item>
        
        <el-row
          v-if="isEditing"
          :gutter="20"
        >
          <el-col :span="12">
            <el-form-item label="开始时间">
              <el-date-picker
                v-model="startTime"
                type="datetime"
                placeholder="选择开始时间"
                format="YYYY-MM-DD HH:mm:ss"
                value-format="YYYY-MM-DD HH:mm:ss"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="结束时间">
              <el-date-picker
                v-model="endTime"
                type="datetime"
                placeholder="选择结束时间"
                format="YYYY-MM-DD HH:mm:ss"
                value-format="YYYY-MM-DD HH:mm:ss"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
        </el-row>
      </el-form>
      
      <template #footer>
        <el-button @click="handleDialogClose">
          取消
        </el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="saveAnnouncement"
        >
          {{ isEditing ? '更新' : '保存' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted, reactive, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getAnnouncements, createAnnouncement, updateAnnouncement, deleteAnnouncement, batchDeleteAnnouncements, batchUpdateAnnouncementStatus } from '@/api/admin'
import { QuillEditor } from '@vueup/vue-quill'
import '@vueup/vue-quill/dist/vue-quill.snow.css'

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
    { required: true, message: '请输入公告标题', trigger: 'blur' }
  ],
  type: [
    { required: true, message: '请选择公告类型', trigger: 'change' }
  ],
  content: [
    { required: true, message: '请输入公告内容', trigger: 'blur' }
  ]
})

// 富文本编辑器配置
const editorOptions = {
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
  },
  placeholder: '请输入公告内容...'
}

// 格式化日期
const formatDate = (dateString) => {
  return new Date(dateString).toLocaleString('zh-CN')
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
    
    // 状态过滤 - 修复逻辑：只有当明确选择了状态值时才传递参数
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
    ElMessage.error('加载公告列表失败')
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
    await ElMessageBox.confirm('确定删除这条公告吗？', '删除公告', {
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      type: 'warning',
    })
    
    await deleteAnnouncement(id)
    ElMessage.success('删除成功')
    await loadAnnouncements()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('删除失败')
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
      ElMessage.success('更新成功')
    } else {
      await createAnnouncement(data)
      ElMessage.success('创建成功')
    }
    
    showAddDialog.value = false
    await loadAnnouncements()
    // 确保对话框关闭后重置表单
    resetForm()
  } catch (error) {
    ElMessage.error(isEditing.value ? '更新失败' : '创建失败')
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
    ElMessage.warning('请选择要删除的公告')
    return
  }
  
  try {
    await ElMessageBox.confirm(
      `确定删除选中的 ${selectedRows.value.length} 条公告吗？`,
      '批量删除公告',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    const ids = selectedRows.value.map(row => row.id)
    await batchDeleteAnnouncements(ids)
    ElMessage.success('批量删除成功')
    selectedRows.value = []
    await loadAnnouncements()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('批量删除失败')
    }
  }
}

// 批量切换状态
const handleBatchToggleStatus = async () => {
  if (selectedRows.value.length === 0) {
    ElMessage.warning('请选择要切换状态的公告')
    return
  }

  // 确定统一的状态：如果选中的所有公告都是启用状态，则全部禁用；否则全部启用
  const allEnabled = selectedRows.value.every(row => row.status === 1)
  const newStatus = allEnabled ? 0 : 1
  const statusText = newStatus === 1 ? '启用' : '禁用'
  
  try {
    await ElMessageBox.confirm(
      `确定将选中的 ${selectedRows.value.length} 条公告${statusText}吗？`,
      '批量状态切换',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    batchUpdating.value = true
    const ids = selectedRows.value.map(row => row.id)
    await batchUpdateAnnouncementStatus(ids, newStatus)
    ElMessage.success(`批量${statusText}成功`)
    selectedRows.value = []
    await loadAnnouncements()
  } catch (error) {
    console.error('批量状态切换失败:', error)
    if (error !== 'cancel') {
      ElMessage.error(`批量${statusText}失败`)
    }
  } finally {
    batchUpdating.value = false
  }
}

// 切换单个公告状态
const toggleAnnouncementStatus = async (announcement) => {
  const newStatus = announcement.status === 1 ? 0 : 1
  const statusText = newStatus === 1 ? '启用' : '禁用'
  
  try {
    await ElMessageBox.confirm(
      `确定${statusText}这条公告吗？`,
      '状态切换',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    await updateAnnouncement(announcement.id, { status: newStatus })
    ElMessage.success(`${statusText}成功`)
    await loadAnnouncements()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(`${statusText}失败`)
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
</script>

<style scoped>
.announcements-container {
  padding: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.filter-container {
  margin-bottom: 20px;
}

:deep(.ql-editor) {
  min-height: 200px;
}

:deep(.ql-container) {
  font-size: 14px;
}

/* 确保对话框宽度固定 */
:deep(.announcement-dialog) {
  width: 1200px !important;
  max-width: 90vw;
}

:deep(.announcement-dialog .el-dialog) {
  width: 1200px !important;
  max-width: 90vw;
}
</style>