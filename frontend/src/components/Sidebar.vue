<template>
  <div class="sidebar">
    <div class="sidebar-header">
      <h3>节点分组</h3>
      <button class="btn-icon" title="添加分组" @click="showAddGroup = true">+</button>
    </div>

    <div class="sidebar-content">
      <!-- 所有节点 -->
      <div
        :class="['group-item', { active: rulesStore.groupFilter === null }]"
        @click="rulesStore.groupFilter = null"
      >
        <span class="group-name">所有节点</span>
        <span class="group-count">{{ rulesStore.totalCount }}</span>
      </div>

      <!-- 未分组 -->
      <div
        :class="['group-item', { active: rulesStore.groupFilter === UNGROUPED_FILTER }]"
        @click="rulesStore.groupFilter = UNGROUPED_FILTER"
      >
        <div class="group-info">
          <span class="group-name">未分组</span>
        </div>
        <span class="group-count">{{ rulesStore.ungroupedCount }}</span>
        <div class="group-actions">
          <button class="btn-icon-small" @click.stop="startGroup('')" title="启动全部">▶</button>
          <button class="btn-icon-small" @click.stop="stopGroup('')" title="停止全部">■</button>
        </div>
      </div>

      <!-- 分组列表 -->
      <div
        v-for="group in groupsStore.groups"
        :key="group.id"
        :class="['group-item', { active: rulesStore.groupFilter === group.id }]"
        @click="rulesStore.groupFilter = group.id"
      >
        <div class="group-info">
          <span class="group-name">{{ group.name }}</span>
          <span v-if="group.source === 'subscription'" class="group-badge">订阅</span>
        </div>
        <span class="group-count">{{ rulesStore.groupCounts[group.id] || 0 }}</span>
        <div class="group-actions">
          <button class="btn-icon-small" @click.stop="startGroup(group.id)" title="启动全部">▶</button>
          <button class="btn-icon-small" @click.stop="stopGroup(group.id)" title="停止全部">■</button>
          <button
            class="btn-icon-small btn-delete"
            @click.stop="handleDeleteGroup(group)"
            title="删除"
          >×</button>
        </div>
      </div>
    </div>

    <div class="sidebar-footer">
      <button class="btn-small btn-block" @click="$emit('showLBDialog')">添加故障转移</button>
      <button class="btn-small btn-block" @click="$emit('showChainDialog')">添加链式代理</button>
      <button class="btn-small btn-block" @click="$emit('showRelayDialog')">添加会话代理</button>
      <button class="btn-small btn-block" @click="$emit('showSubDialog')">订阅管理</button>
    </div>

    <!-- 添加分组对话框 -->
    <div v-if="showAddGroup" class="dialog-overlay" @click.self="showAddGroup = false">
      <div class="dialog">
        <div class="dialog-header">
          <h3>添加分组</h3>
          <button class="dialog-close" @click="showAddGroup = false">&times;</button>
        </div>
        <div class="dialog-body">
          <div class="form-group">
            <label>分组名称：</label>
            <input v-model="newGroupName" type="text" placeholder="例如：美国节点" />
          </div>
          <div class="form-group">
            <label>描述：</label>
            <input v-model="newGroupDesc" type="text" placeholder="可选" />
          </div>
        </div>
        <div class="dialog-footer">
          <button class="btn-secondary" @click="showAddGroup = false">取消</button>
          <button class="btn-primary" @click="handleAddGroup">添加</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRulesStore, UNGROUPED_FILTER } from '../stores/rules.js'
import { useGroupsStore } from '../stores/groups.js'
import { useAppStore } from '../stores/app.js'
import * as api from '../api.js'

defineEmits(['showLBDialog', 'showChainDialog', 'showRelayDialog', 'showSubDialog'])

const rulesStore = useRulesStore()
const groupsStore = useGroupsStore()
const appStore = useAppStore()

const showAddGroup = ref(false)
const newGroupName = ref('')
const newGroupDesc = ref('')

async function handleAddGroup() {
  if (!newGroupName.value.trim()) {
    appStore.showToast('请输入分组名称', 'warning')
    return
  }
  try {
    await groupsStore.createGroup(newGroupName.value.trim(), newGroupDesc.value.trim())
    showAddGroup.value = false
    newGroupName.value = ''
    newGroupDesc.value = ''
    appStore.showToast('分组创建成功', 'success')
  } catch (e) {
    appStore.showToast(`创建分组失败: ${e}`, 'error')
  }
}

