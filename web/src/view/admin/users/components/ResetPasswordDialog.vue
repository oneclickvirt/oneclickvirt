<template>
  <el-dialog
    :model-value="visible"
    :title="$t('admin.users.resetPassword')"
    width="600px"
    @close="$emit('cancel')"
    @update:model-value="$emit('update:visible', $event)"
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
              :model-value="generatedPassword"
              readonly
              style="width: 300px; font-family: monospace; font-size: 16px;"
            >
              <template #append>
                <el-button @click="$emit('copy-password')">
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
        <el-button @click="$emit('cancel')">
          {{ generatedPassword ? $t('common.close') : $t('common.cancel') }}
        </el-button>
        <el-button
          v-if="!generatedPassword"
          type="danger"
          :loading="loading"
          @click="$emit('confirm')"
        >
          {{ $t('admin.users.resetPassword') }}
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { useI18n } from 'vue-i18n'
const { t } = useI18n()

defineProps({
  visible: { type: Boolean, default: false },
  resetPasswordForm: { type: Object, required: true },
  generatedPassword: { type: String, default: '' },
  loading: { type: Boolean, default: false }
})

defineEmits(['update:visible', 'cancel', 'confirm', 'copy-password'])
</script>

<style scoped>
.dialog-footer {
  text-align: right;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}
</style>
