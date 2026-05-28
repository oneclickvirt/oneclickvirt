<template>
  <el-table
    v-loading="loading"
    :data="users"
    class="users-table"
    :cell-style="{ padding: '12px 0' }"
    :header-cell-style="{ background: '#f5f7fa', padding: '14px 0', fontWeight: '600' }"
    @selection-change="$emit('selection-change', $event)"
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
          <div
            v-if="scope.row.isManualExpiry"
            style="margin-top: 4px;"
          >
            <el-tag
              size="small"
              type="info"
            >
              {{ $t('admin.users.manualExpiry') }}
            </el-tag>
          </div>
        </div>
        <span v-else>-</span>
      </template>
    </el-table-column>
    <el-table-column
      :label="$t('common.actions')"
      width="420"
      fixed="right"
      align="center"
    >
      <template #default="scope">
        <div class="action-buttons">
          <el-button
            size="small"
            @click="$emit('edit', scope.row)"
          >
            {{ $t('common.edit') }}
          </el-button>
          <el-dropdown @command="(level) => $emit('set-user-level', scope.row, level)">
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
          <el-button
            size="small"
            type="warning"
            @click="$emit('set-expiry', scope.row)"
          >
            {{ $t('admin.users.setExpiry') }}
          </el-button>
          <el-button
            size="small"
            :type="scope.row.status === 1 ? 'danger' : 'success'"
            @click="$emit('toggle-status', scope.row)"
          >
            {{ scope.row.status === 1 ? $t('admin.users.disable') : $t('admin.users.enable') }}
          </el-button>
          <el-button
            size="small"
            type="warning"
            @click="$emit('reset-password', scope.row)"
          >
            {{ $t('admin.users.resetPassword') }}
          </el-button>
          <el-button
            v-if="scope.row.userType !== 'admin'"
            size="small"
            type="info"
            @click="$emit('login-as', scope.row)"
          >
            {{ $t('admin.users.loginAs') }}
          </el-button>
        </div>
      </template>
    </el-table-column>
  </el-table>
</template>

<script setup>
import { useI18n } from 'vue-i18n'

defineProps({
  users: { type: Array, default: () => [] },
  loading: { type: Boolean, default: false }
})

defineEmits(['selection-change', 'edit', 'set-user-level', 'set-expiry', 'toggle-status', 'reset-password', 'login-as'])

const { t, locale } = useI18n()

const getLevelTagType = (level) => {
  const typeMap = { 1: '', 2: 'success', 3: 'info', 4: 'warning', 5: 'danger' }
  return typeMap[level] || ''
}

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
</script>
