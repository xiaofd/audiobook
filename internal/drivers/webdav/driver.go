package webdav

// Driver 接口实现：Init/List/Link/Open/OpenRange/Put。

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"audiobook/internal/driver"
)

// WebDAV 通过标准 WebDAV 协议访问。
// WebDAV 基于 Basic 认证，无法生成免认证直链，因此不支持 302 模式（走服务器中转）；
// 但 WebDAV 服务器基于 HTTP，普遍支持 Range 请求，中转模式下拖动进度仍可按需拉取。
type WebDAV struct {
	httpClient *http.Client
	baseURL    *url.URL // 服务器根地址（不含挂载子目录）
	cfg        map[string]string
}

// --- 列目录（PROPFIND Depth:1） ---

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:">
  <D:prop>
    <D:resourcetype/>
    <D:getcontentlength/>
    <D:getlastmodified/>
  </D:prop>
</D:propfind>`

func (d *WebDAV) Init(_ context.Context, cfg map[string]string) error {
	d.cfg = cfg
	raw := strings.TrimSpace(cfg["url"])
	if raw == "" {
		return fmt.Errorf("服务器地址不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("服务器地址无效（需以 http:// 或 https:// 开头）")
	}
	d.baseURL = u
	d.httpClient = &http.Client{Timeout: 60 * time.Second}
	// 连通性探测：列出根目录（认证错误/地址错误在此暴露）
	if _, err := d.List(context.Background(), "/"); err != nil {
		return fmt.Errorf("WebDAV 连接失败: %w", err)
	}
	return nil
}

func (d *WebDAV) Drop(_ context.Context) error { return nil }

func (d *WebDAV) List(ctx context.Context, p string) ([]driver.Obj, error) {
	resp, err := d.doRequest(ctx, "PROPFIND", d.requestURL(p), strings.NewReader(propfindBody), map[string]string{
		"Depth":        "1",
		"Content-Type": "application/xml",
		"Accept":       "application/xml",
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("PROPFIND 失败 [%d]: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var ms multistatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("解析 PROPFIND 响应失败: %w", err)
	}

	reqRel := "/" + strings.Trim(p, "/")
	var objs []driver.Obj
	for _, item := range ms.Responses {
		rel := d.toRelPath(item.Href)
		if rel == "" || rel == reqRel {
			continue // 不在挂载前缀下，或是目录自身
		}
		name := path.Base(rel)
		if name == "" || name == "." || name == "/" {
			continue
		}
		// 合并 propstat（部分服务器对不存在的属性返回 404 propstat）
		isDir := false
		var size int64
		var mod time.Time
		for _, ps := range item.Propstat {
			if ps.Status != "" && !strings.Contains(ps.Status, "200") {
				continue
			}
			if ps.Prop.ResourceType.Collection != nil {
				isDir = true
			}
			if n, err := strconv.ParseInt(strings.TrimSpace(ps.Prop.GetContentLength), 10, 64); err == nil {
				size = n
			}
			if t, err := http.ParseTime(strings.TrimSpace(ps.Prop.GetLastModified)); err == nil {
				mod = t
			}
		}
		objs = append(objs, driver.Obj{
			Name:     name,
			Path:     rel,
			Size:     size,
			Modified: mod,
			IsDir:    isDir,
		})
	}
	sort.Slice(objs, func(i, j int) bool {
		if objs[i].IsDir != objs[j].IsDir {
			return objs[i].IsDir
		}
		return objs[i].Name < objs[j].Name
	})
	return objs, nil
}

// Link 不支持直链（Basic 认证无法免鉴权访问）。
func (d *WebDAV) Link(_ context.Context, _ driver.Obj) (*driver.Link, error) {
	return nil, nil
}

func (d *WebDAV) Open(ctx context.Context, file driver.Obj) (io.ReadCloser, int64, error) {
	resp, err := d.doRequest(ctx, "GET", d.requestURL(file.Path), nil, nil)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("下载失败 [%d]", resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, nil
}

// OpenRange 实现 driver.RangeReader 接口：WebDAV 基于 HTTP，普遍支持 Range 请求，
// 中转模式下拖动进度只拉取所需字节（与网盘直链同等的体验）。
func (d *WebDAV) OpenRange(ctx context.Context, file driver.Obj, start, length int64) (io.ReadCloser, int64, error) {
	headers := map[string]string{}
	if length < 0 {
		headers["Range"] = fmt.Sprintf("bytes=%d-", start)
	} else {
		headers["Range"] = fmt.Sprintf("bytes=%d-%d", start, start+length-1)
	}
	resp, err := d.doRequest(ctx, "GET", d.requestURL(file.Path), nil, headers)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode == http.StatusPartialContent {
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

// Put 实现 driver.Writer 接口，支持向 WebDAV 目录上传文件（如封面）。
func (d *WebDAV) Put(ctx context.Context, parentPath, name string, r io.Reader, size int64) (driver.Obj, error) {
	target := d.requestURL(path.Join(parentPath, name))
	req, err := http.NewRequestWithContext(ctx, "PUT", target, r)
	if err != nil {
		return driver.Obj{}, err
	}
	if u := strings.TrimSpace(d.cfg["username"]); u != "" {
		req.SetBasicAuth(u, d.cfg["password"])
	}
	if size > 0 {
		req.ContentLength = size
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return driver.Obj{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return driver.Obj{}, fmt.Errorf("WebDAV 上传失败 [%d]: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return driver.Obj{
		Name:  name,
		Path:  path.Join(parentPath, name),
		Size:  size,
		IsDir: false,
	}, nil
}
