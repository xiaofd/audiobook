package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

var (
	machineKey []byte
	keyPath    string
)

// initCrypto 从数据目录加载/生成机器密钥。
// 如果存在环境变量 AUDIOBOOK_SECRET_KEY，则优先以其派生密钥（便于多节点/跨机器/Docker无感迁移）；
// 否则读取或持久化 data/.machine.key（文件随 data 目录拷贝亦可无缝解密）。
func initCrypto(dataDir string) {
	if envSecret := strings.TrimSpace(os.Getenv("AUDIOBOOK_SECRET_KEY")); envSecret != "" {
		sum := sha256.Sum256([]byte(envSecret))
		machineKey = sum[:]
		return
	}

	keyPath = filepath.Join(dataDir, ".machine.key")
	if b, err := os.ReadFile(keyPath); err == nil {
		s := strings.TrimSpace(string(b))
		if raw, err := hex.DecodeString(s); err == nil && len(raw) == 32 {
			machineKey = raw
			return
		}
	}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	machineKey = raw
	_ = os.WriteFile(keyPath, []byte(hex.EncodeToString(raw)), 0600)
}

// deriveKey 使用 AES-256-GCM（密钥直接采用机器密钥的 SHA-256）。
func deriveKey() []byte {
	sum := sha256.Sum256(machineKey)
	return sum[:]
}

func encryptString(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(deriveKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptString(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	// 如果不是 base64 格式，直接认为是明文
	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return enc, nil
	}
	block, err := aes.NewCipher(deriveKey())
	if err != nil {
		return enc, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return enc, err
	}
	if len(data) < gcm.NonceSize() {
		// 密文长度不足，可能是用户手工填写的明文字符串
		return enc, nil
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// 解密失败（例如跨平台拷贝未拷贝隐藏文件 .machine.key），回退返回原始字符串以防无法使用
		return enc, err
	}
	return string(plain), nil
}

// isSecretKey 判断配置键是否属于需加密的敏感字段。
func isSecretKey(k string) bool {
	kl := strings.ToLower(k)
	for _, s := range []string{"password", "secret", "token", "cookie", "key", "authorization", "auth"} {
		if strings.Contains(kl, s) {
			return true
		}
	}
	return false
}