async function handleDeleteGroup(group) {
  const count = rulesStore.groupCounts[group.id] || 0
  // 一个分组可能挂着多个订阅，删分组要把它们全部删掉
  const subs = groupsStore.subscriptionsByGroup[group.id] || []
  const what = subs.length > 0 ? '订阅分组' : '分组'

  const lines = [`确定要删除${what}「${group.name}」吗？`]
  if (subs.length > 1) {
    lines.push(`\n该分组下有 ${subs.length} 个订阅（${subs.map(s => s.name).join('、')}），将一并删除。`)
  }
  if (count > 0) {
    lines.push(`\n这将停止并删除该分组内的 ${count} 个节点，且无法恢复。`)
  }
  const ok = await appStore.confirmDialog(lines.join(''), { title: `删除${what}`, confirmText: '删除' })
  if (!ok) return

  try {
    // 逐个删订阅（每次只删自己的节点），最后再清掉可能残留的分组
    for (const sub of subs) {
      await groupsStore.deleteSubscription(sub.id)
    }
    // 无订阅的分组，或删完订阅后分组仍在（还有手动添加的节点）
    if (groupsStore.groups.some(g => g.id === group.id)) {
      await groupsStore.deleteGroup(group.id)
    }
    // 若当前正筛选被删分组，切回全部
    if (rulesStore.groupFilter === group.id) rulesStore.groupFilter = null
    await rulesStore.loadRules()
    appStore.showToast('删除成功', 'success')
  } catch (e) {
    appStore.showToast(`删除失败: ${e}`, 'error')
  }
}

async function startGroup(groupId) {
  try {
    await api.startAllRulesInGroup(groupId)
    await rulesStore.loadRules()
    appStore.showToast('已启动分组中的所有规则', 'success')
  } catch (e) {
    appStore.showToast(`启动失败: ${e}`, 'error', 5000)
  }
}

async function stopGroup(groupId) {
  try {
    await api.stopAllRulesInGroup(groupId)
    await rulesStore.loadRules()
    appStore.showToast('已停止分组中的所有规则', 'success')
  } catch (e) {
    appStore.showToast(`停止失败: ${e}`, 'error', 5000)
  }
}
</script>

<style scoped>
.sidebar {
  width: 220px;
  border-right: 1px solid var(--border-color);
  display: flex;
  flex-direction: column;
  background: var(--bg-secondary);
}

.sidebar-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-color);
}

.sidebar-header h3 {
  margin: 0;
  font-size: 14px;
}

.sidebar-content {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}

.group-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 12px;
  border-radius: 6px;
  cursor: pointer;
  margin-bottom: 2px;
  font-size: 13px;
}

.group-item:hover { background: var(--bg-hover); }
.group-item.active { background: var(--primary-light); color: var(--primary-color); font-weight: 500; }

.group-info {
  display: flex;
  align-items: center;
  gap: 6px;
  overflow: hidden;
  flex: 1;
  min-width: 0;
}

.group-name {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.group-badge {
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 3px;
  background: var(--primary-light);
  color: var(--primary-color);
  white-space: nowrap;
}

.group-count {
  font-size: 11px;
  color: var(--text-secondary);
}

/* 操作按钮默认隐藏、hover 时显示。
   但只靠 hover 在触摸板/触屏上很难触达（表现为"点了没反应"），
   因此当前选中的分组也常驻显示。 */
.group-actions {
  display: none;
  gap: 2px;
}

.group-item:hover .group-actions,
.group-item.active .group-actions { display: flex; }
.group-item:hover .group-count,
.group-item.active .group-count { display: none; }

.btn-icon-small {
  padding: 2px 6px;
  border: none;
  background: none;
  cursor: pointer;
  font-size: 12px;
  border-radius: 3px;
  color: var(--text-secondary);
}

.btn-icon-small:hover { background: var(--bg-hover); color: var(--text-primary); }
.btn-delete:hover { color: #e74c3c !important; }

.sidebar-footer {
  padding: 8px;
  border-top: 1px solid var(--border-color);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.btn-small {
  padding: 6px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  background: var(--bg-primary);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 12px;
}

.btn-block { width: 100%; }
.btn-small:hover { background: var(--bg-hover); }

/* Dialog styles */
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
  width: 400px;
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
.dialog-footer { padding: 12px 20px; border-top: 1px solid var(--border-color); display: flex; justify-content: flex-end; gap: 8px; }

.form-group { margin-bottom: 12px; }
.form-group label { display: block; margin-bottom: 4px; font-size: 13px; color: var(--text-secondary); }
.form-group input {
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-primary);
  color: var(--text-primary);
  box-sizing: border-box;
}

.btn-primary {
  padding: 8px 16px;
  background: var(--primary-color);
  color: #fff;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
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

.btn-icon {
  border: none;
  background: none;
  cursor: pointer;
  font-size: 16px;
  padding: 2px 6px;
  border-radius: 4px;
}

.btn-icon:hover { background: var(--bg-hover); }
</style>
