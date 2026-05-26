<template>
  <el-dialog
    :model-value="visible"
    :title="isEditing ? $t('admin.users.editUser') : $t('admin.users.addUser')"
    width="600px"
    @close="$emit('cancel')"
    @update:model-value="$emit('update:visible', $event)"
  >
    <el-form
      ref="formRef"
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
                :label="$t('admin.users.normalAdmin')"
                value="normal_admin"
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
        <el-button @click="$emit('cancel')">
          {{ $t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="loading"
          @click="$emit('submit')"
        >
          {{ isEditing ? $t('common.save') : $t('common.create') }}
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref } from 'vue'

defineProps({
  visible: { type: Boolean, default: false },
  isEditing: { type: Boolean, default: false },
  addUserForm: { type: Object, required: true },
  addUserRules: { type: Object, required: true },
  loading: { type: Boolean, default: false }
})

defineEmits(['update:visible', 'cancel', 'submit'])

const formRef = ref(null)

defineExpose({
  validate: (...args) => formRef.value?.validate(...args),
  resetFields: () => formRef.value?.resetFields()
})
</script>

<style scoped>
.password-hint {
  margin-top: 5px;
  font-size: 12px;
  line-height: 1.4;
}

.dialog-footer {
  text-align: right;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}
</style>
