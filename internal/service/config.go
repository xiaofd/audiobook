package service

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AppConfig 应用全局配置。
type AppConfig struct {
	DataDir            string            `json:"dataDir"`
	Host               string            `json:"host"`
	Port               int               `json:"port"`
	DefaultStreamMode  string            `json:"defaultStreamMode"`
	JWTSecret          string            `json:"jwtSecret"`
	AllowRegistration  bool              `json:"allowRegistration"` // 是否开放自助注册（默认关闭，公网部署安全考虑）
	StorageInstances   []StorageInstance `json:"storageInstances"`
}

var (
	appConf   AppConfig
	confPath  string
	confMutex sync.RWMutex
)

// GetConfig 返回当前配置的并发安全副本。
func GetConfig() AppConfig {
	confMutex.RLock()
	defer confMutex.RUnlock()
	return appConf
}

// UpdateConfig 原子更新配置并落盘。
func UpdateConfig(up AppConfig) error {
	confMutex.Lock()
	appConf = up
	confMutex.Unlock()
	return saveConfig()
}

// InitConfig 初始化配置（含加密、数据目录、config.json、数据库）。
func InitConfig() {
	// 确定数据目录：优先使用环境变量 AUDIOBOOK_DATA_DIR 覆盖（便于部署/调试）
	dataDir := strings.TrimSpace(os.Getenv("AUDIOBOOK_DATA_DIR"))
	if dataDir == "" {
		exe, _ := os.Executable()
		exeDir := filepath.Dir(exe)
		cfgFile := filepath.Join(exeDir, "config.json")
		if st, err := os.Stat(cfgFile); err != nil || st.IsDir() {
			// 便携模式: 数据在 exe 同目录的 data/ 下
			cfgDir := filepath.Join(exeDir, "data")
			os.MkdirAll(cfgDir, 0755)
			cfgFile = filepath.Join(cfgDir, "config.json")
		}
		dataDir = filepath.Dir(cfgFile)
	}
	os.MkdirAll(dataDir, 0755)
	cfgFile := filepath.Join(dataDir, "config.json")

	confPath = cfgFile

	// 初始化加密
	initCrypto(dataDir)

	// 默认配置
	appConf = AppConfig{
		DataDir:           dataDir,
		Host:              "0.0.0.0",
		Port:              8080,
		DefaultStreamMode: StreamAuto,
		JWTSecret:         newID() + newID(),
		StorageInstances:  []StorageInstance{},
	}

	// 加载已有配置（保留原始字节，密钥不匹配/文件损坏时用于备份）
	rawConf, confErr := os.ReadFile(confPath)
	confBroken := false
	if confErr == nil {
		if err := json.Unmarshal(rawConf, &appConf); err != nil {
			confBroken = true // 文件存在但解析失败（写坏/BOM/手改出错），落盘前须备份防静默清空
		}
	}
	if appConf.DataDir == "" {
		appConf.DataDir = dataDir
	}
	if appConf.Host == "" {
		appConf.Host = "0.0.0.0"
	}
	if appConf.DefaultStreamMode == "" {
		appConf.DefaultStreamMode = StreamAuto
	}
	if appConf.JWTSecret == "" {
		appConf.JWTSecret = newID() + newID()
	}
	if appConf.Port == 0 {
		appConf.Port = 8080
	}

	// 先在内存中解密存储实例凭据，再统一落盘（saveConfig 会重新加密敏感字段）。
	// 注意顺序不能颠倒：若在解密前先 saveConfig，会把磁盘上已加密的密文再加密一层。
	decryptFailed := false
	for i := range appConf.StorageInstances {
		si := &appConf.StorageInstances[i]
		cfg, failed := decryptConfig(si.Config)
		si.Config = cfg
		decryptFailed = decryptFailed || failed
	}

	if decryptFailed {
		// 密钥与磁盘密文不匹配（.machine.key 丢失后生成了新密钥，或 AUDIOBOOK_SECRET_KEY 变更）。
		// 此时绝不能 saveConfig：解不开的旧密文会被当作明文用新密钥再加密一层，旧密文即永久损坏。
		// 正确做法：跳过落盘保持磁盘原样，并备份原始配置，待用户找回旧密钥后即可无损恢复。
		backupPath := backupConfigFile(rawConf)
		log.Println("====================================================================")
		log.Println("⚠️  检测到存储凭据无法解密：当前密钥与 config.json 中的密文不匹配")
		log.Println("    常见原因：数据目录迁移时未携带 .machine.key（隐藏文件易漏拷），")
		log.Println("             或 AUDIOBOOK_SECRET_KEY 环境变量发生了变更。")
		log.Println("    已跳过配置落盘（磁盘密文保持原样），并备份原始配置到:")
		log.Println("    " + backupPath)
		log.Println("    恢复方法：将旧的 .machine.key 放回数据目录（或还原")
		log.Println("             AUDIOBOOK_SECRET_KEY）后重启，即可自动解密。")
		log.Println("    若密钥无法找回：请在管理页重新录入各存储的访问凭据。")
		log.Println("====================================================================")
	} else {
		if confBroken {
			// 配置文件损坏：无法解析，只能重建默认配置，但先备份原文件以便人工抢救。
			backupPath := backupConfigFile(rawConf)
			log.Printf("⚠️  config.json 解析失败（可能写入中断或被手工改坏），已备份原文件到 %s 并重建默认配置", backupPath)
		}
		saveConfig()
	}

	// 初始化数据库
	InitStore(dataDir)

	log.Printf("[Config] 数据目录: %s, 已初始化", dataDir)
}

