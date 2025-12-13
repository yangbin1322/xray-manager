package main

import (
	"embed"
	"fmt"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	// 创建应用实例
	app := application.New(application.Options{
		Name: "Xray Manager",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	// 创建主窗口
	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Xray 管理器",
		Width:            1600,
		Height:           800,
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
		// 设置托盘图标提示
		systemTray.SetTooltip("Xray 管理器")
		systemTray.SetLabel("Xray 管理器")
		systemTray.Show()

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
			// 调用服务方法启动所有节点
			go func() {
				rules := Service.GetRules()
				for _, rule := range rules {
					if !rule.Enabled {
						_ = Service.StartRule(rule.ID)
					}
				}
			}()
		})

		// 一键停止所有节点
		menu.Add("停止所有节点").OnClick(func(c *application.Context) {
			// 调用服务方法停止所有节点
			go func() {
				rules := Service.GetRules()
				for _, rule := range rules {
					if rule.Enabled {
						_ = Service.StopRule(rule.ID)
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

	err := app.Run()

	if err != nil {
		log.Fatal(err)
	}
}
