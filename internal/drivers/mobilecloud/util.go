package mobilecloud

// 签名、请求封装与登录辅助（参照 OpenList 的 drivers/<name>/util.go 组织方式）。

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// apiBase personal_new 使用路由查询得到的个人云主机。
func (d *MobileCloud) apiBase() string {
	if d.personalCloudHost != "" {
		return strings.TrimRight(d.personalCloudHost, "/")
	}
	return "https://yun.139.com"
}

// rootFolderID 返回根文件夹 ID（personal_new 根为 "/"）。持锁读取避免与令牌轮换的 COW 替换并发。
func (d *MobileCloud) rootFolderID() string {
	d.authMu.Lock()
	id := strings.TrimSpace(d.cfg["rootFolderId"])
	d.authMu.Unlock()
	if id != "" && id != "/" {
		return id
	}
	return "/"
}

// decodeAccount 从 Authorization（Base64）解码出账号。
func decodeAccount(auth string) string {
	decode, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		return ""
	}
	splits := strings.Split(string(decode), ":")
	if len(splits) >= 2 {
		return splits[1]
	}
	return ""
}

// queryRoute 查询路由策略，获取个人云主机地址。
func (d *MobileCloud) queryRoute() (string, error) {
	randStr := randomString(16)
	ts := time.Now().Format("2006-01-02 15:04:05")
	data := map[string]interface{}{
		"userInfo": map[string]interface{}{
			"userType":    1,
			"accountType": 1,
			"accountName": d.getAccount(),
		},
		"modAddrType": 1,
	}
	bodyBytes, _ := json.Marshal(data)
	sign := calSign(string(bodyBytes), ts, randStr)

	u := "https://user-njs.yun.139.com/user/route/qryRoutePolicy"
	req, _ := http.NewRequest("POST", u, strings.NewReader(string(bodyBytes)))
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("CMS-DEVICE", "default")
	req.Header.Set("Authorization", "Basic "+d.getAuthorization())
	req.Header.Set("mcloud-channel", "1000101")
	req.Header.Set("mcloud-client", "10701")
	req.Header.Set("mcloud-sign", fmt.Sprintf("%s,%s,%s", ts, randStr, sign))
	req.Header.Set("mcloud-version", "7.14.0")
	req.Header.Set("Origin", "https://yun.139.com")
	req.Header.Set("Referer", "https://yun.139.com/w/")
	req.Header.Set("x-DeviceInfo", "||9|7.14.0|chrome|120.0.0.0|||windows 10||zh-CN|||")
	req.Header.Set("x-huawei-channelSrc", "10000034")
	req.Header.Set("x-inner-ntwk", "2")
	req.Header.Set("x-m4c-caller", "PC")
	req.Header.Set("x-m4c-src", "10002")
	req.Header.Set("x-SvcType", "1")
	req.Header.Set("Inner-Hcy-Router-Https", "1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0 Safari/537.36")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求路由接口失败: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("路由接口响应异常 [%d]: %s", resp.StatusCode, string(respBody))
	}
	var rr routeResp
	if err := json.Unmarshal(respBody, &rr); err != nil {
		return "", fmt.Errorf("解析路由响应失败 (%v): %s", err, string(respBody))
	}
	for _, item := range rr.Data.RoutePolicyList {
		if item.ModName == "personal" {
			return item.HttpsUrl, nil
		}
	}
	return "", fmt.Errorf("未找到个人云路由 (响应: %s)", string(respBody))
}

// --- 签名 ---

func encodeURIComponent(s string) string {
	r := url.QueryEscape(s)
	r = strings.Replace(r, "+", "%20", -1)
	r = strings.Replace(r, "%21", "!", -1)
	r = strings.Replace(r, "%27", "'", -1)
	r = strings.Replace(r, "%28", "(", -1)
	r = strings.Replace(r, "%29", ")", -1)
	r = strings.Replace(r, "%2A", "*", -1)
	return r
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// calSign 计算请求签名（与 OpenList 139Yun 驱动一致，为互操作必需的协议算法）。
func calSign(body, ts, randStr string) string {
	body = encodeURIComponent(body)
	strs := strings.Split(body, "")
	sort.Strings(strs)
	body = strings.Join(strs, "")
	body = base64.StdEncoding.EncodeToString([]byte(body))
	res := md5Hex(body) + md5Hex(ts+":"+randStr)
	return strings.ToUpper(md5Hex(res))
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	// 使用密码学安全随机源，避免基于时间的"伪随机"导致签名随机串重复
	for i := range b {
		v, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			// 极端情况下回退（不应发生）
			b[i] = letters[byte(time.Now().UnixNano())%byte(len(letters))]
			continue
		}
		b[i] = letters[v.Int64()]
	}
	return string(b)
}

