<script setup lang="ts">
import type { PRCard } from '../types'
import ScoreBar from './ScoreBar.vue'

defineProps<{
  card: PRCard
}>()

defineEmits<{
  close: []
}>()

const factorLabels: Record<string, string> = {
  blast_radius: 'Blast Radius',
  test_coverage: 'Test Coverage',
  sensitivity: 'Sensitivity',
  complexity: 'Complexity',
  scope_focus: 'Scope Focus',
}
</script>

<template>
  <div class="drawer-overlay" @click="$emit('close')">
    <div class="drawer" @click.stop>
      <div class="drawer-header">
        <h3>#{{ card.pr_number }} {{ card.title }}</h3>
        <button class="close-btn" @click="$emit('close')">&times;</button>
      </div>

      <div class="drawer-body">
        <div class="meta">
          <span>{{ card.author }}</span>
          <span>+{{ card.additions }}/-{{ card.deletions }}</span>
          <span>{{ card.files_changed }} files</span>
        </div>

        <div class="summary">{{ card.risk_summary }}</div>

        <div v-if="card.checks_summary" class="checks" :class="{ failing: card.has_failing_checks }">
          CI: {{ card.checks_summary }}
        </div>

        <div class="factors-detail">
          <div
            v-for="(key, idx) in Object.keys(factorLabels)"
            :key="idx"
            class="factor-row"
          >
            <div class="factor-header">
              <span class="factor-name">{{ factorLabels[key] }}</span>
              <ScoreBar :score="(card.factors as Record<string, any>)[key].score" />
            </div>
            <p class="factor-reason">
              {{ (card.factors as Record<string, any>)[key].reason }}
            </p>
          </div>
        </div>

        <div class="actions">
          <a :href="card.url" target="_blank" rel="noopener" class="gh-link">
            Open on GitHub
          </a>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.drawer-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 100;
  display: flex;
  align-items: flex-end;
  justify-content: center;
}

.drawer {
  background: #161b22;
  border-top: 1px solid #30363d;
  border-radius: 16px 16px 0 0;
  width: 100%;
  max-width: 960px;
  max-height: 70vh;
  overflow-y: auto;
  padding: 20px;
}

.drawer-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 16px;
}

.drawer-header h3 {
  font-size: 18px;
  color: #e6edf3;
}

.close-btn {
  background: none;
  border: none;
  color: #8b949e;
  font-size: 24px;
  cursor: pointer;
  line-height: 1;
}

.meta {
  display: flex;
  gap: 16px;
  color: #8b949e;
  font-size: 13px;
  margin-bottom: 12px;
}

.summary {
  color: #c9d1d9;
  font-size: 14px;
  margin-bottom: 20px;
  padding: 12px;
  background: #0d1117;
  border-radius: 6px;
  border-left: 3px solid #f0883e;
}

.checks {
  font-size: 13px;
  color: #3fb950;
  margin-bottom: 16px;
  padding: 8px 12px;
  background: #0d1117;
  border-radius: 6px;
}

.checks.failing {
  color: #f85149;
  border-left: 3px solid #f85149;
}

.factors-detail {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.factor-row {
  padding: 10px;
  background: #0d1117;
  border-radius: 6px;
}

.factor-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 4px;
}

.factor-name {
  font-weight: 600;
  font-size: 13px;
  color: #e6edf3;
}

.factor-reason {
  font-size: 13px;
  color: #8b949e;
}

.actions {
  margin-top: 20px;
  text-align: center;
}

.gh-link {
  display: inline-block;
  padding: 8px 20px;
  background: #21262d;
  color: #c9d1d9;
  text-decoration: none;
  border-radius: 6px;
  font-size: 14px;
}

.gh-link:hover {
  background: #30363d;
}
</style>
