package main

import (
	"embed"

	"github.com/wailsapp/wails/v3/pkg/application"

	"log"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	// 创建应用实例
	app := application.New(application.Options{
		Name: "My App",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Xray 管理器",
		Width:            1100,
		Height:           700,
		BackgroundColour: application.NewRGB(255, 255, 255),
	})
	Service := NewMyService()
	app.RegisterService(application.NewService(Service))

	err := app.Run()

	if err != nil {
		log.Fatal(err)
	}

}
