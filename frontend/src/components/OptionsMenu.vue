<script setup lang="ts">
import { ref } from 'vue'

defineProps<{
  url: string
}>()

const emit = defineEmits<{
  requestChanges: []
  rescore: []
}>()

const showComment = ref(false)
const comment = ref('')

function submitRequestChanges() {
  emit('requestChanges')
  showComment.value = false
  comment.value = ''
}
</script>

<template>
  <div class="options-menu" @click.stop>
    <a :href="url" target="_blank" rel="noopener" class="menu-item">
      Open on GitHub
    </a>
    <button class="menu-item" @click="showComment = !showComment">
      Request Changes
    </button>
    <div v-if="showComment" class="comment-input">
      <textarea
        v-model="comment"
        placeholder="Comment (optional)"
        rows="2"
      />
      <button class="submit-btn" @click="submitRequestChanges">Submit</button>
    </div>
    <button class="menu-item" @click="$emit('rescore')">
      Re-score
    </button>
  </div>
</template>

<style scoped>
.options-menu {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 4px 0;
  min-width: 180px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
}

.menu-item {
  display: block;
  width: 100%;
  padding: 8px 16px;
  background: none;
  border: none;
  color: #c9d1d9;
  font-size: 14px;
  text-align: left;
  cursor: pointer;
  text-decoration: none;
}

.menu-item:hover {
  background: #1f6feb33;
}

.comment-input {
  padding: 8px 16px;
}

.comment-input textarea {
  width: 100%;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 4px;
  color: #c9d1d9;
  padding: 6px;
  font-size: 13px;
  resize: vertical;
}

.submit-btn {
  margin-top: 4px;
  padding: 4px 12px;
  background: #f85149;
  color: #fff;
  border: none;
  border-radius: 4px;
  font-size: 12px;
  cursor: pointer;
}
</style>
