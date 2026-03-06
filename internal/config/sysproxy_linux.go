//go:build linux

package config

import "fmt"

// enableWindowsProxy stub for Linux
func enableWindowsProxy(port int) error {
	return fmt.Errorf("Windows 代理设置不适用于 Linux")
}

// disableWindowsProxy stub for Linux
func disableWindowsProxy() error {
	return fmt.Errorf("Windows 代理设置不适用于 Linux")
}

// enableDarwinProxy stub for Linux
func enableDarwinProxy(port int) error {
	return fmt.Errorf("macOS 代理设置不适用于 Linux")
}

// disableDarwinProxy stub for Linux
func disableDarwinProxy() error {
	return fmt.Errorf("macOS 代理设置不适用于 Linux")
}
