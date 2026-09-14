package service

import (
	"context"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"audiobook/internal/driver"
)

// ScanStatus 存储扫描进度状态
type ScanStatus struct {
	StorageID string `json:"storageId"`
	Scanning  bool   `json:"scanning"`
	Message   string `json:"message"`
	Current   int    `json:"current"` // 已扫描项/步骤
	Total     int    `json:"total"`   // 预计总项
	Books     int    `json:"books"`
	Episodes  int    `json:"episodes"`
	// ListFailures 列目录失败次数（认证过期 / 网盘限流 / 网络异常）。
	// 之前这些失败被静默吞掉，导致"扫描完成但入库 0 本"的误导性结果。
	ListFailures int    `json:"listFailures,omitempty"`
	Error        string `json:"error,omitempty"`
}

// scanCountingDriver 包装驱动以统计扫描期间的列目录失败次数，
// 让 hasDirectAudio / collectAudioFiles / findCover 等内部 List 调用的错误可被观测。
type scanCountingDriver struct {
	driver.Driver
	failures *int32
}

func (c *scanCountingDriver) List(ctx context.Context, path string) ([]driver.Obj, error) {
	objs, err := c.Driver.List(ctx, path)
	if err != nil {
		n := atomic.AddInt32(c.failures, 1)
		if n <= 10 {
			log.Printf("[Scan] 列目录失败(%d) %s: %v", n, path, err)
		}
	}
	return objs, err
}

// scanFailureWarning 根据失败次数生成追加到完成消息后的警告文案。
func scanFailureWarning(books, failures int) string {
	if failures <= 0 {
		return ""
	}
	if books == 0 {
		return fmt.Sprintf("（警告：%d 次目录读取失败，通常为认证过期或被网盘限流，请检查存储配置后重试）", failures)
	}
	return fmt.Sprintf("（%d 次目录读取失败已跳过）", failures)
}

var (
	scanStatusMu sync.RWMutex
	scanStatusMap = map[string]*ScanStatus{}
	scanLockMu sync.Mutex
	scanningStorages = map[string]bool{}
)

// StartScanStorageBackground 在后台协程启动扫描，防止前端切页或超时中断，并防止重复并发扫描
func StartScanStorageBackground(storageID string) error {
	scanLockMu.Lock()
	if scanningStorages[storageID] {
		scanLockMu.Unlock()
		return errStr("当前存储源正在扫描中，请勿重复发起")
	}
	scanningStorages[storageID] = true
	scanLockMu.Unlock()

	go func() {
		defer func() {
			scanLockMu.Lock()
			delete(scanningStorages, storageID)
			scanLockMu.Unlock()
		}()
		_, _, _ = ScanStorage(storageID)
	}()
	return nil
}

// GetScanStatus 获取存储当前扫描进度
func GetScanStatus(storageID string) ScanStatus {
	scanStatusMu.RLock()
	defer scanStatusMu.RUnlock()
	if s, ok := scanStatusMap[storageID]; ok {
		return *s
	}
	return ScanStatus{StorageID: storageID, Scanning: false}
}

func updateScanStatus(storageID string, fn func(s *ScanStatus)) {
	scanStatusMu.Lock()
	defer scanStatusMu.Unlock()
	s, ok := scanStatusMap[storageID]
	if !ok {
		s = &ScanStatus{StorageID: storageID}
		scanStatusMap[storageID] = s
	}
	fn(s)
}

