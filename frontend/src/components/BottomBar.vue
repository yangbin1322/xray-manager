<template>
  <div class="bottom-bar">
    <div class="bar-left">
      <label class="autostart-label">
        <input type="checkbox" :checked="appStore.autoStart" @change="appStore.setAutoStartEnabled($event.target.checked)" />
        <span>开机自启</span>
      </label>
    </div>
    <div class="bar-right">
      <button class="btn-action" @click="showBatchImport = true">批量导入</button>
      <button class="btn-action" @click="appStore.doImportConfig().then(handleImportDone)">导入规则</button>
      <button class="btn-action" @click="handleExport">导出规则</button>
      <button class="btn-primary" @click="$emit('addRule')">添加规则</button>
    </div>

    <!-- 批量导入对话框 -->
    <div v-if="showBatchImport" class="dialog-overlay" @click.self="showBatchImport = false">
      <div class="dialog">
        <div class="dialog-header">
          <h3>批量导入节点</h3>
          <button class="dialog-close" @click="showBatchImport = false">&times;</button>
        </div>
        <div class="dialog-body">
          <p class="import-hint">支持 vmess:// / vless:// / ss:// / trojan:// 链接，每行一个</p>
          <textarea
            v-model="importText"
            class="import-textarea"
            rows="10"
            placeholder="粘贴分享链接..."
          ></textarea>
          <div class="import-actions">
            <button class="btn-small" @click="pasteFromClipboard">从剪贴板粘贴</button>
          </div>
        </div>
        <div class="dialog-footer">
          <button class="btn-secondary" @click="showBatchImport = false">取消</button>
          <button class="btn-primary" @click="handleBatchImport" :disabled="importing">
            {{ importing ? '导入中...' : '导入节点' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useAppStore } from '../stores/app.js'
import { useRulesStore } from '../stores/rules.js'
import { useGroupsStore } from '../stores/groups.js'

const emit = defineEmits(['addRule'])

const appStore = useAppStore()
const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()

const showBatchImport = ref(false)
const importText = ref('')
const importing = ref(false)

async function handleExport() {
  // 有选中的规则时导出选中的，否则导出全部
  const ids = rulesStore.selectedRuleIds
  await appStore.doExportConfig(ids)
}

async function handleBatchImport() {
  if (!importText.value.trim()) {
    appStore.showToast('请输入分享链接', 'warning')
    return
  }

  importing.value = true
  try {
    const result = await appStore.doImportShareLinks(importText.value)
    if (result && result.successCount > 0) {
      showBatchImport.value = false
      importText.value = ''
      await rulesStore.loadRules()
    }
  } finally {
    importing.value = false
  }
}

async function pasteFromClipboard() {
  try {
    const text = await navigator.clipboard.readText()
    importText.value = text
  } catch {
    appStore.showToast('读取剪贴板失败，请手动粘贴', 'warning')
  }
}

async function handleImportDone(result) {
  if (result) {
    await rulesStore.loadRules()
    await groupsStore.loadGroups()
  }
}
</script>

<style scoped>
.bottom-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 16px;
  border-top: 1px solid var(--border-color);
  background: var(--bg-secondary);
}

.bar-left, .bar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.autostart-label {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  cursor: pointer;
}

.btn-action {
  padding: 6px 14px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  background: var(--bg-primary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}

.btn-action:hover { background: var(--bg-hover); }

.btn-primary {
  padding: 6px 14px;
  background: var(--primary-color);
  color: #fff;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
}

/* Dialog */
.dialog-overlay {
  position: fixed;
  top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.dialog {
  background: var(--bg-primary);
  border-radius: 8px;
  width: 550px;
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.2);
}

.dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border-color);
}

.dialog-header h3 { margin: 0; font-size: 16px; }
.dialog-close { border: none; background: none; font-size: 20px; cursor: pointer; color: var(--text-secondary); }
.dialog-body { padding: 20px; }
.dialog-footer {
  padding: 12px 20px;
  border-top: 1px solid var(--border-color);
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.import-hint {
  font-size: 12px;
  color: var(--text-secondary);
  margin: 0 0 8px 0;
}

.import-textarea {
  width: 100%;
  padding: 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  font-family: monospace;
  resize: vertical;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-sizing: border-box;
}

.import-actions {
  margin-top: 8px;
}

.btn-small {
  padding: 4px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}

.btn-secondary {
  padding: 8px 16px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}
</style>
