<!--
  BatchNodeEditor 批量编辑节点。
  多选普通节点后打开，每个节点一张可折叠卡片，卡片内嵌完整编辑表单，
  底部“全部保存”统一提交。仅处理普通节点（rule），忽略故障转移/链式代理。
-->
<template>
  <div v-if="visible" class="dialog-overlay" @click.self="close">
    <div class="dialog dialog-batch">
      <div class="dialog-header">
        <h3>批量编辑（{{ forms.length }} 个节点）</h3>
        <button class="dialog-close" @click="close">&times;</button>
      </div>

      <div class="dialog-toolbar">
        <button class="btn-small" @click="expandAll(true)">全部展开</button>
        <button class="btn-small" @click="expandAll(false)">全部折叠</button>
        <span v-if="skippedCount > 0" class="skip-hint">
          已忽略 {{ skippedCount }} 个非普通节点（故障转移/链式代理不支持批量编辑）
        </span>
      </div>

      <!-- 批量设置分组 -->
      <div class="dialog-toolbar batch-group-bar">
        <label>批量分组：</label>
        <select v-model="batchGroupSel" class="batch-select">
          <option value="">不分组</option>
          <option value="__new__">+ 新建分组</option>
          <option v-for="g in groupsStore.groups" :key="g.id" :value="g.id">{{ g.name }}</option>
        </select>
        <input
          v-if="batchGroupSel === '__new__'"
          v-model="batchNewGroupName"
          class="batch-newgroup"
          type="text"
          placeholder="新分组名称"
        />
        <button class="btn-small" @click="applyBatchGroup" :disabled="applyingGroup">
          {{ applyingGroup ? '应用中...' : '应用到全部' }}
        </button>
      </div>

      <div class="dialog-body">
        <div v-if="forms.length === 0" class="empty-hint">
          未选中任何可编辑的普通节点
        </div>

        <div v-for="(item, idx) in forms" :key="item.form.id" class="node-card">
          <div class="card-head" @click="toggle(idx)">
            <span class="chevron">{{ item.expanded ? '▾' : '▸' }}</span>
            <span class="card-alias">{{ item.form.alias || '(未命名)' }}</span>
            <span :class="['protocol-badge', `protocol-${item.form.protocol}`]">{{ item.form.protocol }}</span>
            <span class="card-addr">{{ item.form.serverAddr }}:{{ item.form.serverPort }}</span>
          </div>
          <div v-show="item.expanded" class="card-body">
            <NodeForm v-model="item.form" />
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn-secondary" @click="close">取消</button>
        <button class="btn-primary" @click="handleSaveAll" :disabled="saving || forms.length === 0">
          {{ saving ? '保存中...' : '全部保存' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, watch } from 'vue'
import { useRulesStore } from '../stores/rules.js'
import { useGroupsStore } from '../stores/groups.js'
import { useAppStore } from '../stores/app.js'
import NodeForm, { defaultNodeForm, normalizeTransport } from './NodeForm.vue'

const props = defineProps({
  visible: Boolean,
  nodes: { type: Array, default: () => [] }, // 选中的普通节点对象列表
  skippedCount: { type: Number, default: 0 }, // 被忽略的非普通节点数量
})

const emit = defineEmits(['close'])

const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()
const appStore = useAppStore()

// forms: [{ form: <深拷贝的节点>, expanded: bool }]
const forms = ref([])
const saving = ref(false)

// 批量分组
const batchGroupSel = ref('')       // '' 不分组 | '__new__' 新建 | 分组ID
const batchNewGroupName = ref('')
const applyingGroup = ref(false)

// 把选定的分组应用到所有编辑中的节点（更新各 form 的 groupId，随“全部保存”一起提交）
async function applyBatchGroup() {
  let groupId = ''
  if (batchGroupSel.value === '__new__') {
    const name = batchNewGroupName.value.trim()
    if (!name) { appStore.showToast('请输入新分组名称', 'warning'); return }
    applyingGroup.value = true
    try {
      await groupsStore.createGroup(name, '')   // 创建后 groupsStore.groups 会刷新
      const g = groupsStore.groups.find(x => x.name === name)
      if (!g) { appStore.showToast('创建分组失败', 'error'); return }
      groupId = g.id
      batchGroupSel.value = g.id            // 下拉切到新建的分组
      batchNewGroupName.value = ''
    } catch (e) {
      appStore.showToast(`创建分组失败: ${e}`, 'error')
      return
    } finally {
      applyingGroup.value = false
    }
  } else {
    groupId = batchGroupSel.value
  }

  // 应用到所有节点表单
  forms.value.forEach(item => { item.form.groupId = groupId })
  const label = groupId ? (groupsStore.groups.find(g => g.id === groupId)?.name || '分组') : '不分组'
  appStore.showToast(`已将全部节点设为「${label}」，保存后生效`, 'success')
}

// 打开时根据传入节点初始化表单副本（第一个默认展开）
watch(() => props.visible, (v) => {
  if (v) {
    // 重置批量分组选择
    batchGroupSel.value = ''
    batchNewGroupName.value = ''
    forms.value = props.nodes.map((n, i) => {
      const form = JSON.parse(JSON.stringify(n))
      if (!form.settings) form.settings = defaultNodeForm().settings
      // 补齐可能缺失的 settings 字段，避免子表单读取 undefined
      form.settings = { ...defaultNodeForm().settings, ...form.settings }
      return { form, expanded: i === 0 }
    })
  }
})

function toggle(idx) {
  forms.value[idx].expanded = !forms.value[idx].expanded
}

function expandAll(state) {
  forms.value.forEach(item => { item.expanded = state })
}

async function handleSaveAll() {
  // 逐个校验，任一不合法则展开该卡片并提示
  for (let i = 0; i < forms.value.length; i++) {
    const f = forms.value[i].form
    let errMsg = ''
    if (!f.serverAddr) errMsg = '请输入服务器地址'
    else if (!f.serverPort || f.serverPort <= 0) errMsg = '请输入有效的服务器端口'
    else if (!f.localPort || f.localPort <= 0) errMsg = '请输入有效的本地端口'
    if (errMsg) {
      forms.value[i].expanded = true
      appStore.showToast(`节点「${f.alias || '未命名'}」：${errMsg}`, 'warning')
      return
    }
  }

  saving.value = true
  try {
    const updates = forms.value.map(item => {
      const f = item.form
      normalizeTransport(f)
      return f
    })
    await rulesStore.updateNodes(updates)
    appStore.showToast(`已保存 ${updates.length} 个节点`, 'success')
    close()
  } catch (e) {
    appStore.showToast(`批量保存失败: ${e}`, 'error')
  } finally {
    saving.value = false
  }
}

function close() {
  emit('close')
}
</script>

<style scoped>
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
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.2);
  display: flex;
  flex-direction: column;
  max-height: 90vh;
}