// ScanStorage 扫描指定存储实例，构建有声书库。
func ScanStorage(storageID string) (int, int, error) {
	updateScanStatus(storageID, func(s *ScanStatus) {
		s.Scanning = true
		s.Message = "准备扫描驱动并清理旧数据..."
		s.Current = 0
		s.Total = 1
		s.Books = 0
		s.Episodes = 0
		s.Error = ""
	})

	drv := GetDriver(storageID)
	if drv == nil {
		err := errStr("存储驱动未就绪")
		updateScanStatus(storageID, func(s *ScanStatus) {
			s.Scanning = false
			s.Error = err.Error()
		})
		return 0, 0, err
	}
	// 用计数包装器包裹驱动，统计扫描期间的列目录失败（认证过期/限流等）
	var listFailures int32
	cdrv := &scanCountingDriver{Driver: drv, failures: &listFailures}

	// 从存储实例的 RootPath 开始扫描（默认根目录）
	root := "/"
	if si, ok := GetStorageInstance(storageID); ok && si.RootPath != "" {
		root = si.RootPath
	}

	// 清理旧书和分集（保留用户进度，分集 ID 稳定后进度自动关联）
	_ = DeleteBooksByStorageKeepProgress(storageID)

	bookCount, epCount := 0, 0
	saveBook := func(book *Book, episodes []Episode) {
		if book == nil || len(episodes) == 0 {
			return
		}
		if err := UpsertBook(book); err != nil {
			log.Printf("[Scan] 保存书籍失败: %v", err)
			return
		}
		bookCount++
		for i := range episodes {
			episodes[i].BookID = book.ID
			episodes[i].StorageID = storageID
			episodes[i].Order = i + 1
			if err := UpsertEpisode(&episodes[i]); err != nil {
				log.Printf("[Scan] 保存分集失败: %v", err)
			} else {
				epCount++
			}
		}
		updateScanStatus(storageID, func(s *ScanStatus) {
			s.Books = bookCount
			s.Episodes = epCount
			s.Message = "已刮削入库 《" + book.Title + "》 (" + formatNumber(len(episodes)) + " 集)"
		})
	}

	// 情况1：RootPath 本身就是一个书目录（其下直接有音频文件）。
	// 仅检查直接子项，避免把"书库根目录下多部有声书"误判为单本书。
	updateScanStatus(storageID, func(s *ScanStatus) {
		s.Message = "正在探测根目录是否为单本书..."
	})
	rootEntry := driver.Obj{Name: rootDisplayName(cdrv, root), Path: root, IsDir: true}
	if hasDirectAudio(cdrv, root) {
		updateScanStatus(storageID, func(s *ScanStatus) {
			s.Message = "将根目录作为单部有声书深度刮削..."
		})
		if book, episodes := scrapeBook(storageID, cdrv, rootEntry, ""); book != nil && len(episodes) > 0 {
			saveBook(book, episodes)
			_ = CleanupOrphanProgress()
			updateScanStatus(storageID, func(s *ScanStatus) {
				s.Scanning = false
				s.Current = 1
				s.Total = 1
				s.Books = bookCount
				s.Episodes = epCount
				s.ListFailures = int(atomic.LoadInt32(&listFailures))
				s.Message = "单本书扫描完成" + scanFailureWarning(bookCount, s.ListFailures)
			})
			log.Printf("[Scan] 存储 %s: 将 RootPath 作为单本书处理, 发现 %d 本书, %d 集", storageID, bookCount, epCount)
			return bookCount, epCount, nil
		}
	}

	// 情况2：遍历子目录。如果子目录直接含音频，则该子目录为一本书；
	// 如果子目录不直接含音频但包含更深层子目录（例如 作者/书名 结构），则自动向下展开一层，识别为真正的书！
	updateScanStatus(storageID, func(s *ScanStatus) {
		s.Message = "读取书库根目录列表..."
	})
	top, err := cdrv.List(context.Background(), root)
	if err != nil {
		updateScanStatus(storageID, func(s *ScanStatus) {
			s.Scanning = false
			s.Error = err.Error()
		})
		return 0, 0, err
	}

	var dirEntries []driver.Obj
	for _, e := range top {
		if e.IsDir {
			dirEntries = append(dirEntries, e)
		}
	}

	totalDirs := len(dirEntries)
	updateScanStatus(storageID, func(s *ScanStatus) {
		s.Total = totalDirs
		s.Current = 0
		s.Message = "共发现 " + formatNumber(totalDirs) + " 个目录，开始刮削..."
	})

	for idx, entry := range dirEntries {
		updateScanStatus(storageID, func(s *ScanStatus) {
			s.Current = idx + 1
			s.Total = totalDirs
			s.ListFailures = int(atomic.LoadInt32(&listFailures))
			s.Message = "正在扫描 [" + entry.Name + "]..."
		})

		// 检查该目录是否直接含音频
		if hasDirectAudio(cdrv, entry.Path) {
			log.Printf("[Scan] 存储 %s: 发现书籍目录 %q ...", storageID, entry.Name)
			book, episodes := scrapeBook(storageID, cdrv, entry, "")
			saveBook(book, episodes)
			if book != nil {
				log.Printf("[Scan] 存储 %s: 刮削完成 %q -> 书名=%q 作者=%q 共 %d 集", storageID, entry.Name, book.Title, book.Author, len(episodes))
			}
		} else {
			// 该子目录不直接含音频，检查其下一层子目录（处理形如 "作者/书名" 的层级）
			subEntries, err := cdrv.List(context.Background(), entry.Path)
			if err != nil {
				continue
			}
			hasSubBook := false
			for _, sub := range subEntries {
				if sub.IsDir && hasDirectAudio(cdrv, sub.Path) {
					hasSubBook = true
					log.Printf("[Scan] 存储 %s: 发现两级结构书籍目录 %q/%q ...", storageID, entry.Name, sub.Name)
					book, episodes := scrapeBook(storageID, cdrv, sub, entry.Name)
					saveBook(book, episodes)
					if book != nil {
						log.Printf("[Scan] 存储 %s: 刮削完成 %q -> 书名=%q 作者=%q 共 %d 集", storageID, sub.Name, book.Title, book.Author, len(episodes))
					}
				}
			}
			// 如果其下没有子目录含有直接音频，但深层存在音频，则按原有逻辑作为单书容错处理
			if !hasSubBook {
				book, episodes := scrapeBook(storageID, cdrv, entry, "")
				if book != nil && len(episodes) > 0 {
					saveBook(book, episodes)
					log.Printf("[Scan] 存储 %s: 容错刮削深层书籍 %q -> 书名=%q 作者=%q 共 %d 集", storageID, entry.Name, book.Title, book.Author, len(episodes))
				}
			}
		}
	}

	// 自动清理孤儿进度和空书籍壳
	_ = CleanupOrphanProgress()
	_, _ = CleanupEmptyBooks()

	finalFailures := int(atomic.LoadInt32(&listFailures))
	updateScanStatus(storageID, func(s *ScanStatus) {
		s.Scanning = false
		s.Current = totalDirs
		s.Total = totalDirs
		s.Books = bookCount
		s.Episodes = epCount
		s.ListFailures = finalFailures
	})
	// 格式化友好的完成提示文本（含读取失败警告）
	updateScanStatus(storageID, func(s *ScanStatus) {
		s.Message = formatScanSummary(bookCount, epCount) + scanFailureWarning(bookCount, finalFailures)
	})
	log.Printf("[Scan] 存储 %s: 扫描完成, 发现 %d 本书, %d 集, %d 次列目录失败", storageID, bookCount, epCount, finalFailures)
	return bookCount, epCount, nil
}

