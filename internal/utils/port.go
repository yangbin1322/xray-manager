package utils

import (
	"fmt"
	"net"
)

// CheckPortAvailable 检查端口是否可用
func CheckPortAvailable(port int) bool {
	// 尝试监听该端口
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	listener.Close()
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

// RecommendPort 推荐可用端口（从默认起始端口开始）
func RecommendPort() int {
	defaultPorts := []int{10808, 10809, 10810, 10811, 10812, 1080, 1081, 1082}
	for _, port := range defaultPorts {
		if CheckPortAvailable(port) {
			return port
		}
	}
	// 如果默认端口都被占用，从 10800 开始查找
	return FindAvailablePort(10800)
}
