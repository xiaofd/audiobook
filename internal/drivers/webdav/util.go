package webdav

// 路径映射与请求构造（参照 OpenList 的 drivers/<name>/util.go 组织方式）。

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// mountPrefix 挂载子目录对应的 URL 路径前缀（解码形式）。
func (d *WebDAV) mountPrefix() string {
	prefix := strings.TrimSuffix(d.baseURL.Path, "/")
	if mount := strings.Trim(d.cfg["mountPath"], "/"); mount != "" {
		prefix = prefix + "/" + mount
	}
	return strings.TrimSuffix(prefix, "/")
}

// requestURL 将驱动内部相对路径映射为完整请求 URL。
func (d *WebDAV) requestURL(p string) string {
	clean := "/" + strings.Trim(p, "/")
	u := *d.baseURL
	u.Path = d.mountPrefix() + clean
	return u.String()
}

// toRelPath 将服务器返回的 href（URL 路径）转换为驱动内部相对路径；
// 若不在挂载前缀之下则返回空串（忽略）。
func (d *WebDAV) toRelPath(href string) string {
	un, err := url.PathUnescape(href)
	if err != nil {
		un = href
	}
	un = strings.TrimSuffix(un, "/")
	prefix := d.mountPrefix()
	if prefix == "" {
		return "/" + strings.Trim(un, "/")
	}
	if un != prefix && !strings.HasPrefix(un, prefix+"/") {
		return ""
	}
	return "/" + strings.Trim(strings.TrimPrefix(un, prefix), "/")
}

// doRequest 发送带 Basic 认证的请求。
func (d *WebDAV) doRequest(ctx context.Context, method, fullURL string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, err
	}
	if u := strings.TrimSpace(d.cfg["username"]); u != "" {
		req.SetBasicAuth(u, d.cfg["password"])
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return d.httpClient.Do(req)
}
