import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api.js'

export const useGroupsStore = defineStore('groups', () => {
  const groups = ref([])
  const subscriptions = ref([])

  async function loadGroups() {
    try {
      groups.value = await api.getGroups() || []
    } catch (e) {
      console.error('加载分组失败:', e)
    }
  }

  async function loadSubscriptions() {
    try {
      subscriptions.value = await api.getSubscriptions() || []
    } catch (e) {
      console.error('加载订阅失败:', e)
    }
  }

  async function createGroup(name, description) {
    await api.createGroup(name, description)
    await loadGroups()
  }

  async function deleteGroup(groupID) {
    await api.deleteGroup(groupID)
    await loadGroups()
  }

  // groupId 为空表示按订阅名新建分组；传已有分组 ID 则汇入该分组
  async function addSubscription(name, url, autoUpdate, updateInterval, updateMode = 'direct', updateProxyId = '', groupId = '') {
    await api.addSubscription(name, url, autoUpdate, updateInterval, updateMode, updateProxyId, groupId)
    await loadSubscriptions()
    await loadGroups()
  }

  async function updateSubscription(subID) {
    await api.updateSubscriptionByID(subID)
    await loadSubscriptions()
  }

  // groupId 为空表示保持当前分组不变
  async function editSubscription(subID, name, url, autoUpdate, updateInterval, updateMode = 'direct', updateProxyId = '', groupId = '') {
    await api.editSubscription(subID, name, url, autoUpdate, updateInterval, updateMode, updateProxyId, groupId)
    await loadSubscriptions()
    await loadGroups()
  }

  async function deleteSubscription(subID) {
    await api.deleteSubscription(subID)
    await loadSubscriptions()
    await loadGroups()
  }

  // 分组 ID -> 该分组下的订阅列表。一个分组可汇集多个订阅，
  // 删除分组、展示归属都要按这个映射来，不能假设一对一。
  const subscriptionsByGroup = computed(() => {
    const map = {}
    for (const sub of subscriptions.value) {
      if (!sub.groupId) continue
      ;(map[sub.groupId] ||= []).push(sub)
    }
    return map
  })

  function groupById(groupId) {
    return groups.value.find(g => g.id === groupId) || null
  }

  return {
    groups, subscriptions,
    subscriptionsByGroup,
    loadGroups, loadSubscriptions, groupById,
    createGroup, deleteGroup,
    addSubscription, updateSubscription, editSubscription, deleteSubscription,
  }
})
