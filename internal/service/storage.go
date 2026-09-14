package service

import (
	"context"
	"log"
	"sync"
	"time"

	"audiobook/internal/driver"
	"audiobook/internal/drivers"

	// 导入各驱动包以触发 init() 注册
	_ "audiobook/internal/drivers/local"
	_ "audiobook/internal/drivers/mobilecloud"
	_ "audiobook/internal/drivers/onedrive"
	_ "audiobook/internal/drivers/webdav"
)

var (
	driverMutex    sync.RWMutex
	driversMap     = map[string]driver.Driver{} // storageID → driver
	driverErrorMap = map[string]string{}        // storageID → last error message
	cronCancelOnce sync.Once
	cronCancel     context.CancelFunc
)

// GetDriverStatus 返回指定存储实例的驱动运行状态：ready (正常), error (错误), disabled (禁用)
func GetDriverStatus(storageID string) (status string, lastError string) {
	driverMutex.RLock()
	defer driverMutex.RUnlock()
	if _, ok := driversMap[storageID]; ok {
		return "ready", ""
	}
	if errStr, ok := driverErrorMap[storageID]; ok && errStr != "" {
		return "error", errStr
	}
	return "not_ready", "驱动尚未初始化或初始化失败"
}

// StartTokenRefreshCron 启动后台定时刷新网盘 Token 任务（每 30 分钟运行一次）。
// 注意：OneDrive access token 约 1 小时过期，间隔必须小于有效期，
// 配合驱动内的 401 自动刷新重试形成双保险。
func StartTokenRefreshCron() {
	cronCancelOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cronCancel = cancel
		go func() {
			log.Println("[Cron] 启动网盘令牌后台自动刷新任务 (每 30 分钟)")
			ticker := time.NewTicker(30 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					RefreshAllTokens(ctx)
				case <-ctx.Done():
					return
				}
			}
		}()
	})
}

// StopTokenRefreshCron 停止后台定时任务。
func StopTokenRefreshCron() {
	if cronCancel != nil {
		cronCancel()
	}
}

// RefreshAllTokens 轮询所有已启用的存储实例，若实现了 TokenRefresher 则尝试刷新并持久化。
func RefreshAllTokens(ctx context.Context) {
	for _, si := range ListEnabledStorageInstances() {
		drv := GetDriver(si.ID)
		if drv == nil {
			continue
		}
		refresher, ok := drv.(driver.TokenRefresher)
		if !ok {
			continue
		}
		newCfg, err := refresher.RefreshToken(ctx)
		if err != nil {
			log.Printf("[Cron] 存储 %s (%s) 令牌刷新失败: %v", si.Name, si.ID, err)
			continue
		}
		if newCfg != nil {
			si.Config = newCfg
			if err := UpsertStorageInstance(si); err != nil {
				log.Printf("[Cron] 存储 %s 刷新配置持久化失败: %v", si.Name, err)
			} else {
				log.Printf("[Cron] 存储 %s (%s) 令牌定时刷新并持久化成功", si.Name, si.ID)
			}
		}
	}
}

// InitDriver 初始化或重新初始化一个存储实例的驱动。
func InitDriver(si StorageInstance) error {
	driverMutex.Lock()
	defer driverMutex.Unlock()

	// 若已有旧驱动，先释放
	if d, ok := driversMap[si.ID]; ok {
		_ = d.Drop(context.Background())
	}

	d, err := drivers.New(si.Type, si.Config)
	if err != nil {
		driverErrorMap[si.ID] = err.Error()
		return err
	}
	if err := d.Init(context.Background(), si.Config); err != nil {
		driverErrorMap[si.ID] = err.Error()
		return err
	}
	driversMap[si.ID] = d
	delete(driverErrorMap, si.ID)

	// 注入配置持久化回调：驱动凭据轮换（refresh token 刷新）后立即落盘。
	// 必须异步执行：回调可能发生在 d.Init 内部（此时 InitDriver 仍持有 driverMutex 写锁），
	// 同步获取 driverMutex 会死锁；异步后回调会阻塞到初始化完成后才执行。
	// 同时校验 driversMap 中仍是发起回调的驱动实例，避免被丢弃的旧驱动的
	// 在途请求用旧凭据覆盖用户编辑保存后的新配置。
	if cs, ok := d.(driver.ConfigSaver); ok {
		storageID, drvInstance := si.ID, d
		cs.SetConfigSaver(func(cfg map[string]string) {
			go func() {
				driverMutex.RLock()
				cur := driversMap[storageID]
				driverMutex.RUnlock()
				if cur != drvInstance {
					return
				}
				if cur2, ok := GetStorageInstance(storageID); ok {
					cur2.Config = cfg
					if err := UpsertStorageInstance(cur2); err != nil {
						log.Printf("[Storage] 凭据轮换持久化失败 %s: %v", storageID, err)
					}
				}
			}()
		})
	}

	log.Printf("[Storage] 驱动已就绪: %s (%s)", si.Name, si.ID)
	return nil
}

// DropDriver 释放一个存储实例的驱动。
func DropDriver(storageID string) {
	driverMutex.Lock()
	defer driverMutex.Unlock()
	if d, ok := driversMap[storageID]; ok {
		_ = d.Drop(context.Background())
		delete(driversMap, storageID)
	}
}

// GetDriver 获取存储实例对应的驱动接口。
func GetDriver(storageID string) driver.Driver {
	driverMutex.RLock()
	defer driverMutex.RUnlock()
	return driversMap[storageID]
}

// InitAllDrivers 初始化所有已启用的存储实例驱动（启动时调用）。
func InitAllDrivers() {
	for _, si := range ListEnabledStorageInstances() {
		if err := InitDriver(si); err != nil {
			log.Printf("[Storage] 初始化失败 %s: %v", si.Name, err)
		}
	}
}

// GetStreamMode 获取指定存储实例的流式模式（实例级/全局默认），过滤无效值。
func GetStreamMode(storageID string) string {
	if si, ok := GetStorageInstance(storageID); ok && si.StreamMode != "" {
		switch si.StreamMode {
		case StreamProxy, StreamRedirect, StreamAuto:
			return si.StreamMode
		}
	}
	mode := GetConfig().DefaultStreamMode
	switch mode {
	case StreamProxy, StreamRedirect, StreamAuto:
		return mode
	}
	return StreamAuto
}