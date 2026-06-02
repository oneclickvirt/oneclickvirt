import { ref, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { copyToClipboard } from '@/utils/clipboard'
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
  setUserExpiry
} from '@/api/admin'
import { adminLoginAsUser } from '@/api/features'
import { containsUnsafeUsernameContent } from '@/utils/validate'
import { getAdminConfig } from '@/api/config'
import { getSortedLevelKeys, getLevelTagType } from '@/utils/levels'

export function useUserManagement() {
  const { t, locale } = useI18n()

  const validateUsernameSafety = (rule, value, callback) => {
    if (!value) {
      callback()
      return
    }

    if (containsUnsafeUsernameContent(value)) {
      callback(new Error(t('validation.usernameUnsafe')))
      return
    }

    callback()
  }

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
  const freezeLoading = ref(false)
  const freezeForm = reactive({
    userId: null,
    username: '',
    expiresAt: null
  })

  // 搜索相关
  const searchUsername = ref('')
  const searchStatus = ref(null)
  const searchUserType = ref('')

  // 批量选择相关
  const multipleSelection = ref([])
  const availableLevels = ref([1, 2, 3, 4, 5])

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
      { min: 3, max: 20, message: t('validation.usernameLength', { min: 3, max: 20 }), trigger: 'blur' },
      { validator: validateUsernameSafety, trigger: 'blur' }
    ],
    nickname: [],
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

  const loadLevelOptions = async () => {
    try {
      const response = await getAdminConfig()
      const levels = getSortedLevelKeys(response.data?.quota?.levelLimits || {})
      if (levels.length > 0) {
        availableLevels.value = levels
        if (!levels.includes(Number(addUserForm.level))) {
          addUserForm.level = levels[0]
        }
      }
    } catch (error) {
      availableLevels.value = [1, 2, 3, 4, 5]
    }
  }

  const handleSearch = () => {
    currentPage.value = 1
    loadUsers()
  }

  const resetFilters = () => {
    searchUsername.value = ''
    searchStatus.value = null
    searchUserType.value = ''
    currentPage.value = 1
    loadUsers()
  }

  // 批量操作
  const handleSelectionChange = (selection) => {
    multipleSelection.value = selection
  }

  const handleBatchDelete = async () => {
    if (multipleSelection.value.length === 0) {
      ElMessage.warning(t('admin.users.batchDelete'))
      return
    }
    try {
      await ElMessageBox.confirm(t('admin.users.confirmBatchDelete'), t('common.confirm'), {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      })
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

  const handleBatchEnable = async () => {
    if (multipleSelection.value.length === 0) {
      ElMessage.warning(t('admin.users.batchEnable'))
      return
    }
    try {
      await ElMessageBox.confirm(
        t('admin.users.confirmToggleStatus', { action: t('admin.users.enable') }),
        t('common.confirm'),
        { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' }
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

  const handleBatchDisable = async () => {
    if (multipleSelection.value.length === 0) {
      ElMessage.warning(t('admin.users.batchDisable'))
      return
    }
    try {
      await ElMessageBox.confirm(
        t('admin.users.confirmToggleStatus', { action: t('admin.users.disable') }),
        t('common.confirm'),
        { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' }
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

  const handleBatchLevelCommand = async (level) => {
    if (multipleSelection.value.length === 0) {
      ElMessage.warning(t('admin.users.batchSetLevel'))
      return
    }
    try {
      await ElMessageBox.confirm(t('admin.users.confirmBatchDelete'), t('common.confirm'), {
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      })
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

  const handleSetUserLevel = async (user, level) => {
    try {
      await ElMessageBox.confirm(
        t('admin.users.confirmToggleStatus', { action: t('admin.users.setToLevel', { level }) }),
        t('common.confirm'),
        { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' }
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

  // 格式化/标签辅助函数
  const getUserTypeLabel = (userType) => {
    const labelMap = {
      'user': t('admin.users.normalUser'),
      'normal_admin': t('admin.users.normalAdmin'),
      'admin': t('admin.users.adminUser')
    }
    return labelMap[userType] || t('common.unknown')
  }

  const getUserTypeTagType = (userType) => {
    const typeMap = { 'user': '', 'normal_admin': 'warning', 'admin': 'danger' }
    return typeMap[userType] || ''
  }

  // 新增/编辑用户
  const handleAddUser = () => {
    isEditing.value = false
    cancelAddUser()
    addUserForm.level = availableLevels.value[0] || 1
    showAddDialog.value = true
  }

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
      level: availableLevels.value[0] || 1,
      totalQuota: 0,
      status: 1
    })
  }

  const submitAddUser = async () => {
    if (!addUserFormRef.value) return
    try {
      await addUserFormRef.value.validate()
      addUserLoading.value = true
      const userData = { ...addUserForm }
      delete userData.confirmPassword
      if (isEditing.value) {
        if (!userData.password) delete userData.password
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

  const handleToggleUserStatus = async (user) => {
    const action = user.status === 1 ? t('admin.users.disable') : t('admin.users.enable')
    try {
      await ElMessageBox.confirm(
        t('admin.users.confirmToggleStatus', { action }),
        t('common.confirm'),
        { confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel'), type: 'warning' }
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
      generatedPassword.value = response.data.newPassword
      ElMessage.success(t('admin.users.resetPasswordSuccess'))
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

  // 以用户身份登录
  const handleLoginAsUser = async (user) => {
    try {
      await ElMessageBox.confirm(
        t('admin.users.loginAsConfirm', { username: user.username }),
        t('common.confirm'),
        { type: 'warning' }
      )
      const response = await adminLoginAsUser(user.id)
      if (response.code === 200) {
        const token = response.data.token
        const url = window.location.origin + window.location.pathname + '#/user/dashboard'
        const newTab = window.open(url, '_blank')
        if (newTab) {
          newTab.addEventListener('load', () => {
            newTab.sessionStorage.setItem('token', token)
            newTab.sessionStorage.setItem('userType', 'user')
            newTab.sessionStorage.setItem('viewMode', 'user')
            newTab.location.reload()
          })
        }
        ElMessage.success(t('admin.users.loginAsSuccess'))
      }
    } catch (error) {
      if (error === 'cancel') return
      ElMessage.error(error.response?.data?.message || error.message || t('common.operationFailed'))
    }
  }

  // 复制密码到剪贴板
  const copyPassword = async () => {
    if (!generatedPassword.value) {
      ElMessage.warning(t('user.profile.noPasswordToCopy'))
      return
    }
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(generatedPassword.value)
        ElMessage.success(t('user.profile.passwordCopied'))
        return
      }
      const textArea = document.createElement('textarea')
      textArea.value = generatedPassword.value
      textArea.style.position = 'fixed'
      textArea.style.left = '-999999px'
      textArea.style.top = '-999999px'
      document.body.appendChild(textArea)
      textArea.focus()
      textArea.select()
      try {
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

  // 设置过期时间
  const handleSetExpiry = (user) => {
    freezeForm.userId = user.id
    freezeForm.username = user.username
    freezeForm.expiresAt = user.expiresAt || null
    showSetExpiryDialog.value = true
  }

  const confirmSetExpiry = async () => {
    try {
      freezeLoading.value = true
      await setUserExpiry({ userId: freezeForm.userId, expiresAt: freezeForm.expiresAt })
      ElMessage.success(t('admin.users.setExpirySuccess'))
      showSetExpiryDialog.value = false
      await loadUsers()
    } catch (error) {
      ElMessage.error(t('admin.users.setExpiryFailed'))
    } finally {
      freezeLoading.value = false
    }
  }

  const formatDateTime = (dateTimeStr) => {
    if (!dateTimeStr) return '-'
    const date = new Date(dateTimeStr)
    return date.toLocaleString(locale.value, {
      year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'
    })
  }

  const isExpired = (dateTimeStr) => {
    if (!dateTimeStr) return false
    return new Date(dateTimeStr) < new Date()
  }

  const handleSizeChange = (size) => {
    pageSize.value = size
    currentPage.value = 1
    loadUsers()
  }

  const handleCurrentChange = (page) => {
    currentPage.value = page
    loadUsers()
  }

  return {
    // State
    users, loading, showAddDialog, currentUser, saving,
    addUserLoading, addUserFormRef, isEditing,
    showResetPasswordDialog, resetPasswordForm, resetPasswordLoading, generatedPassword,
    showSetExpiryDialog, freezeLoading, freezeForm,
    searchUsername, searchStatus, searchUserType,
    multipleSelection, currentPage, pageSize, total, availableLevels,
    addUserForm, addUserRules,
    // Methods
    loadUsers, loadLevelOptions, handleSearch, resetFilters,
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
  }
}
