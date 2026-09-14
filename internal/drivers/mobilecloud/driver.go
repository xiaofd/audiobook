package mobilecloud

// Driver 接口实现：Init/List/Link/Open/OpenRange/RefreshToken。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"audiobook/internal/driver"
)

// MobileCloud 中国移动云盘（139 云盘）驱动。
type MobileCloud struct {
	httpClient *http.Client
	cfg        map[string]string

	// authMu 保护 authorization/account 的并发读写：
	// 扫描（List）与令牌刷新（refreshToken）可能同时发生。
	authMu sync.RWMutex
	// authorization Basic xxx 后面的内容（Base64 编码的 前缀:账号:令牌|...）
	authorization string
	// account 从 Authorization 解码出的账号
	account           string
	cloudID           string
	personalCloudHost string                   // 路由查询得到的个人云主机（personal_new 使用）
	saveCfg           func(map[string]string)  // 凭据轮换后立即持久化的回调（由服务层注入）
}

// SetConfigSaver 实现 driver.ConfigSaver 接口。
func (d *MobileCloud) SetConfigSaver(save func(map[string]string)) { d.saveCfg = save }

// persistCfg 凭据轮换后立即通知服务层落盘，避免进程重启后使用已失效的旧令牌。
// 调用方必须持有 authMu；cfgMap 为轮换后的新 map（COW，异步回调可安全持有）。
func (d *MobileCloud) persistCfg(cfgMap map[string]string) {
	if d.saveCfg != nil {
		d.saveCfg(cfgMap)
	}
}

// getAuthorization 并发安全地读取当前令牌。
func (d *MobileCloud) getAuthorization() string {
	d.authMu.RLock()
	defer d.authMu.RUnlock()
	return d.authorization
}

// getAccount 并发安全地读取当前账号。
func (d *MobileCloud) getAccount() string {
	d.authMu.RLock()
	defer d.authMu.RUnlock()
	return d.account
}

// setAuth 并发安全地写入令牌与账号。
func (d *MobileCloud) setAuth(auth, account string) {
	d.authMu.Lock()
	d.authorization = auth
	d.account = account
	d.authMu.Unlock()
}

// setCfgKeyLocked 以 COW 方式更新配置键后整体替换 map，
// 避免与其他 goroutine 对 cfg 的只读访问产生 map 并发读写 panic。调用方必须持有 authMu。
func (d *MobileCloud) setCfgKeyLocked(key, val string) map[string]string {
	newCfg := make(map[string]string, len(d.cfg))
	for k, v := range d.cfg {
		newCfg[k] = v
	}
	newCfg[key] = val
	d.cfg = newCfg
	return newCfg
}

func (d *MobileCloud) Init(_ context.Context, cfg map[string]string) error {
	d.cfg = cfg
	d.httpClient = &http.Client{Timeout: 60 * time.Second}

	authType := strings.TrimSpace(cfg["authType"])
	if authType == "" {
		authType = AuthTypeAuthorization
	}

	switch authType {
	case AuthTypePassword:
		mailCookies := strings.TrimSpace(cfg["mailCookies"])
		username := strings.TrimSpace(cfg["username"])
		password := strings.TrimSpace(cfg["password"])
		if username == "" || password == "" {
			return fmt.Errorf("密码登录方式需要填写账号和密码")
		}
		auth, account, err := d.loginWithPassword(mailCookies, username, password)
		if err != nil {
			return fmt.Errorf("移动云盘登录失败: %w", err)
		}
		d.setAuth(auth, account)
		return d.initRoute()

	case AuthTypeCookie:
		mailCookies := strings.TrimSpace(cfg["mailCookies"])
		if mailCookies == "" {
			return fmt.Errorf("Cookie 登录方式需要填写邮箱 Cookie")
		}
		auth, account, err := d.fastLogin(mailCookies)
		if err != nil {
			return fmt.Errorf("移动云盘 Cookie 登录失败: %w", err)
		}
		d.setAuth(auth, account)
		return d.initRoute()

	default: // authorization
		auth := strings.TrimSpace(cfg["authorization"])
		if auth == "" {
			return fmt.Errorf("Authorization 方式需要填写 Authorization (请检查凭据是否填写或跨机解密失败)")
		}
		account := decodeAccount(auth)
		if account == "" {
			return fmt.Errorf("无法从 Authorization 解析出移动云盘账号，请检查 Authorization 格式是否正确")
		}
		d.setAuth(auth, account)
		return d.initRoute()
	}
}

