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

// 批量更新节点（后端只保存一次配置）
export async function updateNodes(rules) {
  return await MyService.UpdateNodes(rules)
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

// 批量启动/停止节点（后端并发处理，只保存一次配置）
export async function startNodes(ids) {
  return await MyService.StartNodes(ids)
}

export async function stopNodes(ids) {
  return await MyService.StopNodes(ids)
}

// ==================== 导入导出 ====================

export async function exportConfig(ruleIds = [], includeSubscriptions = false) {
  return await MyService.ExportConfig(ruleIds, includeSubscriptions)
}

export async function importConfig() {
  return await MyService.ImportConfig()
}

export async function importShareLinks(text, groupId = '', newGroupName = '') {
  return await MyService.ImportShareLinks(text, groupId, newGroupName)
}

export async function getPendingPortConflicts() {
  return await MyService.GetPendingPortConflicts()
}

export async function resolvePortConflicts(resourceIDs) {
  return await MyService.ResolvePortConflicts(resourceIDs)
}

export async function exportShareLinks(ruleIds) {
  return await MyService.ExportShareLinks(ruleIds)
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

export async function addSubscription(name, url, autoUpdate, updateInterval, updateMode = 'direct', updateProxyId = '') {
  return await MyService.AddSubscription(name, url, autoUpdate, updateInterval, updateMode, updateProxyId)
}

export async function setSubscriptionUpdateMode(subID, mode, proxyID) {
  return await MyService.SetSubscriptionUpdateMode(subID, mode, proxyID)
}

export async function editSubscription(subID, name, url, autoUpdate, updateInterval, updateMode = 'direct', updateProxyId = '') {
  return await MyService.EditSubscription(subID, name, url, autoUpdate, updateInterval, updateMode, updateProxyId)
}

export async function updateSubscriptionByID(subID) {
  return await MyService.UpdateSubscriptionByID(subID)
}

export async function deleteSubscription(subID) {
  return await MyService.DeleteSubscription(subID)
}

// ==================== 故障转移 ====================

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

export async function testLoadBalancerSpeed(id) {
  return await MyService.TestLoadBalancerSpeed(id)
}

export async function testChainProxySpeed(id) {
  return await MyService.TestChainProxySpeed(id)
}

// ==================== 健康检查 ====================

export async function getHealthCheckConfig() {
  return await MyService.GetHealthCheckConfig()
}

export async function setHealthCheckConfig(cfg) {
  return await MyService.SetHealthCheckConfig(cfg)
}

// ==================== 测速配置 ====================

export async function getSpeedTestConfig() {
  return await MyService.GetSpeedTestConfig()
}

export async function getDefaultSpeedTestConfig() {
  return await MyService.GetDefaultSpeedTestConfig()
}

export async function setSpeedTestConfig(cfg) {
  return await MyService.SetSpeedTestConfig(cfg)
}

// ==================== HTTP API 配置 ====================

export async function getHTTPAPIConfig() {
  return await MyService.GetHTTPAPIConfig()
}

export async function setHTTPAPIConfig(cfg) {
  return await MyService.SetHTTPAPIConfig(cfg)
}

export async function checkNodeHealth(ruleID) {
  return await MyService.CheckNodeHealth(ruleID)
}

export async function checkSelectedNodesHealth(ruleIDs) {
  return await MyService.CheckSelectedNodesHealth(ruleIDs)
}

export async function checkAllNodesHealth() {
  return await MyService.CheckAllNodesHealth()
}

// ==================== 流量统计 ====================

export async function resetRuleTraffic(ruleID = '') {
  return await MyService.ResetRuleTraffic(ruleID)
}



// ==================== 应用更新 ====================

export async function getAppVersion() {
  return await MyService.GetAppVersion()
}

export async function getUpdateConfig() {
  return await MyService.GetUpdateConfig()
}

export async function setUpdateConfig(cfg) {
  return await MyService.SetUpdateConfig(cfg)
}

export async function checkForUpdate() {
  return await MyService.CheckForUpdate()
}

export async function downloadAndInstallUpdate() {
  return await MyService.DownloadAndInstallUpdate()
}

export async function openReleasePage() {
  return await MyService.OpenReleasePage()
}

// ==================== 全局前置代理 ====================

export async function getPreProxy() {
  return await MyService.GetPreProxy()
}

export async function setPreProxy(nodeID) {
  return await MyService.SetPreProxy(nodeID)
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
