<script setup lang="ts">
import { ref, computed } from 'vue'
import type { PRCard } from '../types'
import ScoreBar from './ScoreBar.vue'
import OptionsMenu from './OptionsMenu.vue'

const props = defineProps<{
  card: PRCard
  selected: boolean
}>()

const emit = defineEmits<{
  approve: []
  approveAndMerge: []
  reject: []
  requestChanges: [body: string]
  rescore: []
  autoRule: [action: 'approve' | 'reject', rule: string]
}>()

const showMenu = ref(false)
const ruleAction = ref<'approve' | 'reject' | null>(null)
const ruleText = ref('')

function openRulePrompt(action: 'approve' | 'reject') {
  ruleAction.value = action
  ruleText.value = ''
}

function submitRule() {
  if (ruleAction.value && ruleText.value.trim()) {
    emit('autoRule', ruleAction.value, ruleText.value.trim())
  }
  ruleAction.value = null
  ruleText.value = ''
}

const reviewBullets = computed(() =>
  props.card.review_notes
    .split('\n')
    .map(l => l.replace(/^[-*]\s*/, '').trim())
    .filter(l => l.length > 0)
)

const verdictClass = {
  approve: 'verdict-approve',
  review: 'verdict-review',
  reject: 'verdict-reject',
}

function diffLineClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'diff-file'
  if (line.startsWith('@@')) return 'diff-hunk'
  if (line.startsWith('+')) return 'diff-add'
  if (line.startsWith('-')) return 'diff-del'
  return 'diff-ctx'
}

const factorEntries = [
  { key: 'blast_radius', label: 'Blast Radius' },
  { key: 'test_coverage', label: 'Test Coverage' },
  { key: 'sensitivity', label: 'Sensitivity' },
  { key: 'complexity', label: 'Complexity' },
  { key: 'scope_focus', label: 'Scope' },
] as const
</script>

<template>
  <div
    class="pr-page"
    :class="[{ selected }, `status-${card.status}`]"
  >
    <!-- Summary card at top -->
    <div class="summary-card">
      <div class="summary-top">
        <div class="pr-identity">
          <span class="pr-number">#{{ card.pr_number }}</span>
          <a :href="card.url" target="_blank" rel="noopener" class="pr-title">
            {{ card.title }}
          </a>
        </div>
        <div class="pr-verdict">
          <ScoreBar :score="card.weighted_score" />
          <span :class="['verdict-badge', verdictClass[card.verdict]]">
            {{ card.verdict }}
          </span>
        </div>
      </div>

      <div class="summary-meta">
        <span class="author">{{ card.author }}</span>
        <span class="diff-stats">+{{ card.additions }}/-{{ card.deletions }}</span>
        <span class="files">{{ card.files_changed }} files</span>
        <span v-if="card.checks_summary" class="checks" :class="{ failing: card.has_failing_checks }">
          {{ card.checks_summary }}
        </span>
      </div>

      <div class="risk-summary">{{ card.risk_summary }}</div>

      <ul v-if="card.review_notes" class="review-notes">
        <li v-for="(line, i) in reviewBullets" :key="i">{{ line }}</li>
      </ul>

      <div class="factors-list">
        <div
          v-for="entry in factorEntries"
          :key="entry.key"
          class="factor-line"
        >
          <span
            class="factor-badge"
            :class="{
              low: (card.factors as Record<string, any>)[entry.key].score <= 2,
              mid: (card.factors as Record<string, any>)[entry.key].score === 3,
              high: (card.factors as Record<string, any>)[entry.key].score >= 4,
            }"
          >{{ (card.factors as Record<string, any>)[entry.key].score }}</span>
          <span class="factor-name">{{ entry.label }}</span>
          <span class="factor-reason">{{ (card.factors as Record<string, any>)[entry.key].reason }}</span>
        </div>
      </div>

      <div class="actions-bar">
        <div class="btn-group">
          <button
            class="btn btn-approve"
            :disabled="card.has_failing_checks"
            :title="card.has_failing_checks ? 'Cannot approve: failing CI checks' : 'Approve PR (a)'"
            @click="$emit('approve')"
          >
            Approve
          </button>
          <button
            class="btn btn-approve btn-split"
            :disabled="card.has_failing_checks"
            title="Approve like this..."
            @click="openRulePrompt('approve')"
          >+</button>
        </div>
        <button
          class="btn btn-merge"
          :disabled="card.has_failing_checks"
          :title="card.has_failing_checks ? 'Cannot merge: failing CI checks' : 'Approve & Merge'"
          @click="$emit('approveAndMerge')"
        >
          Approve &amp; Merge
        </button>
        <div class="btn-group">
          <button class="btn btn-reject" @click="$emit('reject')">
            Reject
          </button>
          <button
            class="btn btn-reject btn-split"
            title="Reject like this..."
            @click="openRulePrompt('reject')"
          >+</button>
        </div>
        <button class="btn btn-menu" @click="showMenu = !showMenu">
          ...
        </button>
        <span v-if="card.status !== 'pending'" class="status-tag" :class="`tag-${card.status}`">
          {{ card.status }}
        </span>
      </div>

      <div v-if="ruleAction" class="rule-prompt">
        <div class="rule-header">
          <span class="rule-title">
            {{ ruleAction === 'approve' ? 'Auto-approve' : 'Auto-reject' }} PRs like this
          </span>
          <button class="rule-close" @click="ruleAction = null">&times;</button>
        </div>
        <textarea
          v-model="ruleText"
          class="rule-input"
          :placeholder="ruleAction === 'approve'
            ? 'e.g. Always approve dependabot PRs for dev-only JS dependencies'
            : 'e.g. Always reject PRs that modify auth without tests'"
          rows="2"
          @keydown.enter.meta="submitRule"
          @keydown.enter.ctrl="submitRule"
        />
        <div class="rule-actions">
          <button class="btn btn-sm" @click="ruleAction = null">Cancel</button>
          <button
            class="btn btn-sm"
            :class="ruleAction === 'approve' ? 'btn-approve' : 'btn-reject'"
            :disabled="!ruleText.trim()"
            @click="submitRule"
          >
            {{ ruleAction === 'approve' ? 'Approve' : 'Reject' }} &amp; Save Rule
          </button>
        </div>
      </div>

      <Teleport to="body">
        <div v-if="showMenu" class="menu-overlay" @click="showMenu = false">
          <div class="menu-position" @click.stop>
            <OptionsMenu
              :url="card.url"
              @request-changes="$emit('requestChanges', ''); showMenu = false"
              @rescore="$emit('rescore'); showMenu = false"
            />
          </div>
        </div>
      </Teleport>
    </div>

    <!-- Diff fills the rest of the page -->
    <div class="diff-area">
      <pre v-if="card.diff" class="diff-content"><code><template
        v-for="(line, i) in card.diff.split('\n')"
        :key="i"
      ><span :class="diffLineClass(line)">{{ line }}
