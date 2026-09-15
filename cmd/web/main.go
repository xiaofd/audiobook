package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"audiobook/internal/service"
)

// Version 版本号
var (
	Version   = "1.4.1"
	BuildTime = "2026-09-15"
)

//go:embed all:dist
var webAssets embed.FS

func printUsage() {
	fmt.Printf(`有声书播放器 Web / 无头服务端 (Audiobook Web Server)
版本: %s (构建时间: %s)

用法:
  audiobook-server [选项]

选项列表:
  -h, --help           显示此帮助信息并退出
  -v, --version        显示版本信息并退出
  -host <ip>           指定监听的绑定 IP 地址 (默认 "0.0.0.0", 支持通过环境变量 AUDIOBOOK_HOST 指定)
  -port <port>         指定监听的端口 (默认 8080, 支持通过环境变量 AUDIOBOOK_PORT 或 PORT 指定)
  -data-dir <path>     指定数据持久化目录 (含 sqlite 数据库与凭据配置, 支持环境变量 AUDIOBOOK_DATA_DIR)

支持的环境变量:
  AUDIOBOOK_HOST       监听 IP 地址 (例如 127.0.0.1 或 0.0.0.0)
  AUDIOBOOK_PORT       监听端口号 (例如 8080)
  PORT                 通用端口号环境变量 (云原生环境兼容)
  AUDIOBOOK_DATA_DIR   数据与配置目录路径

示例:
  # 使用默认配置启动 (0.0.0.0:8080)
  ./audiobook-server

  # 绑定本地回环端口并在自定义目录持久化数据
  ./audiobook-server -host 127.0.0.1 -port 9000 -data-dir /opt/audiobook/data

  # 在 Linux systemd 或 Docker 容器中以后台无头服务运行
  AUDIOBOOK_PORT=8080 ./audiobook-server
`, Version, BuildTime)
}

func main() {
	var (
		flagHelp    bool
		flagVersion bool
		flagHost    string
		flagPort    int
		flagDataDir string
	)

	flag.BoolVar(&flagHelp, "h", false, "显示帮助信息")
	flag.BoolVar(&flagHelp, "help", false, "显示帮助信息")
	flag.BoolVar(&flagVersion, "v", false, "显示版本信息")
	flag.BoolVar(&flagVersion, "version", false, "显示版本信息")
	flag.StringVar(&flagHost, "host", "", "指定绑定的 IP 地址 (默认 0.0.0.0)")
	flag.IntVar(&flagPort, "port", 0, "指定监听端口 (默认 8080)")
	flag.StringVar(&flagDataDir, "data-dir", "", "指定数据目录路径")

	flag.Usage = printUsage
	flag.Parse()

	if flagHelp {
		printUsage()
		os.Exit(0)
	}

	if flagVersion {
		fmt.Printf("Audiobook Server v%s (%s)\n", Version, BuildTime)
		os.Exit(0)
	}

	// 环境变量优先级融合
	if flagDataDir != "" {
		_ = os.Setenv("AUDIOBOOK_DATA_DIR", flagDataDir)
	}

	service.InitConfig()

	cfg := service.GetConfig()
	host := cfg.Host
	port := cfg.Port

	// 检查环境变量
	if envHost := os.Getenv("AUDIOBOOK_HOST"); envHost != "" {
		host = strings.TrimSpace(envHost)
	}
	if envPort := os.Getenv("AUDIOBOOK_PORT"); envPort != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(envPort)); err == nil && p > 0 {
			port = p
		}
	} else if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(strings.TrimSpace(envPort)); err == nil && p > 0 {
			port = p
		}
	}

	// 命令行参数具有最高优先级
	if flagHost != "" {
		host = strings.TrimSpace(flagHost)
	}
	if flagPort > 0 {
		port = flagPort
	}

	_ = service.EnsureDefaultAdmin()
	service.InitAllDrivers()
	service.StartTokenRefreshCron()

	mux := http.NewServeMux()
	service.RegisterHandlers(mux)

	// 静态资源 + SPA 回退
	distFS, err := fs.Sub(webAssets, "dist")
	if err != nil {
		log.Fatalf("无法加载前端资源: %v", err)
	}
	mux.Handle("/", spaHandler(distFS))

	// 请求日志中间件（输出 API 与播放访问调试信息）
	loggingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		mux.ServeHTTP(rw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("[HTTP] %s %s %d (%v)", r.Method, r.URL.Path, rw.statusCode, time.Since(start))
		}
	})

	bindAddr := fmt.Sprintf("%s:%d", host, port)
	log.Printf("=====================================================")
	log.Printf(" 有声书服务端 (Audiobook Server v%s) 已就绪", Version)
	log.Printf(" 访问地址: http://%s (如为0.0.0.0可从本地局域网IP访问)", bindAddr)
	log.Printf(" 数据目录: %s", service.GetConfig().DataDir)
	log.Printf("=====================================================")

	// 注意：不设 ReadTimeout/WriteTimeout——音频中转是长响应流，会被超时切断。
	// ReadHeaderTimeout 防 Slowloris 慢速请求头攻击；IdleTimeout 回收空闲连接。
	srv := &http.Server{
		Addr:              bindAddr,
		Handler:           loggingHandler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Web 服务启动失败: %v", err)
	}
}

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *statusResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// spaHandler 提供静态资源并回退到 index.html。
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if f, err := fsys.Open(path); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA 回退：读取 index.html 内容
		data, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
}