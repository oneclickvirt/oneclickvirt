<template>
  <el-dialog
    :model-value="visible"
    :title="$t('admin.users.setExpiry')"
    width="500px"
    @update:model-value="$emit('update:visible', $event)"
  >
    <el-form label-width="120px">
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
        <el-button @click="$emit('update:visible', false)">
          {{ $t('common.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :loading="loading"
          @click="$emit('confirm')"
        >
          {{ $t('common.confirm') }}
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
  freezeForm: { type: Object, required: true },
  loading: { type: Boolean, default: false }
})

defineEmits(['update:visible', 'confirm'])
</script>

<style scoped>
.dialog-footer {
  text-align: right;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}
</style>
