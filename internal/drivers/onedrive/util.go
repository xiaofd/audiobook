package onedrive

// 请求构造、认证与令牌刷新（参照 OpenList 的 drivers/<name>/util.go 组织方式）。

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// graphBase 按区域返回 Graph API 根地址。
func (d *OneDrive) graphBase() string {
	region := d.cfgGet("region")
	if region == "cn" {
		return "https://microsoftgraph.chinacloudapi.cn"
	} else if region == "us" {
		return "https://graph.microsoft.us"
	} else if region == "de" {
		return "https://graph.microsoft.de"
	}
	return "https://graph.microsoft.com"
}

// drivePrefix 返回 drive 根前缀（个人版 /me/drive 或 SharePoint site）。
func (d *OneDrive) drivePrefix() string {
	siteID := strings.TrimSpace(d.cfgGet("siteId"))
	if siteID != "" {
		return fmt.Sprintf("/v1.0/sites/%s/drive/root", siteID)
	}
	return "/v1.0/me/drive/root"
}

// graphURL 构造列目录（children）请求地址。
func (d *OneDrive) graphURL(p string) string {
	enc := url.PathEscape(p)
	if enc == "/" || enc == "" {
		return d.graphBase() + d.drivePrefix() + "/children"
	}
	return d.graphBase() + d.drivePrefix() + ":" + enc + ":/children"
}

// itemURL 构造单个条目元数据请求地址。
func (d *OneDrive) itemURL(p string) string {
	enc := url.PathEscape(p)
	return d.graphBase() + d.drivePrefix() + ":" + enc
}

// doReq 发送 Graph API 请求，内置 429 限流重试与 401 令牌过期自动刷新。
func (d *OneDrive) doReq(method, url string) (*http.Response, error) {
	resp, err := d.doReqOnce(method, url)
	if err != nil {
		return nil, err
	}
	// 429 限流：Graph API 对密集列举（扫描书库）限流较严，按 Retry-After 等待后重试一次
	if resp.StatusCode == http.StatusTooManyRequests {
		wait := 5 * time.Second
		if sec, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && sec > 0 {
			wait = time.Duration(sec) * time.Second
			if wait > 30*time.Second {
				wait = 30 * time.Second
			}
		}
		resp.Body.Close()
		log.Printf("[OneDrive] 429 限流，等待 %v 后重试: %s", wait, url)
		time.Sleep(wait)
		resp, err = d.doReqOnce(method, url)
		if err != nil {
			return nil, err
		}
	}
	// 401：令牌过期，自动刷新后重试一次（Graph access token 约 1 小时过期）。
	// refreshToken 内部持 authMu 串行化，并发 401 不会双重消耗一次性 refresh token。
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if _, err := d.refreshToken(); err != nil {
			return nil, fmt.Errorf("令牌刷新失败: %w", err)
		}
		resp, err = d.doReqOnce(method, url)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}

// doReqOnce 发送单次请求（无重试）。
func (d *OneDrive) doReqOnce(method, url string) (*http.Response, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.token())
	req.Header.Set("Accept", "application/json")
	return d.httpClient.Do(req)
}

// refreshToken 使用 refresh token 换取 access token。
// 持 authMu 串行化；轮换 refresh token 时以 COW 方式整体替换 cfg map，
// 避免与其他请求的 cfg 只读访问产生 map 并发读写 panic。
// 当未配置 ClientID/ClientSecret 时，使用在线 API（api.oplist.org）刷新令牌；
// 否则使用本地客户端凭据刷新。
func (d *OneDrive) refreshToken() (string, error) {
	d.authMu.Lock()
	defer d.authMu.Unlock()

	rt := strings.TrimSpace(d.cfg["refreshToken"])
	if rt == "" {
		return "", fmt.Errorf("Refresh Token 不能为空")
	}
	clientID := strings.TrimSpace(d.cfg["clientId"])
	clientSecret := strings.TrimSpace(d.cfg["clientSecret"])

	// 方式1：使用在线 API 刷新（无需 ClientID/ClientSecret）
	if clientID == "" || clientSecret == "" {
		return d.refreshTokenOnlineLocked(rt)
	}

	// 方式2：使用本地客户端凭据刷新
	region := d.cfg["region"]
	if region == "" {
		region = "global"
	}
	tenant := "common"
	if region == "cn" {
		tenant = "common"
	}
	data := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {d.cfg["redirectUri"]},
		"refresh_token": {rt},
		"grant_type":    {"refresh_token"},
	}
	endpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenant)
	resp, err := http.PostForm(endpoint, data)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token 刷新失败 [%d]: %s", resp.StatusCode, string(body))
	}
	var result graphTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	// 更新 refresh token 并立即持久化（一次性令牌，丢失即作废）
	if result.RefreshToken != "" {
		d.applyRotationLocked(result.RefreshToken)
	}
	d.accessToken = result.AccessToken
	return result.AccessToken, nil
}

// applyRotationLocked 以 COW 方式轮换 refresh token 并立即持久化。调用方必须持有 authMu。
func (d *OneDrive) applyRotationLocked(newRT string) {
	newCfg := make(map[string]string, len(d.cfg))
	for k, v := range d.cfg {
		newCfg[k] = v
	}
	newCfg["refreshToken"] = newRT
	d.cfg = newCfg
	d.persistCfg(newCfg)
}

// refreshTokenOnlineLocked 使用 oplist.org 在线 API 刷新 token。调用方必须持有 authMu。
func (d *OneDrive) refreshTokenOnlineLocked(rt string) (string, error) {
	apiURL := "https://api.oplist.org/onedrive/renewapi"
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Set("refresh_ui", rt)
	q.Set("server_use", "true")
	q.Set("driver_txt", "onedrive_pr")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result onlineTokenResp
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("在线 API 响应解析失败: %v", err)
	}
	if result.AccessToken == "" || result.RefreshToken == "" {
		msg := result.ErrorMessage
		if msg == "" {
			// 注意：这里并不代表 Refresh Token 一定无效——api.oplist.org 是第三方中转服务，
			// 波动/限流同样会走到该分支。持续失败才需要重新获取 Token。
			msg = "在线 API 未返回有效令牌：可能是第三方 API (api.oplist.org) 波动，请稍后重试；若持续失败则 Refresh Token 已失效，需重新获取"
		}
		return "", fmt.Errorf("%s", msg)
	}
	// 更新 refresh token（在线 API 会返回新的 refresh token）并立即持久化（一次性令牌，丢失即作废）
	d.applyRotationLocked(result.RefreshToken)
	d.accessToken = result.AccessToken
	return result.AccessToken, nil
}
