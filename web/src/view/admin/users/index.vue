<template>
  <div class="users-container">
    <el-card>
      <template #header>
        <div class="card-header">
          <span>用户管理</span>
          <div class="header-actions">
            <el-button
              type="primary"
              @click="showAddDialog = true"
            >
              添加用户
            </el-button>
          </div>
        </div>
      </template>
      
      <!-- 搜索和批量操作 -->
      <div class="toolbar">
        <div class="search-section">
          <el-input
            v-model="searchUsername"
            placeholder="输入用户名搜索"
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
            placeholder="选择状态"
            style="width: 150px; margin-left: 10px;"
            clearable
          >
            <el-option
              label="全部"
              :value="null"
            />
            <el-option
              label="正常"
              :value="1"
            />
            <el-option
              label="禁用"
              :value="0"
            />
          </el-select>
          <el-select
            v-model="searchUserType"
            placeholder="选择用户类型"
            style="width: 180px; margin-left: 10px;"
            clearable
          >
            <el-option
              label="全部"
              value=""
            />
            <el-option
              label="普通用户"
              value="user"
            />
            <el-option
              label="管理员"
              value="admin"
            />
          </el-select>
          <el-button 
            type="primary" 
            style="margin-left: 10px;"
            @click="handleSearch"
          >
            查询
          </el-button>
          <el-button 
            type="default" 
            style="margin-left: 10px;"
            @click="resetFilters"
          >
            重置筛选
          </el-button>
        </div>
        
        <div
          v-if="multipleSelection.length > 0"
          class="batch-actions"
        >
          <span class="selection-info">已选择 {{ multipleSelection.length }} 个用户</span>
          <el-button
            size="small"
            type="danger"
            @click="handleBatchDelete"
          >
            批量删除
          </el-button>
          <el-button
            size="small"
            type="warning"
            @click="handleBatchEnable"
          >
            批量启用
          </el-button>
          <el-button
            size="small"
            type="info"
            @click="handleBatchDisable"
          >
            批量禁用
          </el-button>
          <el-dropdown @command="handleBatchLevelCommand">
            <el-button
              size="small"
              type="primary"
            >
              批量设置等级<el-icon class="el-icon--right">
                <arrow-down />
              </el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="1">
                  设置为等级1
                </el-dropdown-item>
                <el-dropdown-item command="2">
                  设置为等级2
                </el-dropdown-item>
                <el-dropdown-item command="3">
                  设置为等级3
                </el-dropdown-item>
                <el-dropdown-item command="4">
                  设置为等级4
                </el-dropdown-item>
                <el-dropdown-item command="5">
                  设置为等级5
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>
      
      <el-table 
        v-loading="loading" 
        :data="users" 
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
          prop="username"
          label="用户名"
        />
        <el-table-column
          prop="email"
          label="邮箱"
        />
        <el-table-column
          prop="nickname"
          label="昵称"
        />
        <el-table-column
          prop="level"
          label="等级"
          width="100"
        >
          <template #default="scope">
            <el-tag :type="getLevelTagType(scope.row.level)">
              等级{{ scope.row.level }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="userType"
          label="用户类型"
          width="120"
        >
          <template #default="scope">
            <el-tag :type="getUserTypeTagType(scope.row.userType)">
              {{ getUserTypeLabel(scope.row.userType) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="status"
          label="状态"
          width="100"
        >
          <template #default="scope">
            <el-tag :type="scope.row.status === 1 ? 'success' : 'danger'">
              {{ scope.row.status === 1 ? '正常' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          label="操作"
          width="400"
        >
          <template #default="scope">
            <el-button
              size="small"
              @click="editUser(scope.row)"
            >
              编辑
            </el-button>
            <el-dropdown @command="(level) => handleSetUserLevel(scope.row, level)">
              <el-button
                size="small"
                type="primary"
              >
                等级设置<el-icon class="el-icon--right">
                  <arrow-down />
                </el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item :command="1">
                    设置为等级1
                  </el-dropdown-item>
                  <el-dropdown-item :command="2">
                    设置为等级2
                  </el-dropdown-item>
                  <el-dropdown-item :command="3">
                    设置为等级3
                  </el-dropdown-item>
                  <el-dropdown-item :command="4">
                    设置为等级4
                  </el-dropdown-item>
                  <el-dropdown-item :command="5">
                    设置为等级5
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
            <el-button
              size="small"
              :type="scope.row.status === 1 ? 'danger' : 'success'"
              @click="handleToggleUserStatus(scope.row)"
            >
              {{ scope.row.status === 1 ? '禁用' : '启用' }}
            </el-button>
            <el-button
              size="small"
              type="warning"
              @click="handleResetPassword(scope.row)"
            >
              重置密码
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

    <!-- 添加/编辑用户对话框 -->
    <el-dialog
      v-model="showAddDialog"
      :title="isEditing ? '编辑用户' : '添加用户'"
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
              label="用户名"
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
              label="昵称"
              prop="nickname"
            >
              <el-input v-model="addUserForm.nickname" />
            </el-form-item>
          </el-col>
        </el-row>
        
        <el-row :gutter="20">
          <el-col :span="12">
            <el-form-item
              label="邮箱"
              prop="email"
            >
              <el-input v-model="addUserForm.email" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              label="手机号"
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
              label="密码"
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
                  密码需要至少8位，包含大写字母、小写字母、数字和特殊字符
                </el-text>
              </div>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item
              label="确认密码"
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
          <el-col :span="8">
            <el-form-item
              label="用户类型"
              prop="userType"
            >
              <el-select
                v-model="addUserForm.userType"
                style="width: 100%"
              >
                <el-option
                  label="普通用户"
                  value="user"
                />
                <el-option
                  label="管理员"
                  value="admin"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item
              label="等级"
              prop="level"
            >
              <el-select
                v-model="addUserForm.level"
                placeholder="请选择等级"
                style="width: 100%"
              >
                <el-option
                  label="等级1"
                  :value="1"
                />
                <el-option
                  label="等级2"
                  :value="2"
                />
                <el-option
                  label="等级3"
                  :value="3"
                />
                <el-option
                  label="等级4"
                  :value="4"
                />
                <el-option
                  label="等级5"
                  :value="5"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="8">
            <el-form-item
              label="状态"
              prop="status"
            >
              <el-select
                v-model="addUserForm.status"
                style="width: 100%"
              >
                <el-option
                  label="正常"
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
      </el-form>
      
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="cancelAddUser">
            取消
          </el-button>
          <el-button
            type="primary"
            :loading="addUserLoading"
            @click="submitAddUser"
          >
            {{ isEditing ? '更新' : '创建' }}
          </el-button>
        </div>
      </template>
    </el-dialog>

    <!-- 重置密码对话框 -->
    <el-dialog
      v-model="showResetPasswordDialog"
      title="重置用户密码"
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
          <el-form-item label="用户名">
            <el-input 
              v-model="resetPasswordForm.username" 
              disabled
              style="width: 100%;"
            />
          </el-form-item>
        </el-form>
        
        <div style="margin: 20px 0;">
          <el-text type="info">
            点击下方按钮将为用户 <strong>{{ resetPasswordForm.username }}</strong> 生成一个符合安全策略的新密码
          </el-text>
        </div>
        
        <div style="margin: 20px 0;">
          <el-text
            size="small"
            type="warning"
          >
            生成的密码将包含大写字母、小写字母、数字和特殊字符，长度为12位
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
          title="密码重置成功"
          sub-title="已为用户生成新密码，请复制并安全保管"
        >
          <template #extra>
            <div style="margin: 20px 0;">
              <el-text
                type="info"
                style="display: block; margin-bottom: 10px;"
              >
                新密码：
              </el-text>
              <el-input
                v-model="generatedPassword"
                readonly
                style="width: 300px; font-family: monospace; font-size: 16px;"
              >
                <template #append>
                  <el-button @click="copyPassword">
                    复制
                  </el-button>
                </template>
              </el-input>
            </div>
            <div style="margin: 20px 0;">
              <el-text
                size="small"
                type="warning"
              >
                请立即将密码告知用户，并建议用户首次登录后修改密码
              </el-text>
            </div>
          </template>
        </el-result>
      </div>
      
      <template #footer>
        <div class="dialog-footer">
          <el-button @click="cancelResetPassword">
            {{ generatedPassword ? '关闭' : '取消' }}
          </el-button>
          <el-button 
            v-if="!generatedPassword"
            type="danger" 
            :loading="resetPasswordLoading"
            @click="confirmResetPassword"
          >
            生成新密码
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
import { 
  getUserList, 
  createUser, 
  toggleUserStatus, 
  updateUser, 
  batchDeleteUsers,
  batchUpdateUserStatus,
  batchUpdateUserLevel,
  updateUserLevel,
  resetUserPassword
} from '@/api/admin'

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

// 添加用户表单
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
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { min: 3, max: 20, message: '用户名长度在 3 到 20 个字符', trigger: 'blur' }
  ],
  nickname: [
    // 昵称不是必填项
  ],
  email: [
    { required: true, message: '请输入邮箱', trigger: 'blur' },
    { type: 'email', message: '请输入正确的邮箱地址', trigger: 'blur' }
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 8, message: '密码长度不能少于8位', trigger: 'blur' }
  ],
  confirmPassword: [
    { required: true, message: '请再次输入密码', trigger: 'blur' },
    {
      validator: (rule, value, callback) => {
        if (value !== addUserForm.password) {
          callback(new Error('两次输入的密码不一致'))
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
    ElMessage.error('加载用户列表失败')
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
    ElMessage.warning('请选择要删除的用户')
    return
  }

  try {
    await ElMessageBox.confirm(
      `确定删除选中的 ${multipleSelection.value.length} 个用户吗？此操作不可撤销。`,
      '批量删除确认',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    const userIds = multipleSelection.value.map(user => user.id)
    await batchDeleteUsers(userIds)
    ElMessage.success('批量删除成功')
    await loadUsers()
    multipleSelection.value = []
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('批量删除失败')
    }
  }
}

// 批量启用
const handleBatchEnable = async () => {
  if (multipleSelection.value.length === 0) {
    ElMessage.warning('请选择要启用的用户')
    return
  }

  try {
    await ElMessageBox.confirm(
      `确定启用选中的 ${multipleSelection.value.length} 个用户吗？`,
      '批量启用确认',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    const userIds = multipleSelection.value.map(user => user.id)
    await batchUpdateUserStatus(userIds, 1)
    ElMessage.success('批量启用成功')
    await loadUsers()
    multipleSelection.value = []
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('批量启用失败')
    }
  }
}

// 批量禁用
const handleBatchDisable = async () => {
  if (multipleSelection.value.length === 0) {
    ElMessage.warning('请选择要禁用的用户')
    return
  }

  try {
    await ElMessageBox.confirm(
      `确定禁用选中的 ${multipleSelection.value.length} 个用户吗？禁用后用户将无法登录。`,
      '批量禁用确认',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    const userIds = multipleSelection.value.map(user => user.id)
    await batchUpdateUserStatus(userIds, 0)
    ElMessage.success('批量禁用成功')
    await loadUsers()
    multipleSelection.value = []
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('批量禁用失败')
    }
  }
}

// 批量设置等级命令处理
const handleBatchLevelCommand = async (level) => {
  if (multipleSelection.value.length === 0) {
    ElMessage.warning('请选择要设置等级的用户')
    return
  }

  try {
    await ElMessageBox.confirm(
      `确定将选中的 ${multipleSelection.value.length} 个用户的等级设置为等级${level}吗？`,
      '批量设置等级确认',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    const userIds = multipleSelection.value.map(user => user.id)
    await batchUpdateUserLevel(userIds, parseInt(level))
    ElMessage.success(`批量设置用户等级为等级${level}成功`)
    await loadUsers()
    multipleSelection.value = []
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('批量设置等级失败')
    }
  }
}

// 设置单个用户等级
const handleSetUserLevel = async (user, level) => {
  try {
    await ElMessageBox.confirm(
      `确定将用户 "${user.username}" 的等级设置为等级${level}吗？`,
      '设置用户等级确认',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    await updateUserLevel(user.id, parseInt(level))
    ElMessage.success(`设置用户等级为等级${level}成功`)
    await loadUsers()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('设置用户等级失败')
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
    'user': '普通用户',
    'admin': '管理员'
  }
  return labelMap[userType] || '未知'
}

// 获取用户类型标签样式
const getUserTypeTagType = (userType) => {
  const typeMap = {
    'user': '',
    'admin': 'danger'
  }
  return typeMap[userType] || ''
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
      ElMessage.success('用户更新成功')
    } else {
      await createUser(userData)
      ElMessage.success('用户创建成功')
    }
    
    showAddDialog.value = false
    isEditing.value = false
    await loadUsers()
    cancelAddUser()
  } catch (error) {
    ElMessage.error(isEditing.value ? '用户更新失败' : '用户创建失败')
  } finally {
    addUserLoading.value = false
  }
}

// 切换用户状态
const handleToggleUserStatus = async (user) => {
  const action = user.status === 1 ? '禁用' : '启用'
  try {
    await ElMessageBox.confirm(
      `确定${action}用户 "${user.username}" 吗？${user.status === 1 ? '禁用后用户将无法登录。' : ''}`,
      `${action}用户`,
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      }
    )
    
    await toggleUserStatus(user.id, user.status === 1 ? 0 : 1)
    ElMessage.success(`${action}成功`)
    await loadUsers()
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error(`${action}失败`)
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
    ElMessage.success('密码重置成功')
    
    // 重新加载用户列表
    await loadUsers()
    
  } catch (error) {
    ElMessage.error('重置密码失败：' + (error.response?.data?.message || error.message))
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
    ElMessage.warning('没有可复制的密码')
    return
  }
  
  try {
    // 优先使用 Clipboard API
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(generatedPassword.value)
      ElMessage.success('密码已复制到剪贴板')
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
        ElMessage.success('密码已复制到剪贴板')
      } else {
        throw new Error('execCommand failed')
      }
    } finally {
      document.body.removeChild(textArea)
    }
  } catch (error) {
    console.error('复制失败:', error)
    ElMessage.error('复制失败，请手动复制')
  }
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

<style scoped>
.users-container {
  padding: 20px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.toolbar {
  margin-bottom: 20px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.search-section {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.batch-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px;
  background-color: #f5f7fa;
  border-radius: 4px;
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
}
</style>