// initRoute 查询路由策略，获取个人云主机地址（personal_new 必须）。
func (d *MobileCloud) initRoute() error {
	host, err := d.queryRoute()
	if err != nil {
		return fmt.Errorf("移动云盘路由查询失败: %w", err)
	}
	if host == "" {
		return fmt.Errorf("移动云盘未获取到个人云主机地址")
	}
	d.personalCloudHost = host
	return nil
}

func (d *MobileCloud) Drop(_ context.Context) error { d.setAuth("", ""); return nil }

func (d *MobileCloud) List(ctx context.Context, dirPath string) ([]driver.Obj, error) {
	parentFileID := dirPath
	if parentFileID == "" || parentFileID == "/" {
		parentFileID = d.rootFolderID()
	}
	objs := make([]driver.Obj, 0)
	nextPageCursor := ""
	for {
		data := map[string]interface{}{
			"imageThumbnailStyleList": []string{"Small", "Large"},
			"orderBy":                 "updated_at",
			"orderDirection":          "DESC",
			"pageInfo": map[string]interface{}{
				"pageCursor": nextPageCursor,
				"pageSize":   100,
			},
			"parentFileId": parentFileID,
		}
		respBody, err := d.request(ctx, "POST", "/file/list", data)
		if err != nil {
			return nil, err
		}
		var resp personalListResp
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return nil, fmt.Errorf("解析列表响应失败: %v", err)
		}
		if !resp.Success {
			return nil, fmt.Errorf("移动云盘列表失败: %s", string(respBody))
		}
		nextPageCursor = resp.Data.NextPageCursor
		for _, item := range resp.Data.Items {
			isDir := item.Type == "folder"
			objs = append(objs, driver.Obj{
				Name:  item.Name,
				Path:  item.FileId,
				Size:  item.Size,
				IsDir: isDir,
			})
		}
		if nextPageCursor == "" {
			break
		}
	}
	sort.Slice(objs, func(i, j int) bool {
		if objs[i].IsDir != objs[j].IsDir {
			return objs[i].IsDir
		}
		return objs[i].Name < objs[j].Name
	})
	return objs, nil
}

func (d *MobileCloud) Link(ctx context.Context, file driver.Obj) (*driver.Link, error) {
	data := map[string]interface{}{
		"fileId": file.Path,
	}
	respBody, err := d.request(ctx, "POST", "/file/getDownloadUrl", data)
	if err != nil {
		return nil, err
	}
	var resp downloadUrlResp
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	dl := resp.Data.URL
	if resp.Data.CDNUrl != "" && resp.Data.CDNSwitch {
		dl = resp.Data.CDNUrl
	}
	if dl == "" {
		return nil, fmt.Errorf("未返回下载链接")
	}
	return &driver.Link{URL: dl}, nil
}

