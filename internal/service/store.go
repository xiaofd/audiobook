package service

import (
	"database/sql"
	"log"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

// InitStore 初始化 SQLite 数据库并执行迁移。
func InitStore(dataDir string) {
	dbPath := filepath.Join(dataDir, "data.db")
	var err error
	db, err = sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("[Store] 打开数据库失败: %v", err)
	}
	db.SetMaxOpenConns(1) // SQLite 单写模式

	migrate()
	log.Printf("[Store] SQLite 初始化完成: %s", dbPath)
}

func migrate() {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS books (
			id TEXT PRIMARY KEY,
			storage_id TEXT NOT NULL,
			title TEXT NOT NULL,
			author TEXT DEFAULT '',
			cover TEXT DEFAULT '',
			description TEXT DEFAULT '',
			root_path TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS episodes (
			id TEXT PRIMARY KEY,
			book_id TEXT NOT NULL,
			storage_id TEXT NOT NULL,
			title TEXT NOT NULL,
			file_path TEXT NOT NULL,
			size INTEGER DEFAULT 0,
			ord INTEGER DEFAULT 0,
			duration INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS progress (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			episode_id TEXT NOT NULL,
			position REAL DEFAULT 0,
			duration REAL DEFAULT 0,
			is_finished INTEGER DEFAULT 0,
			updated_at TEXT NOT NULL,
			UNIQUE(user_id, episode_id)
		)`,
		`CREATE TABLE IF NOT EXISTS user_settings (
			user_id TEXT PRIMARY KEY,
			playback_rate REAL DEFAULT 1.0,
			auto_next INTEGER DEFAULT 1,
			theme TEXT DEFAULT 'dark',
			skip_forward INTEGER DEFAULT 15,
			skip_backward INTEGER DEFAULT 15
		)`,
	}
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			log.Printf("[Store] 迁移警告: %v", err)
		}
	}
	// 兼容旧表：为 progress 增加 is_finished 列（若不存在）
	if _, err := db.Exec(`ALTER TABLE progress ADD COLUMN is_finished INTEGER DEFAULT 0`); err != nil {
		// 列已存在则忽略
	}
}

// --- User Settings CRUD ---

func GetUserSettings(userID string) (*UserSettings, error) {
	row := db.QueryRow("SELECT user_id, playback_rate, auto_next, theme, skip_forward, skip_backward FROM user_settings WHERE user_id=?", userID)
	s := &UserSettings{
		UserID:       userID,
		PlaybackRate: 1.0,
		AutoNext:     true,
		Theme:        "dark",
		SkipForward:  15,
		SkipBackward: 15,
	}
	var autoNextInt int
	err := row.Scan(&s.UserID, &s.PlaybackRate, &autoNextInt, &s.Theme, &s.SkipForward, &s.SkipBackward)
	if err == sql.ErrNoRows {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	s.AutoNext = autoNextInt == 1
	if s.PlaybackRate <= 0 {
		s.PlaybackRate = 1.0
	}
	return s, nil
}

func SaveUserSettings(s *UserSettings) error {
	autoNextInt := 0
	if s.AutoNext {
		autoNextInt = 1
	}
	if s.PlaybackRate <= 0 {
		s.PlaybackRate = 1.0
	}
	if s.SkipForward <= 0 {
		s.SkipForward = 15
	}
	if s.SkipBackward <= 0 {
		s.SkipBackward = 15
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO user_settings(user_id, playback_rate, auto_next, theme, skip_forward, skip_backward)
		VALUES(?,?,?,?,?,?)`,
		s.UserID, s.PlaybackRate, autoNextInt, s.Theme, s.SkipForward, s.SkipBackward)
	return err
}

// --- User CRUD ---

func CreateUser(u *User) error {
	if u.ID == "" {
		u.ID = newID()
	}
	u.CreatedAt = time.Now()
	_, err := db.Exec(
		"INSERT INTO users(id, username, password_hash, role, created_at) VALUES(?,?,?,?,?)",
		u.ID, u.Username, u.PasswordHash, u.Role, u.CreatedAt.Format(time.RFC3339))
	return err
}

func GetUserByUsername(username string) (*User, error) {
	row := db.QueryRow("SELECT id, username, password_hash, role, created_at FROM users WHERE username=?", username)
	return scanUser(row)
}

func GetUserByID(id string) (*User, error) {
	row := db.QueryRow("SELECT id, username, password_hash, role, created_at FROM users WHERE id=?", id)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*User, error) {
	u := &User{}
	var ca string
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &ca)
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	return u, nil
}

