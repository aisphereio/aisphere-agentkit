package objectstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type filesystemStore struct {
	root   string
	prefix string
}

func NewFilesystemStore(root, prefix string) Store {
	return &filesystemStore{root: root, prefix: cleanPrefix(prefix)}
}

func (s *filesystemStore) Put(ctx context.Context, key string, r io.Reader, size int64, opts PutOptions) (*ObjectInfo, error) {
	_ = ctx
	if r == nil {
		return nil, fmt.Errorf("reader is required")
	}
	p, finalKey, err := s.pathForKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, fmt.Errorf("create object directory: %w", err)
	}
	tmp := p + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create object temp file: %w", err)
	}
	written, copyErr := io.Copy(out, r)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("write object: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("close object: %w", closeErr)
	}
	if size >= 0 && written != size {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("object size mismatch: wrote %d bytes, expected %d", written, size)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("commit object: %w", err)
	}
	st, _ := os.Stat(p)
	info := &ObjectInfo{Key: finalKey, Size: written, ContentType: opts.ContentType, LastModified: time.Now()}
	if st != nil {
		info.Size = st.Size()
		info.LastModified = st.ModTime()
	}
	return info, nil
}

func (s *filesystemStore) Get(ctx context.Context, key string) (io.ReadCloser, *ObjectInfo, error) {
	_ = ctx
	p, finalKey, err := s.pathForKey(key)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, err
	}
	st, _ := f.Stat()
	info := &ObjectInfo{Key: finalKey}
	if st != nil {
		info.Size = st.Size()
		info.LastModified = st.ModTime()
	}
	return f, info, nil
}

func (s *filesystemStore) Delete(ctx context.Context, key string) error {
	_ = ctx
	p, _, err := s.pathForKey(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *filesystemStore) DeletePrefix(ctx context.Context, prefix string) error {
	_ = ctx
	p, _, err := s.pathForKey(prefix)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *filesystemStore) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	_ = ctx
	base, finalPrefix, err := s.pathForKey(prefix)
	if err != nil {
		return nil, err
	}
	root := base
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ObjectInfo
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if s.prefix != "" {
			key = strings.TrimPrefix(key, s.prefix+"/")
		}
		if finalPrefix != "" && !strings.HasPrefix(key, finalPrefix) {
			return nil
		}
		out = append(out, ObjectInfo{Key: key, Size: st.Size(), LastModified: st.ModTime()})
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *filesystemStore) Exists(ctx context.Context, key string) (bool, error) {
	_ = ctx
	p, _, err := s.pathForKey(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *filesystemStore) pathForKey(key string) (string, string, error) {
	cleanKey, err := cleanKey(key)
	if err != nil {
		return "", "", err
	}
	fullKey := cleanKey
	if s.prefix != "" {
		fullKey = s.prefix + "/" + cleanKey
	}
	p := filepath.Join(s.root, filepath.FromSlash(fullKey))
	rootAbs, _ := filepath.Abs(s.root)
	pAbs, _ := filepath.Abs(p)
	if pAbs != rootAbs && !strings.HasPrefix(pAbs, rootAbs+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("object key escapes root")
	}
	return p, cleanKey, nil
}

func cleanKey(key string) (string, error) {
	key = filepath.ToSlash(strings.TrimSpace(key))
	key = strings.TrimPrefix(key, "/")
	key = filepath.ToSlash(filepath.Clean(key))
	if key == "." || key == "" || strings.HasPrefix(key, "../") || key == ".." {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	return key, nil
}

func cleanPrefix(prefix string) string {
	prefix = filepath.ToSlash(strings.TrimSpace(prefix))
	prefix = strings.Trim(prefix, "/")
	if prefix == "." {
		return ""
	}
	return prefix
}
