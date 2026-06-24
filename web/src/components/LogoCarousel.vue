<template>
  <div
    class="logo-carousel"
    @mouseenter="isPaused = true"
    @mouseleave="isPaused = false"
  >
    <div
      class="logo-carousel-track"
      :class="`dir-${direction}`"
      :style="trackStyle"
    >
      <slot
        v-for="(item, idx) in doubledItems"
        :key="idx"
        :item="item"
        :index="idx % items.length"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'

const props = defineProps({
  items: {
    type: Array,
    required: true
  },
  speed: {
    type: Number,
    default: 35
  },
  direction: {
    type: String,
    default: 'left',
    validator: v => ['left', 'right'].includes(v)
  },
  gap: {
    type: Number,
    default: 24
  }
})

const isPaused = ref(false)

const doubledItems = computed(() => [...props.items, ...props.items])

const trackStyle = computed(() => ({
  '--carousel-speed': `${props.speed}s`,
  '--carousel-gap': `${props.gap}px`,
  animationDuration: `${props.speed}s`,
  animationPlayState: isPaused.value ? 'paused' : 'running'
}))
</script>

<style scoped>
.logo-carousel {
  overflow: hidden;
  mask-image: linear-gradient(
    to right,
    transparent 0%,
    black 8%,
    black 92%,
    transparent 100%
  );
  -webkit-mask-image: linear-gradient(
    to right,
    transparent 0%,
    black 8%,
    black 92%,
    transparent 100%
  );
  padding: 8px 0;
}

.logo-carousel-track {
  display: flex;
  gap: var(--carousel-gap, 24px);
  width: max-content;
  will-change: transform;
  animation-timing-function: linear;
  animation-iteration-count: infinite;
}

.logo-carousel-track.dir-left {
  animation-name: logo-scroll-left;
}

.logo-carousel-track.dir-right {
  animation-name: logo-scroll-right;
  transform: translateX(-50%);
}

@keyframes logo-scroll-left {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(-50%);
  }
}

@keyframes logo-scroll-right {
  from {
    transform: translateX(-50%);
  }
  to {
    transform: translateX(0);
  }
}
</style>
