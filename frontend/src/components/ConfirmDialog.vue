<template>
  <div v-if="state" class="confirm-overlay" @click.self="cancel">
    <div class="confirm-box" role="alertdialog" aria-modal="true">
      <div class="confirm-title">{{ state.title }}</div>
      <!-- 消息里带换行（例如列出受影响的订阅），用 pre-line 保留 -->
      <div class="confirm-message">{{ state.message }}</div>
      <div class="confirm-actions">
        <button class="btn-cancel" @click="cancel">{{ state.cancelText }}</button>
        <button
          ref="confirmBtn"
          :class="['btn-confirm', { 'btn-danger': state.danger }]"
          @click="accept"
        >{{ state.confirmText }}</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { useAppStore } from '../stores/app.js'

const appStore = useAppStore()
const state = computed(() => appStore.confirmState)
const confirmBtn = ref(null)

function accept() { appStore.resolveConfirm(true) }
function cancel() { appStore.resolveConfirm(false) }

// 键盘：Enter 确认、Esc 取消。挂在 window 上，保证焦点在哪都生效。
function onKeydown(e) {
  if (!state.value) return
  if (e.key === 'Escape') {
    e.preventDefault()
    cancel()
  } else if (e.key === 'Enter') {
    e.preventDefault()
    accept()
  }
}

// 打开时把焦点移到确认按钮，回车即可确认
watch(state, (v) => {
  if (v) nextTick(() => confirmBtn.value?.focus())
})

onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<style scoped>
.confirm-overlay {
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  /* 高于其他对话框：确认框常从对话框内部触发（如订阅管理里删订阅） */
  z-index: 11000;
}

.confirm-box {
  background: var(--bg-primary);
  border-radius: 8px;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.3);
  width: 400px;
  max-width: calc(100vw - 40px);
  padding: 20px;
}

.confirm-title {
  font-size: 15px;
  font-weight: 600;
  margin-bottom: 10px;
  color: var(--text-primary);
}

.confirm-message {
  font-size: 13px;
  line-height: 1.7;
  color: var(--text-primary);
  white-space: pre-line;
  max-height: 50vh;
  overflow-y: auto;
  word-break: break-word;
}

.confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 20px;
}

.btn-cancel,
.btn-confirm {
  padding: 7px 18px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
  border: 1px solid var(--border-color);
}

.btn-cancel {
  background: var(--bg-secondary);
  color: var(--text-primary);
}
.btn-cancel:hover { background: var(--bg-hover); }

.btn-confirm {
  background: var(--primary-color);
  border-color: var(--primary-color);
  color: #fff;
}
.btn-confirm:hover { opacity: 0.9; }

.btn-danger {
  background: #e74c3c;
  border-color: #e74c3c;
}
</style>
