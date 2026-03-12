<script setup lang="ts">
const props = defineProps<{
  score: number
  max?: number
}>()

const max = props.max ?? 5
const filled = Math.round(props.score)
</script>

<template>
  <div class="score-bar" :title="`${score} / ${max}`">
    <span
      v-for="i in max"
      :key="i"
      class="block"
      :class="{ filled: i <= filled }"
    />
    <span class="score-label">{{ score }}</span>
  </div>
</template>

<style scoped>
.score-bar {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

.block {
  width: 12px;
  height: 16px;
  border-radius: 2px;
  background: #21262d;
}

.block.filled {
  background: #f0883e;
}

.block.filled:nth-child(-n+2) {
  background: #3fb950;
}

.block.filled:nth-child(n+4) {
  background: #f85149;
}

.score-label {
  margin-left: 6px;
  font-size: 13px;
  font-weight: 600;
  color: #8b949e;
}
</style>
