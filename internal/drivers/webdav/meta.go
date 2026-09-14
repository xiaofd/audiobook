// Package webdav 实现 WebDAV 存储驱动（坚果云 / Nextcloud / 群晖 / AList 等均支持）。
package webdav

import (
	"audiobook/internal/driver"
	"audiobook/internal/drivers"
)

// 驱动注册（工厂模式，参照 OpenList 的 drivers/<name>/meta.go 组织方式）。
func init() {
	drivers.Register("webdav", func(cfg map[string]string) (driver.Driver, error) {
		return &WebDAV{}, nil
	})
}

// Config 声明驱动元信息与用户可配置字段（前端据此动态渲染表单）。
func (d *WebDAV) Config() driver.Config {
	return driver.Config{
		Name:         "webdav",
		DisplayName:  "WebDAV",
		SupportsLink: false, // Basic 认证无法直链，默认走中转（Range 透传）
		Fields: []driver.Field{
			{Name: "url", Label: "服务器地址", Type: "string", Required: true,
				Help: "如 https://dav.jianguoyun.com/dav/"},
			{Name: "username", Label: "用户名", Type: "string", Required: true},
			{Name: "password", Label: "密码 / 应用密码", Type: "password", Required: true,
				Help: "坚果云等请使用「安全选项 → 应用密码」生成的专用密码"},
			{Name: "mountPath", Label: "挂载子目录（可选，默认根目录）", Type: "string", Required: false,
				Help: "如 /audiobooks，仅挂载该目录作为存储根"},
		},
	}
}
