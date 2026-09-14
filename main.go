package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"audiobook/internal/service"
)

type App struct {
	ctx context.Context
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func main() {
	// 初始化配置与全局服务
	service.InitConfig()
	_ = service.EnsureDefaultAdmin()
	service.InitAllDrivers()
	service.StartTokenRefreshCron()

	app := NewApp()

	// 构建 /api/* → 业务层 handler
	apiMux := http.NewServeMux()
	service.RegisterHandlers(apiMux)

	distFS, err := fs.Sub(assets, "cmd/web/dist")
	if err != nil {
		log.Fatalf("无法加载前端资源: %v", err)
	}

	err = wails.Run(&options.App{
		Title:     "有声书播放器",
		Width:     1200,
		Height:    800,
		MinWidth:  800,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: distFS,
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/api/") {
						apiMux.ServeHTTP(w, r)
						return
					}
					next.ServeHTTP(w, r)
				})
			},
		},
		BackgroundColour: &options.RGBA{R: 18, G: 18, B: 24, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}