// Package mobilecloud 实现中国移动云盘（139 云盘）存储驱动。
//
// 协议参照 OpenList 的 139Yun 驱动文档（https://doc.oplist.org/guide/drivers/139），
// 采用 "新的个人盘"（personal_new）类型：路由查询得到 PersonalCloudHost，
// 使用 /file/list 与 /file/getDownloadUrl 接口。
//
// 支持三种鉴权方式（通过 authType 下拉选择）：
//  1. authorization：最快，填 yun.139.com 的请求头 Authorization 中 Basic 后面的内容
//  2. password：密码登录回退（兼容性差），MailCookies + Username + Password，
//     驱动自动生成并持久化 Authorization，令牌过期自动刷新，刷新失败回退账号密码登录
//  3. cookie：邮箱 Cookie 快速登录
package mobilecloud

import (
	"audiobook/internal/driver"
	"audiobook/internal/drivers"
)

// 鉴权方式常量。
const (
	AuthTypeAuthorization = "authorization"
	AuthTypePassword      = "password"
	AuthTypeCookie        = "cookie"
)

// 驱动注册（工厂模式，参照 OpenList 的 drivers/<name>/meta.go 组织方式）。
func init() {
	drivers.Register("mobilecloud", func(cfg map[string]string) (driver.Driver, error) {
		return &MobileCloud{}, nil
	})
}

// Config 声明驱动元信息与用户可配置字段（前端据此动态渲染表单）。
func (d *MobileCloud) Config() driver.Config {
	return driver.Config{
		Name:         "mobilecloud",
		DisplayName:  "中国移动云盘",
		SupportsLink: true,
		Fields: []driver.Field{
			{Name: "authType", Label: "鉴权方式", Type: "select", Required: true,
				Options: []string{"authorization", "password", "cookie"}},
			{Name: "authorization", Label: "Authorization（yun.139.com 的 Basic 令牌）", Type: "password", Required: false,
				DependsOn: "authType=authorization"},
			{Name: "mailCookies", Label: "邮箱 Cookie（key1=value1; key2=value2）", Type: "password", Required: false,
				DependsOn: "authType=password|cookie"},
			{Name: "username", Label: "账号（139 邮箱/手机号）", Type: "string", Required: false,
				DependsOn: "authType=password"},
			{Name: "password", Label: "密码（兼容性差，推荐优先使用 Authorization 方式）", Type: "password", Required: false,
				DependsOn: "authType=password"},
			{Name: "rootFolderId", Label: "根文件夹 ID（默认 /）", Type: "string", Required: false},
			{Name: "cloudId", Label: "Cloud ID（家庭云/群组云）", Type: "string", Required: false},
		},
	}
}
