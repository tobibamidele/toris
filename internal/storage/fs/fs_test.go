package fs_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fsstorage "github.com/tobibamidele/toris/internal/storage/fs"
)

func newBackend(t *testing.T) *fsstorage.Backend {
	t.Helper()
	b, err := fsstorage.New(t.TempDir())
	if err != nil {
		t.Fatalf("creating backend: %v", err)
	}
	return b
}

// ─── Write / Read round-trip ──────────────────────────────────────────────────

func TestWriteRead_RoundTrip(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	data := []byte("hello toris storage")
	if err := b.Write(ctx, "test/hello.txt", bytes.NewReader(data)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rc, err := b.Read(ctx, "test/hello.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("data mismatch: got %q, want %q", got, data)
	}
}

func TestWriteRead_LargePayload(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	data := bytes.Repeat([]byte("x"), 5*1024*1024) // 5 MB
	if err := b.Write(ctx, "large/file.bin", bytes.NewReader(data)); err != nil {
		t.Fatalf("Write large: %v", err)
	}

	rc, err := b.Read(ctx, "large/file.bin")
	if err != nil {
		t.Fatalf("Read large: %v", err)
	}
	defer rc.Close()

	got, _ := io.ReadAll(rc)
	if len(got) != len(data) {
		t.Errorf("large read size mismatch: got %d, want %d", len(got), len(data))
	}
}

// ─── Atomicity ────────────────────────────────────────────────────────────────

func TestWrite_IsAtomic(t *testing.T) {
	// A reader must never see a partial write. We verify this by checking
	// that no temp file remains after a successful write.
	b := newBackend(t)
	ctx := context.Background()

	if err := b.Write(ctx, "atomic/test.txt", strings.NewReader("data")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Walk the base directory — no .toris_write_* temp files should remain.
	err := filepath.Walk(b.BaseDir(), func(path string, info os.FileInfo, _ error) error {
		if info != nil && strings.HasPrefix(info.Name(), ".toris_write_") {
			t.Errorf("leftover temp file: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
}

func TestWrite_OverwriteIsAtomic(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	if err := b.Write(ctx, "over/file.txt", strings.NewReader("v1")); err != nil {
		t.Fatal(err)
	}
	if err := b.Write(ctx, "over/file.txt", strings.NewReader("version2")); err != nil {
		t.Fatal(err)
	}

	rc, _ := b.Read(ctx, "over/file.txt")
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "version2" {
		t.Errorf("overwrite failed: got %q", got)
	}
}

// ─── Read errors ──────────────────────────────────────────────────────────────

func TestRead_NotFound(t *testing.T) {
	b := newBackend(t)
	_, err := b.Read(context.Background(), "does/not/exist.txt")
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

// ─── Delete ───────────────────────────────────────────────────────────────────

func TestDelete_RemovesObject(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	b.Write(ctx, "del/file.txt", strings.NewReader("bye"))
	if err := b.Delete(ctx, "del/file.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := b.Read(ctx, "del/file.txt")
	if err == nil {
		t.Error("expected not-found after delete, got nil")
	}
}

func TestDelete_NonExistentIsNoError(t *testing.T) {
	b := newBackend(t)
	if err := b.Delete(context.Background(), "never/existed.txt"); err != nil {
		t.Errorf("Delete of non-existent key should not error, got: %v", err)
	}
}

// ─── List ─────────────────────────────────────────────────────────────────────

func TestList_ReturnsAllKeys(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	for _, k := range []string{"a/1.txt", "a/2.txt", "b/3.txt"} {
		b.Write(ctx, k, strings.NewReader("x"))
	}

	keys, err := b.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d: %v", len(keys), keys)
	}
}

func TestList_FiltersByPrefix(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	for _, k := range []string{"backups/abc/base.tar", "backups/abc/wal.tar", "backups/def/base.tar"} {
		b.Write(ctx, k, strings.NewReader("x"))
	}

	keys, err := b.List(ctx, "backups/abc/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys for prefix backups/abc/, got %d: %v", len(keys), keys)
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, "backups/abc/") {
			t.Errorf("key %q does not match prefix backups/abc/", k)
		}
	}
}

func TestList_EmptyDir(t *testing.T) {
	b := newBackend(t)
	keys, err := b.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List on empty backend: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected empty list, got %v", keys)
	}
}

func TestList_IsSorted(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	for _, k := range []string{"c/3", "a/1", "b/2"} {
		b.Write(ctx, k, strings.NewReader("x"))
	}
	keys, _ := b.List(ctx, "")
	for i := 1; i < len(keys); i++ {
		if keys[i] < keys[i-1] {
			t.Errorf("list not sorted: %v", keys)
		}
	}
}

// ─── Stat ─────────────────────────────────────────────────────────────────────

func TestStat_ReturnsSize(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	data := "hello world"
	b.Write(ctx, "stat/file.txt", strings.NewReader(data))

	info, err := b.Stat(ctx, "stat/file.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.SizeBytes != int64(len(data)) {
		t.Errorf("size mismatch: got %d, want %d", info.SizeBytes, len(data))
	}
	if info.Key != "stat/file.txt" {
		t.Errorf("key mismatch: got %q", info.Key)
	}
}

func TestStat_NotFound(t *testing.T) {
	b := newBackend(t)
	_, err := b.Stat(context.Background(), "no/such/file")
	if err == nil {
		t.Error("expected not-found error from Stat on missing key")
	}
}

// ─── Path traversal ───────────────────────────────────────────────────────────

func TestWrite_PathTraversal_IsSafe(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()

	// A malicious key should not escape the base directory.
	// It may succeed (written to an __invalid__ path inside base) or fail,
	// but it must never write outside base.
	_ = b.Write(ctx, "../../etc/passwd", strings.NewReader("evil"))

	// Verify /etc/passwd was not touched.
	if _, err := os.Stat("/etc/passwd"); err == nil {
		content, _ := os.ReadFile("/etc/passwd")
		if string(content) == "evil" {
			t.Fatal("path traversal succeeded — security bug")
		}
	}
}

// ─── KeyForBackup / BackupPrefix helpers ─────────────────────────────────────

func TestKeyForBackup(t *testing.T) {
	key := fsstorage.KeyForBackup("abc123", "base.tar.gz")
	expected := "backups/abc123/base.tar.gz"
	if key != expected {
		t.Errorf("got %q, want %q", key, expected)
	}
}

func TestBackupPrefix(t *testing.T) {
	prefix := fsstorage.BackupPrefix("abc123")
	expected := "backups/abc123/"
	if prefix != expected {
		t.Errorf("got %q, want %q", prefix, expected)
	}
}

// ─── Cancellation ────────────────────────────────────────────────────────────

func TestWrite_CanceledContext(t *testing.T) {
	b := newBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := b.Write(ctx, "canceled/file.txt", strings.NewReader("data"))
	if err == nil {
		t.Error("expected error for canceled context, got nil")
	}
}

func TestRead_CanceledContext(t *testing.T) {
	b := newBackend(t)
	ctx := context.Background()
	b.Write(ctx, "exists/file.txt", strings.NewReader("data"))

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := b.Read(canceledCtx, "exists/file.txt")
	if err == nil {
		t.Error("expected error for canceled context on Read")
	}
}
