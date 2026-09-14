package service

// SaveProgressForUser 保存用户的单集播放进度。
func SaveProgressForUser(userID, episodeID string, position, duration float64) error {
	existing, err := GetProgress(userID, episodeID)
	p := &Progress{
		UserID:    userID,
		EpisodeID: episodeID,
		Position:  position,
		Duration:  duration,
	}
	if err == nil && existing != nil {
		p.ID = existing.ID
	}
	return SaveProgress(p)
}

// GetEpisodeProgress 获取用户对某集的进度。
func GetEpisodeProgress(userID, episodeID string) *Progress {
	p, err := GetProgress(userID, episodeID)
	if err != nil {
		return nil
	}
	return p
}

// GetBookProgressSummary 汇总用户对某本书的进度（已听集数/总集数）。
func GetBookProgressSummary(userID, bookID string) (int, int) {
	eps, err := ListEpisodesByBook(bookID)
	if err != nil {
		return 0, 0
	}
	progs, err := ListProgressesByBook(userID, bookID)
	if err != nil {
		return 0, len(eps)
	}
	done := 0
	for _, p := range progs {
		if p.IsFinished {
			done++
		}
	}
	return done, len(eps)
}

// ProgressExportItem 导出条目：附带书籍/分集定位信息。
// 分集 ID 含 storageID（md5(storageID:filePath)），换部署后必然变化，
// 仅靠 ID 无法跨实例迁移——导入时先按 ID 精确匹配，miss 后按 (书名+文件路径) 回退匹配。
// 嵌入 Progress 保证旧版导出文件（纯 Progress 数组）仍可解析（定位字段为零值）。
type ProgressExportItem struct {
	Progress
	BookTitle string `json:"bookTitle"`
	EpTitle   string `json:"epTitle"`
	FilePath  string `json:"filePath"`
}

// ExportProgressData 导出用户全部进度（含定位信息）。
func ExportProgressData(userID string) ([]ProgressExportItem, error) {
	rows, err := db.Query(`SELECT p.id, p.user_id, p.episode_id, p.position, p.duration, p.is_finished, p.updated_at,
		COALESCE(b.title, ''), COALESCE(e.title, ''), COALESCE(e.file_path, '')
		FROM progress p
		LEFT JOIN episodes e ON p.episode_id = e.id
		LEFT JOIN books b ON e.book_id = b.id
		WHERE p.user_id=? ORDER BY p.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []ProgressExportItem
	for rows.Next() {
		var it ProgressExportItem
		var ua string
		var fin int
		if err := rows.Scan(&it.ID, &it.UserID, &it.EpisodeID, &it.Position, &it.Duration, &fin, &ua,
			&it.BookTitle, &it.EpTitle, &it.FilePath); err != nil {
			continue
		}
		it.IsFinished = fin == 1
		list = append(list, it)
	}
	return list, nil
}

// ImportProgressData 导入进度：
//   - position/duration 负值归零（导入内容不可信）
//   - 先按分集 ID 精确匹配；miss 且携带 filePath 时按 (书名+文件路径) 回退匹配
//   - 仍无法定位的条目跳过（不产生孤儿记录）
//
// 返回（成功数, 跳过数）。
func ImportProgressData(items []ProgressExportItem) (int, int, error) {
	imported, skipped := 0, 0
	for _, it := range items {
		if it.Position < 0 {
			it.Position = 0
		}
		if it.Duration < 0 {
			it.Duration = 0
		}
		epID := it.EpisodeID
		if _, err := GetEpisode(epID); err != nil {
			epID = "" // 需要回退匹配
		}
		if epID == "" && it.FilePath != "" {
			var id string
			q := `SELECT e.id FROM episodes e LEFT JOIN books b ON e.book_id = b.id WHERE e.file_path = ?`
			args := []interface{}{it.FilePath}
			if it.BookTitle != "" {
				q += ` AND b.title = ?`
				args = append(args, it.BookTitle)
			}
			q += ` LIMIT 1`
			if err := db.QueryRow(q, args...).Scan(&id); err == nil {
				epID = id
			}
		}
		if epID == "" {
			skipped++
			continue
		}
		if err := SaveProgressForUser(it.UserID, epID, it.Position, it.Duration); err != nil {
			skipped++
			continue
		}
		imported++
	}
	return imported, skipped, nil
}
