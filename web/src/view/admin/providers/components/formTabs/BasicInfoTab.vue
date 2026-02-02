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
    </el-form-item>
    <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.hostTip') }}
      </el-text>
    </div>
    <el-form-item
      :label="$t('admin.providers.portIP')"
      prop="portIP"
    >
      <el-input
        v-model="modelValue.portIP"
        :placeholder="$t('admin.providers.portIPPlaceholder')"
      />
    </el-form-item>
    <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.portIPTip') }}
      </el-text>
    </div>
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

    <!-- 节点模式选择 -->
    <el-form-item
      :label="$t('admin.providers.nodeMode')"
      prop="discoverMode"
    >
      <el-radio-group v-model="modelValue.discoverMode">
        <el-radio :label="false">{{ $t('admin.providers.cleanNode') }}</el-radio>
        <el-radio :label="true">{{ $t('admin.providers.nodeWithInstances') }}</el-radio>
      </el-radio-group>
    </el-form-item>
    <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.nodeModeTip') }}
      </el-text>
    </div>

    <!-- 发现模式配置 - 仅在选择"有实例的节点"时显示 -->
    <template v-if="modelValue.discoverMode">
      <el-form-item
        :label="$t('admin.providers.autoImport')"
        prop="autoImport"
      >
        <el-switch
          v-model="modelValue.autoImport"
          :active-text="$t('common.enabled')"
          :inactive-text="$t('common.disabled')"
        />
      </el-form-item>
      <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.autoImportTip') }}
        </el-text>
      </div>

      <el-form-item
        v-if="modelValue.autoImport"
        :label="$t('admin.providers.autoAdjustQuota')"
        prop="autoAdjustQuota"
      >
        <el-switch
          v-model="modelValue.autoAdjustQuota"
          :active-text="$t('common.enabled')"
          :inactive-text="$t('common.disabled')"
        />
      </el-form-item>
      <div v-if="modelValue.autoImport" class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.autoAdjustQuotaTip') }}
        </el-text>
      </div>

      <el-form-item
        v-if="modelValue.autoImport"
        :label="$t('admin.providers.importedInstanceOwner')"
        prop="importedInstanceOwner"
      >
        <el-input
          v-model="modelValue.importedInstanceOwner"
          :placeholder="$t('admin.providers.importedInstanceOwnerPlaceholder')"
        />
      </el-form-item>
      <div v-if="modelValue.autoImport" class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.importedInstanceOwnerTip') }}
        </el-text>
      </div>
    </template>

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
    </el-form-item>
    <div class="form-tip" style="margin-top: -10px; margin-bottom: 15px; margin-left: 120px;">
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.architectureTip') }}
      </el-text>
    </div>
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