// backupConfigFile 将磁盘上的原始配置字节备份为 config.json.bak。
// 已存在则不覆盖——保留最早的完好副本，避免后续异常启动用坏文件顶掉好备份。
func backupConfigFile(raw []byte) string {
	backupPath := confPath + ".bak"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		_ = os.WriteFile(backupPath, raw, 0600)
	}
	return backupPath
}

// encryptConfig 加密配置中的敏感字段。
func encryptConfig(cfg map[string]string) map[string]string {
	if cfg == nil {
		return nil
	}
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		if isSecretKey(k) && v != "" {
			enc, err := encryptString(v)
			if err != nil {
				out[k] = v
			} else {
				out[k] = enc
			}
		} else {
			out[k] = v
		}
	}
	return out
}

// decryptConfig 解密配置中的敏感字段。
// 返回 failed=true 表示存在 GCM 解密失败的字段——几乎必然是密钥不匹配
// （.machine.key 丢失/AUDIOBOOK_SECRET_KEY 变更），调用方须避免将其再加密落盘。
// 注意：非 base64 / 过短的值视为用户手填明文直通，不算失败。
func decryptConfig(cfg map[string]string) (map[string]string, bool) {
	if cfg == nil {
		return nil, false
	}
	out := make(map[string]string, len(cfg))
	failed := false
	for k, v := range cfg {
		if isSecretKey(k) && v != "" {
			dec, err := decryptString(v)
			if err != nil {
				failed = true
				log.Printf("[Config] 敏感凭据字段 %q 解密失败，保留原始密文", k)
				out[k] = v
			} else {
				out[k] = dec
			}
		} else {
			out[k] = v
		}
	}
	return out, failed
}

func saveConfig() error {
	confMutex.RLock()
	defer confMutex.RUnlock()

	disk := appConf
	disk.StorageInstances = make([]StorageInstance, len(appConf.StorageInstances))
	for i, si := range appConf.StorageInstances {
		disk.StorageInstances[i] = si
		disk.StorageInstances[i].Config = encryptConfig(si.Config)
	}

	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	// 文件含明文 jwtSecret 与加密凭据，收紧为仅属主可读写（Windows 下等效无操作）
	if err := os.WriteFile(confPath, data, 0600); err != nil {
		return err
	}
	_ = os.Chmod(confPath, 0600) // 已存在的旧文件 WriteFile 不会改权限，显式收紧
	return nil
}

// SetAllowRegistration 更新自助注册开关并落盘。
func SetAllowRegistration(v bool) error {
	confMutex.Lock()
	appConf.AllowRegistration = v
	confMutex.Unlock()
	return saveConfig()
}

// --- Storage Instance CRUD ---

func UpsertStorageInstance(si StorageInstance) error {
	confMutex.Lock()
	for i := range appConf.StorageInstances {
		if appConf.StorageInstances[i].ID == si.ID {
			appConf.StorageInstances[i] = si
			confMutex.Unlock()
			return saveConfig()
		}
	}
	if si.ID == "" {
		si.ID = newID()
	}
	appConf.StorageInstances = append(appConf.StorageInstances, si)
	confMutex.Unlock()
	return saveConfig()
}

func DeleteStorageInstance(id string) error {
	confMutex.Lock()
	for i, si := range appConf.StorageInstances {
		if si.ID == id {
			appConf.StorageInstances = append(appConf.StorageInstances[:i], appConf.StorageInstances[i+1:]...)
			confMutex.Unlock()
			return saveConfig()
		}
	}
	confMutex.Unlock()
	return nil
}

func GetStorageInstance(id string) (StorageInstance, bool) {
	confMutex.RLock()
	defer confMutex.RUnlock()
	for _, si := range appConf.StorageInstances {
		if si.ID == id {
			return si, true
		}
	}
	return StorageInstance{}, false
}

func ListStorageInstances() []StorageInstance {
	confMutex.RLock()
	defer confMutex.RUnlock()
	out := make([]StorageInstance, len(appConf.StorageInstances))
	copy(out, appConf.StorageInstances)
	return out
}

func ListEnabledStorageInstances() []StorageInstance {
	all := ListStorageInstances()
	out := make([]StorageInstance, 0, len(all))
	for _, si := range all {
		if si.Enabled {
			out = append(out, si)
		}
	}
	return out
}