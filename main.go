package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	// 从嵌入的 frontend/dist 中提取子文件系统
	distFS, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("无法加载前端资源: %v", err)
	}

	// 创建应用实例
	app := application.New(application.Options{
		Name: "Xray Manager",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(distFS),
		},
	})

	// 创建主窗口
	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Xray 管理器",
		Width:  1600,
		Height: 800,
		// 低于这个尺寸表格会被压得没法用；界面本身是自适应的，
		// 更小的窗口由内部区域自己滚动而不是撑破布局
		MinWidth:         1000,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(255, 255, 255),
		Mac:              application.MacWindow{},
	})

	// 注册服务
	Service := NewMyService()
	app.RegisterService(application.NewService(Service))

	// 设置窗口关闭事件处理器

	mainWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		fmt.Println(e)
		// 显示对话框，让用户选择操作
		exitButton := &application.Button{
			Label: "Yes",
			Callback: func() {
				app.Quit()
			},
		}

		hideButton := &application.Button{
			Label: "No",
			Callback: func() {
				e.Cancel() //取消窗口关闭事件
				mainWindow.Hide()
			},
		}

		app.Dialog.Question().
			SetTitle("Xray 管理器").
			SetMessage("请选择操作：\nYes = 退出程序\nNo = 最小化到托盘").
			AddButtons([]*application.Button{exitButton, hideButton}).
			SetDefaultButton(hideButton). // 默认最小化
			SetCancelButton(hideButton).  // ESC 也最小化
			Show()

	})
	// 创建系统托盘
	systemTray := app.SystemTray.New()
	if systemTray != nil {
		// 设置托盘图标（macOS 使用模板图标，自动适配亮色/暗色模式）
		systemTray.SetTemplateIcon(appIcon)
		systemTray.SetTooltip("Xray 管理器")
		// 创建托盘菜单
		menu := app.Menu.New()

		// 显示/隐藏主窗口
		menu.Add("显示主窗口").OnClick(func(c *application.Context) {
			mainWindow.Show()
			mainWindow.Focus()
		})

		menu.AddSeparator()

		// 一键启动所有节点
		menu.Add("启动所有节点").OnClick(func(c *application.Context) {
			// 调用服务方法启动所有节点（包括规则、负载均衡、链式代理）
			go func() {
				rules := Service.GetRules()
				for _, rule := range rules {
					if !rule.Enabled {
						_ = Service.StartRule(rule.ID)
					}
				}
				lbs := Service.GetLoadBalancers()
				for _, lb := range lbs {
					if !lb.Enabled {
						_ = Service.StartLoadBalancer(lb.ID)
					}
				}
				chains := Service.GetChainProxies()
				for _, chain := range chains {
					if !chain.Enabled {
						_ = Service.StartChainProxy(chain.ID)
					}
				}
			}()
		})

		// 一键停止所有节点
		menu.Add("停止所有节点").OnClick(func(c *application.Context) {
			// 调用服务方法停止所有节点（包括规则、负载均衡、链式代理）
			go func() {
				rules := Service.GetRules()
				for _, rule := range rules {
					if rule.Enabled {
						_ = Service.StopRule(rule.ID)
					}
				}
				lbs := Service.GetLoadBalancers()
				for _, lb := range lbs {
					if lb.Enabled {
						_ = Service.StopLoadBalancer(lb.ID)
					}
				}
				chains := Service.GetChainProxies()
				for _, chain := range chains {
					if chain.Enabled {
						_ = Service.StopChainProxy(chain.ID)
					}
				}
			}()
		})

		menu.AddSeparator()

		// 退出程序
		menu.Add("退出").OnClick(func(c *application.Context) {
			app.Quit()
		})

		// 设置托盘菜单
		systemTray.SetMenu(menu)

		// 托盘图标点击事件
		systemTray.OnClick(func() {
			if mainWindow.IsVisible() {
				mainWindow.Hide()
			} else {
				mainWindow.Show()
				mainWindow.Focus()
			}
		})
	}

	if err = app.Run(); err != nil {
		log.Fatal(err)
	}
}
