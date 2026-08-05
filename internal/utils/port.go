package utils

import (
	"fmt"
	"net"
)

// CheckPortAvailable 检查端口是否可用
func CheckPortAvailable(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	// Windows 允许通配地址与特定地址出现不同的绑定结果；分别探测回环和
	// 通配 IPv4，才能覆盖其他客户端及本应用代理核心的监听方式。
	for _, host := range []string{"127.0.0.1", "0.0.0.0"} {
		listener, err := net.Listen("tcp4", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
		if err != nil {
			return false
		}
		_ = listener.Close()
	}
	return true
}

// CheckPortBindable 检查端口能否被 mixed 入站绑定（TCP 与 UDP 都要可用）。
//
// 比 CheckPortAvailable 更严格，专用于分片启动前的预检：sing-box 的 mixed
// 入站会同时绑 TCP 和 UDP，任一被占用都会让整个进程启动失败——而分片里
// 一个端口绑不上就意味着同片几百个节点全部起不来。
//
// 仅探测 UDP 无法覆盖 Windows 的「排除端口范围」（netsh int ipv4 show
// excludedportrange，多为 Hyper-V/WSL 预留）：那些端口不显示为已监听，
// 但 bind 会直接失败。这里靠真实 bind 而非查表，因此两种情况都能识别。
func CheckPortBindable(port int) bool {
	if !CheckPortAvailable(port) {
		return false
	}
	conn, err := net.ListenPacket("udp4", net.JoinHostPort("0.0.0.0", fmt.Sprintf("%d", port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// FindAvailablePort 从起始端口开始查找可用端口
func FindAvailablePort(startPort int) int {
	for port := startPort; port < 65535; port++ {
		if CheckPortAvailable(port) {
			return port
		}
	}
	return 0 // 没有可用端口
}

// FindAvailablePorts 查找多个可用端口
func FindAvailablePorts(startPort, count int) []int {
	ports := make([]int, 0, count)
	port := startPort
	for len(ports) < count && port < 65535 {
		if CheckPortAvailable(port) {
			ports = append(ports, port)
		}
		port++
	}
	return ports
}

// DefaultRecommendPortStart 推荐端口默认起始值
const DefaultRecommendPortStart = 11000

// RecommendPort 推荐可用端口（从默认起始端口 11000 开始）
func RecommendPort() int {
	return FindAvailablePort(DefaultRecommendPortStart)
}
