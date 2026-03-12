<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, nextTick } from 'vue'
import type { PRCard, ScanStatus } from './types'
import {
  fetchPRs, scanPRs, getScanStatus,
  approvePR, rejectPR, requestChanges, rescorePR,
} from './api'
import PRCardComponent from './components/PRCard.vue'

const cards = ref<PRCard[]>([])
const loading = ref(true)
const scanStatus = ref<ScanStatus>({ scanning: false, total: 0, scored: 0 })
const error = ref('')
const selectedIndex = ref(0)

let pollTimer: ReturnType<typeof setInterval> | null = null

const pendingCards = computed(() => cards.value.filter(c => c.status === 'pending'))
const stats = computed(() => ({
  approved: cards.value.filter(c => c.status === 'approved').length,
  rejected: cards.value.filter(c => c.status === 'rejected').length,
  skipped: cards.value.filter(c => c.status === 'skipped').length,
}))
const allTriaged = computed(
  () => cards.value.length > 0 && pendingCards.value.length === 0 && !scanStatus.value.scanning
)

function scrollToCard(index: number) {
  const el = document.querySelector(`[data-card-index="${index}"]`)
  if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function loadPRs() {
  try {
    cards.value = await fetchPRs()
  } catch (e) {
    error.value = String(e)
  }
}

async function checkScanAndLoad() {
  const status = await getScanStatus()
  scanStatus.value = status
  await loadPRs()

  if (status.scanning) {
    startPolling()
  } else if (cards.value.length === 0) {
    await triggerScan()
  }
}

function startPolling() {
  if (pollTimer) return
  pollTimer = setInterval(async () => {
    try {
      const status = await getScanStatus()
      scanStatus.value = status
      await loadPRs()
      if (!status.scanning) {
        stopPolling()
      }
    } catch {
      // ignore transient errors during polling
    }
  }, 3000)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function triggerScan() {
  error.value = ''
  try {
    scanStatus.value = await scanPRs()
    startPolling()
  } catch (e) {
    error.value = String(e)
  }
}

function updateCardStatus(prNumber: number, status: PRCard['status']) {
  const card = cards.value.find(c => c.pr_number === prNumber)
  if (card) card.status = status
}

async function handleApprove(card: PRCard) {
  try {
    await approvePR(card.pr_number)
    updateCardStatus(card.pr_number, 'approved')
  } catch (e) {
    error.value = `Approve failed: ${e}`
  }
}

async function handleReject(card: PRCard) {
  try {
    await rejectPR(card.pr_number)
    updateCardStatus(card.pr_number, 'rejected')
  } catch (e) {
    error.value = `Reject failed: ${e}`
  }
}

async function handleRequestChanges(card: PRCard, body: string) {
  try {
    await requestChanges(card.pr_number, body)
    updateCardStatus(card.pr_number, 'changes_requested')
  } catch (e) {
    error.value = `Request changes failed: ${e}`
  }
}

async function handleApproveAndMerge(card: PRCard) {
  try {
    await approvePR(card.pr_number)
    updateCardStatus(card.pr_number, 'approved')
    // TODO: call merge endpoint once implemented
  } catch (e) {
    error.value = `Approve & merge failed: ${e}`
  }
}

function handleAutoRule(_card: PRCard, action: 'approve' | 'reject', rule: string) {
  // TODO: persist rule to backend once implemented
  console.log(`Auto-${action} rule saved:`, rule)
}

async function handleRescore(card: PRCard) {
  try {
    await rescorePR(card.pr_number)
    await loadPRs()
  } catch (e) {
    error.value = `Rescore failed: ${e}`
  }
}

function onKeydown(e: KeyboardEvent) {
  const target = e.target as HTMLElement
  if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA') return

  switch (e.key) {
    case 'j':
      e.preventDefault()
      selectedIndex.value = Math.min(selectedIndex.value + 1, cards.value.length - 1)
      nextTick(() => scrollToCard(selectedIndex.value))
      break
    case 'k':
      e.preventDefault()
      selectedIndex.value = Math.max(selectedIndex.value - 1, 0)
      nextTick(() => scrollToCard(selectedIndex.value))
      break
    case 'a': {
      const card = cards.value[selectedIndex.value]
      if (card) handleApprove(card)
      break
    }
    case 'r': {
      const card = cards.value[selectedIndex.value]
      if (card) handleReject(card)
      break
    }
    case 'o': {
      const card = cards.value[selectedIndex.value]
      if (card) window.open(card.url, '_blank')
      break
    }
  }
}

onMounted(async () => {
  document.addEventListener('keydown', onKeydown)
  loading.value = true
  await checkScanAndLoad()
  loading.value = false
})

onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  stopPolling()
})
</script>

