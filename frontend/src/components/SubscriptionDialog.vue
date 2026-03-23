<template>
  <div v-if="visible" class="dialog-overlay" @click.self="close">
    <div class="dialog dialog-large">
      <div class="dialog-header">
        <h3>订阅管理</h3>
        <button class="dialog-close" @click="close">&times;</button>
      </div>

      <div class="dialog-body">
        <!-- 订阅列表 -->
        <table class="sub-table">
          <thead>
            <tr>
              <th>名称</th>
              <th>节点数</th>
              <th>类型</th>
              <th>上次更新</th>
              <th>下次更新</th>
              <th>自动</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="groupsStore.subscriptions.length === 0">
              <td colspan="7" class="empty-row">暂无订阅</td>
            </tr>
            <tr v-for="sub in groupsStore.subscriptions" :key="sub.id">
              <td>{{ sub.name }}</td>
              <td>{{ sub.nodeCount || 0 }}</td>
              <td>{{ sub.type || '-' }}</td>
              <td class="td-small">{{ sub.lastUpdate || '-' }}</td>
              <td class="td-small">{{ sub.nextUpdate || '-' }}</td>
              <td>{{ sub.autoUpdate ? '✓' : '×' }}</td>
              <td>
                <button class="btn-action-sm" @click="handleUpdate(sub)" :disabled="updating === sub.id">
                  {{ updating === sub.id ? '更新中...' : '更新' }}
                </button>
                <button class="btn-action-sm btn-del" @click="handleDelete(sub)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>

        <!-- 添加订阅表单 -->
        <div class="form-section" style="margin-top: 16px;">
          <h4>添加订阅</h4>
          <div class="form-row">
            <div class="form-group">
              <label>订阅名称：</label>
              <input v-model="form.name" type="text" placeholder="例如：机场订阅" />
            </div>
            <div class="form-group" style="flex: 2;">
              <label>订阅地址：</label>
              <input v-model="form.url" type="text" placeholder="https://..." />
            </div>
          </div>
          <div class="form-row">
            <div class="form-group">
              <label>
                <input v-model="form.autoUpdate" type="checkbox" /> 自动更新
              </label>
            </div>
            <div class="form-group">
              <label>更新间隔（小时）：</label>
              <input v-model.number="form.updateInterval" type="number" min="1" placeholder="6" />
            </div>
            <div class="form-group" style="display:flex;align-items:flex-end;">
              <button class="btn-primary" @click="handleAdd" :disabled="adding">
                {{ adding ? '添加中...' : '添加订阅' }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn-secondary" @click="close">关闭</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, watch } from 'vue'
import { useGroupsStore } from '../stores/groups.js'
import { useRulesStore } from '../stores/rules.js'
import { useAppStore } from '../stores/app.js'

const props = defineProps({ visible: Boolean })
const emit = defineEmits(['close'])

const groupsStore = useGroupsStore()
const rulesStore = useRulesStore()
const appStore = useAppStore()

const updating = ref(null)
const adding = ref(false)

const defaultForm = () => ({ name: '', url: '', autoUpdate: true, updateInterval: 6 })
const form = ref(defaultForm())

watch(() => props.visible, (v) => {
  if (v) {
    groupsStore.loadSubscriptions()
  }
})

async function handleAdd() {
  if (!form.value.name.trim()) { appStore.showToast('请输入订阅名称', 'warning'); return }
  if (!form.value.url.trim()) { appStore.showToast('请输入订阅地址', 'warning'); return }
  if (!form.value.updateInterval || form.value.updateInterval < 1) { appStore.showToast('请输入有效的更新间隔', 'warning'); return }

  adding.value = true
  try {
    await groupsStore.addSubscription(form.value.name.trim(), form.value.url.trim(), form.value.autoUpdate, form.value.updateInterval)
    await rulesStore.loadRules()
    form.value = defaultForm()
    appStore.showToast('订阅添加成功', 'success')
  } catch (e) {
    appStore.showToast(`添加订阅失败: ${e}`, 'error')
  } finally {
    adding.value = false
  }
}

async function handleUpdate(sub) {
  updating.value = sub.id
  try {
    await groupsStore.updateSubscription(sub.id)
    await rulesStore.loadRules()
    appStore.showToast('订阅更新成功', 'success')
  } catch (e) {
    appStore.showToast(`更新订阅失败: ${e}`, 'error')
  } finally {
    updating.value = null
  }
}

async function handleDelete(sub) {
  if (!confirm(`确定要删除订阅「${sub.name}」吗？这将同时删除该订阅的所有节点！`)) return
  try {
    await groupsStore.deleteSubscription(sub.id)
    await rulesStore.loadRules()
    appStore.showToast('订阅已删除', 'success')
  } catch (e) {
    appStore.showToast(`删除订阅失败: ${e}`, 'error')
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
.dialog-large { width: 750px; }
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

.sub-table { width: 100%; border-collapse: collapse; font-size: 13px; }
.sub-table th {
  background: var(--bg-secondary);
  padding: 8px 10px;
  text-align: left;
  font-weight: 500;
  font-size: 12px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border-color);
}
.sub-table td {
  padding: 7px 10px;
  border-bottom: 1px solid var(--border-color);
}
.td-small { font-size: 11px; }
.empty-row { text-align: center; color: var(--text-secondary); padding: 20px !important; }

.form-section { margin-bottom: 16px; }
.form-section h4 {
  margin: 0 0 10px 0;
  font-size: 14px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border-color);
  padding-bottom: 6px;
}
.form-row { display: flex; gap: 12px; flex-wrap: wrap; margin-bottom: 8px; }
.form-group { flex: 1; min-width: 120px; }
.form-group label {
  display: block;
  margin-bottom: 4px;
  font-size: 12px;
  color: var(--text-secondary);
}
.form-group input[type="text"],
.form-group input[type="number"] {
  width: 100%;
  padding: 7px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-sizing: border-box;
}

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
.btn-action-sm {
  padding: 3px 8px;
  border: 1px solid var(--border-color);
  border-radius: 3px;
  background: var(--bg-secondary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 11px;
  margin-right: 3px;
}
.btn-action-sm:disabled { opacity: 0.6; cursor: not-allowed; }
.btn-del { color: #95a5a6; }
.btn-del:hover { color: #e74c3c; border-color: #e74c3c; }
</style>