func formatScanSummary(books, episodes int) string {
	return "扫描刮削完成，共入库 " + formatNumber(books) + " 本书，" + formatNumber(episodes) + " 个音频集"
}

// BookProgressSummary 书库页聚合进度（一次查询返回全部书籍进度，避免前端 N+1 请求）。
type BookProgressSummary struct {
	BookID        string  `json:"bookId"`
	Total         int     `json:"total"`         // 总分集数
	Done          int     `json:"done"`          // 已听完分集数
	Started       int     `json:"started"`       // 已开始收听分集数
	LastEpisodeID string  `json:"lastEpisodeId"` // 最近收听的分集（断点续播）
	LastPosition  float64 `json:"lastPosition"`  // 最近收听位置（秒）
	// LastUpdated 最近收听时间（RFC3339），用于书库"继续收听/最近收听排序"
	LastUpdated string `json:"lastUpdated,omitempty"`
}

// GetLibrarySummary 汇总用户在全部书籍上的收听进度。
func GetLibrarySummary(userID string) map[string]BookProgressSummary {
	out := map[string]BookProgressSummary{}

	// 1) 每本书的总分集数
	if rows, err := db.Query(`SELECT book_id, COUNT(*) FROM episodes GROUP BY book_id`); err == nil {
		for rows.Next() {
			var id string
			var n int
			if rows.Scan(&id, &n) == nil {
				out[id] = BookProgressSummary{BookID: id, Total: n}
			}
		}
		rows.Close()
	}

	// 2) 用户已听完的分集数（按书分组）
	if rows, err := db.Query(`SELECT e.book_id, COUNT(*) FROM progress p JOIN episodes e ON p.episode_id=e.id
		WHERE p.user_id=? AND p.is_finished=1 GROUP BY e.book_id`, userID); err == nil {
		for rows.Next() {
			var id string
			var n int
			if rows.Scan(&id, &n) == nil {
				s := out[id]
				s.BookID = id
				s.Done = n
				out[id] = s
			}
		}
		rows.Close()
	}

	// 3) 用户已开始收听（position>0）的分集数
	if rows, err := db.Query(`SELECT e.book_id, COUNT(*) FROM progress p JOIN episodes e ON p.episode_id=e.id
		WHERE p.user_id=? AND p.position > 0 GROUP BY e.book_id`, userID); err == nil {
		for rows.Next() {
			var id string
			var n int
			if rows.Scan(&id, &n) == nil {
				s := out[id]
				s.BookID = id
				s.Started = n
				out[id] = s
			}
		}
		rows.Close()
	}

	// 4) 每本书最近一次收听的分集与位置（按 updated_at 倒序，取每本书首条）
	if rows, err := db.Query(`SELECT e.book_id, p.episode_id, p.position, p.updated_at FROM progress p JOIN episodes e ON p.episode_id=e.id
		WHERE p.user_id=? ORDER BY p.updated_at DESC`, userID); err == nil {
		seen := map[string]bool{}
		for rows.Next() {
			var bookID, epID, ua string
			var pos float64
			if rows.Scan(&bookID, &epID, &pos, &ua) == nil && !seen[bookID] {
				seen[bookID] = true
				s := out[bookID]
				s.BookID = bookID
				s.LastEpisodeID = epID
				s.LastPosition = pos
				s.LastUpdated = ua
				out[bookID] = s
			}
		}
		rows.Close()
	}

	return out
}

