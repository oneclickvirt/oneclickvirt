<template>
  <span class="instance-alias">
    <span class="instance-alias-heading">
      <span class="instance-alias-title">{{ alias || name }}</span>
      <el-button
        v-if="editable && instanceId"
        type="primary"
        link
        :icon="EditPen"
        :loading="saving"
        :title="t('user.instances.editAlias')"
        :aria-label="t('user.instances.editAlias')"
        @click.stop="editAlias"
      />
    </span>
    <small
      v-if="alias"
      class="instance-original-name"
    >{{ t('user.instances.originalName') }}: {{ name }}</small>
  </span>
</template>

<script setup>
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { EditPen } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { updateAdminInstanceAlias } from '@/api/admin/instances'
import { updateUserInstanceAlias } from '@/api/user'

const props = defineProps({
  instanceId: { type: Number, required: true },
  name: { type: String, default: '' },
  alias: { type: String, default: '' },
  admin: { type: Boolean, default: false },
  editable: { type: Boolean, default: true }
})
const emit = defineEmits(['updated'])
const { t } = useI18n()
const saving = ref(false)

const editAlias = async () => {
  if (saving.value) return
  try {
    await ElMessageBox.prompt(t('user.instances.aliasHint'), t('user.instances.editAlias'), {
      inputValue: props.alias,
      inputPlaceholder: t('user.instances.aliasPlaceholder'),
      confirmButtonText: t('common.save'),
      cancelButtonText: t('common.cancel'),
      closeOnClickModal: false,
      inputValidator: value => {
        const alias = (value || '').trim()
        return (Array.from(alias).length <= 128 && !/\p{Cc}/u.test(alias)) || t('user.instances.aliasInvalid')
      },
      beforeClose: async (action, dialog, done) => {
        if (saving.value) return
        if (action !== 'confirm') {
          done()
          return
        }
        saving.value = true
        dialog.confirmButtonLoading = true
        try {
          const update = props.admin ? updateAdminInstanceAlias : updateUserInstanceAlias
          const response = await update(props.instanceId, (dialog.inputValue || '').trim())
          emit('updated', response.data.alias)
          ElMessage.success(t('user.instances.aliasSaved'))
          done()
        } catch (error) {
          ElMessage.error(error?.fullMessage || error?.message || t('user.instances.aliasSaveFailed'))
        } finally {
          saving.value = false
          dialog.confirmButtonLoading = false
        }
      }
    })
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') {
      ElMessage.error(error?.message || t('user.instances.aliasSaveFailed'))
    }
  }
}
</script>

<style scoped>
.instance-alias {
  display: inline-flex;
  flex-direction: column;
  max-width: 100%;
  min-width: 0;
  vertical-align: middle;
}

.instance-alias-heading {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.instance-alias-title {
  overflow-wrap: anywhere;
  min-width: 0;
}

.instance-original-name {
  font-size: 12px;
  font-weight: normal;
  color: var(--el-text-color-secondary);
  overflow-wrap: anywhere;
}
</style>
