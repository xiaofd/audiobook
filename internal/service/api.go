package service

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"audiobook/internal/driver"
	"audiobook/internal/drivers"
)

// RegisterHandlers 注册全部 /api/* 端点。
func RegisterHandlers(mux *http.ServeMux) {
	// 认证与偏好
	mux.HandleFunc("POST /api/auth/register", handleRegister)
	mux.HandleFunc("POST /api/auth/login", handleLogin)
	mux.HandleFunc("GET /api/auth/me", requireAuth(handleMe))
	mux.HandleFunc("GET /api/auth/config", handleAuthConfig) // 公开：注册开关等
	mux.HandleFunc("PUT /api/auth/password", requireAuth(handleChangePassword))
	mux.HandleFunc("GET /api/user/settings", requireAuth(handleGetUserSettings))
	mux.HandleFunc("PUT /api/user/settings", requireAuth(handleSaveUserSettings))

	// 书库
	mux.HandleFunc("GET /api/books", requireAuth(handleListBooks))
	mux.HandleFunc("GET /api/library/summary", requireAuth(handleLibrarySummary))
	mux.HandleFunc("GET /api/books/{bookId}", requireAuth(handleGetBook))
	mux.HandleFunc("GET /api/books/{bookId}/episodes", requireAuth(handleListEpisodes))
	mux.HandleFunc("GET /api/books/{bookId}/cover", requireAuth(handleBookCover))
	// 书籍元数据/封面/重刮为全局共享数据的写操作，仅管理员（M5：普通用户仅浏览/播放/读写自身进度）
	mux.HandleFunc("POST /api/books/{bookId}/cover", requireAdmin(handleUploadBookCover))
	mux.HandleFunc("PUT /api/books/{bookId}", requireAdmin(handleUpdateBook))
	mux.HandleFunc("POST /api/books/{bookId}/rescan", requireAdmin(handleRescanBook))

	// 播放与进度
	mux.HandleFunc("GET /api/episodes/{episodeId}/stream", requireAuth(handleStream))
	mux.HandleFunc("GET /api/progress", requireAuth(handleGetProgress))
	mux.HandleFunc("POST /api/progress", requireAuth(handleSaveProgress))
	mux.HandleFunc("GET /api/progress/export", requireAuth(handleExportProgress))
	mux.HandleFunc("POST /api/progress/import", requireAuth(handleImportProgress))

	// 存储实例（管理员权限）
	mux.HandleFunc("GET /api/storages", requireAdmin(handleListStorages))
	mux.HandleFunc("GET /api/storages/fields", requireAdmin(handleStorageFields))
	mux.HandleFunc("GET /api/storages/{storageId}", requireAdmin(handleGetStorage))
	mux.HandleFunc("GET /api/storages/{storageId}/browse", requireAdmin(handleBrowseStorage))
	mux.HandleFunc("POST /api/storages", requireAdmin(handleUpsertStorage))
	mux.HandleFunc("DELETE /api/storages/{storageId}", requireAdmin(handleDeleteStorage))
	mux.HandleFunc("POST /api/storages/{storageId}/scan", requireAdmin(handleScanStorage))
	mux.HandleFunc("GET /api/storages/{storageId}/scan/status", requireAdmin(handleScanStorageStatus))
	mux.HandleFunc("POST /api/storages/cleanup-empty", requireAdmin(handleCleanupEmptyBooks))

	// 用户管理（管理员）
	mux.HandleFunc("GET /api/admin/users", requireAdmin(handleAdminListUsers))
	mux.HandleFunc("POST /api/admin/users", requireAdmin(handleAdminCreateUser))
	mux.HandleFunc("DELETE /api/admin/users/{userId}", requireAdmin(handleAdminDeleteUser))
	mux.HandleFunc("POST /api/admin/users/{userId}/reset-password", requireAdmin(handleAdminResetPassword))
	mux.HandleFunc("GET /api/admin/settings", requireAdmin(handleGetAdminSettings))
	mux.HandleFunc("PUT /api/admin/settings", requireAdmin(handleSaveAdminSettings))

	// 健康检查
	mux.HandleFunc("GET /api/health", handleHealth)
}

