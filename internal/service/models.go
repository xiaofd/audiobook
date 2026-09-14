// Package service 提供有声书播放器的核心业务逻辑（纯 Go，双端共用）。
package service

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"path"
	"strings"
	"time"
)

// 音频扩展名白名单。
var audioExts = map[string]bool{
	".mp3": true, ".m4a": true, ".m4b": true, ".flac": true,
	".wav": true, ".ogg": true, ".aac": true, ".opus": true, ".wma": true,
}

// 封面扩展名白名单。
var coverExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// 流式模式。
const (
	StreamAuto     = "auto"
	StreamProxy    = "proxy"
	StreamRedirect = "redirect"
)

// 用户角色。
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// stableID 基于输入字符串生成稳定的 32 位 hex ID（MD5）。
// 用于分集 ID，保证重新扫描时同一文件的 ID 不变，从而保留播放进度。
func stableID(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func isAudioFile(name string) bool {
	return audioExts[strings.ToLower(path.Ext(name))]
}

func isCoverFile(name string) bool {
	return coverExts[strings.ToLower(path.Ext(name))]
}

// StorageInstance 一个存储书源实例。
type StorageInstance struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"` // local / onedrive / mobilecloud
	Enabled    bool              `json:"enabled"`
	StreamMode string            `json:"streamMode"` // auto / proxy / redirect
	RootPath   string            `json:"rootPath"`   // 书库根目录（相对存储根），扫描时从该目录开始
	Config     map[string]string `json:"config"`     // 驱动配置（含凭据，落盘时加密）
}

// User 用户。
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"createdAt"`
}

// UserSettings 用户个性化偏好配置（跨端同步）。
type UserSettings struct {
	UserID        string  `json:"userId"`
	PlaybackRate  float64 `json:"playbackRate"`  // 默认播放倍速（如 1.0, 1.25, 1.5, 2.0）
	AutoNext      bool    `json:"autoNext"`      // 连播开关
	Theme         string  `json:"theme"`         // 主题偏好 (dark / light / system)
	SkipForward   int     `json:"skipForward"`   // 快进秒数 (默认 15)
	SkipBackward  int     `json:"skipBackward"`  // 快退秒数 (默认 15)
}

// Book 有声书。
type Book struct {
	ID          string `json:"id"`
	StorageID   string `json:"storageId"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Cover       string `json:"cover"`       // 相对存储根的文件路径
	Description string `json:"description"`
	RootPath    string `json:"rootPath"`    // 相对存储根的书目录路径
	StorageName string `json:"storageName"` // 存储实例名称（计算字段，不持久化）
	EpisodeCount int   `json:"episodeCount,omitempty"` // 包含的有效分集数（计算字段）
}

// Episode 分集。
type Episode struct {
	ID        string `json:"id"`
	BookID    string `json:"bookId"`
	StorageID string `json:"storageId"`
	Title     string `json:"title"`
	FilePath  string `json:"filePath"` // 相对存储根的文件路径
	Size      int64  `json:"size"`
	Order     int    `json:"order"`
	Duration  int    `json:"duration"` // 秒，未知为 0
}

// Progress 播放进度。
type Progress struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	EpisodeID string    `json:"episodeId"`
	Position  float64   `json:"position"`  // 秒
	Duration  float64   `json:"duration"`  // 秒
	IsFinished bool     `json:"isFinished"` // 是否听完（position/duration >= 0.9）
	UpdatedAt time.Time `json:"updatedAt"`
}

// Percent 返回播放进度百分比（0-100）。
func (p *Progress) Percent() int {
	if p.Duration <= 0 {
		return 0
	}
	pct := int(p.Position / p.Duration * 100)
	if pct > 100 {
		return 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}
