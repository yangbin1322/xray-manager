<template>
  <div v-if="visible" class="dialog-overlay">
    <div class="dialog">
      <div class="dialog-header">
        <div>
          <h3>本地端口冲突</h3>
          <p>以下节点的端口已被其他客户端配置占用。</p>
        </div>
      </div>

      <div class="dialog-body">
        <label class="select-all">
          <input type="checkbox" :checked="allSelected" @change="toggleAll" />
          <span>选择全部冲突节点</span>
          <strong>{{ selectedIds.size }}/{{ conflicts.length }}</strong>
        </label>

        <div class="conflict-list">
          <label v-for="item in conflicts" :key="item.resourceId" class="conflict-row">
            <input
              type="checkbox"
              :checked="selectedIds.has(item.resourceId)"
              @change="toggleItem(item.resourceId)"
            />
            <div class="conflict-content">
              <div class="conflict-title">
                <strong>{{ item.alias || '未命名节点' }}</strong>
                <span class="type-label">{{ typeLabel(item.resourceType) }}</span>
                <span class="port">端口 {{ item.port }}</span>
              </div>
              <div class="owner">已由 {{ typeLabel(item.ownerResourceType) }}「{{ item.ownerAlias || '未命名节点' }}」占用</div>
              <div class="path" :title="item.ownerExecutablePath">程序：{{ item.ownerExecutablePath }}</div>
              <div class="path" :title="item.ownerConfigPath">配置：{{ item.ownerConfigPath }}</div>
            </div>
          </label>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn-secondary" :disabled="saving" @click="$emit('keep')">保持原端口</button>
        <button class="btn-primary" :disabled="saving || selectedIds.size === 0" @click="resolveSelected">
          {{ saving ? '正在分配...' : `重新分配选中节点 (${selectedIds.size})` }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'

const props = defineProps({
  visible: Boolean,
  conflicts: { type: Array, default: () => [] },
  saving: Boolean,
})
const emit = defineEmits(['keep', 'resolve'])
const selectedIds = ref(new Set())

watch(() => props.conflicts, (items) => {
  selectedIds.value = new Set(items.map(item => item.resourceId))
}, { immediate: true })

const allSelected = computed(() => props.conflicts.length > 0 && selectedIds.value.size === props.conflicts.length)

function toggleItem(resourceId) {
  const next = new Set(selectedIds.value)
  if (next.has(resourceId)) next.delete(resourceId)
  else next.add(resourceId)
  selectedIds.value = next
}

function toggleAll(event) {
  selectedIds.value = event.target.checked
    ? new Set(props.conflicts.map(item => item.resourceId))
    : new Set()
}

function resolveSelected() {
  emit('resolve', Array.from(selectedIds.value))
}

function typeLabel(type) {
  return ({ rule: '规则', loadBalancer: '故障转移', chainProxy: '链式代理' })[type] || '节点'
}
</script>

<style scoped>
.dialog-overlay { position: fixed; inset: 0; z-index: 1400; display: flex; align-items: center; justify-content: center; padding: 20px; background: rgba(0, 0, 0, 0.55); }
.dialog { width: min(760px, 100%); max-height: min(720px, 90vh); display: flex; flex-direction: column; overflow: hidden; border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-primary); box-shadow: 0 16px 48px rgba(0, 0, 0, 0.28); }
.dialog-header { padding: 18px 20px 14px; border-bottom: 1px solid var(--border-color); }
.dialog-header h3 { margin: 0; font-size: 17px; }
.dialog-header p { margin: 5px 0 0; color: var(--text-secondary); font-size: 13px; }
.dialog-body { min-height: 0; padding: 14px 20px; overflow-y: auto; }
.select-all { display: flex; align-items: center; gap: 9px; padding: 9px 10px; margin-bottom: 10px; border-bottom: 1px solid var(--border-color); cursor: pointer; }
.select-all strong { margin-left: auto; color: var(--text-secondary); font-size: 12px; }
.conflict-list { display: grid; gap: 8px; }
.conflict-row { display: grid; grid-template-columns: 18px minmax(0, 1fr); gap: 10px; padding: 12px; border: 1px solid var(--border-color); border-radius: 6px; cursor: pointer; }
.conflict-row:hover { background: var(--bg-hover); }
.conflict-row input { margin-top: 3px; }
.conflict-content { min-width: 0; }
.conflict-title { display: flex; align-items: center; gap: 8px; min-width: 0; }
.conflict-title strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.type-label, .port { flex: none; padding: 2px 6px; border-radius: 4px; font-size: 11px; }
.type-label { color: var(--text-secondary); background: var(--bg-secondary); }
.port { color: #fff; background: #c0392b; }
.owner { margin-top: 6px; color: var(--text-primary); font-size: 12px; }
.path { margin-top: 3px; overflow-wrap: anywhere; color: var(--text-secondary); font-size: 12px; }
.dialog-footer { display: flex; justify-content: flex-end; gap: 8px; padding: 12px 20px; border-top: 1px solid var(--border-color); background: var(--bg-primary); }
.btn-primary, .btn-secondary { min-height: 34px; padding: 7px 14px; border-radius: 4px; cursor: pointer; font-size: 13px; }
.btn-primary { border: 1px solid var(--primary-color); color: #fff; background: var(--primary-color); }
.btn-secondary { border: 1px solid var(--border-color); color: var(--text-primary); background: var(--bg-secondary); }
button:disabled { cursor: not-allowed; opacity: 0.55; }
@media (max-width: 600px) { .dialog-overlay { padding: 10px; } .dialog-footer { flex-wrap: wrap; } .dialog-footer button { flex: 1 1 210px; } }
</style>
