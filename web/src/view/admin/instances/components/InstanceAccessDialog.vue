<template>
  <el-dialog
    :model-value="modelValue"
    :title="t('admin.instances.editAccess')"
    width="520px"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
  >
    <el-form
      v-if="instance"
      label-position="top"
      @submit.prevent="save"
    >
      <el-form-item :label="t('admin.instances.sshHost')">
        <el-input
          v-model="form.sshHost"
          :placeholder="t('admin.instances.sshHostPlaceholder')"
          autocomplete="off"
        />
      </el-form-item>
      <el-form-item :label="t('admin.instances.sshPort')">
        <el-input-number
          v-model="form.sshPort"
          :min="1"
          :max="65535"
          controls-position="right"
          style="width: 100%;"
        />
      </el-form-item>
      <el-form-item :label="t('admin.instances.username')">
        <el-input
          v-model="form.username"
          autocomplete="username"
        />
      </el-form-item>
      <el-form-item :label="t('admin.instances.password')">
        <el-input
          v-model="form.password"
          type="password"
          show-password
          :placeholder="t('admin.instances.keepExistingSecret')"
          autocomplete="new-password"
        />
        <el-checkbox v-model="clearPassword">
          {{ t('admin.instances.clearPassword') }}
        </el-checkbox>
      </el-form-item>
      <el-form-item :label="t('admin.instances.sshKey')">
        <el-input
          v-model="form.sshKey"
          type="textarea"
          :rows="5"
          :placeholder="t('admin.instances.keepExistingSecret')"
          autocomplete="off"
        />
        <div class="secret-row">
          <span v-if="instance.hasSshKey">{{ t('admin.instances.sshKeyConfigured') }}</span>
          <el-checkbox v-model="clearSSHKey">
            {{ t('admin.instances.clearSSHKey') }}
          </el-checkbox>
        </div>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">
        {{ t('common.cancel') }}
      </el-button>
      <el-button
        type="primary"
        :loading="saving"
        @click="save"
      >
        {{ t('common.confirm') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { updateInstance } from '@/api/admin'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  instance: { type: Object, default: null }
})
const emit = defineEmits(['update:modelValue', 'updated'])
const { t } = useI18n()
const saving = ref(false)
const clearPassword = ref(false)
const clearSSHKey = ref(false)
const form = reactive({ sshHost: '', sshPort: 22, username: '', password: '', sshKey: '' })

const resetForm = () => {
  const source = props.instance || {}
  form.sshHost = source.sshHost || ''
  form.sshPort = Number(source.sshPort) || 22
  form.username = source.username || ''
  form.password = ''
  form.sshKey = ''
  clearPassword.value = false
  clearSSHKey.value = false
}

watch(() => [props.modelValue, props.instance], ([visible]) => {
  if (visible) resetForm()
}, { immediate: true, deep: false })

const save = async () => {
  if (!props.instance?.id || saving.value) return
  const port = Number(form.sshPort)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    ElMessage.error(t('admin.instances.sshPortInvalid'))
    return
  }
  if (form.password && clearPassword.value) {
    ElMessage.error(t('admin.instances.secretConflict'))
    return
  }
  if (form.sshKey && clearSSHKey.value) {
    ElMessage.error(t('admin.instances.secretConflict'))
    return
  }

  const payload = {
    sshHost: form.sshHost.trim(),
    sshPort: port,
    username: form.username.trim()
  }
  if (form.password) payload.password = form.password
  if (clearPassword.value) payload.password = ''
  if (form.sshKey) payload.sshKey = form.sshKey
  if (clearSSHKey.value) payload.sshKey = ''

  saving.value = true
  try {
    await updateInstance(props.instance.id, payload)
    ElMessage.success(t('admin.instances.editAccessSuccess'))
    emit('update:modelValue', false)
    emit('updated')
  } catch (error) {
    ElMessage.error(error?.fullMessage || error?.userMessage || error?.message || t('admin.instances.editAccessFailed'))
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.secret-row {
  display: flex;
  width: 100%;
  justify-content: space-between;
  align-items: center;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
