<template>
  <el-form
    ref="formRef"
    :model="modelValue"
    :rules="rules"
    label-width="120px"
    class="server-form"
  >
    <el-form-item
      :label="$t('admin.providers.serverName')"
      prop="name"
    >
      <el-input
        v-model="modelValue.name"
        :placeholder="$t('admin.providers.serverNamePlaceholder')"
        maxlength="7"
        show-word-limit
      />
    </el-form-item>
    <el-form-item
      :label="$t('admin.providers.serverType')"
      prop="type"
    >
      <el-select
        v-model="modelValue.type"
        :placeholder="$t('admin.providers.serverTypePlaceholder')"
      >
        <el-option
          label="Docker"
          value="docker"
        />
        <el-option
          label="LXD"
          value="lxd"
        />
        <el-option
          label="Incus"
          value="incus"
        />
        <el-option
          label="Proxmox"
          value="proxmox"
        />
      </el-select>
    </el-form-item>
    <el-form-item
      :label="$t('admin.providers.hostAddress')"
      prop="host"
    >
      <el-input
        v-model="modelValue.host"
        :placeholder="$t('admin.providers.hostPlaceholder')"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.hostTip') }}
        </el-text>
      </div>
    </el-form-item>
    <el-form-item
      :label="$t('admin.providers.portIP')"
      prop="portIP"
    >
      <el-input
        v-model="modelValue.portIP"
        :placeholder="$t('admin.providers.portIPPlaceholder')"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.portIPTip') }}
        </el-text>
      </div>
    </el-form-item>
    <el-form-item
      :label="$t('admin.providers.port')"
      prop="port"
    >
      <el-input-number
        v-model="modelValue.port"
        :min="1"
        :max="65535"
        :controls="false"
      />
    </el-form-item>
    <el-form-item
      :label="$t('common.description')"
      prop="description"
    >
      <el-input 
        v-model="modelValue.description" 
        type="textarea" 
        :rows="3"
        :placeholder="$t('admin.providers.descriptionPlaceholder')"
      />
    </el-form-item>
    <el-form-item
      :label="$t('common.status')"
      prop="status"
    >
      <el-select
        v-model="modelValue.status"
        :placeholder="$t('admin.providers.statusPlaceholder')"
      >
        <el-option
          :label="$t('common.enabled')"
          value="active"
        />
        <el-option
          :label="$t('common.disabled')"
          value="inactive"
        />
      </el-select>
    </el-form-item>
    <el-form-item
      :label="$t('admin.providers.architecture')"
      prop="architecture"
    >
      <el-select
        v-model="modelValue.architecture"
        :placeholder="$t('admin.providers.architecturePlaceholder')"
      >
        <el-option
          label="amd64 (x86_64)"
          value="amd64"
        />
        <el-option
          label="arm64 (aarch64)"
          value="arm64"
        />
        <el-option
          label="s390x (IBM Z)"
          value="s390x"
        />
      </el-select>
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.architectureTip') }}
        </el-text>
      </div>
    </el-form-item>
  </el-form>
</template>

<script setup>
import { ref } from 'vue'

defineProps({
  modelValue: {
    type: Object,
    required: true
  },
  rules: {
    type: Object,
    required: true
  }
})

// 暴露表单引用供父组件使用
const formRef = ref()
defineExpose({
  formRef
})
</script>

<style scoped>
.server-form {
  max-height: 500px;
  overflow-y: auto;
  padding-right: 10px;
}

.form-tip {
  margin-top: 5px;
}
</style>