func formatNumber(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	t := n
	for t > 0 {
		digits = append([]byte{byte('0' + t%10)}, digits...)
		t /= 10
	}
	return string(digits)
}

// BrowseStorage 浏览存储目录，返回指定路径下的子目录和音频文件（用于配置书库根目录）。
func BrowseStorage(storageID, path string) ([]driver.Obj, error) {
	drv := GetDriver(storageID)
	if drv == nil {
		// 尝试主动初始化一次驱动
		if si, ok := GetStorageInstance(storageID); ok {
			if err := InitDriver(si); err == nil {
				drv = GetDriver(storageID)
			} else {
				return nil, fmt.Errorf("存储驱动未就绪且初始化失败: %w", err)
			}
		} else {
			return nil, errStr("未找到该存储实例配置")
		}
	}
	if drv == nil {
		return nil, errStr("存储驱动未就绪")
	}
	if path == "" {
		path = "/"
	}
	return drv.List(context.Background(), path)
}

// RescanBook 重新刮削单本书（根据 RootPath 重新扫描目录，重建分集）。
func RescanBook(bookID string) (*Book, int, error) {
	book, err := GetBook(bookID)
	if err != nil {
		return nil, 0, errStr("书籍不存在")
	}
	drv := GetDriver(book.StorageID)
	if drv == nil {
		return nil, 0, errStr("存储驱动未就绪")
	}
	var listFailures int32
	cdrv := &scanCountingDriver{Driver: drv, failures: &listFailures}

	rootEntry := driver.Obj{Name: book.Title, Path: book.RootPath, IsDir: true}
	_, episodes := scrapeBook(book.StorageID, cdrv, rootEntry, book.Author)
	if len(episodes) == 0 {
		if n := int(atomic.LoadInt32(&listFailures)); n > 0 {
			return nil, 0, fmt.Errorf("目录读取失败 %d 次（认证过期或被网盘限流），请检查存储配置后重试", n)
		}
		return nil, 0, errStr("未在目录中发现音频文件")
	}

	// 重建分集：先删除旧分集
	_, _ = db.Exec("DELETE FROM episodes WHERE book_id=?", bookID)

	// 保留手动编辑的元数据（标题/作者/封面/描述），仅重建分集
	for i := range episodes {
		episodes[i].BookID = bookID
		episodes[i].StorageID = book.StorageID
		episodes[i].Order = i + 1
		if err := UpsertEpisode(&episodes[i]); err != nil {
			log.Printf("[Rescan] 保存分集失败: %v", err)
		}
	}
	return book, len(episodes), nil
}

// UpdateBookMeta 更新书籍元数据（书名/作者/封面/描述）。
func UpdateBookMeta(bookID string, title, author, cover, description string) (*Book, error) {
	book, err := GetBook(bookID)
	if err != nil {
		return nil, errStr("书籍不存在")
	}
	if title != "" {
		book.Title = title
	}
	if author != "" {
		book.Author = author
	}
	if cover != "" {
		book.Cover = cover
	}
	if description != "" {
		book.Description = description
	}
	if err := UpsertBook(book); err != nil {
		return nil, err
	}
	return book, nil
}