// request 发送带签名和特殊请求头的请求（personal_new 使用 Mcloud-* / X-Yun-* 头）。
// 401 时自动刷新令牌并重试一次。
func (d *MobileCloud) request(ctx context.Context, method, pathname string, data interface{}) ([]byte, error) {
	bodyBytes, _ := json.Marshal(data)
	randStr := randomString(16)
	ts := time.Now().Format("2006-01-02 15:04:05")
	sign := calSign(string(bodyBytes), ts, randStr)

	u := d.apiBase() + pathname
	req, err := http.NewRequestWithContext(ctx, method, u, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Authorization", "Basic "+d.getAuthorization())
	req.Header.Set("Caller", "web")
	req.Header.Set("Cms-Device", "default")
	req.Header.Set("Mcloud-Channel", "1000101")
	req.Header.Set("Mcloud-Client", "10701")
	req.Header.Set("Mcloud-Route", "001")
	req.Header.Set("Mcloud-Sign", fmt.Sprintf("%s,%s,%s", ts, randStr, sign))
	req.Header.Set("Mcloud-Version", "7.14.0")
	req.Header.Set("x-DeviceInfo", "||9|7.14.0|chrome|120.0.0.0|||windows 10||zh-CN|||")
	req.Header.Set("x-huawei-channelSrc", "10000034")
	req.Header.Set("x-inner-ntwk", "2")
	req.Header.Set("x-m4c-caller", "PC")
	req.Header.Set("x-m4c-src", "10002")
	req.Header.Set("x-SvcType", "1")
	req.Header.Set("X-Yun-Api-Version", "v1")
	req.Header.Set("X-Yun-App-Channel", "10000034")
	req.Header.Set("X-Yun-Channel-Source", "10000034")
	req.Header.Set("X-Yun-Client-Info", "||9|7.14.0|chrome|120.0.0.0|||windows 10||zh-CN|||dW5kZWZpbmVk||")
	req.Header.Set("X-Yun-Module-Type", "100")
	req.Header.Set("X-Yun-Svc-Type", "1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0 Safari/537.36")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	// 401 未授权：尝试刷新令牌后重试一次
	if resp.StatusCode == http.StatusUnauthorized {
		if err := d.refreshToken(ctx); err != nil {
			return nil, fmt.Errorf("移动云盘令牌刷新失败: %w", err)
		}
		req2, _ := http.NewRequestWithContext(ctx, method, u, strings.NewReader(string(bodyBytes)))
		req2.Header = req.Header.Clone()
		req2.Header.Set("Authorization", "Basic "+d.getAuthorization())
		randStr2 := randomString(16)
		ts2 := time.Now().Format("2006-01-02 15:04:05")
		sign2 := calSign(string(bodyBytes), ts2, randStr2)
		req2.Header.Set("Mcloud-Sign", fmt.Sprintf("%s,%s,%s", ts2, randStr2, sign2))
		resp2, err := d.httpClient.Do(req2)
		if err != nil {
			return nil, err
		}
		defer resp2.Body.Close()
		respBody, _ = io.ReadAll(resp2.Body)
		if resp2.StatusCode != 200 {
			return nil, fmt.Errorf("移动云盘请求失败 [%d]: %s", resp2.StatusCode, string(respBody))
		}
		return respBody, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("移动云盘请求失败 [%d]: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// --- 登录辅助 ---

func (d *MobileCloud) fastLogin(mailCookies string) (string, string, error) {
	// 通过邮箱 Cookie 请求获取登录信息
	req, _ := http.NewRequest("POST", d.apiBase()+"/orchestration/login/getLoginInfo", nil)
	req.Header.Set("Cookie", mailCookies)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data loginResp
	if err := json.Unmarshal(body, &data); err != nil {
		return "", "", err
	}
	if data.Data.Authorization == "" {
		return "", "", fmt.Errorf("Cookie 登录未返回 Authorization")
	}
	return data.Data.Authorization, data.Data.Account, nil
}

func (d *MobileCloud) loginWithPassword(mailCookies, username, password string) (string, string, error) {
	// 先尝试 Cookie 快速登录
	if mailCookies != "" {
		if auth, account, err := d.fastLogin(mailCookies); err == nil && auth != "" {
			return auth, account, nil
		}
	}
	// 回退到账号密码登录（简化实现，兼容性不保证）
	reqBody := map[string]string{"account": username, "password": password}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", d.apiBase()+"/orchestration/login/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var data loginResp
	if err := json.Unmarshal(respBody, &data); err != nil {
		return "", "", err
	}
	if data.Data.Authorization == "" {
		return "", "", fmt.Errorf("账号密码登录未返回 Authorization")
	}
	return data.Data.Authorization, data.Data.Account, nil
}