// --- 响应辅助 ---

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decodeBody(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// --- 鉴权中间件 ---

type ctxKey int

const ctxUserKey ctxKey = 0

// requireAuth 校验令牌并将用户写入上下文。
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "未登录")
			return
		}
		claims, err := VerifyToken(token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, err.Error())
			return
		}
		user, err := GetUserByID(claims.Sub)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "用户不存在")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserKey, user)
		next(w, r.WithContext(ctx))
	}
}

// requireAdmin 校验管理员角色。
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(func(w http.ResponseWriter, r *http.Request) {
		user := currentUser(r)
		if user == nil || user.Role != RoleAdmin {
			writeErr(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next(w, r)
	})
}

// bearerToken 从请求中提取令牌。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	// 也允许通过 cookie 传递（Web 前端使用）
	if c, err := r.Cookie("token"); err == nil {
		return c.Value
	}
	// 音频/图片等无法自定义 header 的资源，通过 query 参数传递 token
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	return ""
}

// --- 登录限流（防止公网部署下的密码暴力破解） ---

const (
	loginMaxFailCount  = 5                // 连续失败次数上限
	loginLockDuration  = 5 * time.Minute  // 锁定时长
	loginLimiterPrune  = 1024             // 超过该条目数时触发惰性清理
)

type loginFailInfo struct {
	count    int
	lockedTo time.Time
}

var loginLimiter = struct {
	sync.Mutex
	fails map[string]loginFailInfo
}{fails: make(map[string]loginFailInfo)}

func loginAllowed(key string) bool {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	info, ok := loginLimiter.fails[key]
	if !ok {
		return true
	}
	if time.Now().Before(info.lockedTo) {
		return false
	}
	// 仅在"锁定已过期"时清理条目重新计数；
	// 未锁定（计数累积中）不能删除，否则失败次数永远无法累计到阈值。
	if !info.lockedTo.IsZero() {
		delete(loginLimiter.fails, key)
	}
	return true
}

func recordLoginFail(key string) {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	if len(loginLimiter.fails) >= loginLimiterPrune {
		now := time.Now()
		for k, v := range loginLimiter.fails {
			if now.After(v.lockedTo) {
				delete(loginLimiter.fails, k)
			}
		}
	}
	info := loginLimiter.fails[key]
	info.count++
	if info.count >= loginMaxFailCount {
		info.lockedTo = time.Now().Add(loginLockDuration)
		info.count = 0
	}
	loginLimiter.fails[key] = info
}

func clearLoginFails(key string) {
	loginLimiter.Lock()
	defer loginLimiter.Unlock()
	delete(loginLimiter.fails, key)
}

// clientIP 提取客户端 IP。
// 仅当部署在可信反向代理之后（AUDIOBOOK_TRUST_PROXY=1）才解析 X-Forwarded-For——
// 直连部署下无条件信任 XFF 会被攻击者每请求伪造一个新 XFF 绕过登录限流。
var trustProxy = func() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AUDIOBOOK_TRUST_PROXY"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}()

func clientIP(r *http.Request) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- 认证端点 ---

