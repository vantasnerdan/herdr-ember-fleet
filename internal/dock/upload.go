package dock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vantasnerdan/herdr-ember-fleet/internal/e2e"
	"github.com/vantasnerdan/herdr-ember-fleet/internal/herdr"
)

const (
	maxUpload  = 50 << 20
	uploadKeep = 7 * 24 * time.Hour
)

// UploadDir is where files sent from the fleet page land for this user.
func UploadDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "ember-fleet", "uploads")
}

// safeName keeps a file name readable but harmless: its last path element,
// letters, digits, dot, dash and underscore only, no leading dot.
func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.TrimLeft(b.String(), "._")
	if len(out) > 100 {
		ext := filepath.Ext(out)
		if len(ext) > 12 {
			ext = ""
		}
		out = out[:100-len(ext)] + ext
	}
	if out == "" {
		out = "file"
	}
	return out
}

// upload receives one file (exactly Size bytes) and answers with its path.
func (d *Dock) upload(ctx context.Context, c *chanCtx) {
	h := c.h
	if !herdr.ValidSession(h.Session) {
		c.fail("bad_target", "bad session")
		return
	}
	if h.Size <= 0 || h.Size > maxUpload {
		c.fail("too_large", "files are limited to 50 MB")
		return
	}
	dir := filepath.Join(UploadDir(), time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		c.fail("io", "cannot create the upload folder: "+err.Error())
		return
	}
	tag := make([]byte, 3)
	rand.Read(tag)
	path := filepath.Join(dir, hex.EncodeToString(tag)+"-"+safeName(h.Name))
	f, err := os.OpenFile(path+".part", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		c.fail("io", "cannot create the file: "+err.Error())
		return
	}
	if c.ok() != nil {
		f.Close()
		os.Remove(path + ".part")
		return
	}
	n, err := io.Copy(f, io.LimitReader(c.r, h.Size))
	cerr := f.Close()
	if err != nil || cerr != nil || n != h.Size {
		os.Remove(path + ".part")
		e2e.WriteJSON(c.conn, e2e.Reply{Code: "short", Error: "the upload was cut short"})
		return
	}
	if err := os.Rename(path+".part", path); err != nil {
		os.Remove(path + ".part")
		e2e.WriteJSON(c.conn, e2e.Reply{Code: "io", Error: err.Error()})
		return
	}
	d.audit.log(c, "upload", filepath.Base(path))
	e2e.WriteJSON(c.conn, e2e.Reply{OK: true, Path: path})
}

// cleanUploads deletes upload folders older than a week, now and daily.
func cleanUploads(ctx context.Context) {
	for {
		entries, _ := os.ReadDir(UploadDir())
		for _, e := range entries {
			day, err := time.Parse("2006-01-02", e.Name())
			if err == nil && e.IsDir() && time.Since(day) > uploadKeep+24*time.Hour {
				if err := os.RemoveAll(filepath.Join(UploadDir(), e.Name())); err != nil {
					log.Printf("clean uploads: %v", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}