/* 窗口比对话框窄时收缩，避免溢出产生横向滚动条 */
.dialog-batch { width: 760px; max-width: calc(100vw - 40px); }

.dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border-color);
}

.dialog-header h3 { margin: 0; font-size: 16px; }
.dialog-close { border: none; background: none; font-size: 20px; cursor: pointer; color: var(--text-secondary); }

.dialog-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 20px;
  border-bottom: 1px solid var(--border-color);
  background: var(--bg-secondary);
}

.skip-hint {
  font-size: 12px;
  color: var(--text-secondary);
  margin-left: auto;
}

.batch-group-bar label {
  font-size: 13px;
  color: var(--text-secondary);
  white-space: nowrap;
}
.batch-select, .batch-newgroup {
  padding: 5px 8px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
}
.batch-newgroup { flex: 1; max-width: 200px; }

.dialog-body {
  padding: 12px 20px;
  overflow-y: auto;
  flex: 1;
}

.empty-hint {
  text-align: center;
  color: var(--text-secondary);
  padding: 40px;
}

.node-card {
  border: 1px solid var(--border-color);
  border-radius: 6px;
  margin-bottom: 10px;
  overflow: hidden;
}

.card-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  cursor: pointer;
  background: var(--bg-secondary);
  user-select: none;
}

.card-head:hover { background: var(--bg-hover); }

.chevron { font-size: 12px; color: var(--text-secondary); width: 12px; }
.card-alias { font-weight: 500; font-size: 13px; }
.card-addr { font-size: 12px; color: var(--text-secondary); margin-left: auto; }

.card-body {
  padding: 14px 14px 4px;
  border-top: 1px solid var(--border-color);
}

.protocol-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 500;
}
.protocol-vmess { background: #e8f4fd; color: #2980b9; }
.protocol-vless { background: #fef3e2; color: #e67e22; }
.protocol-shadowsocks { background: #e8f8f5; color: #1abc9c; }
.protocol-trojan { background: #fde8e8; color: #e74c3c; }
.protocol-hysteria2 { background: #e8fdf0; color: #16a085; }
.protocol-tuic { background: #fdf3e8; color: #d35400; }
.protocol-http { background: #f0f0f0; color: #555; }
.protocol-socks { background: #f0f0f0; color: #555; }

.dialog-footer {
  padding: 12px 20px;
  border-top: 1px solid var(--border-color);
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.btn-small {
  padding: 5px 12px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  background: var(--bg-primary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}
.btn-small:hover { background: var(--bg-hover); }

.btn-primary {
  padding: 8px 20px;
  background: var(--primary-color);
  color: #fff;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}
.btn-primary:disabled { opacity: 0.6; cursor: not-allowed; }

.btn-secondary {
  padding: 8px 20px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}
</style>