<template>
  <div class="app">
    <div v-if="error" class="error-banner">
      {{ error }}
      <button @click="error = ''">Dismiss</button>
    </div>

    <div v-if="loading && cards.length === 0" class="loading">
      <div class="spinner" />
      Loading PRs...
    </div>

    <div v-else-if="allTriaged" class="loading">
      <div class="checkmark">&#10003;</div>
      <h2>All done!</h2>
      <p>
        {{ stats.approved }} approved, {{ stats.rejected }} rejected, {{ stats.skipped }} skipped
      </p>
      <button class="scan-btn" @click="triggerScan">Re-scan</button>
    </div>

    <div v-else-if="cards.length === 0 && !loading && !scanStatus.scanning" class="loading">
      <h2>No PRs found</h2>
      <p>Click "Scan PRs" to fetch and score open pull requests.</p>
      <button class="scan-btn" @click="triggerScan">Scan PRs</button>
    </div>

    <div v-else class="card-feed">
      <PRCardComponent
        v-for="(card, idx) in cards"
        :key="card.pr_number"
        :card="card"
        :selected="idx === selectedIndex"
        :data-card-index="idx"
        @approve="handleApprove(card)"
        @approve-and-merge="handleApproveAndMerge(card)"
        @reject="handleReject(card)"
        @request-changes="handleRequestChanges(card, $event)"
        @rescore="handleRescore(card)"
        @auto-rule="(action: 'approve' | 'reject', rule: string) => handleAutoRule(card, action, rule)"
      />

      <div v-if="scanStatus.scanning" class="scan-page">
        <div class="spinner" />
        <p>Scoring PRs ({{ scanStatus.scored }}/{{ scanStatus.total }})...</p>
        <p class="dim">Cards appear as they're ready</p>
      </div>
    </div>

    <div class="hud">
      <div class="hud-left">
        <span class="hud-counter">{{ selectedIndex + 1 }}/{{ cards.length }}</span>
        <span v-if="scanStatus.scanning" class="hud-scanning">
          Scoring {{ scanStatus.scored }}/{{ scanStatus.total }}
        </span>
      </div>
      <div class="hud-shortcuts">
        <kbd>j</kbd>/<kbd>k</kbd>
        <kbd>a</kbd> approve
        <kbd>r</kbd> reject
        <kbd>o</kbd> GitHub
      </div>
      <button
        class="scan-btn small"
        :disabled="scanStatus.scanning"
        @click="triggerScan"
      >
        {{ scanStatus.scanning ? 'Scanning...' : 'Re-scan' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.app {
  width: 100%;
  height: 100vh;
  overflow: hidden;
}

.card-feed {
  height: 100vh;
  overflow-y: scroll;
  scroll-snap-type: y mandatory;
}

.error-banner {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 50;
  background: #da363322;
  border-bottom: 1px solid #f85149;
  padding: 10px 24px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: #f85149;
  font-size: 14px;
}

.error-banner button {
  background: none;
  border: none;
  color: #f85149;
  cursor: pointer;
  text-decoration: underline;
}

.loading {
  height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: #8b949e;
  font-size: 16px;
}

.loading h2 {
  font-size: 24px;
  color: #e6edf3;
}

.checkmark {
  font-size: 64px;
  color: #3fb950;
}

.spinner {
  width: 32px;
  height: 32px;
  border: 3px solid #30363d;
  border-top-color: #1f6feb;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.scan-page {
  height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: #8b949e;
  scroll-snap-align: start;
}

.dim { opacity: 0.5; }

.hud {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  z-index: 50;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 24px;
  background: rgba(13, 17, 23, 0.9);
  backdrop-filter: blur(8px);
  border-top: 1px solid #21262d;
  font-size: 13px;
  color: #8b949e;
}

.hud-left {
  display: flex;
  gap: 12px;
  align-items: center;
}

.hud-counter {
  font-weight: 600;
  color: #e6edf3;
}

.hud-scanning {
  color: #f0883e;
}

.hud-shortcuts {
  display: flex;
  gap: 8px;
  align-items: center;
}

kbd {
  background: #21262d;
  border: 1px solid #30363d;
  border-radius: 3px;
  padding: 1px 5px;
  font-family: monospace;
  font-size: 11px;
  color: #c9d1d9;
}

.scan-btn {
  padding: 6px 16px;
  background: #238636;
  color: #fff;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.scan-btn:hover { background: #2ea043; }
.scan-btn:disabled { opacity: 0.6; cursor: not-allowed; }
.scan-btn.small { padding: 4px 12px; font-size: 12px; }
</style>
