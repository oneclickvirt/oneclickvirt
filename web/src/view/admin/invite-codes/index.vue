<template>
  <div class="invite-codes-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.inviteCodes.title') }}</span>
          <div>
            <el-button
              type="success"
              @click="showCreateDialog = true"
            >
              {{ $t('admin.inviteCodes.createCustomCode') }}
            </el-button>
            <el-button
              type="primary"
              @click="showGenerateDialog = true"
            >
              {{ $t('admin.inviteCodes.batchGenerate') }}
            </el-button>
          </div>
        </div>
      </template>

      <!-- 筛选栏 -->
      <div class="filter-bar">
        <el-form :inline="true">
          <el-form-item :label="$t('admin.inviteCodes.usageStatus')">
            <el-select
              v-model="filterForm.isUsed"
              :placeholder="$t('common.all')"
              clearable
              style="width: 120px"
              @change="handleFilterChange"
            >
              <el-option
                :label="$t('common.all')"
                :value="null"
              />
              <el-option
                :label="$t('admin.inviteCodes.unused')"
                :value="false"
              />
              <el-option
                :label="$t('admin.inviteCodes.used')"
                :value="true"
              />
            </el-select>
          </el-form-item>
          <el-form-item :label="$t('common.status')">
            <el-select
              v-model="filterForm.status"
              :placeholder="$t('common.all')"
              clearable
              style="width: 120px"
              @change="handleFilterChange"
            >
              <el-option
                :label="$t('common.all')"
                :value="0"
              />
              <el-option
                :label="$t('admin.inviteCodes.available')"
                :value="1"
              />
            </el-select>
          </el-form-item>
        </el-form>
      </div>

      <!-- 批量操作按钮 -->
      <div
        v-if="selectedCodes.length > 0"
        class="batch-actions"
      >
        <el-button
          type="primary"
          @click="handleBatchExport"
        >
          {{ $t('admin.inviteCodes.exportSelected') }} ({{ selectedCodes.length }})
        </el-button>
        <el-button
          type="danger"
          @click="handleBatchDelete"
        >
          {{ $t('admin.inviteCodes.deleteSelected') }} ({{ selectedCodes.length }})
        </el-button>
      </div>
      
      <el-table
        v-loading="loading"
        :data="inviteCodes"
        style="width: 100%"
        @selection-change="handleSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
        />
        <el-table-column
          prop="id"
          label="ID"
          width="60"
        />
        <el-table-column
          prop="code"
          :label="$t('admin.inviteCodes.code')"
        />
        <el-table-column
          prop="maxUses"
          :label="$t('admin.inviteCodes.maxUses')"
          width="120"
        >
          <template #default="scope">
            {{ scope.row.maxUses === 0 ? $t('admin.inviteCodes.unlimited') : scope.row.maxUses }}
          </template>
        </el-table-column>
        <el-table-column
          prop="usedCount"
          :label="$t('admin.inviteCodes.usedCount')"
          width="120"
        />
        <el-table-column
          prop="status"
          :label="$t('common.status')"
          width="100"
        >
          <template #default="scope">
            <el-tag :type="scope.row.status === 1 ? 'success' : 'info'">
              {{ scope.row.status === 1 ? $t('admin.inviteCodes.available') : $t('admin.inviteCodes.expired') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="expiresAt"
          :label="$t('admin.inviteCodes.expiryDate')"
          width="160"
        >
          <template #default="scope">
            {{ scope.row.expiresAt ? new Date(scope.row.expiresAt).toLocaleString() : $t('admin.inviteCodes.neverExpires') }}
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="$t('common.createTime')"
          width="160"
        >
          <template #default="scope">
            {{ new Date(scope.row.createdAt).toLocaleString() }}
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="120"
        >
          <template #default="scope">
            <el-button
              size="small"
              type="danger"
              @click="deleteCode(scope.row.id)"
            >
              {{ $t('common.delete') }}
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="pagination-wrapper">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </el-card>

    <!-- 创建自定义邀请码对话框 -->
    <el-dialog 
      v-model="showCreateDialog" 
      :title="$t('admin.inviteCodes.createCustomCode')" 
      width="500px"
      :before-close="handleCreateDialogClose"
    >
      <el-form 
        ref="createFormRef" 
        :model="createForm" 
        :rules="createRules" 
        label-width="120px"
      >
        <el-form-item
          :label="$t('admin.inviteCodes.code')"
          prop="code"
        >
          <el-input 
            v-model="createForm.code" 
            :placeholder="$t('admin.inviteCodes.codeInputPlaceholder')"
            maxlength="50"
            show-word-limit
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.codeFormatTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('admin.inviteCodes.maxUses')"
          prop="maxUses"
        >
          <el-input-number
            v-model="createForm.maxUses"
            :min="0"
            :controls="false"
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.maxUsesTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('admin.inviteCodes.expiryDate')"
          prop="expiresAt"
        >
          <el-date-picker
            v-model="createForm.expiresAt"
            type="datetime"
            :placeholder="$t('admin.inviteCodes.selectExpiryDate')"
            format="YYYY-MM-DD HH:mm:ss"
            value-format="YYYY-MM-DD HH:mm:ss"
            style="width: 100%"
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.expiryDateTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('common.description')"
          prop="description"
        >
          <el-input 
            v-model="createForm.description" 
            type="textarea" 
            :rows="3"
            :placeholder="$t('admin.inviteCodes.descriptionPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="cancelCreate">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="primary"
            :loading="createLoading"
            @click="submitCreate"
          >{{ $t('common.create') }}</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 生成邀请码对话框 -->
    <el-dialog 
      v-model="showGenerateDialog" 
      :title="$t('admin.inviteCodes.batchGenerate')" 
      width="500px"
    >
      <el-form 
        ref="generateFormRef" 
        :model="generateForm" 
        :rules="generateRules" 
        label-width="120px"
      >
        <el-form-item
          :label="$t('admin.inviteCodes.generateCount')"
          prop="count"
        >
          <el-input-number
            v-model="generateForm.count"
            :min="1"
            :max="100"
            :controls="false"
          />
        </el-form-item>
        <el-form-item
          :label="$t('admin.inviteCodes.maxUses')"
          prop="maxUses"
        >
          <el-input-number
            v-model="generateForm.maxUses"
            :min="0"
            :controls="false"
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.maxUsesTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('admin.inviteCodes.expiryDate')"
          prop="expiresAt"
        >
          <el-date-picker
            v-model="generateForm.expiresAt"
            type="datetime"
            :placeholder="$t('admin.inviteCodes.selectExpiryDate')"
            format="YYYY-MM-DD HH:mm:ss"
            value-format="YYYY-MM-DD HH:mm:ss"
            style="width: 100%"
          />
          <div class="form-tip">
            {{ $t('admin.inviteCodes.expiryDateTip') }}
          </div>
        </el-form-item>
        <el-form-item
          :label="$t('common.description')"
          prop="description"
        >
          <el-input 
            v-model="generateForm.description" 
            type="textarea" 
            :rows="3"
            :placeholder="$t('admin.inviteCodes.descriptionPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="cancelGenerate">{{ $t('common.cancel') }}</el-button>
          <el-button
            type="primary"
            :loading="generateLoading"
            @click="submitGenerate"
          >{{ $t('admin.inviteCodes.generate') }}</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 导出邀请码对话框 -->
    <el-dialog
      v-model="showExportDialog"
      :title="$t('admin.inviteCodes.exportCodes')"
      width="600px"
    >
      <div class="export-content">
        <el-input
          v-model="exportedCodes"
          type="textarea"
          :rows="15"
          readonly
        />
      </div>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="showExportDialog = false">{{ $t('common.close') }}</el-button>
          <el-button
            type="primary"
            @click="copyExportedCodes"
          >{{ $t('common.copy') }}</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { useInviteCodeManagement } from './composables/useInviteCodeManagement.js'

const {
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
} = useInviteCodeManagement()
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

.filter-bar {
  margin-bottom: 20px;
}

.batch-actions {
  margin-bottom: 15px;
  padding: 10px;
  background-color: var(--neutral-bg);
  border-radius: 4px;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.form-tip {
  font-size: 12px;
  color: var(--text-color-secondary);
  margin-top: 4px;
}

.export-content {
  margin: 20px 0;
}
</style>
