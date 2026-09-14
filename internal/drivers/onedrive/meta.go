// Package onedrive 实现 OneDrive 存储驱动（Microsoft Graph API）。
package onedrive

import (
	"audiobook/internal/driver"
	"audiobook/internal/drivers"
)

// 驱动注册（工厂模式，参照 OpenList 的 drivers/<name>/meta.go 组织方式）。
func init() {
	drivers.Register("onedrive", func(cfg map[string]string) (driver.Driver, error) {
		return &OneDrive{}, nil
	})
}

// Config 声明驱动元信息与用户可配置字段（前端据此动态渲染表单）。
func (d *OneDrive) Config() driver.Config {
	return driver.Config{
		Name:         "onedrive",
		DisplayName:  "OneDrive",
		SupportsLink: true,
		Fields: []driver.Field{
			{Name: "region", Label: "区域", Type: "select", Required: true,
				Options: []string{"global", "cn", "us", "de"}},
			{Name: "redirectUri", Label: "Redirect URI（重定向地址，必填）", Type: "string", Required: true,
				Help: "默认 https://api.oplist.org/onedrive/callback"},
			{Name: "refreshToken", Label: "Refresh Token（刷新令牌，必填）", Type: "password", Required: true},
			{Name: "clientId", Label: "Client ID（可选，使用在线 API 时无需填写）", Type: "string", Required: false},
			{Name: "clientSecret", Label: "Client Secret（可选，使用在线 API 时无需填写）", Type: "password", Required: false},
			{Name: "siteId", Label: "Site ID（SharePoint）", Type: "string", Required: false},
		},
	}
}
