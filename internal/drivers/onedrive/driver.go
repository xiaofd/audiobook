package onedrive

// Driver 接口实现：Init/List/Link/Open/OpenRange/Put/RefreshToken。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"audiobook/internal/driver"
)

// OneDrive 通过 Microsoft Graph API 访问 OneDrive 个人版/商业版。
type OneDrive struct {
	httpClient *http.Client

	// authMu 保护 accessToken 与 cfg：并发 401 时多个请求同时刷新令牌，
	// 会双重消耗一次性 refresh token（第二次用旧 token 必然失败），
	// 且并发写 cfg map 会触发 runtime panic。
	authMu      sync.Mutex
	accessToken string
	cfg         map[string]string
	saveCfg     func(map[string]string) // 凭据轮换后立即持久化的回调（由服务层注入）
}

// token 并发安全地读取当前 access token。
func (d *OneDrive) token() string {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	return d.accessToken
}

// cfgGet 并发安全地读取配置项。
func (d *OneDrive) cfgGet(key string) string {
	d.authMu.Lock()
	defer d.authMu.Unlock()
	return d.cfg[key]
}

// SetConfigSaver 实现 driver.ConfigSaver 接口。
func (d *OneDrive) SetConfigSaver(save func(map[string]string)) { d.saveCfg = save }

// persistCfg 凭据轮换后立即通知服务层落盘（OneDrive refresh token 一次性，
// 若仅在内存更新而进程重启，磁盘上的旧 token 已作废，将导致永久认证失败）。
// 调用方必须持有 authMu；cfgMap 为轮换后的新 map（COW，异步回调可安全持有）。
func (d *OneDrive) persistCfg(cfgMap map[string]string) {
	if d.saveCfg != nil {
		d.saveCfg(cfgMap)
	}
}

func (d *OneDrive) Init(ctx context.Context, cfg map[string]string) error {
	d.cfg = cfg
	d.httpClient = &http.Client{Timeout: 30 * time.Second}
	// 用 refresh token 换 access token（支持在线 API 与本地客户端两种方式）；
	// accessToken 与 cfg 轮换在 refreshToken 内部持锁完成
	if _, err := d.refreshToken(); err != nil {
		return fmt.Errorf("onedrive 认证失败: %w", err)
	}
	_ = ctx
	return nil
}

func (d *OneDrive) Drop(_ context.Context) error { d.accessToken = ""; return nil }

func (d *OneDrive) List(_ context.Context, p string) ([]driver.Obj, error) {
	u := d.graphURL(strings.TrimRight(p, "/"))
	objs := make([]driver.Obj, 0)
	// 跟随 @odata.nextLink 分页，避免单目录超过 200 条时分集被截断
	for u != "" {
		resp, err := d.doReq("GET", u)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("List 失败 [%d]: %s", resp.StatusCode, string(body))
		}
		var data graphChildrenResp
		err = json.NewDecoder(resp.Body).Decode(&data)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, v := range data.Value {
			mod, _ := time.Parse(time.RFC3339, v.LastMod)
			// Graph 的 audio.duration 为毫秒；映射为秒（0 表示未知）
			dur := 0
			if v.Audio != nil && v.Audio.Duration > 0 {
				dur = int(v.Audio.Duration / 1000)
			}
			objs = append(objs, driver.Obj{
				Name:     v.Name,
				Path:     path.Join(p, v.Name),
				Size:     v.Size,
				Modified: mod,
				IsDir:    v.Folder != nil,
				Duration: dur,
			})
		}
		u = data.NextLink
	}
	sort.Slice(objs, func(i, j int) bool {
		if objs[i].IsDir != objs[j].IsDir {
			return objs[i].IsDir
		}
		return objs[i].Name < objs[j].Name
	})
	return objs, nil
}

func (d *OneDrive) Link(_ context.Context, file driver.Obj) (*driver.Link, error) {
	u := d.itemURL(file.Path)
	resp, err := d.doReq("GET", u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("获取直链失败 [%d]", resp.StatusCode)
	}
	var item graphItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, err
	}
	if item.DownloadURL == "" {
		return nil, fmt.Errorf("onedrive 未返回直链")
	}
	return &driver.Link{URL: item.DownloadURL}, nil
}

func (d *OneDrive) Open(ctx context.Context, file driver.Obj) (io.ReadCloser, int64, error) {
	link, err := d.Link(ctx, file)
	if err != nil {
		return nil, 0, err
	}
	// 使用带超时的自定义 client（http.Get 默认无超时，直链下载挂起会永久占用 goroutine）
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
		return nil, 0, fmt.Errorf("直链下载失败 [%d]", resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, nil
}

// OpenRange 实现 driver.RangeReader 接口：通过直链透传 Range 头按需拉取字节范围，
// 避免中转模式下拖动进度时重复下载整个文件前缀。
func (d *OneDrive) OpenRange(ctx context.Context, file driver.Obj, start, length int64) (io.ReadCloser, int64, error) {
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

// Put 实现 driver.Writer 接口，支持向 OneDrive 目录上传文件（如封面）。
func (d *OneDrive) Put(ctx context.Context, parentPath string, name string, r io.Reader, size int64) (driver.Obj, error) {
	targetPath := path.Join(parentPath, name)
	enc := url.PathEscape(strings.TrimPrefix(targetPath, "/"))
	uploadURL := fmt.Sprintf("%s%s:/%s:/content", d.graphBase(), d.drivePrefix(), enc)

	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, r)
	if err != nil {
		return driver.Obj{}, err
	}
	req.Header.Set("Authorization", "Bearer "+d.token())
	if size > 0 {
		req.ContentLength = size
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return driver.Obj{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return driver.Obj{}, fmt.Errorf("onedrive 上传失败 [%d]: %s", resp.StatusCode, string(bodyBytes))
	}

	var item graphItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return driver.Obj{Name: name, Path: targetPath, Size: size, IsDir: false}, nil
	}

	return driver.Obj{
		Name:  item.Name,
		Path:  targetPath,
		Size:  item.Size,
		IsDir: false,
	}, nil
}

// RefreshToken 实现 driver.TokenRefresher 接口，用于定时任务触发。
func (d *OneDrive) RefreshToken(ctx context.Context) (map[string]string, error) {
	if _, err := d.refreshToken(); err != nil {
		return nil, err
	}
	// 拷贝需持锁，避免与令牌轮换的 COW 替换并发
	d.authMu.Lock()
	res := make(map[string]string, len(d.cfg))
	for k, v := range d.cfg {
		res[k] = v
	}
	d.authMu.Unlock()
	return res, nil
}
