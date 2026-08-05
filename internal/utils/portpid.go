package utils

import (
	"sync"
	"time"
)

// portPIDCacheTTL 全表缓存有效期。
//
// 批量启动/停止上千节点时，每个节点都要查一次端口占用；这些查询集中在很短的
// 时间窗口内，共用一次 netstat/lsof 的结果即可，没必要各自 fork 一次。
// 取值需要平衡两个方向：太长会读到过期的占用信息（端口刚被释放却仍报占用），
// 太短则起不到合并效果。1 秒足以覆盖一次批量操作中的连续查询。
const portPIDCacheTTL = time.Second

var (
	portPIDMu      sync.Mutex
	portPIDCache   map[int][]int
	portPIDFetched time.Time
)

// GetAllPortPIDs 返回「监听端口 -> PID 列表」全表，结果在 portPIDCacheTTL 内复用。
func GetAllPortPIDs() map[int][]int {
	portPIDMu.Lock()
	defer portPIDMu.Unlock()
	return allPortPIDsLocked()
}

// GetPortPIDs 获取监听指定 TCP 端口的进程 PID 列表。
//
// 内部走全表查询：一次 netstat/lsof 的开销与单端口查询相同，但批量场景下
// 后续调用可以直接命中缓存，避免上千次 fork。
func GetPortPIDs(port int) []int {
	portPIDMu.Lock()
	defer portPIDMu.Unlock()
	return allPortPIDsLocked()[port]
}

// InvalidatePortPIDCache 丢弃缓存，强制下次查询重新取数。
//
// 停止进程后端口占用情况会立即改变，此时必须让缓存失效，
// 否则等待端口释放的逻辑会一直读到停止前的旧快照。
func InvalidatePortPIDCache() {
	portPIDMu.Lock()
	defer portPIDMu.Unlock()
	portPIDCache = nil
	portPIDFetched = time.Time{}
}

// allPortPIDsLocked 需已持有 portPIDMu。
func allPortPIDsLocked() map[int][]int {
	if portPIDCache != nil && time.Since(portPIDFetched) < portPIDCacheTTL {
		return portPIDCache
	}
	table := listAllPortPIDs()
	if table == nil {
		// 取数失败时不缓存，避免把"查不到"固化住
		return map[int][]int{}
	}
	portPIDCache = table
	portPIDFetched = time.Now()
	return portPIDCache
}