func (d *MobileCloud) Open(ctx context.Context, file driver.Obj) (io.ReadCloser, int64, error) {
	link, err := d.Link(ctx, file)
	if err != nil {
		return nil, 0, err
	}
	// 使用带超时的自定义 client（http.Get 默认无超时，下载挂起会永久占用 goroutine）
	req, err := http.NewRequestWithContext(ctx, "GET", link.URL, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("下载失败 [%d]", resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, nil
}

// OpenRange 实现 driver.RangeReader 接口：通过下载直链透传 Range 头按需拉取字节范围。
func (d *MobileCloud) OpenRange(ctx context.Context, file driver.Obj, start, length int64) (io.ReadCloser, int64, error) {
	link, err := d.Link(ctx, file)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", link.URL, nil)
	if err != nil {
		return nil, 0, err
	}
	if length < 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
	} else {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+length-1))
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode == http.StatusPartialContent {
		// 服务器支持 Range，直接透传
		return resp.Body, resp.ContentLength, nil
	}
	if resp.StatusCode == http.StatusOK {
		// 服务器忽略 Range 返回全文：丢弃前 start 字节并截取 length 字节，
		// 保证返回的流与声明的范围一致（否则调用方写出的 206 头与实际数据错位）
		if start > 0 {
			if _, err := io.CopyN(io.Discard, resp.Body, start); err != nil {
				resp.Body.Close()
				return nil, 0, fmt.Errorf("跳过范围前缀失败: %w", err)
			}
		}
		if length < 0 {
			return resp.Body, resp.ContentLength, nil
		}
		return struct {
			io.Reader
			io.Closer
		}{io.LimitReader(resp.Body, length), resp.Body}, length, nil
	}
	resp.Body.Close()
	return nil, 0, fmt.Errorf("范围下载失败 [%d]", resp.StatusCode)
}

// refreshToken 解析 Authorization，令牌过期时刷新，失败回退账号密码登录。
func (d *MobileCloud) refreshToken(ctx context.Context) error {
	d.authMu.Lock()
	defer d.authMu.Unlock()

	// 尝试用 Authorization 中的令牌刷新
	decode, err := base64.StdEncoding.DecodeString(d.authorization)
	if err == nil {
		splits := strings.Split(string(decode), ":")
		if len(splits) >= 3 {
			// 调用令牌刷新接口
			reqBody := "<root><token>" + splits[2] + "</token><account>" + splits[1] + "</account><clienttype>656</clienttype></root>"
			req, _ := http.NewRequestWithContext(ctx, "POST", "https://aas.caiyun.feixin.10086.cn:443/tellin/authTokenRefresh.do", strings.NewReader(reqBody))
			req.Header.Set("Content-Type", "application/xml")
			resp, err := d.httpClient.Do(req)
			if err == nil {
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				var refreshResp tokenRefreshResp
				if json.Unmarshal(body, &refreshResp) == nil && refreshResp.Return == "0" && refreshResp.Token != "" {
					d.authorization = base64.StdEncoding.EncodeToString([]byte(splits[0] + ":" + splits[1] + ":" + refreshResp.Token))
					d.persistCfg(d.setCfgKeyLocked("authorization", d.authorization))
					return nil
				}
			}
		}
	}

	// 回退到账号密码登录
	authType := strings.TrimSpace(d.cfg["authType"])
	if authType == AuthTypePassword {
		mailCookies := strings.TrimSpace(d.cfg["mailCookies"])
		username := strings.TrimSpace(d.cfg["username"])
		password := strings.TrimSpace(d.cfg["password"])
		auth, account, err := d.loginWithPassword(mailCookies, username, password)
		if err != nil {
			return err
		}
		d.authorization = auth
		d.account = account
		d.persistCfg(d.setCfgKeyLocked("authorization", auth))
		return nil
	}
	return fmt.Errorf("令牌刷新失败且无账号密码回退")
}

// RefreshToken 实现 driver.TokenRefresher 接口，用于定时任务触发。
func (d *MobileCloud) RefreshToken(ctx context.Context) (map[string]string, error) {
	if err := d.refreshToken(ctx); err != nil {
		return nil, err
	}
	// 拷贝需持锁，避免与令牌轮换的 COW 替换并发
	d.authMu.Lock()
	res := make(map[string]string, len(d.cfg))
	for k, v := range d.cfg {
		res[k] = v
	}
	d.authMu.Unlock()
	res["authorization"] = d.getAuthorization()
	return res, nil
}