func ListUsers() ([]User, error) {
	rows, err := db.Query("SELECT id, username, password_hash, role, created_at FROM users")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		u := User{}
		var ca string
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &ca); err != nil {
			continue
		}
		u.CreatedAt, _ = time.Parse(time.RFC3339, ca)
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].CreatedAt.Before(users[j].CreatedAt) })
	return users, nil
}

func DeleteUser(id string) error {
	// 级联清理该用户的进度与偏好（隐私：删除账户应连带删除其数据）
	_, _ = db.Exec("DELETE FROM progress WHERE user_id=?", id)
	_, _ = db.Exec("DELETE FROM user_settings WHERE user_id=?", id)
	_, err := db.Exec("DELETE FROM users WHERE id=?", id)
	return err
}

func UpdateUser(u *User) error {
	_, err := db.Exec("UPDATE users SET username=?, password_hash=?, role=? WHERE id=?",
		u.Username, u.PasswordHash, u.Role, u.ID)
	return err
}

// --- Book CRUD ---

func UpsertBook(b *Book) error {
	if b.ID == "" {
		b.ID = newID()
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO books(id, storage_id, title, author, cover, description, root_path)
		VALUES(?,?,?,?,?,?,?)`,
		b.ID, b.StorageID, b.Title, b.Author, b.Cover, b.Description, b.RootPath)
	return err
}

func ListBooksByStorage(storageID string) ([]Book, error) {
	rows, err := db.Query("SELECT id, storage_id, title, author, cover, description, root_path FROM books WHERE storage_id=? ORDER BY title", storageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBooks(rows)
}

func ListAllBooks() ([]Book, error) {
	rows, err := db.Query("SELECT id, storage_id, title, author, cover, description, root_path FROM books ORDER BY title")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBooks(rows)
}

func GetBook(id string) (*Book, error) {
	row := db.QueryRow("SELECT id, storage_id, title, author, cover, description, root_path FROM books WHERE id=?", id)
	b := &Book{}
	err := row.Scan(&b.ID, &b.StorageID, &b.Title, &b.Author, &b.Cover, &b.Description, &b.RootPath)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func DeleteBook(id string) error {
	// 删除关联分集和进度
	_, _ = db.Exec("DELETE FROM progress WHERE episode_id IN (SELECT id FROM episodes WHERE book_id=?)", id)
	_, _ = db.Exec("DELETE FROM episodes WHERE book_id=?", id)
	_, err := db.Exec("DELETE FROM books WHERE id=?", id)
	return err
}

func DeleteBooksByStorage(storageID string) error {
	_, _ = db.Exec("DELETE FROM progress WHERE episode_id IN (SELECT id FROM episodes WHERE book_id IN (SELECT id FROM books WHERE storage_id=?))", storageID)
	_, _ = db.Exec("DELETE FROM episodes WHERE book_id IN (SELECT id FROM books WHERE storage_id=?)", storageID)
	_, err := db.Exec("DELETE FROM books WHERE storage_id=?", storageID)
	return err
}

// DeleteBooksByStorageKeepProgress 删除存储下的书和分集，但保留用户进度。
// 用于重新扫描时保留进度（分集 ID 基于文件路径稳定生成，进度可自动关联）。
func DeleteBooksByStorageKeepProgress(storageID string) error {
	_, _ = db.Exec("DELETE FROM episodes WHERE book_id IN (SELECT id FROM books WHERE storage_id=?)", storageID)
	_, err := db.Exec("DELETE FROM books WHERE storage_id=?", storageID)
	return err
}

// CleanupOrphanProgress 删除关联不到有效分集的孤儿进度记录。
// 用于清理历史遗留（旧随机分集 ID 产生的进度，重新扫描后无法关联）。
func CleanupOrphanProgress() error {
	_, err := db.Exec(`DELETE FROM progress WHERE episode_id NOT IN (SELECT id FROM episodes)`)
	return err
}

func scanBooks(rows *sql.Rows) ([]Book, error) {
	var books []Book
	for rows.Next() {
		b := Book{}
		if err := rows.Scan(&b.ID, &b.StorageID, &b.Title, &b.Author, &b.Cover, &b.Description, &b.RootPath); err != nil {
			continue
		}
		books = append(books, b)
	}
	return books, nil
}

// CleanupEmptyBooks 删除没有任何分集的空书籍（排除脏数据或扫描异常创建的空书壳）。
func CleanupEmptyBooks() (int64, error) {
	res, err := db.Exec(`DELETE FROM books WHERE id NOT IN (SELECT DISTINCT book_id FROM episodes)`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- Episode CRUD ---

func UpsertEpisode(e *Episode) error {
	if e.ID == "" {
		// 基于 storageID + filePath 生成稳定 ID，保证重新扫描时同一文件的进度不丢失
		e.ID = stableID(e.StorageID + ":" + e.FilePath)
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO episodes(id, book_id, storage_id, title, file_path, size, ord, duration)
		VALUES(?,?,?,?,?,?,?,?)`,
		e.ID, e.BookID, e.StorageID, e.Title, e.FilePath, e.Size, e.Order, e.Duration)
	return err
}

