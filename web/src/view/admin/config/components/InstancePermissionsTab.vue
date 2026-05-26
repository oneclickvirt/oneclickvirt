<template>
  <el-form
    v-loading="loading"
    :model="permissions"
    label-width="180px"
    class="config-form"
  >
    <el-alert
      :title="$t('admin.config.instancePermissionsDesc')"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    >
      {{ $t('admin.config.instancePermissionsHint') }}
    </el-alert>

    <!-- 创建权限 -->
    <el-divider content-position="left">
      <el-icon><Plus /></el-icon> {{ $t('admin.config.createPermissions') }}
    </el-divider>
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.containerCreateMinLevel')">
          <el-select
            v-model="permissions.minLevelForContainer"
            :placeholder="$t('admin.config.selectLevel')"
            style="width: 100%"
          >
            <el-option
              v-for="level in [1, 2, 3, 4, 5]"
              :key="level"
              :label="$t('admin.config.levelN', { level })"
              :value="level"
            />
          </el-select>
          <div class="form-item-hint">
            {{ $t('admin.config.containerCreateHint') }}
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.vmCreateMinLevel')">
          <el-select
            v-model="permissions.minLevelForVM"
            :placeholder="$t('admin.config.selectLevel')"
            style="width: 100%"
          >
            <el-option
              v-for="level in [1, 2, 3, 4, 5]"
              :key="level"
              :label="$t('admin.config.levelN', { level })"
              :value="level"
            />
          </el-select>
          <div class="form-item-hint">
            {{ $t('admin.config.vmCreateHint') }}
          </div>
        </el-form-item>
      </el-col>
    </el-row>

    <!-- 删除权限 -->
    <el-divider content-position="left">
      <el-icon><Delete /></el-icon> {{ $t('admin.config.deletePermissions') }}
    </el-divider>
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.containerDeleteMinLevel')">
          <el-select
            v-model="permissions.minLevelForDeleteContainer"
            :placeholder="$t('admin.config.selectLevel')"
            style="width: 100%"
          >
            <el-option
              v-for="level in [1, 2, 3, 4, 5]"
              :key="level"
              :label="$t('admin.config.levelN', { level })"
              :value="level"
            />
          </el-select>
          <div class="form-item-hint">
            {{ $t('admin.config.containerDeleteHint') }}
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.vmDeleteMinLevel')">
          <el-select
            v-model="permissions.minLevelForDeleteVM"
            :placeholder="$t('admin.config.selectLevel')"
            style="width: 100%"
          >
            <el-option
              v-for="level in [1, 2, 3, 4, 5]"
              :key="level"
              :label="$t('admin.config.levelN', { level })"
              :value="level"
            />
          </el-select>
          <div class="form-item-hint">
            {{ $t('admin.config.vmDeleteHint') }}
          </div>
        </el-form-item>
      </el-col>
    </el-row>

    <!-- 重置系统权限 -->
    <el-divider content-position="left">
      <el-icon><Refresh /></el-icon> {{ $t('admin.config.resetPermissions') }}
    </el-divider>
    <el-row :gutter="20">
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.containerResetMinLevel')">
          <el-select
            v-model="permissions.minLevelForResetContainer"
            :placeholder="$t('admin.config.selectLevel')"
            style="width: 100%"
          >
            <el-option
              v-for="level in [1, 2, 3, 4, 5]"
              :key="level"
              :label="$t('admin.config.levelN', { level })"
              :value="level"
            />
          </el-select>
          <div class="form-item-hint">
            {{ $t('admin.config.containerResetHint') }}
          </div>
        </el-form-item>
      </el-col>
      <el-col :span="12">
        <el-form-item :label="$t('admin.config.vmResetMinLevel')">
          <el-select
            v-model="permissions.minLevelForResetVM"
            :placeholder="$t('admin.config.selectLevel')"
            style="width: 100%"
          >
            <el-option
              v-for="level in [1, 2, 3, 4, 5]"
              :key="level"
              :label="$t('admin.config.levelN', { level })"
              :value="level"
            />
          </el-select>
          <div class="form-item-hint">
            {{ $t('admin.config.vmResetHint') }}
          </div>
        </el-form-item>
      </el-col>
    </el-row>

    <el-alert
      :title="$t('admin.config.permissionsSuggestions')"
      type="warning"
      :closable="false"
      show-icon
      style="margin-top: 20px;"
    >
      <ul style="margin: 0; padding-left: 20px;">
        <li>{{ $t('admin.config.containerCreateSuggestion') }}</li>
        <li>{{ $t('admin.config.vmCreateSuggestion') }}</li>
        <li>{{ $t('admin.config.containerDeleteResetSuggestion') }}</li>
        <li>{{ $t('admin.config.vmDeleteResetSuggestion') }}</li>
      </ul>
    </el-alert>
  </el-form>
</template>

<script setup>
defineProps({
  permissions: { type: Object, required: true },
  loading: { type: Boolean, default: false }
})
</script>

<style scoped>
.config-form {
  max-height: 600px;
  overflow-y: auto;
}
.form-item-hint {
  font-size: 12px;
  color: var(--text-color-tertiary);
  margin-top: 4px;
  line-height: 1.4;
}
</style>
