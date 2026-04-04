<template>
  <i
    v-if="osInfo?.icon"
    class="os-icon"
    :class="[osInfo.icon, 'fl-fw']"
    :style="{ fontSize: size + 'px', color: osInfo.color }"
    :title="osInfo.displayName || name"
  />
  <span
    v-else
    class="os-icon-fallback"
    :style="{
      backgroundColor: osInfo?.color || '#909399',
      width: size + 'px',
      height: size + 'px',
      fontSize: (size * 0.45) + 'px',
      lineHeight: size + 'px'
    }"
    :title="osInfo?.displayName || name"
  >
    {{ osInfo?.abbr || '?' }}
  </span>
</template>

<script setup>
import { computed } from 'vue'
import { matchOperatingSystem } from '@/utils/operating-systems'

const props = defineProps({
  name: { type: String, default: '' },
  size: { type: Number, default: 24 }
})

const osInfo = computed(() => matchOperatingSystem(props.name))
</script>

<style scoped>
.os-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  vertical-align: middle;
  flex-shrink: 0;
}

.os-icon-fallback {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  color: #fff;
  font-weight: 600;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  vertical-align: middle;
  flex-shrink: 0;
  user-select: none;
}
</style>
