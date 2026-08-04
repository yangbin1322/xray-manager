import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

const STORAGE_KEY = 'nodeListColumns'

// 节点列表的可配置列。key 与模板中的 col-* 类名对应。
// 勾选框和操作列不在此列表里——它们是交互入口，隐藏后表格就没法用了。
export const NODE_COLUMNS = [
  { key: 'alias', label: '别名' },
  { key: 'group', label: '所属分组' },
  { key: 'protocol', label: '协议' },
  { key: 'server', label: '服务器地址' },
  { key: 'sport', label: '服务器端口' },
  { key: 'lport', label: '本地端口' },
  { key: 'health', label: '健康' },
  { key: 'latency', label: '延迟' },
  { key: 'speed', label: '速度' },
  { key: 'traffic', label: '实时流量' },
  { key: 'trafficTotal', label: '今日/累计' },
  { key: 'ip', label: '真实IP' },
  { key: 'status', label: '状态' },
]

// 默认全部显示，保持与加此功能之前一致
function defaultVisible() {
  return Object.fromEntries(NODE_COLUMNS.map(c => [c.key, true]))
}

function loadVisible() {
  const base = defaultVisible()
  try {
    const saved = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
    // 只认识已知列：升级后新增的列默认显示，删掉的列自动忽略
    for (const col of NODE_COLUMNS) {
      if (typeof saved[col.key] === 'boolean') base[col.key] = saved[col.key]
    }
  } catch {
    // 存储损坏时退回默认，不影响使用
  }
  return base
}

export const useColumnsStore = defineStore('columns', () => {
  const visible = ref(loadVisible())

  function persist() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(visible.value))
    } catch {
      // 隐私模式下 localStorage 可能不可写，忽略即可
    }
  }

  function toggle(key) {
    // 整体替换以触发响应式更新
    visible.value = { ...visible.value, [key]: !visible.value[key] }
    persist()
  }

  function setAll(show) {
    visible.value = Object.fromEntries(NODE_COLUMNS.map(c => [c.key, show]))
    persist()
  }

  function reset() {
    visible.value = defaultVisible()
    persist()
  }

  const isVisible = (key) => visible.value[key] !== false

  // 表头/空行的 colspan 需要算上勾选框和操作两个固定列
  const visibleCount = computed(
    () => NODE_COLUMNS.filter(c => visible.value[c.key]).length + 2
  )

  const hiddenCount = computed(
    () => NODE_COLUMNS.filter(c => !visible.value[c.key]).length
  )

  return { visible, isVisible, visibleCount, hiddenCount, toggle, setAll, reset }
})