func handleRegister(w http.ResponseWriter, r *http.Request) {
	// 注册开关（默认关闭，管理员可在管理页开启）
	if !GetConfig().AllowRegistration {
		writeErr(w, http.StatusForbidden, "注册功能已关闭，请联系管理员创建账户")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if req.Username == "" || len(req.Password) < 6 {
		writeErr(w, http.StatusBadRequest, "用户名不能为空且密码至少 6 位")
		return
	}
	if _, err := GetUserByUsername(req.Username); err == nil {
		writeErr(w, http.StatusConflict, "用户名已存在")
		return
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "密码处理失败")
		return
	}
	u := &User{Username: req.Username, PasswordHash: hash, Role: RoleUser}
	if err := CreateUser(u); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	token, _ := IssueToken(u.ID, u.Role, 7*24*time.Hour)
	writeJSON(w, http.StatusOK, map[string]interface{}{"token": token, "user": u})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	// 登录限流：同一 用户名+IP 连续失败 5 次锁定 5 分钟
	rateKey := strings.ToLower(req.Username) + "|" + clientIP(r)
	if !loginAllowed(rateKey) {
		writeErr(w, http.StatusTooManyRequests, "登录尝试过于频繁，请 5 分钟后再试")
		return
	}
	u, err := Authenticate(req.Username, req.Password)
	if err != nil {
		recordLoginFail(rateKey)
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	clearLoginFails(rateKey)
	token, _ := IssueToken(u.ID, u.Role, 7*24*time.Hour)
	writeJSON(w, http.StatusOK, map[string]interface{}{"token": token, "user": u})
}

// handleAuthConfig 返回公开的认证配置（登录页据此决定是否显示注册入口）。
func handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"allowRegistration": GetConfig().AllowRegistration})
}

// handleChangePassword 当前用户校验原密码后修改密码。
func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if len(req.NewPassword) < 6 {
		writeErr(w, http.StatusBadRequest, "新密码至少 6 位")
		return
	}
	if err := ChangePassword(user.ID, req.OldPassword, req.NewPassword); err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}

