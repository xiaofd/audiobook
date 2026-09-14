// Package drivers 管理所有存储驱动，提供注册表与工厂模式。
package drivers

import (
	"fmt"
	"sync"

	"audiobook/internal/driver"
)

// Factory 根据配置构造一个驱动实例。
type Factory func(cfg map[string]string) (driver.Driver, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register 注册一个驱动（各驱动包在 init 中调用）。
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = f
}

// New 按名称构造驱动实例。
func New(name string, cfg map[string]string) (driver.Driver, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("未知的存储类型: %s", name)
	}
	return f(cfg)
}

// Names 返回所有已注册的驱动名称。
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}
