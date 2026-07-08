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
      <button class="btn-action" @click="showExportDialog = true">导出规则</button>
      <button class="btn-primary" @click="$emit('addRule')">添加规则</button>
    </div>

    <!-- 导出选项对话框 -->
    <div v-if="showExportDialog" class="dialog-overlay" @click.self="showExportDialog = false">
      <div class="dialog">
        <div class="dialog-header">
          <h3>导出规则</h3>
          <button class="dialog-close" @click="showExportDialog = false">&times;</button>
        </div>
        <div class="dialog-body">
          <p class="import-hint">
            {{ selectedCount > 0
              ? `将导出选中的 ${selectedCount} 个节点，并自动包含其完整所属的链式代理、故障转移及相关分组。`
              : '未选中任何节点，将导出全部节点与配置。' }}
          </p>
          <label class="export-check">
            <input type="checkbox" v-model="includeSubscriptions" />
            同时导出订阅及订阅分组（默认不导出，导出的订阅节点将转为手动节点）
          </label>
        </div>
        <div class="dialog-footer">
          <button class="btn-secondary" @click="showExportDialog = false">取消</button>
          <button class="btn-primary" @click="handleExport">导出</button>
        </div>
      </div>
    </div>

    <!-- 批量导入对话框 -->
    <div v-if="showBatchImport" class="dialog-overlay" @click.self="showBatchImport = false">
      <div class="dialog">
        <div class="dialog-header">
          <h3>批量导入节点</h3>
          <button class="dialog-close" @click="showBatchImport = false">&times;</button>
        </div>
        <div class="dialog-body">
          <p class="import-hint">支持 vmess:// / vless:// / ss:// / trojan:// / hysteria2:// / hy2:// / tuic:// 链接，每行一个</p>
          <textarea
            v-model="importText"
            class="import-textarea"
            rows="10"
            placeholder="粘贴分享链接..."
          ></textarea>
          <div class="import-actions">
            <button class="btn-small" @click="pasteFromClipboard">从剪贴板粘贴</button>
          </div>

          <!-- 导入到分组 -->
          <div class="import-group-row">
            <label>导入到分组：</label>
            <select v-model="importGroupSel" class="import-select">
              <option value="">不分组</option>
              <option value="__new__">+ 新建分组</option>
              <option v-for="g in groupsStore.groups" :key="g.id" :value="g.id">{{ g.name }}</option>
            </select>
            <input
              v-if="importGroupSel === '__new__'"
              v-model="importNewGroupName"
              class="import-newgroup"
              type="text"
              placeholder="新分组名称"
            />
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
import { ref, computed } from 'vue'
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
const importGroupSel = ref('')        // '' 不分组 | '__new__' 新建 | 分组ID
const importNewGroupName = ref('')    // importGroupSel === '__new__' 时的新分组名
const showExportDialog = ref(false)
const includeSubscriptions = ref(false)

const selectedCount = computed(() =>
  rulesStore.selectedRuleIds.filter(id => rulesStore.rules.some(r => r.id === id)).length
)

async function handleExport() {
  // 仅统计普通节点的选中项（链式/故障转移会按关联关系自动带出）
  const ids = rulesStore.selectedRuleIds.filter(id => rulesStore.rules.some(r => r.id === id))
  showExportDialog.value = false
  await appStore.doExportConfig(ids, includeSubscriptions.value)
}

async function handleBatchImport() {
  if (!importText.value.trim()) {
    appStore.showToast('请输入分享链接', 'warning')
    return
  }

  // 解析分组选择：新建分组 / 现有分组 / 不分组
  let groupId = ''
  let newGroupName = ''
  if (importGroupSel.value === '__new__') {
    newGroupName = importNewGroupName.value.trim()
    if (!newGroupName) {
      appStore.showToast('请输入新分组名称', 'warning')
      return
    }
  } else {
    groupId = importGroupSel.value
  }

  importing.value = true
  try {
    const result = await appStore.doImportShareLinks(importText.value, groupId, newGroupName)
    if (result && result.successCount > 0) {
      showBatchImport.value = false
      importText.value = ''
      importGroupSel.value = ''
      importNewGroupName.value = ''
      await rulesStore.loadRules()
      await groupsStore.loadGroups() // 新建分组后刷新侧边栏
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

.import-group-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
  font-size: 13px;
  color: var(--text-primary);
}
.import-group-row label { white-space: nowrap; color: var(--text-secondary); }
.import-select, .import-newgroup {
  padding: 6px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.import-newgroup { flex: 1; }

.export-check {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 13px;
  cursor: pointer;
  line-height: 1.5;
  color: var(--text-primary);
}
.export-check input { margin-top: 3px; }

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
