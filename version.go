package main

// AppVersion 当前应用版本（可通过 -ldflags "-X main.AppVersion=x.y.z" 注入）
var AppVersion = "2.4.1"

// GitHub 仓库（用于 Releases 更新检查）
const (
	githubOwner = "yangbin1322"
	githubRepo  = "xray-manager"
)