// scrapeBook 从一个目录及其子目录中提取书信息和分集列表。
// parentAuthor 可传入上级目录作为默认作者（如 "作者/书名" 架构）。
func scrapeBook(storageID string, drv driver.Driver, rootEntry driver.Obj, parentAuthor string) (*Book, []Episode) {
	ctx := context.Background()

	// 递归收集所有音频文件
	audioFiles := collectAudioFiles(ctx, drv, rootEntry.Path)

	if len(audioFiles) == 0 {
		return nil, nil
	}

	// 按自然顺序排序
	sortAudioFiles(audioFiles)

	// 查找封面
	cover := findCover(ctx, drv, rootEntry)

	// 构建书的元数据
	rawName := rootEntry.Name
	title := rawName
	author := strings.TrimSpace(parentAuthor)

	// 尝试从目录名中智能拆分 "作者 - 书名" 或 "书名 - 作者" 或 "【作者】书名"
	if parts := strings.SplitN(rawName, " - ", 2); len(parts) == 2 {
		p0, p1 := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if strings.HasPrefix(p0, "作者") || strings.HasPrefix(p0, "演播") {
			author = p0
			title = p1
		} else if author == "" {
			author = p0
			title = p1
		}
	} else if strings.HasPrefix(rawName, "【") && strings.Contains(rawName, "】") {
		idx := strings.Index(rawName, "】")
		if author == "" {
			author = strings.Trim(rawName[1:idx], " ")
		}
		title = strings.TrimSpace(rawName[idx+len("】"):])
	}

	// 基于 storageID + rootEntry.Path 生成确定性稳定书籍 ID，杜绝重复创建
	bookID := stableID(storageID + ":book:" + rootEntry.Path)

	book := &Book{
		ID:          bookID,
		StorageID:   storageID,
		Title:       title,
		Author:      author,
		Cover:       cover,
		Description: "",
		RootPath:    rootEntry.Path,
	}

	// 构建分集
	episodes := make([]Episode, 0, len(audioFiles))
	for _, f := range audioFiles {
		epTitle := episodeTitle(f, rootEntry)
		episodes = append(episodes, Episode{
			Title:    epTitle,
			FilePath: f.Path,
			Size:     f.Size,
			Duration: f.Duration, // 驱动提供的音频时长（OneDrive 等可免费获得；未知为 0）
		})
	}

	return book, episodes
}

// collectAudioFiles 递归收集目录下的所有音频文件。
func collectAudioFiles(ctx context.Context, drv driver.Driver, dirPath string) []driver.Obj {
	var files []driver.Obj
	entries, err := drv.List(ctx, dirPath)
	if err != nil {
		return files
	}
	for _, e := range entries {
		if e.IsDir {
			files = append(files, collectAudioFiles(ctx, drv, e.Path)...)
		} else if isAudioFile(e.Name) {
			files = append(files, e)
		}
	}
	return files
}

// hasDirectAudio 判断目录的直接子项中是否存在音频文件（不递归）。
// 用于区分"单本书目录"（直接含音频）与"书库根目录"（子目录才是书）。
func hasDirectAudio(drv driver.Driver, dirPath string) bool {
	entries, err := drv.List(context.Background(), dirPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir && isAudioFile(e.Name) {
			return true
		}
	}
	return false
}

// rootDisplayName 获取书库根目录的展示名称。
// 对于移动云盘等以 ID 作为路径的驱动，尝试从父目录列表中解析出实际目录名。
func rootDisplayName(drv driver.Driver, root string) string {
	if root == "" || root == "/" {
		return "书库"
	}
	parent := path.Dir(strings.TrimRight(root, "/"))
	if parent == "." || parent == "/" {
		parent = "/"
	}
	if entries, err := drv.List(context.Background(), parent); err == nil {
		for _, e := range entries {
			if e.IsDir && e.Path == root {
				return e.Name
			}
		}
	}
	return path.Base(strings.TrimRight(root, "/"))
}

// sortAudioFiles 按文件名自然排序。
// 注意：对于移动云盘等以 ID 作为 Path 的驱动，Path 是文件 ID，无法反映真实顺序，
// 因此必须按 Name（文件名）排序，文件名通常包含集数（如 "0009.xxx.mp3"）。
func sortAudioFiles(files []driver.Obj) {
	sort.Slice(files, func(i, j int) bool {
		return naturalLess(files[i].Name, files[j].Name)
	})
}

