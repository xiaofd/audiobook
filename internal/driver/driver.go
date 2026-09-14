// Package driver 定义有声书存储驱动接口（借鉴 OpenList 的 Driver 架构思路，自行实现）。
package driver

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Obj 统一文件/目录对象模型，屏蔽不同存储后端的差异。
type Obj struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"` // 驱动内部使用的路径标识（如相对挂载根的路径）
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	IsDir    bool      `json:"isDir"`
	// Duration 音频时长（秒），未知为 0。部分驱动（如 OneDrive Graph 的 audio 元数据）
	// 可在列目录时免费获得，用于入库时直接填充分集时长。
	Duration int `json:"duration,omitempty"`
}

// Link 表示可直链访问的下载地址（302 直链模式使用）。
type Link struct {
	URL    string      `json:"url"`
	Header http.Header `json:"-"`
}

// Field 描述驱动需要用户配置的一项参数。
type Field struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Type     string `json:"type"` // string / password / number / bool / select
	Required bool   `json:"required"`
	// Options 仅当 Type 为 select 时使用，下拉选项。
	Options []string `json:"options,omitempty"`
	// DependsOn 该字段在哪个父字段值下才显示，格式 "fieldName=value"。
	DependsOn string `json:"dependsOn,omitempty"`
	// Help 字段的帮助/提示文本（前端显示在输入框下方）。
	Help string `json:"help,omitempty"`
}

// Config 驱动元信息与能力声明。
type Config struct {
	Name         string  `json:"name"`
	DisplayName  string  `json:"displayName"`
	SupportsLink bool    `json:"supportsLink"` // 是否支持生成直链（302 模式）
	Fields       []Field `json:"fields"`
}

// Driver 存储驱动接口。
// 读能力（List/Link/Open）为必须；写能力按需扩展。
type Driver interface {
	// Config 返回驱动元信息与配置字段声明。
	Config() Config
	// Init 使用 cfg 初始化驱动（认证、客户端建立等）。
	Init(ctx context.Context, cfg map[string]string) error
	// Drop 清理资源（存储被禁用/删除时调用）。
	Drop(ctx context.Context) error
	// List 列出 path 目录下的条目。
	List(ctx context.Context, path string) ([]Obj, error)
	// Link 返回直链；不支持直链时返回 (nil, nil)。
	Link(ctx context.Context, file Obj) (*Link, error)
	// Open 打开文件流用于服务器中转（proxy 模式）。
	Open(ctx context.Context, file Obj) (io.ReadCloser, int64, error)
}

// Writer 可选接口：支持上传/保存文件的驱动实现该接口（用于上传封面、写回元数据等）
type Writer interface {
	// Put 在指定父目录写入文件
	Put(ctx context.Context, parentPath string, name string, r io.Reader, size int64) (Obj, error)
}

// RangeReader 可选接口：支持按字节范围拉取文件流的驱动实现该接口。
// 用于服务器中转模式下的 Range 请求（拖动进度/断点续传），
// 网盘驱动通过直链透传 Range 头，避免为 seek 而重复下载整个文件前缀。
type RangeReader interface {
	// OpenRange 打开文件流的 [start, start+length-1] 字节范围；length < 0 表示从 start 到文件末尾。
	OpenRange(ctx context.Context, file Obj, start, length int64) (io.ReadCloser, int64, error)
}

// TokenRefresher 可选接口：支持后台定期刷新令牌的驱动实现该接口。
type TokenRefresher interface {
	// RefreshToken 手动触发刷新令牌，并在发生变更时返回更新后的 config map。
	RefreshToken(ctx context.Context) (map[string]string, error)
}

// ConfigSaver 可选接口：驱动凭据轮换（如 refresh token 刷新）后回调保存。
// 网盘的 refresh token 通常是一次性的（每次刷新作废旧的产生新的），
// 若新令牌仅存内存而进程在持久化前重启，磁盘上的旧令牌已作废，
// 将导致永久认证失败。服务层通过该接口注入持久化回调，轮换后立即落盘。
type ConfigSaver interface {
	SetConfigSaver(save func(cfg map[string]string))
}
