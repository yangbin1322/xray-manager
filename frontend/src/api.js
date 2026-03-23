/**
 * API 封装层 - 统一封装 Wails 后端绑定调用
 * 所有后端交互都通过此层，避免前端直接拼接逻辑
 */
import { MyService } from '../bindings/xray-manager/index.js'

// ==================== 规则管理 ====================

export async function getRules() {
  return await MyService.GetRules()
}

export async function addRule(rule) {
  return await MyService.AddRule(rule)
}

export async function updateRule(id, rule) {
  return await MyService.UpdateRule(id, rule)
}

export async function deleteRule(id) {
  return await MyService.DeleteRule(id)
}

export async function startRule(id) {
  return await MyService.StartRule(id)
}

export async function stopRule(id) {
  return await MyService.StopRule(id)
}

export async function saveRuleOrder(orderedIDs) {
  return await MyService.SaveRuleOrder(orderedIDs)
}

// ==================== 导入导出 ====================

export async function exportConfig(ruleIds = []) {
  return await MyService.ExportConfig(ruleIds)
}

export async function importConfig() {
  return await MyService.ImportConfig()
}

export async function importShareLinks(text) {
  return await MyService.ImportShareLinks(text)
}

// ==================== 分组管理 ====================

export async function getGroups() {
  return await MyService.GetGroups()
}

export async function createGroup(name, description) {
  return await MyService.CreateGroup(name, description)
}

export async function deleteGroup(groupID) {
  return await MyService.DeleteGroup(groupID)
}

export async function startAllRulesInGroup(groupID) {
  return await MyService.StartAllRulesInGroup(groupID)
}

export async function stopAllRulesInGroup(groupID) {
  return await MyService.StopAllRulesInGroup(groupID)
}

// ==================== 订阅管理 ====================

export async function getSubscriptions() {
  return await MyService.GetSubscriptions()
}

export async function addSubscription(name, url, autoUpdate, updateInterval) {
  return await MyService.AddSubscription(name, url, autoUpdate, updateInterval)
}

export async function updateSubscriptionByID(subID) {
  return await MyService.UpdateSubscriptionByID(subID)
}

export async function deleteSubscription(subID) {
  return await MyService.DeleteSubscription(subID)
}

// ==================== 负载均衡 ====================

export async function getLoadBalancers() {
  return await MyService.GetLoadBalancers()
}

export async function addLoadBalancer(lb) {
  return await MyService.AddLoadBalancer(lb)
}

export async function updateLoadBalancer(lb) {
  return await MyService.UpdateLoadBalancer(lb)
}

export async function deleteLoadBalancer(id) {
  return await MyService.DeleteLoadBalancer(id)
}

export async function startLoadBalancer(id) {
  return await MyService.StartLoadBalancer(id)
}

export async function stopLoadBalancer(id) {
  return await MyService.StopLoadBalancer(id)
}

// ==================== 链式代理 ====================

export async function getChainProxies() {
  return await MyService.GetChainProxies()
}

export async function addChainProxy(chain) {
  return await MyService.AddChainProxy(chain)
}

export async function updateChainProxy(chain) {
  return await MyService.UpdateChainProxy(chain)
}

export async function deleteChainProxy(id) {
  return await MyService.DeleteChainProxy(id)
}

export async function startChainProxy(id) {
  return await MyService.StartChainProxy(id)
}

export async function stopChainProxy(id) {
  return await MyService.StopChainProxy(id)
}

// ==================== 测速 ====================

export async function testRuleSpeed(ruleID) {
  return await MyService.TestRuleSpeed(ruleID)
}

export async function testAllRulesSpeed() {
  return await MyService.TestAllRulesSpeed()
}

export async function testSelectedRulesSpeed(ruleIDs) {
  return await MyService.TestSelectedRulesSpeed(ruleIDs)
}

// ==================== 系统代理 ====================

export async function enableSystemProxy(ruleID) {
  return await MyService.EnableSystemProxy(ruleID)
}

export async function disableSystemProxy() {
  return await MyService.DisableSystemProxy()
}

export async function getSystemProxyStatus() {
  return await MyService.GetSystemProxyStatus()
}

// ==================== 日志 ====================

export async function getLogs() {
  return await MyService.GetLogs()
}

export async function searchLogs(keyword, level) {
  return await MyService.SearchLogs(keyword, level)
}

export async function filterLogsByLevel(level) {
  return await MyService.FilterLogsByLevel(level)
}

export async function clearLogs() {
  return await MyService.ClearLogs()
}

// ==================== 端口 ====================

export async function checkPortAvailable(port) {
  return await MyService.CheckPortAvailable(port)
}

export async function recommendPort() {
  return await MyService.RecommendPort()
}

// ==================== 自启动 ====================

export async function getAutoStart() {
  return await MyService.GetAutoStart()
}

export async function setAutoStart(enabled) {
  return await MyService.SetAutoStart(enabled)
}