func handleGetUserSettings(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	settings, err := GetUserSettings(user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func handleSaveUserSettings(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	// 用指针区分"未提供"与"false"（如只传 playbackRate 时不覆盖 autoNext）
	var req struct {
		PlaybackRate *float64 `json:"playbackRate"`
		AutoNext     *bool    `json:"autoNext"`
		Theme        *string  `json:"theme"`
		SkipForward  *int     `json:"skipForward"`
		SkipBackward *int     `json:"skipBackward"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "参数错误")
		return
	}
	// 以现有设置为基底，仅覆盖请求中显式提供的字段
	final := UserSettings{UserID: user.ID}
	if existing, err := GetUserSettings(user.ID); err == nil {
		final = *existing
	}
	if req.PlaybackRate != nil && *req.PlaybackRate > 0 {
		final.PlaybackRate = *req.PlaybackRate
	}
	if req.AutoNext != nil {
		final.AutoNext = *req.AutoNext
	}
	if req.Theme != nil && *req.Theme != "" {
		final.Theme = *req.Theme
	}
	if req.SkipForward != nil && *req.SkipForward > 0 {
		final.SkipForward = *req.SkipForward
	}
	if req.SkipBackward != nil && *req.SkipBackward > 0 {
		final.SkipBackward = *req.SkipBackward
	}
	if err := SaveUserSettings(&final); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, final)
}

// --- 辅助 ---

func enrichBook(b *Book) *Book {
	if si, ok := GetStorageInstance(b.StorageID); ok {
		b.StorageName = si.Name
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM episodes WHERE book_id=?", b.ID).Scan(&count)
	b.EpisodeCount = count
	return b
}

// enrichBooks 批量补充存储名与分集数。
// 分集数用单次 GROUP BY 查询（替代逐书 COUNT 的 N+1 模式，书库大时差异显著）。
func enrichBooks(books []Book) []Book {
	if len(books) == 0 {
		return books
	}
	// 1) 存储名
	storageNames := map[string]string{}
	for _, si := range ListStorageInstances() {
		storageNames[si.ID] = si.Name
	}
	// 2) 分集数：一次查询全部
	counts := map[string]int{}
	if rows, err := db.Query("SELECT book_id, COUNT(*) FROM episodes GROUP BY book_id"); err == nil {
		for rows.Next() {
			var id string
			var n int
			if rows.Scan(&id, &n) == nil {
				counts[id] = n
			}
		}
		rows.Close()
	}
	for i := range books {
		if name, ok := storageNames[books[i].StorageID]; ok {
			books[i].StorageName = name
		}
		books[i].EpisodeCount = counts[books[i].ID]
	}
	return books
}

// --- 书库端点 ---

func handleListBooks(w http.ResponseWriter, r *http.Request) {
	books, err := ListAllBooks()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if books == nil {
		books = []Book{}
	}
	enrichBooks(books)
	writeJSON(w, http.StatusOK, books)
}

// handleLibrarySummary 返回用户在全部书籍上的聚合进度（书库页一次拉取，避免 N+1）。
func handleLibrarySummary(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	writeJSON(w, http.StatusOK, GetLibrarySummary(user.ID))
}

func handleGetBook(w http.ResponseWriter, r *http.Request) {
	book, err := GetBook(r.PathValue("bookId"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "书籍不存在")
		return
	}
	writeJSON(w, http.StatusOK, enrichBook(book))
}

func handleListEpisodes(w http.ResponseWriter, r *http.Request) {
	eps, err := ListEpisodesByBook(r.PathValue("bookId"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if eps == nil {
		eps = []Episode{}
	}
	writeJSON(w, http.StatusOK, eps)
}

func handleBookCover(w http.ResponseWriter, r *http.Request) {
	book, err := GetBook(r.PathValue("bookId"))
	if err != nil || book.Cover == "" {
		writeErr(w, http.StatusNotFound, "无封面")
		return
	}
	drv := GetDriver(book.StorageID)
	if drv == nil {
		if si, ok := GetStorageInstance(book.StorageID); ok {
			_ = InitDriver(si)
			drv = GetDriver(book.StorageID)
		}
	}
	if drv == nil {
		log.Printf("[Cover] 存储驱动未就绪: storageID=%s", book.StorageID)
		writeErr(w, http.StatusServiceUnavailable, "存储驱动未就绪")
		return
	}
	reader, size, err := drv.Open(r.Context(), driver.Obj{Name: "cover", Path: book.Cover, Size: 0, IsDir: false})
	if err != nil {
		log.Printf("[Cover] 读取封面失败 cover=%s: %v", book.Cover, err)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", mimeByExt(book.Cover))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	io.Copy(w, reader)
}

func handleUploadBookCover(w http.ResponseWriter, r *http.Request) {
	bookID := r.PathValue("bookId")
	book, err := GetBook(bookID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "书籍不存在")
		return
	}
	drv := GetDriver(book.StorageID)
	if drv == nil {
		writeErr(w, http.StatusServiceUnavailable, "存储驱动未就绪")
		return
	}

	// 限制上传大小最大 10MB
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "文件过大或表单解析失败")
		return
	}
	file, header, err := r.FormFile("cover")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "请选择封面图片文件(字段名: cover)")
		return
	}
	defer file.Close()

	ext := strings.ToLower(path.Ext(header.Filename))
	if ext == "" || !isCoverFile(ext) {
		ext = ".jpg"
	}
	coverFileName := "cover" + ext

	writer, ok := drv.(driver.Writer)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "当前网盘/存储暂不支持直接写入文件")
		return
	}

	targetDir := book.RootPath
	if targetDir == "" {
		targetDir = "/"
	}
	obj, err := writer.Put(r.Context(), targetDir, coverFileName, file, header.Size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "保存封面失败: "+err.Error())
		return
	}

	book.Cover = obj.Path
	if err := UpsertBook(book); err != nil {
		writeErr(w, http.StatusInternalServerError, "更新书籍封面元数据失败: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "封面上传并保存成功",
		"book":    enrichBook(book),
		"cover":   book.Cover,
	})
}

func handleUpdateBook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Author      string `json:"author"`
		Cover       string `json:"cover"`
		Description string `json:"description"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	book, err := UpdateBookMeta(r.PathValue("bookId"), req.Title, req.Author, req.Cover, req.Description)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, enrichBook(book))
}

func handleRescanBook(w http.ResponseWriter, r *http.Request) {
	book, epCount, err := RescanBook(r.PathValue("bookId"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"book": enrichBook(book), "episodes": epCount,
	})
}

func handleStorageFields(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	if typ == "" { typ = "local" }
	d, err := drivers.New(typ, nil)
	if err != nil { writeErr(w, http.StatusBadRequest, "未知存储类型: "+typ); return }
	writeJSON(w, http.StatusOK, d.Config().Fields)
}

func handleGetStorage(w http.ResponseWriter, r *http.Request) {
	si, ok := GetStorageInstance(r.PathValue("storageId"))
	if !ok { writeErr(w, http.StatusNotFound, "存储不存在"); return }
	// 返回详情，敏感字段用占位符表示已配置（不泄露明文）
	safeConfig := make(map[string]string, len(si.Config))
	for k, v := range si.Config {
		if isSecretKey(k) && v != "" {
			safeConfig[k] = "******"
		} else {
			safeConfig[k] = v
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id": si.ID, "name": si.Name, "type": si.Type,
		"enabled": si.Enabled, "streamMode": si.StreamMode,
		"rootPath": si.RootPath, "config": safeConfig,
	})
}

func handleBrowseStorage(w http.ResponseWriter, r *http.Request) {
	storageID := r.PathValue("storageId")
	path := r.URL.Query().Get("path")
	objs, err := BrowseStorage(storageID, path)
	if err != nil {
		log.Printf("[API] 浏览存储目录失败 storage=%s path=%s: %v", storageID, path, err)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if objs == nil {
		objs = []driver.Obj{}
	}
	writeJSON(w, http.StatusOK, objs)
}

// --- 播放与进度端点 ---

func handleStream(w http.ResponseWriter, r *http.Request) {
	episodeID := r.PathValue("episodeId")
	ep, err := GetEpisode(episodeID)
	if err != nil {
		log.Printf("[Stream] 未找到分集: %s (%v)", episodeID, err)
		writeErr(w, http.StatusNotFound, "分集不存在")
		return
	}
	ServeStream(w, r, ep)
}

func handleGetProgress(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	episodeID := r.URL.Query().Get("episodeId")
	if episodeID != "" {
		p := GetEpisodeProgress(user.ID, episodeID)
		if p == nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"position": 0, "duration": 0})
			return
		}
		writeJSON(w, http.StatusOK, p)
		return
	}
	bookID := r.URL.Query().Get("bookId")
	if bookID != "" {
		list, _ := ListProgressesByBook(user.ID, bookID)
		if list == nil {
			list = []Progress{}
		}
		writeJSON(w, http.StatusOK, list)
		return
	}
	list, _ := ListProgressesByUser(user.ID)
	if list == nil {
		list = []Progress{}
	}
	writeJSON(w, http.StatusOK, list)
}

