<template>
  <div v-if="visible" class="dialog-overlay" @click.self="close">
    <div class="dialog dialog-large">
      <div class="dialog-header">
        <h3>{{ isEditing ? '编辑故障转移' : '添加故障转移' }}</h3>
        <button class="dialog-close" @click="close">&times;</button>
      </div>

      <div class="dialog-body">
        <div class="form-section">
          <h4>基本信息</h4>
          <div class="form-row">
            <div class="form-group">
              <label>别名：</label>
              <input v-model="form.alias" type="text" placeholder="例如：故障转移-香港" />
            </div>
            <div class="form-group">
              <label>所属分组：</label>
              <select v-model="form.groupId">
                <option value="">无分组</option>
                <option v-for="g in groupsStore.groups" :key="g.id" :value="g.id">{{ g.name }}</option>
              </select>
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>本地代理端口（混合端口，同时支持 HTTP/SOCKS5）：</label>
              <input v-model.number="form.localPort" type="number" placeholder="1080" min="1" max="65535" />
            </div>
          </div>
        </div>

        <div class="form-section">
          <h4>选择子节点</h4>
          <input
            v-model="nodeSearch"
            type="text"
            class="node-search"
            placeholder="搜索节点（别名/协议/地址）..."
          />
          <div class="node-select-list">
            <div v-if="rulesStore.rules.length === 0" class="empty-hint">暂无可用节点</div>
            <div v-else-if="filteredRules.length === 0" class="empty-hint">无匹配节点</div>
            <label v-for="rule in filteredRules" :key="rule.id" class="node-select-item">
              <input type="checkbox" :value="rule.id" v-model="form.nodeIds" />
              <span>{{ rule.alias }} ({{ rule.protocol }} - {{ rule.serverAddr }})</span>
            </label>
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn-secondary" @click="close">取消</button>
        <button class="btn-primary" @click="handleSave" :disabled="saving">
          {{ saving ? '保存中...' : (isEditing ? '保存' : '添加') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRulesStore } from '../stores/rules.js'
import { useGroupsStore } from '../stores/groups.js'
import { useAppStore } from '../stores/app.js'
import * as api from '../api.js'

const props = defineProps({
  visible: Boolean,
  editingLB: Object,
})
const emit = defineEmits(['close'])

const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()
const appStore = useAppStore()
const saving = ref(false)

const isEditing = computed(() => !!props.editingLB)

const nodeSearch = ref('')
const filteredRules = computed(() => {
  const kw = nodeSearch.value.trim().toLowerCase()
  if (!kw) return rulesStore.rules
  return rulesStore.rules.filter(r =>
    (r.alias && r.alias.toLowerCase().includes(kw)) ||
    (r.protocol && r.protocol.toLowerCase().includes(kw)) ||
    (r.serverAddr && r.serverAddr.toLowerCase().includes(kw))
  )
})

const defaultForm = () => ({
  alias: '',
  localType: 'mixed',
  localPort: 0,
  groupId: '',
  nodeIds: [],
})
const form = ref(defaultForm())

watch(() => props.visible, (v) => {
  if (v) {
    nodeSearch.value = ''
    if (props.editingLB) {
      form.value = {
        alias: props.editingLB.alias || '',
        localType: 'mixed',
        localPort: props.editingLB.localPort || 0,
        groupId: props.editingLB.groupId || '',
        nodeIds: props.editingLB.nodeIds ? [...props.editingLB.nodeIds] : [],
      }
    } else {
      form.value = defaultForm()
    }
  }
})

async function handleSave() {
  if (!form.value.alias.trim()) { appStore.showToast('请输入别名', 'warning'); return }
  if (!form.value.localPort || form.value.localPort < 1 || form.value.localPort > 65535) {
    appStore.showToast('请输入有效端口', 'warning'); return
  }
  if (form.value.nodeIds.length === 0) { appStore.showToast('请至少选择一个子节点', 'warning'); return }

  saving.value = true
  try {
    const lbData = {
      alias: form.value.alias.trim(),
      localType: form.value.localType,
      localPort: form.value.localPort,
      nodeIds: form.value.nodeIds,
      groupId: form.value.groupId,
    }

    if (isEditing.value) {
      lbData.id = props.editingLB.id
      await api.updateLoadBalancer(lbData)
      appStore.showToast('故障转移已更新', 'success')
    } else {
      await api.addLoadBalancer(lbData)
      appStore.showToast('故障转移已添加', 'success')
    }
    await rulesStore.loadRules()
    close()
  } catch (e) {
    appStore.showToast(`操作失败: ${e}`, 'error')
  } finally {
    saving.value = false
  }
}

function close() { emit('close') }
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
  max-height: 90vh;
  overflow-y: auto;
}
.dialog-large { width: 700px; }
.dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border-color);
  position: sticky; top: 0;
  background: var(--bg-primary);
  z-index: 1;
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
  position: sticky; bottom: 0;
  background: var(--bg-primary);
}

.form-section { margin-bottom: 16px; }
.form-section h4 {
  margin: 0 0 10px 0;
  font-size: 14px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border-color);
  padding-bottom: 6px;
}
.form-row { display: flex; gap: 12px; flex-wrap: wrap; margin-bottom: 8px; }
.form-group { flex: 1; min-width: 150px; }
.form-group label {
  display: block;
  margin-bottom: 4px;
  font-size: 12px;
  color: var(--text-secondary);
}
.form-group input,
.form-group select {
  width: 100%;
  padding: 7px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-sizing: border-box;
}

.node-search {
  width: 100%;
  padding: 6px 10px;
  margin-bottom: 8px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-sizing: border-box;
}
.node-select-list {
  max-height: 200px;
  overflow-y: auto;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  padding: 8px;
}
.node-select-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 0;
  font-size: 13px;
  cursor: pointer;
}
.node-select-item:hover { background: var(--bg-hover); }
.node-select-item input[type="checkbox"] { width: auto; }
.empty-hint { color: var(--text-secondary); font-size: 12px; text-align: center; padding: 12px; }

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