</span></template></code></pre>
      <div v-else class="diff-placeholder">
        No diff available. Press <kbd>o</kbd> to view on GitHub.
      </div>
    </div>
  </div>
</template>

<style scoped>
.pr-page {
  height: 100vh;
  scroll-snap-align: start;
  display: flex;
  flex-direction: column;
  padding: 16px 24px 48px;
  border-bottom: 1px solid #21262d;
}

.pr-page.selected {
  background: #161b2208;
}

.pr-page.status-approved { border-left: 4px solid #3fb950; }
.pr-page.status-rejected { border-left: 4px solid #f85149; }

/* Summary card */
.summary-card {
  flex-shrink: 0;
}

.summary-top {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 8px;
}

.pr-identity {
  flex: 1;
  min-width: 0;
}

.pr-number {
  color: #8b949e;
  font-size: 14px;
  font-weight: 600;
  margin-right: 8px;
}

.pr-title {
  font-size: 20px;
  font-weight: 700;
  color: #e6edf3;
  text-decoration: none;
  line-height: 1.3;
}

.pr-title:hover {
  text-decoration: underline;
}

.pr-verdict {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.verdict-badge {
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
  padding: 3px 10px;
  border-radius: 12px;
}

.verdict-approve { background: #238636; color: #fff; }
.verdict-review { background: #9e6a03; color: #fff; }
.verdict-reject { background: #da3633; color: #fff; }

.summary-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 13px;
  color: #8b949e;
  margin-bottom: 10px;
}

.diff-stats { font-family: monospace; }

.checks { color: #3fb950; }
.checks.failing { color: #f85149; font-weight: 600; }

.risk-summary {
  font-size: 15px;
  color: #c9d1d9;
  margin-bottom: 12px;
  padding: 10px 14px;
  background: #0d1117;
  border-radius: 6px;
  border-left: 3px solid #f0883e;
}

.review-notes {
  font-size: 14px;
  color: #c9d1d9;
  margin: 0 0 12px;
  padding: 8px 14px 8px 30px;
  background: #0d1117;
  border-radius: 6px;
  line-height: 1.6;
  list-style: disc;
}

.review-notes li {
  margin-bottom: 2px;
}

/* Factor lines */
.factors-list {
  display: flex;
  flex-direction: column;
  gap: 3px;
  margin-bottom: 10px;
}

.factor-line {
  display: flex;
  align-items: baseline;
  gap: 6px;
  font-size: 13px;
  line-height: 1.4;
}

.factor-badge {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 700;
  font-family: monospace;
  color: #fff;
}

.factor-badge.low { background: #238636; }
.factor-badge.mid { background: #9e6a03; }
.factor-badge.high { background: #da3633; }

.factor-name {
  flex-shrink: 0;
  font-weight: 600;
  color: #e6edf3;
  width: 100px;
}

.factor-reason {
  color: #8b949e;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Actions */
.actions-bar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 12px;
}

.btn {
  padding: 6px 20px;
  font-size: 14px;
  font-weight: 600;
  border: 1px solid #30363d;
  border-radius: 6px;
  cursor: pointer;
  background: #21262d;
  color: #c9d1d9;
}

.btn:disabled { opacity: 0.4; cursor: not-allowed; }
.btn:hover:not(:disabled) { background: #30363d; }

.btn-approve:hover:not(:disabled) {
  background: #238636;
  border-color: #238636;
  color: #fff;
}

.btn-reject:hover {
  background: #da3633;
  border-color: #da3633;
  color: #fff;
}

.btn-group {
  display: flex;
}

.btn-group .btn:first-child {
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
}

.btn-group .btn-split {
  border-top-left-radius: 0;
  border-bottom-left-radius: 0;
  border-left: 1px solid rgba(255, 255, 255, 0.15);
  padding: 6px 8px;
  font-size: 13px;
}

.btn-merge {
  background: #21262d;
  color: #c9d1d9;
}

.btn-merge:hover:not(:disabled) {
  background: #1f6feb;
  border-color: #1f6feb;
  color: #fff;
}

.btn-sm {
  padding: 4px 12px;
  font-size: 12px;
}

.btn-menu {
  padding: 6px 10px;
  font-size: 16px;
  line-height: 1;
}

/* Rule prompt */
.rule-prompt {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 6px;
  padding: 10px 14px;
  margin-bottom: 12px;
}

.rule-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.rule-title {
  font-size: 13px;
  font-weight: 600;
  color: #e6edf3;
}

.rule-close {
  background: none;
  border: none;
  color: #8b949e;
  font-size: 18px;
  cursor: pointer;
  line-height: 1;
}

.rule-input {
  width: 100%;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 4px;
  color: #c9d1d9;
  font-size: 13px;
  padding: 8px;
  resize: vertical;
  font-family: inherit;
  margin-bottom: 8px;
}

.rule-input::placeholder {
  color: #484f58;
}

.rule-input:focus {
  outline: none;
  border-color: #1f6feb;
}

.rule-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.status-tag {
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  padding: 3px 10px;
  border-radius: 4px;
  background: #21262d;
  color: #8b949e;
  margin-left: auto;
}

.tag-approved { color: #3fb950; border: 1px solid #3fb950; }
.tag-rejected { color: #f85149; border: 1px solid #f85149; }

/* Diff area */
.diff-area {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  background: #0d1117;
  border-radius: 6px;
  border: 1px solid #21262d;
}

.diff-content {
  margin: 0;
  padding: 8px 12px;
  font-size: 12px;
  line-height: 1.45;
  overflow-x: auto;
  color: #c9d1d9;
  tab-size: 4;
}

.diff-content code {
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
}

.diff-add { color: #3fb950; background: rgba(63, 185, 80, 0.1); }
.diff-del { color: #f85149; background: rgba(248, 81, 73, 0.1); }
.diff-hunk { color: #1f6feb; font-weight: 600; }
.diff-file { color: #d29922; font-weight: 700; }
.diff-ctx { color: #8b949e; }

.diff-placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: #484f58;
  font-size: 14px;
}

.diff-placeholder kbd {
  background: #21262d;
  border: 1px solid #30363d;
  border-radius: 3px;
  padding: 1px 5px;
  font-family: monospace;
  font-size: 12px;
  margin: 0 4px;
}

/* Menu overlay */
.menu-overlay {
  position: fixed;
  inset: 0;
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.3);
}

.menu-position {
  position: relative;
}
</style>
