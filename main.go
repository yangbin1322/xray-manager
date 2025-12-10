package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
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
		Width:            1100,
		Height:           700,
		BackgroundColour: application.NewRGB(255, 255, 255),
		// 不自动关闭窗口，由事件处理器决定
		Hidden: false,
	})

	// 设置窗口关闭事件处理器
	mainWindow.OnWindowClose(func(ctx context.Context) {
		// 显示对话框，让用户选择操作
		selection, err := app.Dialog.Message("关闭窗口").
			Title("Xray 管理器").
			Message("请选择操作：").
			Buttons("退出程序", "最小化到托盘").
			DefaultButton("最小化到托盘").
			Info()

		if err != nil {
			// 如果对话框出错，默认最小化到托盘
			mainWindow.Hide()
			return
		}

		if selection == "退出程序" {
			// 退出程序
			app.Quit()
		} else {
			// 最小化到托盘
			mainWindow.Hide()
		}
	})

	// 注册服务
	Service := NewMyService()
	app.RegisterService(application.NewService(Service))

	// 创建系统托盘
	systemTray := app.Systray()
	if systemTray != nil {
		// 设置托盘图标提示
		systemTray.SetTooltip("Xray 管理器")

		// 创建托盘菜单
		menu := app.Menu.NewMenu()

		// 显示/隐藏主窗口
		menu.AddItem("显示主窗口").OnClick(func(ctx context.Context, data application.ClickEventData) {
			mainWindow.Show()
			mainWindow.Focus()
		})

		menu.AddSeparator()

		// 一键启动所有节点
		menu.AddItem("启动所有节点").OnClick(func(ctx context.Context, data application.ClickEventData) {
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
		menu.AddItem("停止所有节点").OnClick(func(ctx context.Context, data application.ClickEventData) {
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
		menu.AddItem("退出").OnClick(func(ctx context.Context, data application.ClickEventData) {
			app.Quit()
		})

		// 设置托盘菜单
		systemTray.SetMenu(menu)

		// 托盘图标点击事件
		systemTray.OnClick(func(ctx context.Context) {
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
