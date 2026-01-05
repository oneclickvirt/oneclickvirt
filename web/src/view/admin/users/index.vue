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
      
      <el-table 
        v-loading="loading" 
        :data="users" 
        class="users-table"
        :row-style="{ height: '60px' }"
        :cell-style="{ padding: '12px 0' }"
        :header-cell-style="{ background: '#f5f7fa', padding: '14px 0', fontWeight: '600' }"
        @selection-change="handleSelectionChange"
      >
        <el-table-column
          type="selection"
          width="55"
          align="center"
        />
        <el-table-column
          prop="id"
          label="ID"
          width="80"
          align="center"
        />
        <el-table-column
          prop="username"
          :label="$t('admin.users.username')"
          min-width="140"
          show-overflow-tooltip
        />
        <el-table-column
          prop="email"
          :label="$t('admin.users.email')"
          min-width="180"
          show-overflow-tooltip
        />
        <el-table-column
          prop="nickname"
          :label="$t('admin.users.nickname')"
          min-width="140"
          show-overflow-tooltip
        />
        <el-table-column
          prop="level"
          :label="$t('admin.users.level')"
          width="100"
          align="center"
        >
          <template #default="scope">
            <el-tag :type="getLevelTagType(scope.row.level)">
              {{ $t('admin.users.levelTag', { level: scope.row.level }) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="userType"
          :label="$t('admin.users.userType')"
          width="120"
          align="center"
        >
          <template #default="scope">
            <el-tag :type="getUserTypeTagType(scope.row.userType)">
              {{ getUserTypeLabel(scope.row.userType) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          :label="$t('common.status')"
          width="100"
          align="center"
        >
          <template #default="scope">
            <el-tag :type="scope.row.status === 1 ? 'success' : 'danger'">
              {{ scope.row.status === 1 ? $t('admin.users.active') : $t('admin.users.disabled') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="expiresAt"
          :label="$t('admin.users.expiresAt')"
          width="180"
          align="center"
        >
          <template #default="scope">
            <div v-if="scope.row.expiresAt">
              <el-tag 
                :type="isExpired(scope.row.expiresAt) ? 'danger' : 'success'"
                size="small"
              >
                {{ formatDateTime(scope.row.expiresAt) }}
              </el-tag>
              <div v-if="scope.row.isManualExpiry" style="margin-top: 4px;">
                <el-tag size="small" type="info">{{ $t('admin.users.manualExpiry') }}</el-tag>
              </div>
            </div>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="isFrozen"
          :label="$t('admin.users.freezeStatus')"
          width="120"
          align="center"
        >
          <template #default="scope">
            <div>
              <el-tag :type="scope.row.isFrozen ? 'danger' : 'success'">
                {{ scope.row.isFrozen ? $t('admin.users.frozen') : $t('admin.users.normal') }}
              </el-tag>
              <div v-if="scope.row.isFrozen && scope.row.frozenReason" style="margin-top: 4px;">
                <el-tooltip :content="scope.row.frozenReason">
                  <el-tag size="small" type="warning">{{ $t('admin.users.hasReason') }}</el-tag>
                </el-tooltip>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          :label="$t('common.actions')"
          width="350"
          fixed="right"
          align="center"
        >
          <template #default="scope">
            <div class="action-buttons">
              <el-button
                size="small"
                @click="editUser(scope.row)"
              >
                {{ $t('common.edit') }}
              </el-button>
              <el-dropdown @command="(level) => handleSetUserLevel(scope.row, level)">
                <el-button
                  size="small"
                  type="primary"
                >
                  {{ $t('admin.users.levelSetting') }}<el-icon class="el-icon--right">
                    <arrow-down />
                  </el-icon>
                </el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item :command="1">
                      {{ $t('admin.users.setToLevel', { level: 1 }) }}
                    </el-dropdown-item>
                    <el-dropdown-item :command="2">
                      {{ $t('admin.users.setToLevel', { level: 2 }) }}
                    </el-dropdown-item>
                    <el-dropdown-item :command="3">
                      {{ $t('admin.users.setToLevel', { level: 3 }) }}
                    </el-dropdown-item>
                    <el-dropdown-item :command="4">
                      {{ $t('admin.users.setToLevel', { level: 4 }) }}
                    </el-dropdown-item>
                    <el-dropdown-item :command="5">
                      {{ $t('admin.users.setToLevel', { level: 5 }) }}
                    </el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
              <el-dropdown @command="(cmd) => handleFreezeCommand(scope.row, cmd)">
                <el-button
                  size="small"
                  :type="scope.row.isFrozen ? 'success' : 'warning'"
                >
                  {{ $t('admin.users.freezeManage') }}<el-icon class="el-icon--right">
                    <arrow-down />
                  </el-icon>
                </el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="setExpiry">
                      {{ $t('admin.users.setExpiry') }}
                    </el-dropdown-item>
                    <el-dropdown-item 
                      command="freeze"
                      :disabled="scope.row.isFrozen"
                    >
                      {{ $t('admin.users.freeze') }}
                    </el-dropdown-item>
                    <el-dropdown-item 
                      command="unfreeze"
                      :disabled="!scope.row.isFrozen"
                    >
                      {{ $t('admin.users.unfreeze') }}
                    </el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
              <el-button
                size="small"
                :type="scope.row.status === 1 ? 'danger' : 'success'"
                @click="handleToggleUserStatus(scope.row)"
              >
                {{ scope.row.status === 1 ? $t('admin.users.disable') : $t('admin.users.enable') }}
              </el-button>
              <el-button
                size="small"
                type="warning"
                @click="handleResetPassword(scope.row)"
              >
                {{ $t('admin.users.resetPassword') }}
              </el-button>
            </div>
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

    <!-- 添加/编辑用户对话框 -->
    <el-dialog
      v-model="showAddDialog"
      :title="isEditing ? $t('admin.users.editUser') : $t('admin.users.addUser')"
      width="600px"
      @close="cancelAddUser"
    >
      <el-form
        ref="addUserFormRef"
        :model="addUserForm"
        :rules="addUserRules"
        label-width="100px"
      >
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.users.username')"
              prop="username"
            >
              <el-input
                v-model="addUserForm.username"
                :disabled="isEditing"
              />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.users.nickname')"
              prop="nickname"
            >
              <el-input v-model="addUserForm.nickname" />
            </el-form-item>
          </el-col>
        </el-row>
        
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.users.email')"
              prop="email"
            >
              <el-input v-model="addUserForm.email" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="$t('user.profile.phone')"
              prop="phone"
            >
              <el-input v-model="addUserForm.phone" />
            </el-form-item>
          </el-col>
        </el-row>

        <el-row
          v-if="!isEditing"
          :gutter="20"
        >
          <el-col :span="12">
            <el-form-item
              :label="$t('login.password')"
              prop="password"
            >
              <el-input
                v-model="addUserForm.password"
                type="password"
              />
              <div class="password-hint">
                <el-text
                  size="small"
                  type="info"
                >
                  {{ $t('register.passwordHint') }}
                </el-text>
              </div>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="$t('register.confirmPassword')"
              prop="confirmPassword"
            >
              <el-input
                v-model="addUserForm.confirmPassword"
                type="password"
              />
            </el-form-item>
          </el-col>
        </el-row>

        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.users.userType')"
              prop="userType"
            >
              <el-select
                v-model="addUserForm.userType"
                style="width: 100%"
              >
                <el-option
                  :label="$t('admin.users.normalUser')"
                  value="user"
                />
                <el-option
                  :label="$t('admin.users.adminUser')"
                  value="admin"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              :label="$t('common.status')"
              prop="status"
            >
              <el-select
                v-model="addUserForm.status"
                style="width: 100%"
              >
                <el-option
                  :label="$t('admin.users.active')"
                  :value="1"
                />
                <el-option
                  :label="$t('admin.users.disabled')"
                  :value="0"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              :label="$t('admin.users.level')"
              prop="level"
            >
              <el-select
                v-model="addUserForm.level"
                :placeholder="$t('common.selectAll')"
                style="width: 100%"
              >
                <el-option
                  :label="$t('admin.users.levelTag', { level: 1 })"
                  :value="1"
                />
                <el-option
                  :label="$t('admin.users.levelTag', { level: 2 })"
                  :value="2"
                />
                <el-option
                  :label="$t('admin.users.levelTag', { level: 3 })"
                  :value="3"
                />
                <el-option
                  :label="$t('admin.users.levelTag', { level: 4 })"
                  :value="4"
                />
                <el-option
                  :label="$t('admin.users.levelTag', { level: 5 })"
                  :value="5"
                />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
      </el-form>
      
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="cancelAddUser">
            {{ $t('common.cancel') }}
          </el-button>
          <el-button
            type="primary"
            :loading="addUserLoading"
            @click="submitAddUser"
          >
            {{ isEditing ? $t('common.save') : $t('common.create') }}
          </el-button>
        </div>
      </template>
    </el-dialog>

    <!-- 重置密码对话框 -->
    <el-dialog
      v-model="showResetPasswordDialog"
      :title="$t('admin.users.resetPassword')"
      width="600px"
      @close="cancelResetPassword"
    >
      <div
        v-if="!generatedPassword"
        style="text-align: center;"
      >
        <el-form
          label-width="120px"
          style="max-width: 500px; margin: 0 auto;"
        >
          <el-form-item :label="$t('admin.users.username')">
            <el-input 
              v-model="resetPasswordForm.username" 
              disabled
              style="width: 100%;"
            />
          </el-form-item>
        </el-form>
        
        <div style="margin: 20px 0;">
          <el-text type="info">
            {{ $t('admin.users.passwordResetInfo') }} <strong>{{ resetPasswordForm.username }}</strong>
          </el-text>
        </div>
        
        <div style="margin: 20px 0;">
          <el-text
            size="small"
            type="warning"
          >
            {{ $t('register.passwordHint') }}
          </el-text>
        </div>
      </div>
      
      <!-- 显示生成的密码 -->
      <div
        v-else
        style="text-align: center;"
      >
        <el-result
          icon="success"
          :title="$t('admin.users.resetPasswordSuccess')"
          :sub-title="$t('admin.users.passwordResetInfo')"
        >
          <template #extra>
            <div style="margin: 20px 0;">
              <el-text
                type="info"
                style="display: block; margin-bottom: 10px;"
              >
                {{ $t('admin.users.newPassword') }}：
              </el-text>
              <el-input
                v-model="generatedPassword"
                readonly
                style="width: 300px; font-family: monospace; font-size: 16px;"
              >
                <template #append>
                  <el-button @click="copyPassword">
                    {{ $t('common.copy') }}
                  </el-button>
                </template>
              </el-input>
            </div>
            <div style="margin: 20px 0;">
              <el-text
                size="small"
                type="warning"
              >
                {{ $t('register.passwordHint') }}
              </el-text>
            </div>
          </template>
        </el-result>
      </div>
      
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="cancelResetPassword">
            {{ generatedPassword ? $t('common.close') : $t('common.cancel') }}
          </el-button>
          <el-button 
            v-if="!generatedPassword"
            type="danger" 
            :loading="resetPasswordLoading"
            @click="confirmResetPassword"
          >
            {{ $t('admin.users.resetPassword') }}
          </el-button>
        </div>
      </template>
    </el-dialog>

    <!-- 设置过期时间对话框 -->
    <el-dialog
      v-model="showSetExpiryDialog"
      :title="$t('admin.users.setExpiry')"
      width="500px"
    >
      <el-form
        label-width="120px"
      >
        <el-form-item :label="$t('admin.users.username')">
          <el-input 
            v-model="freezeForm.username" 
            disabled
          />
        </el-form-item>
        <el-form-item :label="$t('admin.users.expiresAt')">
          <el-date-picker
            v-model="freezeForm.expiresAt"
            type="datetime"
            :placeholder="$t('admin.users.selectExpiryTime')"
            format="YYYY-MM-DD HH:mm:ss"
            value-format="YYYY-MM-DDTHH:mm:ssZ"
            style="width: 100%;"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="showSetExpiryDialog = false">
            {{ $t('common.cancel') }}
          </el-button>
          <el-button
            type="primary"
            :loading="freezeLoading"
            @click="confirmSetExpiry"
          >
            {{ $t('common.confirm') }}
          </el-button>
        </div>
      </template>
    </el-dialog>

    <!-- 冻结对话框 -->
    <el-dialog
      v-model="showFreezeDialog"
      :title="$t('admin.users.freeze')"
      width="500px"
    >
      <el-form
        label-width="120px"
      >
        <el-form-item :label="$t('admin.users.username')">
          <el-input 
            v-model="freezeForm.username" 
            disabled
          />
        </el-form-item>
        <el-form-item :label="$t('admin.users.freezeReason')">
          <el-input
            v-model="freezeForm.reason"
            type="textarea"
            :rows="4"
            :placeholder="$t('admin.users.enterFreezeReason')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="showFreezeDialog = false">
            {{ $t('common.cancel') }}
          </el-button>
          <el-button
            type="warning"
            :loading="freezeLoading"
            @click="confirmFreeze"
          >
            {{ $t('common.confirm') }}
          </el-button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search, ArrowDown } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { 
  getUserList, 
  createUser, 
  toggleUserStatus, 
  updateUser, 
  batchDeleteUsers,
  batchUpdateUserStatus,
  batchUpdateUserLevel,
  updateUserLevel,
  resetUserPassword,
  setUserExpiry,
  freezeUser,
  unfreezeUser
} from '@/api/admin'

const { t } = useI18n()

const users = ref([])
const loading = ref(false)
const showAddDialog = ref(false)
const currentUser = ref(null)
const saving = ref(false)
const addUserLoading = ref(false)
const addUserFormRef = ref()
const isEditing = ref(false)

// 重置密码相关
const showResetPasswordDialog = ref(false)
const resetPasswordForm = reactive({
  userId: null,
  username: ''
})
const resetPasswordLoading = ref(false)
const generatedPassword = ref('')

// 冻结管理相关
const showSetExpiryDialog = ref(false)
const showFreezeDialog = ref(false)
const freezeLoading = ref(false)
const freezeForm = reactive({
  userId: null,
  username: '',
  expiresAt: null,
  reason: ''
})

// 搜索相关
const searchUsername = ref('')
const searchStatus = ref(null) // 默认为null，显示所有状态
const searchUserType = ref('') // 默认为空字符串，显示所有类型

// 批量选择相关
const multipleSelection = ref([])

// 分页
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 用户表单
const addUserForm = reactive({
  id: null,
  username: '',
  password: '',
  confirmPassword: '',
  nickname: '',
  email: '',
  phone: '',
  userType: 'user',
  level: 1,
  totalQuota: 0,
  status: 1
})

// 表单验证规则
const addUserRules = {
  username: [
    { required: true, message: t('validation.usernameRequired'), trigger: 'blur' },
    { min: 3, max: 20, message: t('validation.usernameLength', { min: 3, max: 20 }), trigger: 'blur' }
  ],
  nickname: [
    // 昵称不是必填项
  ],
  email: [
    { required: true, message: t('validation.emailRequired'), trigger: 'blur' },
    { type: 'email', message: t('validation.emailFormat'), trigger: 'blur' }
  ],
  password: [
    { required: true, message: t('validation.passwordRequired'), trigger: 'blur' },
    { min: 8, message: t('validation.passwordLength'), trigger: 'blur' }
  ],
  confirmPassword: [
    { required: true, message: t('register.pleaseConfirmPassword'), trigger: 'blur' },
    {
      validator: (rule, value, callback) => {
        if (value !== addUserForm.password) {
          callback(new Error(t('validation.confirmPasswordMatch')))
        } else {
          callback()
        }
      },
      trigger: 'blur'
    }
  ]
}

// 生命周期
onMounted(() => {
  loadUsers()
})

// 加载用户列表
const loadUsers = async () => {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      pageSize: pageSize.value,
      username: searchUsername.value || undefined,
      userType: searchUserType.value || undefined
    }
    
    // 只有在明确选择状态时才传递status参数
    if (searchStatus.value !== null && searchStatus.value !== undefined) {
      params.status = searchStatus.value
    }
    
    const response = await getUserList(params)
    users.value = response.data.list || []
    total.value = response.data.total || 0
  } catch (error) {
    ElMessage.error(t('admin.users.loadUsersFailed'))
  } finally {
    loading.value = false
  }
}

// 搜索处理
const handleSearch = () => {
  currentPage.value = 1
  loadUsers()
}

// 重置筛选器
const resetFilters = () => {
  searchUsername.value = ''
  searchStatus.value = null
  searchUserType.value = ''
  currentPage.value = 1
  loadUsers()
}

// 批量选择处理
const handleSelectionChange = (selection) => {
  multipleSelection.value = selection
}

// 批量删除
const handleBatchDelete = async () => {
  if (multipleSelection.value.length === 0) {
    ElMessage.warning(t('admin.users.batchDelete'))
    return
  }

  try {
    await ElMessageBox.confirm(
      t('admin.users.confirmBatchDelete'),
      t('common.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    const userIds = multipleSelection.value.map(user => user.id)
    await batchDeleteUsers(userIds)
    ElMessage.success(t('admin.users.deleteSuccess'))
    await loadUsers()
    multipleSelection.value = []
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.users.deleteFailed'))
    }
  }
}

// 批量启用
const handleBatchEnable = async () => {
  if (multipleSelection.value.length === 0) {
    ElMessage.warning(t('admin.users.batchEnable'))
    return
  }

  try {
    await ElMessageBox.confirm(
      t('admin.users.confirmToggleStatus', { action: t('admin.users.enable') }),
      t('common.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    const userIds = multipleSelection.value.map(user => user.id)
    await batchUpdateUserStatus(userIds, 1)
    ElMessage.success(t('admin.users.updateSuccess'))
    await loadUsers()
    multipleSelection.value = []
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.users.updateFailed'))
    }
  }
}

// 批量禁用
const handleBatchDisable = async () => {
  if (multipleSelection.value.length === 0) {
    ElMessage.warning(t('admin.users.batchDisable'))
    return
  }

  try {
    await ElMessageBox.confirm(
      t('admin.users.confirmToggleStatus', { action: t('admin.users.disable') }),
      t('common.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    const userIds = multipleSelection.value.map(user => user.id)
    await batchUpdateUserStatus(userIds, 0)
    ElMessage.success(t('admin.users.updateSuccess'))
    await loadUsers()
    multipleSelection.value = []
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.users.updateFailed'))
    }
  }
}

// 批量设置等级命令处理
const handleBatchLevelCommand = async (level) => {
  if (multipleSelection.value.length === 0) {
    ElMessage.warning(t('admin.users.batchSetLevel'))
    return
  }

  try {
    await ElMessageBox.confirm(
      t('admin.users.confirmBatchDelete'),
      t('common.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    const userIds = multipleSelection.value.map(user => user.id)
    await batchUpdateUserLevel(userIds, parseInt(level))
    ElMessage.success(t('admin.users.updateSuccess'))
    await loadUsers()
    multipleSelection.value = []
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.users.updateFailed'))
    }
  }
}

// 设置单个用户等级
const handleSetUserLevel = async (user, level) => {
  try {
    await ElMessageBox.confirm(
      t('admin.users.confirmToggleStatus', { action: t('admin.users.setToLevel', { level }) }),
      t('common.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    await updateUserLevel(user.id, parseInt(level))
    ElMessage.success(t('admin.users.updateSuccess'))
    await loadUsers()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.users.updateFailed'))
    }
  }
}

// 获取等级标签类型
const getLevelTagType = (level) => {
  const typeMap = {
    1: '',
    2: 'success',
    3: 'info',
    4: 'warning',
    5: 'danger'
  }
  return typeMap[level] || ''
}

// 获取用户类型标签文本
const getUserTypeLabel = (userType) => {
  const labelMap = {
    'user': t('admin.users.normalUser'),
    'admin': t('admin.users.adminUser')
  }
  return labelMap[userType] || t('common.unknown')
}

// 获取用户类型标签样式
const getUserTypeTagType = (userType) => {
  const typeMap = {
    'user': '',
    'admin': 'danger'
  }
  return typeMap[userType] || ''
}

// 新用户
const handleAddUser = () => {
  // 重置为新增模式
  isEditing.value = false
  // 重置表单到初始状态
  cancelAddUser()
  // 打开对话框
  showAddDialog.value = true
}

// 编辑用户
const editUser = (user) => {
  Object.assign(addUserForm, {
    id: user.id,
    username: user.username,
    nickname: user.nickname,
    email: user.email,
    phone: user.phone || '',
    userType: user.userType || 'user',
    level: user.level || 1,
    totalQuota: user.totalQuota || 0,
    status: user.status,
    password: '',
    confirmPassword: ''
  })
  isEditing.value = true
  showAddDialog.value = true
}

// 取消添加用户
const cancelAddUser = () => {
  showAddDialog.value = false
  isEditing.value = false
  addUserFormRef.value?.resetFields()
  Object.assign(addUserForm, {
    id: null,
    username: '',
    password: '',
    confirmPassword: '',
    nickname: '',
    email: '',
    phone: '',
    userType: 'user',
    level: 1,
    totalQuota: 0,
    status: 1
  })
}

// 提交添加/编辑用户
const submitAddUser = async () => {
  if (!addUserFormRef.value) return
  
  try {
    await addUserFormRef.value.validate()
    addUserLoading.value = true
    
    const userData = { ...addUserForm }
    delete userData.confirmPassword
    
    if (isEditing.value) {
      // 编辑用户时，如果密码为空则不更新密码
      if (!userData.password) {
        delete userData.password
      }
      await updateUser(userData.id, userData)
      ElMessage.success(t('admin.users.updateSuccess'))
    } else {
      await createUser(userData)
      ElMessage.success(t('message.createSuccess'))
    }
    
    showAddDialog.value = false
    isEditing.value = false
    await loadUsers()
    cancelAddUser()
  } catch (error) {
    ElMessage.error(isEditing.value ? t('admin.users.updateFailed') : t('message.createFailed'))
  } finally {
    addUserLoading.value = false
  }
}

// 切换用户状态
const handleToggleUserStatus = async (user) => {
  const action = user.status === 1 ? t('admin.users.disable') : t('admin.users.enable')
  try {
    await ElMessageBox.confirm(
      t('admin.users.confirmToggleStatus', { action }),
      t('common.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      }
    )
    
    await toggleUserStatus(user.id, user.status === 1 ? 0 : 1)
    ElMessage.success(t('admin.users.updateSuccess'))
    await loadUsers()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.users.updateFailed'))
    }
  }
}

// 重置密码
const handleResetPassword = (user) => {
  resetPasswordForm.userId = user.id
  resetPasswordForm.username = user.username
  generatedPassword.value = ''
  showResetPasswordDialog.value = true
}

const confirmResetPassword = async () => {
  try {
    resetPasswordLoading.value = true
    
    const response = await resetUserPassword(resetPasswordForm.userId)
    
    // 显示生成的密码
    generatedPassword.value = response.data.newPassword
    ElMessage.success(t('admin.users.resetPasswordSuccess'))
    
    // 重新加载用户列表
    await loadUsers()
    
  } catch (error) {
    ElMessage.error(t('admin.users.resetPasswordFailed') + ': ' + (error.response?.data?.message || error.message))
  } finally {
    resetPasswordLoading.value = false
  }
}

const cancelResetPassword = () => {
  showResetPasswordDialog.value = false
  resetPasswordForm.userId = null
  resetPasswordForm.username = ''
  generatedPassword.value = ''
}

// 复制密码到剪贴板
const copyPassword = async () => {
  if (!generatedPassword.value) {
    ElMessage.warning(t('user.profile.noPasswordToCopy'))
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(generatedPassword.value)
      ElMessage.success(t('user.profile.passwordCopied'))
      return
    }
    
    // 降级方案：使用传统的 document.execCommand
    const textArea = document.createElement('textarea')
    textArea.value = generatedPassword.value
    textArea.style.position = 'fixed'
    textArea.style.left = '-999999px'
    textArea.style.top = '-999999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()
    
    try {
      // @ts-ignore - execCommand 已废弃但作为降级方案仍需使用
      const successful = document.execCommand('copy')
      if (successful) {
        ElMessage.success(t('user.profile.passwordCopied'))
      } else {
        throw new Error('execCommand failed')
      }
    } finally {
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    ElMessage.error(t('user.profile.copyFailed'))
  }
}

// 冻结管理命令处理
const handleFreezeCommand = (user, command) => {
  freezeForm.userId = user.id
  freezeForm.username = user.username
  
  switch (command) {
    case 'setExpiry':
      freezeForm.expiresAt = user.expiresAt || null
      showSetExpiryDialog.value = true
      break
    case 'freeze':
      freezeForm.reason = ''
      showFreezeDialog.value = true
      break
    case 'unfreeze':
      handleUnfreeze(user)
      break
  }
}

// 确认设置过期时间
const confirmSetExpiry = async () => {
  try {
    freezeLoading.value = true
    await setUserExpiry({
      userID: freezeForm.userId,
      expiresAt: freezeForm.expiresAt
    })
    ElMessage.success(t('admin.users.setExpirySuccess'))
    showSetExpiryDialog.value = false
    await loadUsers()
  } catch (error) {
    ElMessage.error(t('admin.users.setExpiryFailed'))
  } finally {
    freezeLoading.value = false
  }
}

// 确认冻结
const confirmFreeze = async () => {
  try {
    freezeLoading.value = true
    await freezeUser({
      userID: freezeForm.userId,
      reason: freezeForm.reason || ''
    })
    ElMessage.success(t('admin.users.freezeSuccess'))
    showFreezeDialog.value = false
    await loadUsers()
  } catch (error) {
    ElMessage.error(t('admin.users.freezeFailed'))
  } finally {
    freezeLoading.value = false
  }
}

// 解冻用户
const handleUnfreeze = async (user) => {
  try {
    await ElMessageBox.confirm(
      t('admin.users.confirmUnfreeze'),
      t('common.confirm'),
      {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
    
    await unfreezeUser({ userID: user.id })
    ElMessage.success(t('admin.users.unfreezeSuccess'))
    await loadUsers()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(t('admin.users.unfreezeFailed'))
    }
  }
}

// 格式化日期时间
const formatDateTime = (dateTimeStr) => {
  if (!dateTimeStr) return '-'
  const date = new Date(dateTimeStr)
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
}

// 检查是否已过期
const isExpired = (dateTimeStr) => {
  if (!dateTimeStr) return false
  return new Date(dateTimeStr) < new Date()
}

// 分页处理
const handleSizeChange = (size) => {
  pageSize.value = size
  currentPage.value = 1
  loadUsers()
}

const handleCurrentChange = (page) => {
  currentPage.value = page
  loadUsers()
}
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
    color: #303133;
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
  background-color: #f5f7fa;
  border-radius: 4px;
  
  .el-button {
    padding: 8px 16px;
  }
}

.selection-info {
  color: #409eff;
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
  color: #909399;
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
