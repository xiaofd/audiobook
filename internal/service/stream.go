package service

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"audiobook/internal/driver"
)

// ServeStream 处理流式播放请求：根据存储实例的 StreamMode 决定走中转还是 302 直链。
func ServeStream(w http.ResponseWriter, r *http.Request, episode *Episode) {
	storageID := episode.StorageID
	mode := GetStreamMode(storageID)
	drv := GetDriver(storageID)
	if drv == nil {
		// 尝试主动重新拉起驱动
		if si, ok := GetStorageInstance(storageID); ok {
			if err := InitDriver(si); err == nil {
				drv = GetDriver(storageID)
			} else {
				log.Printf("[Stream] 存储驱动初始化失败: storageID=%s, err=%v", storageID, err)
			}
		}
	}
	if drv == nil {
		log.Printf("[Stream] 存储驱动未就绪: storageID=%s, episode=%s", storageID, episode.ID)
		http.Error(w, "存储驱动未就绪", http.StatusServiceUnavailable)
		return
	}

	dobj := fileObj(episode)
	contentType := mimeByExt(episode.FilePath)

	// 尝试直链（redirect / auto）
	if mode == StreamRedirect || mode == StreamAuto {
		link, err := drv.Link(r.Context(), dobj)
		if err == nil && link != nil && link.URL != "" {
			// 直链含临时下载令牌，日志不落盘 URL（脱敏）
			log.Printf("[Stream] 302 直链: %s", episode.Title)
			w.Header().Set("Cache-Control", "no-cache")
			http.Redirect(w, r, link.URL, http.StatusFound)
			return
		}
		if err != nil {
			log.Printf("[Stream] 直链生成失败 (mode=%s): %v, 将尝试中转模式", mode, err)
		}
		if mode == StreamRedirect {
			http.Error(w, "直链不可用: "+fmt.Sprintf("%v", err), http.StatusBadGateway)
			return
		}
	}

	// 服务器中转（proxy / auto 降级）
	if r.Header.Get("Range") != "" {
		serveRange(w, r, drv, episode, dobj, contentType)
		return
	}
	reader, size, err := drv.Open(r.Context(), dobj)
	if err != nil {
		log.Printf("[Stream] 无法打开音频文件流: path=%s, err=%v", episode.FilePath, err)
		http.Error(w, "无法打开文件: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, reader)
}

// serveRange 处理 HTTP Range 请求（支持拖动进度 / 断点续传）。
// 优先走驱动的 RangeReader 快路径（网盘直链透传 Range，只拉取所需字节），
// 不可用时回退为全量打开 + 本地 seek / 逐字节跳过。
func serveRange(w http.ResponseWriter, r *http.Request, drv driver.Driver, episode *Episode, dobj driver.Obj, contentType string) {
	raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Range"), "bytes="))

	// 快路径：驱动支持按范围拉流（网盘中转模式避免重复下载整个文件前缀）
	if rr, ok := drv.(driver.RangeReader); ok && episode.Size > 0 {
		if start, length, ok2 := parseRangeSpec(raw, episode.Size); ok2 && start < episode.Size {
			reader, _, err := rr.OpenRange(r.Context(), dobj, start, length)
			if err == nil {
				defer reader.Close()
				writePartialHeader(w, start, length, episode.Size, contentType)
				io.Copy(w, reader)
				return
			}
			log.Printf("[Stream] Range 快路径失败，降级全量中转: %v", err)
		}
	}

	// 慢路径：全量打开后本地 seek/跳过
	reader, size, err := drv.Open(r.Context(), dobj)
	if err != nil {
		log.Printf("[Stream] 无法打开音频文件流: path=%s, err=%v", episode.FilePath, err)
		http.Error(w, "无法打开文件: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	start, length, ok := parseRangeSpec(raw, size)
	if !ok {
		// Range 语法无法解析（或总长未知的后缀范围）→ 忽略 Range，返回 200 全量
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		io.Copy(w, reader)
		return
	}
	if start >= size {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}

	writePartialHeader(w, start, length, size, contentType)

	// 跳过 start 字节并拷贝 length 字节
	if rs, ok := reader.(io.ReadSeeker); ok {
		_, _ = rs.Seek(start, io.SeekStart)
		_, _ = io.CopyN(w, rs, length)
	} else {
		// 回退到逐字节跳过（非 seekable reader）
		_, _ = io.CopyN(io.Discard, reader, start)
		_, _ = io.CopyN(w, reader, length)
	}
}

// writePartialHeader 写出 206 Partial Content 响应头。
func writePartialHeader(w http.ResponseWriter, start, length, total int64, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, total))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusPartialContent)
}

// parseRangeSpec 解析单段 Range 规范，支持三种形式：
//   - "start-end"：闭区间
//   - "start-"：从 start 到文件末尾
//   - "-suffix"：文件末尾 suffix 字节（需要已知 total）
//
// 多段 Range（逗号分隔）仅取第一段。返回 ok=false 表示语法无效。
// start >= total 的不满足情况由调用方判断。
func parseRangeSpec(raw string, total int64) (start, length int64, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, false
	}
	if c := strings.Index(raw, ","); c >= 0 {
		raw = raw[:c]
	}
	di := strings.Index(raw, "-")
	if di < 0 {
		return 0, 0, false
	}
	left := strings.TrimSpace(raw[:di])
	right := strings.TrimSpace(raw[di+1:])

	if left == "" {
		// 后缀范围 bytes=-N
		if total <= 0 || right == "" {
			return 0, 0, false
		}
		n, err := strconv.ParseInt(right, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > total {
			n = total
		}
		return total - n, n, true
	}

	s, err := strconv.ParseInt(left, 10, 64)
	if err != nil || s < 0 {
		return 0, 0, false
	}
	if right == "" {
		// 开放范围 bytes=s-
		if total <= 0 {
			return 0, 0, false
		}
		if s >= total {
			return s, 0, true
		}
		return s, total - s, true
	}

	e, err := strconv.ParseInt(right, 10, 64)
	if err != nil || e < s {
		return 0, 0, false
	}
	if total > 0 && e >= total {
		e = total - 1
	}
	return s, e - s + 1, true
}

func fileObj(e *Episode) driver.Obj {
	return driver.Obj{
		Name:  e.Title,
		Path:  e.FilePath,
		Size:  e.Size,
		IsDir: false,
	}
}

func mimeByExt(p string) string {
	e := strings.ToLower(p)
	if i := strings.LastIndex(e, "."); i != -1 {
		e = e[i:]
	}
	// 图片类型（封面等）
	switch e {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	// 音频类型
	switch e {
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".m4b", ".aac":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".wma":
		return "audio/x-ms-wma"
	default:
		return "audio/mpeg"
	}
}