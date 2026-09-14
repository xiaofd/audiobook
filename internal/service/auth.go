package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword 使用 bcrypt 生成密码哈希。
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验密码。
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// tokenClaims 简易签名令牌的载荷。
type tokenClaims struct {
	Sub  string `json:"sub"`  // user id
	Role string `json:"role"` // admin / user
	Exp  int64  `json:"exp"`  // unix 秒
}

// IssueToken 签发签名令牌（HS256 风格，标准库实现）。
func IssueToken(userID, role string, ttl time.Duration) (string, error) {
	payload := tokenClaims{Sub: userID, Role: role, Exp: time.Now().Add(ttl).Unix()}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(raw)
	sig := hmacSHA256([]byte(GetConfig().JWTSecret), []byte(payloadB64))
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return payloadB64 + "." + sigB64, nil
}

// VerifyToken 校验令牌并返回载荷。
func VerifyToken(token string) (*tokenClaims, error) {
	parts := splitToken(token)
	if len(parts) != 2 {
		return nil, errors.New("令牌格式无效")
	}
	expected := hmacSHA256([]byte(GetConfig().JWTSecret), []byte(parts[0]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(expected, sig) {
		return nil, errors.New("令牌签名无效")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("令牌载荷无效")
	}
	var claims tokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, err
	}
	if claims.Exp < time.Now().Unix() {
		return nil, errors.New("令牌已过期")
	}
	return &claims, nil
}

func splitToken(t string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(t); i++ {
		if i == len(t) || t[i] == '.' {
			parts = append(parts, t[start:i])
			start = i + 1
		}
	}
	return parts
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// Authenticate 校验用户名密码，成功返回用户。
func Authenticate(username, password string) (*User, error) {
	u, err := GetUserByUsername(username)
	if err != nil {
		return nil, errors.New("用户名或密码错误")
	}
	if !CheckPassword(u.PasswordHash, password) {
		return nil, errors.New("用户名或密码错误")
	}
	return u, nil
}

// ChangePassword 校验原密码后将用户密码更新为新密码。
func ChangePassword(userID, oldPw, newPw string) error {
	u, err := GetUserByID(userID)
	if err != nil {
		return errors.New("用户不存在")
	}
	if !CheckPassword(u.PasswordHash, oldPw) {
		return errors.New("原密码错误")
	}
	hash, err := HashPassword(newPw)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	return UpdateUser(u)
}

// AdminResetPassword 管理员直接重置指定用户的密码（无需原密码）。
func AdminResetPassword(userID, newPw string) error {
	u, err := GetUserByID(userID)
	if err != nil {
		return errors.New("用户不存在")
	}
	hash, err := HashPassword(newPw)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	return UpdateUser(u)
}

// EnsureDefaultAdmin 确保存在默认管理员账户（首次启动引导）。
func EnsureDefaultAdmin() error {
	users, err := ListUsers()
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Role == RoleAdmin {
			return nil
		}
	}
	hash, err := HashPassword("admin123")
	if err != nil {
		return err
	}
	return CreateUser(&User{
		Username:     "admin",
		PasswordHash: hash,
		Role:         RoleAdmin,
	})
}