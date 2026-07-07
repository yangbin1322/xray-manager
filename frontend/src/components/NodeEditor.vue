<template>
  <div v-if="visible" class="dialog-overlay" @click.self="close">
    <div class="dialog dialog-large">
      <div class="dialog-header">
        <h3>{{ isEditing ? '编辑规则' : '添加规则' }}</h3>
        <button class="dialog-close" @click="close">&times;</button>
      </div>

      <div class="dialog-body">
        <NodeForm v-model="form" />
      </div>

      <div class="dialog-footer">
        <button class="btn-secondary" @click="close">取消</button>
        <button class="btn-primary" @click="handleSave">{{ isEditing ? '保存' : '添加' }}</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRulesStore } from '../stores/rules.js'
import { useAppStore } from '../stores/app.js'
import NodeForm, { defaultNodeForm, normalizeTransport } from './NodeForm.vue'

const props = defineProps({
  visible: Boolean,
  editingRule: Object,
})

const emit = defineEmits(['close'])

const rulesStore = useRulesStore()
const appStore = useAppStore()

const isEditing = computed(() => !!props.editingRule)

const form = ref(defaultNodeForm())

// 当 editingRule 变化时填充表单
watch(() => props.editingRule, (rule) => {
  if (rule) {
    form.value = JSON.parse(JSON.stringify(rule))
    if (!form.value.settings) form.value.settings = defaultNodeForm().settings
  } else {
    form.value = defaultNodeForm()
  }
}, { immediate: true })

watch(() => props.visible, (v) => {
  if (v && !props.editingRule) {
    form.value = defaultNodeForm()
  }
})

async function handleSave() {
  // 基本校验
  if (!form.value.serverAddr) {
    appStore.showToast('请输入服务器地址', 'warning')
    return
  }
  if (!form.value.serverPort || form.value.serverPort <= 0) {
    appStore.showToast('请输入有效的服务器端口', 'warning')
    return
  }
  if (!form.value.localPort || form.value.localPort <= 0) {
    appStore.showToast('请输入有效的本地端口', 'warning')
    return
  }

  // 规范化传输层配置
  normalizeTransport(form.value)

  try {
    if (isEditing.value) {
      await rulesStore.updateRule(form.value.id, form.value)
      appStore.showToast('规则已更新', 'success')
    } else {
      await rulesStore.addRule(form.value)
      appStore.showToast('规则已添加', 'success')
    }
    close()
  } catch (e) {
    appStore.showToast(`保存失败: ${e}`, 'error')
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
  position: sticky;
  top: 0;
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
  position: sticky;
  bottom: 0;
  background: var(--bg-primary);
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