// findCover 在目录下查找封面文件（优先匹配 cover/folder/front/poster 等规范命名）。
func findCover(ctx context.Context, drv driver.Driver, entry driver.Obj) string {
	entries, err := drv.List(ctx, entry.Path)
	if err != nil {
		return ""
	}
	var firstCandidate string
	for _, e := range entries {
		if !e.IsDir && isCoverFile(e.Name) {
			lower := strings.ToLower(e.Name)
			// 如果命名包含 cover / folder / front / poster / 封面，优先返回
			if strings.Contains(lower, "cover") || strings.Contains(lower, "folder") ||
				strings.Contains(lower, "front") || strings.Contains(lower, "poster") ||
				strings.Contains(lower, "封面") {
				return e.Path
			}
			if firstCandidate == "" {
				firstCandidate = e.Path
			}
		}
	}
	return firstCandidate
}

// episodeTitle 从文件路径生成分集标题。
func episodeTitle(f driver.Obj, bookEntry driver.Obj) string {
	rel := strings.TrimPrefix(f.Path, bookEntry.Path)
	rel = strings.Trim(rel, "/")
	name := strings.TrimSuffix(f.Name, path.Ext(f.Name))

	// 如果不在书根目录，则在标题前加子目录名
	dir := path.Dir(rel)
	if dir != "" && dir != "." {
		return dir + " / " + name
	}
	return name
}

// chineseNumToArab 将常见中文数字字符映射转成阿拉伯数字，便于自然排序比较
func chineseNumToArab(r rune) (int, bool) {
	switch r {
	case '零', '〇':
		return 0, true
	case '一':
		return 1, true
	case '二', '两':
		return 2, true
	case '三':
		return 3, true
	case '四':
		return 4, true
	case '五':
		return 5, true
	case '六':
		return 6, true
	case '七':
		return 7, true
	case '八':
		return 8, true
	case '九':
		return 9, true
	case '十':
		return 10, true
	case '百':
		return 100, true
	case '千':
		return 1000, true
	default:
		return 0, false
	}
}

// parseChineseNumber 尝试解析中文字符串开头的连续中文数字（如 "第二十一章" -> 21）
func parseChineseNumber(s []rune, start int) (int, int) {
	idx := start
	total := 0
	val := 0
	matched := false

	for idx < len(s) {
		r := s[idx]
		n, ok := chineseNumToArab(r)
		if !ok {
			break
		}
		matched = true
		if n >= 10 {
			if val == 0 {
				val = 1
			}
			total += val * n
			val = 0
		} else {
			val = n
		}
		idx++
	}
	total += val
	if !matched {
		return 0, start
	}
	return total, idx
}

// naturalLess 实现自然排序比较，支持常规数字与中文数字（如 "第2集" < "第10集", "第十章" < "第二十一章"）。
func naturalLess(a, b string) bool {
	ra := []rune(a)
	rb := []rune(b)
	ai, bi := 0, 0

	for ai < len(ra) && bi < len(rb) {
		ca, cb := ra[ai], rb[bi]
		// 1. 阿拉伯数字比较
		if ca >= '0' && ca <= '9' && cb >= '0' && cb <= '9' {
			anum, anext := readRuneDigits(ra, ai)
			bnum, bnext := readRuneDigits(rb, bi)
			if anum != bnum {
				return anum < bnum
			}
			ai, bi = anext, bnext
			continue
		}

		// 2. 中文数字比较（若两边都命中中文数字字符）
		if _, okA := chineseNumToArab(ca); okA {
			if _, okB := chineseNumToArab(cb); okB {
				cnumA, anext := parseChineseNumber(ra, ai)
				cnumB, bnext := parseChineseNumber(rb, bi)
				if cnumA != cnumB {
					return cnumA < cnumB
				}
				ai, bi = anext, bnext
				continue
			}
		}

		if ca != cb {
			return ca < cb
		}
		ai++
		bi++
	}
	return len(ra) < len(rb)
}

func readRuneDigits(s []rune, start int) (int, int) {
	i := start
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	num := 0
	for j := start; j < i; j++ {
		num = num*10 + int(s[j]-'0')
	}
	return num, i
}

func errStr(msg string) error {
	return &simpleError{msg: msg}
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }