<template>
  <div class="users-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>{{ $t('admin.users.title') }}</span>
          <div class="header-actions">
            <el-button
              type="primary"
              @click="handleAddUser"
            >
              {{ $t('admin.users.addUser') }}
            </el-button>
          </div>
        </div>
      </template>
      
      <!-- 搜索和批量操作 -->
      <div class="toolbar">
        <div class="search-section">
          <el-input
            v-model="searchUsername"
            :placeholder="$t('admin.users.searchByUsername')"
            style="width: 200px;"
            clearable
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-select
            v-model="searchStatus"
            :placeholder="$t('admin.users.selectStatus')"
            style="width: 150px; margin-left: 10px;"
            clearable
          >
            <el-option
              :label="$t('admin.users.all')"
              :value="null"
            />
            <el-option
              :label="$t('admin.users.active')"
              :value="1"
            />
            <el-option
              :label="$t('admin.users.disabled')"
              :value="0"
            />
          </el-select>
          <el-select
            v-model="searchUserType"
            :placeholder="$t('admin.users.selectUserType')"
            style="width: 180px; margin-left: 10px;"
            clearable
          >
            <el-option
              :label="$t('admin.users.all')"
              value=""
            />
            <el-option
              :label="$t('admin.users.normalUser')"
              value="user"
            />
            <el-option
              :label="$t('admin.users.normalAdmin')"
              value="normal_admin"
            />
            <el-option
              :label="$t('admin.users.adminUser')"
              value="admin"
            />
          </el-select>
          <el-button 
            type="primary" 
            style="margin-left: 10px;"
            @click="handleSearch"
          >
            {{ $t('admin.users.query') }}
          </el-button>
          <el-button 
            type="default" 
            style="margin-left: 10px;"
            @click="resetFilters"
          >
            {{ $t('admin.users.resetFilters') }}
          </el-button>
        </div>
        
        <div
          v-if="multipleSelection.length > 0"
          class="batch-actions"
        >
          <span class="selection-info">{{ $t('admin.users.selected') }} {{ multipleSelection.length }} {{ $t('admin.users.users') }}</span>
          <el-button
            size="small"
            type="danger"
            @click="handleBatchDelete"
          >
            {{ $t('admin.users.batchDelete') }}
          </el-button>
          <el-button
            size="small"
            type="warning"
            @click="handleBatchEnable"
          >
            {{ $t('admin.users.batchEnable') }}
          </el-button>
          <el-button
            size="small"
            type="info"
            @click="handleBatchDisable"
          >
            {{ $t('admin.users.batchDisable') }}
          </el-button>
          <el-dropdown @command="handleBatchLevelCommand">
            <el-button
              size="small"
              type="primary"
            >
              {{ $t('admin.users.batchSetLevel') }}<el-icon class="el-icon--right">
                <arrow-down />
              </el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="1">
                  {{ $t('admin.users.setToLevel', { level: 1 }) }}
                </el-dropdown-item>
                <el-dropdown-item command="2">
                  {{ $t('admin.users.setToLevel', { level: 2 }) }}
                </el-dropdown-item>
                <el-dropdown-item command="3">
                  {{ $t('admin.users.setToLevel', { level: 3 }) }}
                </el-dropdown-item>
                <el-dropdown-item command="4">
                  {{ $t('admin.users.setToLevel', { level: 4 }) }}
                </el-dropdown-item>
                <el-dropdown-item command="5">
                  {{ $t('admin.users.setToLevel', { level: 5 }) }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>
      
      <UsersTable
        :users="users"
        :loading="loading"
        @selection-change="handleSelectionChange"
        @edit="editUser"
        @set-user-level="handleSetUserLevel"
        @set-expiry="handleSetExpiry"
        @toggle-status="handleToggleUserStatus"
        @reset-password="handleResetPassword"
        @login-as="handleLoginAsUser"
      />

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

    <AddEditUserDialog
      ref="addUserFormRef"
      :visible="showAddDialog"
      :is-editing="isEditing"
      :add-user-form="addUserForm"
      :add-user-rules="addUserRules"
      :loading="addUserLoading"
      @update:visible="showAddDialog = $event"
      @cancel="cancelAddUser"
      @submit="submitAddUser"
    />

    <ResetPasswordDialog
      :visible="showResetPasswordDialog"
      :reset-password-form="resetPasswordForm"
      :generated-password="generatedPassword"
      :loading="resetPasswordLoading"
      @update:visible="showResetPasswordDialog = $event"
      @cancel="cancelResetPassword"
      @confirm="confirmResetPassword"
      @copy-password="copyPassword"
    />

    <SetExpiryDialog
      :visible="showSetExpiryDialog"
      :freeze-form="freezeForm"
      :loading="freezeLoading"
      @update:visible="showSetExpiryDialog = $event"
      @confirm="confirmSetExpiry"
    />
  </div>
</template>

<script setup>
import { onMounted } from 'vue'
import { Search, ArrowDown } from '@element-plus/icons-vue'
import { useUserManagement } from './composables/useUserManagement'
import UsersTable from './components/UsersTable.vue'
import AddEditUserDialog from './components/AddEditUserDialog.vue'
import ResetPasswordDialog from './components/ResetPasswordDialog.vue'
import SetExpiryDialog from './components/SetExpiryDialog.vue'

const {
  users, loading, showAddDialog, addUserLoading, addUserFormRef, isEditing,
  showResetPasswordDialog, resetPasswordForm, resetPasswordLoading, generatedPassword,
  showSetExpiryDialog, freezeLoading, freezeForm,
  searchUsername, searchStatus, searchUserType,
  multipleSelection, currentPage, pageSize, total,
  addUserForm, addUserRules,
  loadUsers, handleSearch, resetFilters,
  handleSelectionChange, handleBatchDelete, handleBatchEnable, handleBatchDisable,
  handleBatchLevelCommand, handleSetUserLevel,
  getLevelTagType, getUserTypeLabel, getUserTypeTagType,
  handleAddUser, editUser, cancelAddUser, submitAddUser,
  handleToggleUserStatus,
  handleResetPassword, confirmResetPassword, cancelResetPassword,
  handleLoginAsUser, copyPassword,
  handleSetExpiry, confirmSetExpiry,
  formatDateTime, isExpired,
  handleSizeChange, handleCurrentChange,
  t
} = useUserManagement()

onMounted(() => {
  loadUsers()
})
</script>

<style scoped lang="scss">
.users-container {
  .el-card {
    :deep(.el-card__header) {
      padding: 20px 24px;
      border-bottom: 1px solid #ebeef5;
    }
    
    :deep(.el-card__body) {
      padding: 24px;
    }
  }
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  
  > span {
    font-size: 18px;
    font-weight: 600;
    color: var(--text-color-primary);
  }
  
  .header-actions {
    .el-button {
      padding: 10px 20px;
    }
  }
}

.users-table {
  width: 100%;
  
  .action-buttons {
    display: flex;
    gap: 12px;
    justify-content: center;
    align-items: center;
    flex-wrap: wrap;
    padding: 4px 0;
    
    .el-button {
      margin: 0 !important;
      padding: 8px 16px;
    }
    
    .el-dropdown {
      .el-button {
        margin: 0 !important;
        padding: 8px 16px;
      }
    }
  }
  
  :deep(.el-table__cell) {
    .cell {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }
}

.toolbar {
  margin-bottom: 20px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  
  .el-button {
    padding: 10px 20px;
  }
}

.search-section {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  
  .el-button {
    padding: 10px 20px;
  }
}

.batch-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  background-color: var(--neutral-bg);
  border-radius: 4px;
  
  .el-button {
    padding: 8px 16px;
  }
}

.selection-info {
  color: #16a34a;
  font-weight: 500;
}

.role-tag {
  margin-right: 5px;
}

.pagination-wrapper {
  margin-top: 20px;
  display: flex;
  justify-content: center;
}

.password-hint {
  margin-top: 5px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--text-color-secondary);
}

.dialog-footer {
  text-align: right;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  
  .el-button {
    padding: 10px 24px;
    margin: 0 !important;
  }
}

:deep(.el-dialog) {
  .el-dialog__body {
    padding: 24px 24px 10px;
  }
  
  .el-form {
    .el-form-item {
      margin-bottom: 24px;
    }
    
    .el-row {
      margin-bottom: 8px;
    }
    
    .el-input {
      .el-input__inner {
        padding: 8px 12px;
      }
    }
    
    .el-select {
      .el-input__inner {
        padding: 8px 12px;
      }
    }
  }
  
  .el-input-group__append {
    .el-button {
      padding: 8px 16px;
    }
  }
}
</style>
