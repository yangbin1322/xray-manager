<template>
  <div v-if="visible" class="dialog-overlay" @click.self="close">
    <div class="dialog dialog-large">
      <div class="dialog-header">
        <h3>{{ isEditing ? '编辑链式代理' : '添加链式代理' }}</h3>
        <button class="dialog-close" @click="close">&times;</button>
      </div>

      <div class="dialog-body">
        <div class="form-section">
          <h4>基本信息</h4>
          <div class="form-row">
            <div class="form-group">
              <label>别名：</label>
              <input v-model="form.alias" type="text" placeholder="例如：链式-香港转日本" />
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
          <h4>可用节点（点击添加到链）</h4>
          <div class="node-select-list">
            <!-- 普通节点 -->
            <div v-for="rule in rulesStore.rules" :key="rule.id" class="node-select-item">
              <button class="btn-add" @click="addToChain(rule.id, rule.alias, 'rule')">+ 添加</button>
              <span>{{ rule.alias }} ({{ rule.protocol }})</span>
            </div>

            <hr v-if="rulesStore.loadBalancers.length > 0" style="margin: 8px 0;" />
            <strong v-if="rulesStore.loadBalancers.length > 0" style="font-size: 12px;">负载均衡节点：</strong>
            <div v-for="lb in rulesStore.loadBalancers" :key="lb.id" class="node-select-item">
              <button class="btn-add" @click="addToChain(lb.id, lb.alias, 'lb')">+ 添加</button>
              <span>[LB] {{ lb.alias }}</span>
            </div>
          </div>
        </div>

        <div class="form-section">
          <h4>链路顺序（拖拽排序）</h4>
          <div class="chain-container">
            <span v-if="chainNodes.length === 0" class="empty-hint">请从上方列表添加节点</span>
            <template v-for="(node, index) in chainNodes" :key="index">
              <span
                class="chain-node-item"
                draggable="true"
                @dragstart="onDragStart(index, $event)"
                @dragover.prevent="onDragOver(index, $event)"
                @dragleave="onDragLeave($event)"
                @drop="onDrop(index, $event)"
                @dragend="onDragEnd"
              >
                {{ node.type === 'lb' ? '[LB] ' : '' }}{{ node.name }}
                <span class="chain-remove" @click="removeFromChain(index)">&times;</span>
              </span>
              <span v-if="index < chainNodes.length - 1" class="chain-arrow"> &rarr; </span>
            </template>
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
  editingChain: Object,
})
const emit = defineEmits(['close'])

const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()
const appStore = useAppStore()
const saving = ref(false)

const isEditing = computed(() => !!props.editingChain)

const defaultForm = () => ({
  alias: '',
  localType: 'mixed',
  localPort: 0,
  groupId: '',
})
const form = ref(defaultForm())
const chainNodes = ref([])

let dragIndex = null

watch(() => props.visible, (v) => {
  if (v) {
    if (props.editingChain) {
      form.value = {
        alias: props.editingChain.alias || '',
        localType: 'mixed',
        localPort: props.editingChain.localPort || 0,
        groupId: props.editingChain.groupId || '',
      }
      // Reconstruct chain nodes from IDs
      chainNodes.value = (props.editingChain.chainNodes || []).map(id => {
        const rule = rulesStore.rules.find(r => r.id === id)
        const lb = rulesStore.loadBalancers.find(l => l.id === id)
        return {
          id,
          name: rule ? rule.alias : (lb ? lb.alias : id),
          type: lb ? 'lb' : 'rule',
        }
      })
    } else {
      form.value = defaultForm()
      chainNodes.value = []
    }
  }
})

function addToChain(id, name, type) {
  chainNodes.value.push({ id, name, type })
}

function removeFromChain(index) {
  chainNodes.value.splice(index, 1)
}

function onDragStart(index, e) {
  dragIndex = index
  e.dataTransfer.effectAllowed = 'move'
  e.target.style.opacity = '0.4'
}

function onDragOver(index, e) {
  e.dataTransfer.dropEffect = 'move'
  e.target.closest('.chain-node-item')?.classList.add('chain-drag-over')
}

function onDragLeave(e) {
  e.target.closest('.chain-node-item')?.classList.remove('chain-drag-over')
}

function onDrop(toIndex, e) {
  e.target.closest('.chain-node-item')?.classList.remove('chain-drag-over')
  if (dragIndex !== null && dragIndex !== toIndex) {
    const [moved] = chainNodes.value.splice(dragIndex, 1)
    chainNodes.value.splice(toIndex, 0, moved)
  }
  dragIndex = null
}

function onDragEnd(e) {
  e.target.style.opacity = '1'
  dragIndex = null
}

async function handleSave() {
  if (!form.value.alias.trim()) { appStore.showToast('请输入别名', 'warning'); return }
  if (!form.value.localPort || form.value.localPort < 1 || form.value.localPort > 65535) {
    appStore.showToast('请输入有效端口', 'warning'); return
  }
  if (chainNodes.value.length < 2) { appStore.showToast('链式代理至少需要2个节点', 'warning'); return }

  saving.value = true
  try {
    const chainData = {
      alias: form.value.alias.trim(),
      localType: form.value.localType,
      localPort: form.value.localPort,
      chainNodes: chainNodes.value.map(n => n.id),
      groupId: form.value.groupId,
    }

    if (isEditing.value) {
      chainData.id = props.editingChain.id
      await api.updateChainProxy(chainData)
      appStore.showToast('链式代理已更新', 'success')
    } else {
      await api.addChainProxy(chainData)
      appStore.showToast('链式代理已添加', 'success')
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

.node-select-list {
  max-height: 180px;
  overflow-y: auto;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  padding: 8px;
}
.node-select-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 0;
  font-size: 13px;
}
.btn-add {
  padding: 2px 8px;
  border: 1px solid var(--primary-color);
  border-radius: 3px;
  background: transparent;
  color: var(--primary-color);
  cursor: pointer;
  font-size: 11px;
  white-space: nowrap;
}
.btn-add:hover { background: var(--primary-light); }

.chain-container {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  min-height: 40px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  padding: 8px;
}
.chain-node-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  background: var(--primary-light);
  border: 1px solid var(--primary-color);
  border-radius: 4px;
  font-size: 12px;
  cursor: grab;
  user-select: none;
}
.chain-node-item.chain-drag-over {
  border-style: dashed;
  background: var(--bg-hover);
}
.chain-remove {
  cursor: pointer;
  color: var(--text-secondary);
  font-weight: bold;
  margin-left: 2px;
}
.chain-remove:hover { color: #e74c3c; }
.chain-arrow { color: var(--text-secondary); font-size: 14px; }
.empty-hint { color: var(--text-secondary); font-size: 12px; }

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
