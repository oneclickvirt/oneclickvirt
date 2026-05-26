<template>
  <div class="announcements-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.announcements.title') }}</span>
          <div class="header-actions">
            <el-button 
              v-if="selectedRows.length > 0" 
              type="danger" 
              :disabled="selectedRows.length === 0"
              @click="handleBatchDelete"
            >
              {{ $t('admin.announcements.batchDelete') }} ({{ selectedRows.length }})
            </el-button>
            <el-button 
              v-if="selectedRows.length > 0" 
              type="warning" 
              :disabled="selectedRows.length === 0"
              :loading="batchUpdating"
              @click="handleBatchToggleStatus"
            >
              {{ $t('admin.announcements.batchToggleStatus') }} ({{ selectedRows.length }})
            </el-button>
            <el-button
              type="primary"
              @click="addAnnouncement"
            >
              {{ $t('admin.announcements.addAnnouncement') }}
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
              :placeholder="$t('admin.announcements.selectType')"
              clearable
              @change="loadAnnouncements"
            >
              <el-option
                :label="$t('admin.announcements.all')"
                value=""
              />
              <el-option
                :label="$t('admin.announcements.homepageAnnouncement')"
                value="homepage"
              />
              <el-option
                :label="$t('admin.announcements.topbarAnnouncement')"
                value="topbar"
              />
            </el-select>
          </el-col>
          <el-col :span="6">
            <el-select
              v-model="filterStatus"
              :placeholder="$t('admin.announcements.selectStatus')"
              clearable
              @change="loadAnnouncements"
            >
              <el-option
                :label="$t('admin.announcements.all')"
                :value="null"
              />
              <el-option
                :label="$t('common.enabled')"
                :value="1"
              />
              <el-option
                :label="$t('common.disabled')"
                :value="0"
              />
            </el-select>
          </el-col>
          <el-col :span="6">
            <el-input 
              v-model="filterTitle" 
              :placeholder="$t('admin.announcements.searchTitle')" 
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
              {{ $t('admin.announcements.resetFilters') }}
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
          :label="$t('admin.announcements.announcementTitle')"
          width="200"
        />
        <el-table-column
          prop="type"
          :label="$t('common.name')"
          width="120"
        >
          <template #default="scope">
            <el-tag :type="scope.row.type === 'homepage' ? 'success' : 'warning'">
              {{ scope.row.type === 'homepage' ? $t('admin.announcements.homepage') : $t('admin.announcements.topbar') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="priority"
          :label="$t('admin.announcements.priority')"
          width="80"
        />
        <el-table-column
          prop="isSticky"
          :label="$t('admin.announcements.isSticky')"
          width="80"
        >
          <template #default="scope">
            <el-tag
              :type="scope.row.isSticky ? 'danger' : 'info'"
              size="small"
            >
              {{ scope.row.isSticky ? $t('common.yes') : $t('common.no') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          :label="$t('common.status')"
          width="80"
        >
          <template #default="scope">
            <el-tag
              :type="scope.row.status === 1 ? 'success' : 'danger'"
              size="small"
            >
              {{ scope.row.status === 1 ? $t('common.enabled') : $t('common.disabled') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="content"
          :label="$t('admin.announcements.content')"
          show-overflow-tooltip
        />
        <el-table-column
          prop="createdAt"
          :label="$t('common.createTime')"
          width="160"
        >
          <template #default="scope">
            {{ formatDate(scope.row.createdAt) }}
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="200"
          fixed="right"
        >
          <template #default="scope">
            <el-button
              size="small"
              @click="editAnnouncement(scope.row)"
            >
              {{ $t('common.edit') }}
            </el-button>
            <el-button 
              size="small" 
              :type="scope.row.status === 1 ? 'warning' : 'success'"
              @click="toggleAnnouncementStatus(scope.row)"
            >
              {{ scope.row.status === 1 ? $t('common.disable') : $t('common.enable') }}
            </el-button>
            <el-button
              size="small"
              type="danger"
              @click="deleteAnnouncementHandler(scope.row.id)"
            >
              {{ $t('common.delete') }}
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 添加/编辑公告对话框 -->
    <el-dialog 
      v-model="showAddDialog" 
      :title="isEditing ? $t('admin.announcements.editAnnouncement') : $t('admin.announcements.addAnnouncement')" 
      width="1200px"
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
              :label="$t('admin.announcements.title')"
              prop="title"
            >
              <el-input
                v-model="form.title"
                :placeholder="$t('admin.announcements.titlePlaceholder')"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.announcements.type')"
              prop="type"
            >
              <el-select
                v-model="form.type"
                :placeholder="$t('admin.announcements.typePlaceholder')"
                style="width: 100%"
              >
                <el-option
                  :label="$t('admin.announcements.typeHomepage')"
                  value="homepage"
                />
                <el-option
                  :label="$t('admin.announcements.typeTopbar')"
                  value="topbar"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        
        <el-row :gutter="20">
          <el-col :span="8">
            <el-form-item :label="$t('admin.announcements.priority')">
              <el-input-number
                v-model="form.priority"
                :min="0"
                :max="100"
                :controls="false"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item :label="$t('admin.announcements.isSticky')">
              <el-switch v-model="form.isSticky" />
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item
              v-if="isEditing"
              :label="$t('common.status')"
            >
              <el-select
                v-model="form.status"
                style="width: 100%"
              >
                <el-option
                  :label="$t('common.enabled')"
                  :value="1"
                />
                <el-option
                  :label="$t('common.disabled')"
                  :value="0"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        
        <el-form-item
          :label="$t('admin.announcements.content')"
          prop="content"
        >
          <div class="quill-editor-wrapper">
            <QuillEditor
              v-model:content="form.content"
              content-type="html"
              theme="snow"
              :options="editorOptions"
              @update:content="handleContentChange"
            />
          </div>
        </el-form-item>
        
        <el-row
          v-if="isEditing"
          :gutter="20"
        >
          <el-col :span="12">
            <el-form-item :label="$t('admin.announcements.startTime')">
              <el-date-picker
                v-model="startTime"
                type="datetime"
                :placeholder="$t('admin.announcements.selectStartTime')"
                format="YYYY-MM-DD HH:mm:ss"
                value-format="YYYY-MM-DD HH:mm:ss"
                style="width: 100%"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item :label="$t('admin.announcements.endTime')">
              <el-date-picker
                v-model="endTime"
                type="datetime"
                :placeholder="$t('admin.announcements.selectEndTime')"
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
          {{ $t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="saveAnnouncement"
        >
          {{ isEditing ? $t('common.update') : $t('common.save') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { useAnnouncementManagement } from './composables/useAnnouncementManagement'
import { QuillEditor } from '@vueup/vue-quill'
import '@vueup/vue-quill/dist/vue-quill.snow.css'

const {
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
  handleDialogClose,
  handleSelectionChange,
  handleBatchDelete,
  handleBatchToggleStatus,
  toggleAnnouncementStatus,
  resetFilters
} = useAnnouncementManagement()
</script>

<style scoped>
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: var(--text-color-primary);
  }
}

.filter-container {
  margin-bottom: 20px;
}

/* 富文本编辑器容器固定大小 */
.quill-editor-wrapper {
  width: 100%;
  height: 400px;
  border: 1px solid #ccc;
  border-radius: 4px;
}

:deep(.quill-editor-wrapper .ql-container) {
  height: calc(100% - 42px);
  font-size: 14px;
}

:deep(.quill-editor-wrapper .ql-editor) {
  min-height: 100%;
  max-height: 100%;
  overflow-y: auto;
}

:deep(.quill-editor-wrapper .ql-toolbar) {
  border-top-left-radius: 4px;
  border-top-right-radius: 4px;
}

:deep(.quill-editor-wrapper .ql-container) {
  border-bottom-left-radius: 4px;
  border-bottom-right-radius: 4px;
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