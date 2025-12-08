<template>
  <el-form
    :model="modelValue"
    label-width="180px"
    class="server-form"
  >
    <el-alert
      :title="$t('admin.providers.hardwareConfigTip')"
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 20px;"
    />

    <!-- 通用配置（容器和虚拟机都支持） -->
    <el-divider content-position="left">
      <el-text type="primary" size="large">{{ $t('admin.providers.commonConfig') || '通用配置（容器和虚拟机）' }}</el-text>
    </el-divider>

    <!-- 内存交换（容器和虚拟机都支持） -->
    <el-form-item
      :label="$t('admin.providers.containerMemorySwap')"
      prop="containerMemorySwap"
    >
      <el-switch
        v-model="modelValue.containerMemorySwap"
        :active-text="$t('common.enable')"
        :inactive-text="$t('common.disable')"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.containerMemorySwapTip') }}
        </el-text>
      </div>
    </el-form-item>

    <!-- 容器专用配置 -->
    <template v-if="modelValue.containerEnabled">
      <el-divider content-position="left">
        <el-text type="warning" size="large">{{ $t('admin.providers.containerOnlyConfig') || '容器专用配置' }}</el-text>
      </el-divider>

      <!-- 特权模式 -->
      <el-form-item
        :label="$t('admin.providers.containerPrivileged')"
        prop="containerPrivileged"
      >
      <el-switch
        v-model="modelValue.containerPrivileged"
        :active-text="$t('common.enable')"
        :inactive-text="$t('common.disable')"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="warning"
        >
          {{ $t('admin.providers.containerPrivilegedTip') }}
        </el-text>
      </div>
    </el-form-item>

    <!-- 容器嵌套 -->
    <el-form-item
      :label="$t('admin.providers.containerAllowNesting')"
      prop="containerAllowNesting"
    >
      <el-switch
        v-model="modelValue.containerAllowNesting"
        :active-text="$t('common.enable')"
        :inactive-text="$t('common.disable')"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.containerAllowNestingTip') }}
        </el-text>
      </div>
    </el-form-item>

    <!-- CPU限制（容器专用：与limits.cpu互斥） -->
    <el-form-item
      :label="$t('admin.providers.containerCpuAllowance')"
      prop="containerCpuAllowance"
    >
      <el-input
        v-model="modelValue.containerCpuAllowance"
        :placeholder="$t('admin.providers.containerCpuAllowancePlaceholder')"
        style="width: 200px"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="warning"
        >
          {{ $t('admin.providers.containerCpuAllowanceTip') || 'CPU使用率限制，设置为100%等同于不限制。与limits.cpu互斥，优先使用此配置。' }}
        </el-text>
      </div>
    </el-form-item>

    <!-- 最大进程数 -->
    <el-form-item
      :label="$t('admin.providers.containerMaxProcesses')"
      prop="containerMaxProcesses"
    >
      <el-input-number
        v-model="modelValue.containerMaxProcesses"
        :min="0"
        :max="100000"
        :step="100"
        :controls="false"
        :placeholder="$t('admin.providers.containerMaxProcessesPlaceholder')"
        style="width: 200px"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.containerMaxProcessesTip') }}
        </el-text>
      </div>
    </el-form-item>

    <!-- 磁盘IO限制 -->
    <el-form-item
      :label="$t('admin.providers.containerDiskIoLimit')"
      prop="containerDiskIoLimit"
    >
      <el-input
        v-model="modelValue.containerDiskIoLimit"
        :placeholder="$t('admin.providers.containerDiskIoLimitPlaceholder')"
        style="width: 200px"
      />
      <div class="form-tip">
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.containerDiskIoLimitTip') }}
        </el-text>
      </div>
    </el-form-item>
    </template>

    <!-- 虚拟机配置提示 -->
    <template v-if="modelValue.vmEnabled && !modelValue.containerEnabled">
      <el-divider content-position="left">
        <el-text type="info" size="large">{{ $t('admin.providers.vmConfigNote') || '虚拟机配置说明' }}</el-text>
      </el-divider>
      <el-alert
        :title="$t('admin.providers.vmHardwareConfigTip') || '虚拟机支持内存Swap配置，但不支持容器专用的特权模式、嵌套、CPU百分比限制等配置。'"
        type="info"
        :closable="false"
        show-icon
      />
    </template>
  </el-form>
</template>

<script setup>
defineProps({
  modelValue: {
    type: Object,
    required: true
  }
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
