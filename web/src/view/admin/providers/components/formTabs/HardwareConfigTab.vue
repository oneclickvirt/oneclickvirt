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
      <el-text
        type="primary"
        size="large"
      >
        {{ $t('admin.providers.commonConfig') }}
      </el-text>
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
    </el-form-item>
    <div
      class="form-tip"
      style="margin-top: -10px; margin-bottom: 15px; margin-left: 180px;"
    >
      <el-text
        size="small"
        type="info"
      >
        {{ $t('admin.providers.containerMemorySwapTip') }}
      </el-text>
    </div>

    <!-- 容器专用配置 -->
    <template v-if="modelValue.containerEnabled">
      <el-divider content-position="left">
        <el-text
          type="warning"
          size="large"
        >
          {{ $t('admin.providers.containerOnlyConfig') }}
        </el-text>
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
      </el-form-item>
      <div
        v-if="modelValue.containerEnabled"
        class="form-tip"
        style="margin-top: -10px; margin-bottom: 15px; margin-left: 180px;"
      >
        <el-text
          size="small"
          type="warning"
        >
          {{ $t('admin.providers.containerPrivilegedTip') }}
        </el-text>
      </div>

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
      </el-form-item>
      <div
        v-if="modelValue.containerEnabled"
        class="form-tip"
        style="margin-top: -10px; margin-bottom: 15px; margin-left: 180px;"
      >
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.containerAllowNestingTip') }}
        </el-text>
      </div>

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
      </el-form-item>
      <div
        v-if="modelValue.containerEnabled"
        class="form-tip"
        style="margin-top: -10px; margin-bottom: 15px; margin-left: 180px;"
      >
        <el-text
          size="small"
          type="warning"
        >
          {{ $t('admin.providers.containerCpuAllowanceTip') }}
        </el-text>
      </div>

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
      </el-form-item>
      <div
        v-if="modelValue.containerEnabled"
        class="form-tip"
        style="margin-top: -10px; margin-bottom: 15px; margin-left: 180px;"
      >
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.containerMaxProcessesTip') }}
        </el-text>
      </div>

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
      </el-form-item>
      <div
        v-if="modelValue.containerEnabled"
        class="form-tip"
        style="margin-top: -10px; margin-bottom: 15px; margin-left: 180px;"
      >
        <el-text
          size="small"
          type="info"
        >
          {{ $t('admin.providers.containerDiskIoLimitTip') }}
        </el-text>
      </div>
    </template>

    <!-- 虚拟机配置提示 -->
    <template v-if="modelValue.vmEnabled && !modelValue.containerEnabled">
      <el-divider content-position="left">
        <el-text
          type="info"
          size="large"
        >
          {{ $t('admin.providers.vmConfigNote') }}
        </el-text>
      </el-divider>
      <el-alert
        :title="$t('admin.providers.vmHardwareConfigTip')"
        type="info"
        :closable="false"
        show-icon
      />
    </template>

    <!-- GPU 直通配置（仅 LXD / Incus 节点） -->
    <template v-if="modelValue.type === 'lxd' || modelValue.type === 'incus'">
      <el-divider content-position="left">
        <el-text
          type="warning"
          size="large"
        >
          {{ $t('admin.providers.gpuPassthrough') }}
        </el-text>
      </el-divider>

      <el-alert
        :title="$t('admin.providers.gpuDriverWarning')"
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 16px;"
      />

      <el-form-item
        :label="$t('admin.providers.gpuEnabled')"
        prop="gpuEnabled"
      >
        <el-switch
          v-model="modelValue.gpuEnabled"
          :active-text="$t('common.enable')"
          :inactive-text="$t('common.disable')"
        />
      </el-form-item>

      <template v-if="modelValue.gpuEnabled">
        <el-form-item
          :label="$t('admin.providers.gpuDeviceIds')"
          prop="gpuDeviceIds"
        >
          <el-input
            v-model="modelValue.gpuDeviceIds"
            :placeholder="$t('admin.providers.gpuDeviceIdsPlaceholder')"
            style="width: 300px"
          />
          <el-button
            v-if="modelValue.id"
            type="primary"
            plain
            :loading="detectingGpus"
            style="margin-left: 8px;"
            @click="handleDetectGPUs"
          >
            {{ $t('admin.providers.gpuDetect') }}
          </el-button>
        </el-form-item>
        <div
          class="form-tip"
          style="margin-top: -10px; margin-bottom: 15px; margin-left: 180px;"
        >
          <el-text
            size="small"
            type="info"
          >
            {{ $t('admin.providers.gpuDeviceIdsTip') }}
          </el-text>
        </div>

        <!-- 检测到的 GPU 列表 -->
        <div
          v-if="detectedGpus.length > 0"
          style="margin-left: 180px; margin-bottom: 16px;"
        >
          <el-text
            size="small"
            type="success"
            style="display:block; margin-bottom: 8px;"
          >
            {{ $t('admin.providers.gpuDetectedList') }}
          </el-text>
          <el-tag
            v-for="(gpu, idx) in detectedGpus"
            :key="idx"
            style="margin-right: 6px; margin-bottom: 6px; cursor: pointer;"
            @click="selectGpuId(gpu)"
          >
            {{ formatDeviceLabel(gpu, idx) }}
          </el-tag>
        </div>
      </template>
    </template>
  </el-form>
</template>

<script setup>
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { detectProviderGPUs } from '@/api/admin'

const { t } = useI18n()

const props = defineProps({
  modelValue: {
    type: Object,
    required: true
  }
})

const detectingGpus = ref(false)
const detectedGpus = ref([])

async function handleDetectGPUs() {
  const providerId = props.modelValue?.id
  if (!providerId) return

  detectingGpus.value = true
  try {
    const res = await detectProviderGPUs(providerId)
    const gpuList = res?.data?.gpus || []
    const npuList = res?.data?.npus || []
    detectedGpus.value = res?.data?.accelerators || [...gpuList, ...npuList]
    if (detectedGpus.value.length === 0) {
      ElMessage.info(t('admin.providers.gpuNoneFound'))
    } else {
      ElMessage.success(t('admin.providers.gpuDetectSuccess', {
        count: detectedGpus.value.length,
        gpuCount: gpuList.length,
        npuCount: npuList.length
      }))
    }
  } catch (e) {
    ElMessage.error(t('admin.providers.gpuDetectFailed'))
  } finally {
    detectingGpus.value = false
  }
}

function selectGpuId(gpu) {
  if (gpu?.kind === 'npu') {
    ElMessage.info(t('admin.providers.npuIdHint'))
    return
  }
  const id = gpu.id
  if (!id) return
  const current = props.modelValue.gpuDeviceIds || ''
  const ids = current ? current.split(',').map(s => s.trim()).filter(Boolean) : []
  if (!ids.includes(String(id))) {
    ids.push(String(id))
    props.modelValue.gpuDeviceIds = ids.join(',')
  }
}

function formatDeviceLabel(gpu, idx) {
  const typeLabel = gpu?.kind === 'npu'
    ? t('admin.providers.acceleratorNpu')
    : t('admin.providers.acceleratorGpu')
  const idPart = gpu?.id ? `${gpu.id} - ` : `${idx} - `
  const namePart = gpu?.product || gpu?.name || gpu?.card || t('admin.providers.gpuUnknown')
  return `[${typeLabel}] ${idPart}${namePart}`
}
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
