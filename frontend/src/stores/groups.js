import { defineStore } from 'pinia'
import { ref } from 'vue'
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

  async function addSubscription(name, url, autoUpdate, updateInterval) {
    await api.addSubscription(name, url, autoUpdate, updateInterval)
    await loadSubscriptions()
    await loadGroups()
  }

  async function updateSubscription(subID) {
    await api.updateSubscriptionByID(subID)
    await loadSubscriptions()
  }

  async function deleteSubscription(subID) {
    await api.deleteSubscription(subID)
    await loadSubscriptions()
    await loadGroups()
  }

  return {
    groups, subscriptions,
    loadGroups, loadSubscriptions,
    createGroup, deleteGroup,
    addSubscription, updateSubscription, deleteSubscription,
  }
})