func handleSaveProgress(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var req struct {
		EpisodeID string  `json:"episodeId"`
		Position  float64 `json:"position"`
		Duration  float64 `json:"duration"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if req.EpisodeID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 episodeId")
		return
	}
	if err := SaveProgressForUser(user.ID, req.EpisodeID, req.Position, req.Duration); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleExportProgress(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	list, err := ExportProgressData(user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=progress.json")
	writeJSON(w, http.StatusOK, list)
}

func handleImportProgress(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var list []ProgressExportItem
	if err := decodeBody(r, &list); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	for i := range list {
		list[i].UserID = user.ID
	}
	imported, skipped, err := ImportProgressData(list)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"imported": imported, "skipped": skipped})
}

// --- 存储实例端点 ---

func handleListStorages(w http.ResponseWriter, r *http.Request) {
	list := ListStorageInstances()
	// 去除敏感配置，避免泄露，同时附带驱动运行状态
	safe := make([]map[string]interface{}, 0, len(list))
	for _, si := range list {
		status, lastErr := GetDriverStatus(si.ID)
		safe = append(safe, map[string]interface{}{
			"id":           si.ID,
			"name":         si.Name,
			"type":         si.Type,
			"enabled":      si.Enabled,
			"streamMode":   si.StreamMode,
			"rootPath":     si.RootPath,
			"driverStatus": status,
			"driverError":  lastErr,
		})
	}
	writeJSON(w, http.StatusOK, safe)
}

func handleUpsertStorage(w http.ResponseWriter, r *http.Request) {
	var si StorageInstance
	if err := decodeBody(r, &si); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if si.ID == "" {
		si.ID = newID()
	}
	if si.StreamMode == "" {
		si.StreamMode = StreamAuto
	}
	// 编辑已有存储时，敏感字段为占位符 "******" 表示未修改，保留原值
	if existing, ok := GetStorageInstance(si.ID); ok {
		for k, v := range existing.Config {
			if isSecretKey(k) && si.Config[k] == "******" {
				si.Config[k] = v
			}
		}
	}
	// 验证驱动可用性
	if err := InitDriver(si); err != nil {
		writeErr(w, http.StatusBadRequest, "驱动初始化失败: "+err.Error())
		return
	}
	if err := UpsertStorageInstance(si); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "id": si.ID})
}

func handleDeleteStorage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("storageId")
	DropDriver(id)
	_ = DeleteStorageInstance(id)
	_ = DeleteBooksByStorage(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleScanStorage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("storageId")
	// 确保驱动已就绪
	if si, ok := GetStorageInstance(id); ok && GetDriver(id) == nil {
		if err := InitDriver(si); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// 启动后台异步扫描协程，避免前端超时或切页面中断
	if err := StartScanStorageBackground(id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "扫描任务已在后台启动，可通过进度端点实时轮询",
	})
}

func handleScanStorageStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("storageId")
	writeJSON(w, http.StatusOK, GetScanStatus(id))
}

func handleCleanupEmptyBooks(w http.ResponseWriter, r *http.Request) {
	n, err := CleanupEmptyBooks()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"deleted": n,
		"message": "已清理空书籍记录",
	})
}

// --- 用户管理端点（管理员） ---

func handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := ListUsers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if req.Username == "" || len(req.Password) < 6 {
		writeErr(w, http.StatusBadRequest, "用户名不能为空且密码至少 6 位")
		return
	}
	if req.Role != RoleAdmin && req.Role != RoleUser {
		req.Role = RoleUser
	}
	if _, err := GetUserByUsername(req.Username); err == nil {
		writeErr(w, http.StatusConflict, "用户名已存在")
		return
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "密码处理失败")
		return
	}
	u := &User{Username: req.Username, PasswordHash: hash, Role: req.Role}
	if err := CreateUser(u); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("userId")
	if id == currentUser(r).ID {
		writeErr(w, http.StatusBadRequest, "不能删除自己")
		return
	}
	_ = DeleteUser(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminResetPassword 管理员重置指定用户的密码。
func handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userId")
	if userID == "" {
		writeErr(w, http.StatusBadRequest, "缺少用户 ID")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if len(req.Password) < 6 {
		writeErr(w, http.StatusBadRequest, "新密码至少 6 位")
		return
	}
	if err := AdminResetPassword(userID, req.Password); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleGetAdminSettings 返回管理端系统设置。
func handleGetAdminSettings(w http.ResponseWriter, r *http.Request) {
	c := GetConfig()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"allowRegistration": c.AllowRegistration,
	})
}

// handleSaveAdminSettings 保存管理端系统设置。
func handleSaveAdminSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AllowRegistration *bool `json:"allowRegistration"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if req.AllowRegistration != nil {
		if err := SetAllowRegistration(*req.AllowRegistration); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- 上下文辅助 ---

func currentUser(r *http.Request) *User {
	if u, ok := r.Context().Value(ctxUserKey).(*User); ok {
		return u
	}
	return nil
}