func ListEpisodesByBook(bookID string) ([]Episode, error) {
	rows, err := db.Query("SELECT id, book_id, storage_id, title, file_path, size, ord, duration FROM episodes WHERE book_id=? ORDER BY ord", bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEpisodes(rows)
}

func GetEpisode(id string) (*Episode, error) {
	row := db.QueryRow("SELECT id, book_id, storage_id, title, file_path, size, ord, duration FROM episodes WHERE id=?", id)
	e := &Episode{}
	err := row.Scan(&e.ID, &e.BookID, &e.StorageID, &e.Title, &e.FilePath, &e.Size, &e.Order, &e.Duration)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func scanEpisodes(rows *sql.Rows) ([]Episode, error) {
	var eps []Episode
	for rows.Next() {
		e := Episode{}
		if err := rows.Scan(&e.ID, &e.BookID, &e.StorageID, &e.Title, &e.FilePath, &e.Size, &e.Order, &e.Duration); err != nil {
			continue
		}
		eps = append(eps, e)
	}
	return eps, nil
}

// --- Progress CRUD ---

func SaveProgress(p *Progress) error {
	if p.ID == "" {
		p.ID = newID()
	}
	p.UpdatedAt = time.Now()
	// 自动计算是否听完（position/duration >= 0.9）
	p.IsFinished = p.Duration > 0 && p.Position/p.Duration >= 0.9
	finished := 0
	if p.IsFinished {
		finished = 1
	}
	_, err := db.Exec(`INSERT INTO progress(id, user_id, episode_id, position, duration, is_finished, updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(user_id, episode_id) DO UPDATE SET position=excluded.position, duration=excluded.duration, is_finished=excluded.is_finished, updated_at=excluded.updated_at`,
		p.ID, p.UserID, p.EpisodeID, p.Position, p.Duration, finished, p.UpdatedAt.Format(time.RFC3339))
	return err
}

func GetProgress(userID, episodeID string) (*Progress, error) {
	row := db.QueryRow("SELECT id, user_id, episode_id, position, duration, is_finished, updated_at FROM progress WHERE user_id=? AND episode_id=?", userID, episodeID)
	return scanProgress(row)
}

func scanProgress(row *sql.Row) (*Progress, error) {
	p := &Progress{}
	var ua string
	var fin int
	err := row.Scan(&p.ID, &p.UserID, &p.EpisodeID, &p.Position, &p.Duration, &fin, &ua)
	if err != nil {
		return nil, err
	}
	p.IsFinished = fin == 1
	p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return p, nil
}

func ListProgressesByUser(userID string) ([]Progress, error) {
	rows, err := db.Query("SELECT id, user_id, episode_id, position, duration, is_finished, updated_at FROM progress WHERE user_id=? ORDER BY updated_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProgresses(rows)
}

func ListProgressesByBook(userID, bookID string) ([]Progress, error) {
	rows, err := db.Query(`SELECT p.id, p.user_id, p.episode_id, p.position, p.duration, p.is_finished, p.updated_at
		FROM progress p JOIN episodes e ON p.episode_id = e.id
		WHERE p.user_id=? AND e.book_id=? ORDER BY e.ord`, userID, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProgresses(rows)
}

func scanProgresses(rows *sql.Rows) ([]Progress, error) {
	var list []Progress
	for rows.Next() {
		p := Progress{}
		var ua string
		var fin int
		if err := rows.Scan(&p.ID, &p.UserID, &p.EpisodeID, &p.Position, &p.Duration, &fin, &ua); err != nil {
			continue
		}
		p.IsFinished = fin == 1
		p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
		list = append(list, p)
	}
	return list, nil
}
