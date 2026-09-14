// Package local 实现本地文件系统存储驱动。
package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"audiobook/internal/driver"
	"audiobook/internal/drivers"
)

func init() {
	drivers.Register("local", func(cfg map[string]string) (driver.Driver, error) {
		return &Local{}, nil
	})
}

// Local 本地文件系统驱动。
type Local struct {
	root string
}

func (d *Local) Config() driver.Config {
	return driver.Config{
		Name:         "local",
		DisplayName:  "本地存储",
		SupportsLink: false, // 本地文件默认走服务器中转
		Fields: []driver.Field{
			{Name: "root", Label: "根目录路径", Type: "string", Required: true},
		},
	}
}

func (d *Local) Init(_ context.Context, cfg map[string]string) error {
	root := strings.TrimSpace(cfg["root"])
	if root == "" {
		return fmt.Errorf("根目录路径不能为空")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return os.ErrNotExist
	}
	d.root = abs
	return nil
}

func (d *Local) Drop(_ context.Context) error { return nil }

func (d *Local) resolve(p string) (string, error) {
	// 相对挂载根的路径映射到本地绝对路径，严格限制在 d.root 内，杜绝路径穿越攻击 (../ 等)。
	clean := filepath.Clean("/" + strings.TrimPrefix(p, "/"))
	target := filepath.Join(d.root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	rel, err := filepath.Rel(d.root, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", os.ErrPermission
	}
	return target, nil
}

func (d *Local) List(_ context.Context, p string) ([]driver.Obj, error) {
	dir, err := d.resolve(p)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	objs := make([]driver.Obj, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(p, e.Name()))
		objs = append(objs, driver.Obj{
			Name:     e.Name(),
			Path:     rel,
			Size:     info.Size(),
			Modified: info.ModTime(),
			IsDir:    e.IsDir(),
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

func (d *Local) Link(_ context.Context, _ driver.Obj) (*driver.Link, error) {
	return nil, nil // 本地不提供直链
}

func (d *Local) Open(_ context.Context, file driver.Obj) (io.ReadCloser, int64, error) {
	target, err := d.resolve(file.Path)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(target)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// Put 实现 driver.Writer 接口，支持将文件写入指定目录（如封皮保存）
func (d *Local) Put(_ context.Context, parentPath string, name string, r io.Reader, size int64) (driver.Obj, error) {
	dir, err := d.resolve(parentPath)
	if err != nil {
		return driver.Obj{}, err
	}
	target := filepath.Join(dir, name)
	rel, err := filepath.Rel(d.root, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return driver.Obj{}, os.ErrPermission
	}
	out, err := os.Create(target)
	if err != nil {
		return driver.Obj{}, err
	}
	defer out.Close()

	written, err := io.Copy(out, r)
	if err != nil {
		return driver.Obj{}, err
	}
	return driver.Obj{
		Name:     name,
		Path:     filepath.ToSlash(filepath.Join(parentPath, name)),
		Size:     written,
		IsDir:    false,
	}, nil
}
