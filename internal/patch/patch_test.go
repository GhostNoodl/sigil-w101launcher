package patch

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cedws/w101-client-go/dml"
)

func TestVerifyCacheSkipsUnchangedFileAndInvalidatesChange(t *testing.T) {
	payload := []byte("verified payload")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	dir := t.TempDir()
	entry := testEntry(server.URL+"/payload", "Data/payload.bin", payload)
	p := &patcher{
		opts: Options{
			Dir:          dir,
			Retries:      1,
			HTTPClient:   server.Client(),
			ProgressFunc: func(string) {},
		},
		cache: newVerifyCache("manifest-a"),
	}
	p.opts.defaults()

	cached, err := p.verifyOrDownload(context.Background(), entry)
	if err != nil {
		t.Fatalf("first verification: %v", err)
	}
	if cached {
		t.Fatal("first verification unexpectedly used cache")
	}
	if requests.Load() != 1 {
		t.Fatalf("download requests = %d, want 1", requests.Load())
	}

	cached, err = p.verifyOrDownload(context.Background(), entry)
	if err != nil {
		t.Fatalf("cached verification: %v", err)
	}
	if !cached {
		t.Fatal("unchanged file did not use cache")
	}
	if requests.Load() != 1 {
		t.Fatalf("cached check made a download request; requests = %d", requests.Load())
	}

	abs := filepath.Join(dir, filepath.FromSlash(entry.target))
	if err := os.WriteFile(abs, []byte("damaged payload!"), 0o644); err != nil {
		t.Fatal(err)
	}
	changedTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(abs, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}

	cached, err = p.verifyOrDownload(context.Background(), entry)
	if err != nil {
		t.Fatalf("changed-file verification: %v", err)
	}
	if cached {
		t.Fatal("changed file incorrectly used cache")
	}
	if requests.Load() != 2 {
		t.Fatalf("changed file download requests = %d, want 2", requests.Load())
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("repaired content = %q, want %q", got, payload)
	}

	p.opts.Full = true
	cached, err = p.verifyOrDownload(context.Background(), entry)
	if err != nil {
		t.Fatalf("full verification: %v", err)
	}
	if cached {
		t.Fatal("full verification did not bypass cache")
	}
}

func TestDownloadFailurePreservesOriginalAndRemovesPartFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "bad!")
	}))
	defer server.Close()

	dir := t.TempDir()
	abs := filepath.Join(dir, "client.bin")
	if err := os.WriteFile(abs, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := testEntry(server.URL+"/client.bin", "client.bin", []byte("good"))
	p := &patcher{opts: Options{
		Dir:          dir,
		HTTPClient:   server.Client(),
		ProgressFunc: func(string) {},
	}}
	p.opts.defaults()

	if err := p.download(context.Background(), abs, entry); err == nil || !strings.Contains(err.Error(), "CRC mismatch") {
		t.Fatalf("download error = %v, want CRC mismatch", err)
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("original content changed to %q", got)
	}
	parts, err := filepath.Glob(filepath.Join(dir, ".client.bin-*.part"))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 0 {
		t.Fatalf("partial files left behind: %v", parts)
	}
}

func TestProcessTablesPreservesCRCFileOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	dir := t.TempDir()
	patchInfo := filepath.Join(dir, "PatchInfo")
	if err := os.MkdirAll(patchInfo, 0o755); err != nil {
		t.Fatal(err)
	}
	crcPath := filepath.Join(patchInfo, "CRC_Base.dat")
	if err := os.WriteFile(crcPath, []byte("previous-good-metadata"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := []byte("already valid")
	if err := os.WriteFile(filepath.Join(dir, "good.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	table := dml.Table{Name: "Base", Records: []dml.Record{
		testRecord("good.bin", payload),
		testRecord("missing.bin", []byte("expected")),
	}}
	p := &patcher{
		opts: Options{
			Dir:          dir,
			Concurrency:  2,
			Retries:      1,
			HTTPClient:   server.Client(),
			ProgressFunc: func(string) {},
		},
		cache: newVerifyCache("manifest-a"),
	}
	p.opts.defaults()

	if err := p.processTables(context.Background(), server.URL, []dml.Table{table}); err == nil {
		t.Fatal("processTables succeeded despite failed download")
	}
	got, err := os.ReadFile(crcPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "previous-good-metadata" {
		t.Fatalf("CRC metadata was replaced on failure: %q", got)
	}
}

func TestProcessTablesWritesCRCRecordsInManifestOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "PatchInfo"), 0o755); err != nil {
		t.Fatal(err)
	}

	first := testRecord("first.bin", []byte("first"))
	second := testRecord("second.bin", []byte("second"))
	if err := os.WriteFile(filepath.Join(dir, "first.bin"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "second.bin"), []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &patcher{
		opts: Options{
			Dir:          dir,
			Concurrency:  2,
			Retries:      1,
			ProgressFunc: func(string) {},
		},
		cache: newVerifyCache("manifest-a"),
	}
	p.opts.defaults()
	table := dml.Table{Name: "Base", Records: []dml.Record{first, second}}
	if err := p.processTables(context.Background(), "http://unused.invalid", []dml.Table{table}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "PatchInfo", "CRC_Base.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 48 {
		t.Fatalf("CRC data length = %d, want 48", len(data))
	}
	if got, want := binary.LittleEndian.Uint32(data[0:4]), sourceCRC("first.bin"); got != want {
		t.Fatalf("first source CRC = %08x, want %08x", got, want)
	}
	if got, want := binary.LittleEndian.Uint32(data[24:28]), sourceCRC("second.bin"); got != want {
		t.Fatalf("second source CRC = %08x, want %08x", got, want)
	}
}

func TestAtomicWriteAndInstallLock(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "state.dat")
	if err := os.WriteFile(filename, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected failure")
	if err := writeAtomic(filename, func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial-new")
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("writeAtomic error = %v, want injected failure", err)
	}
	if data, err := os.ReadFile(filename); err != nil || string(data) != "old" {
		t.Fatalf("failed atomic write changed file: data=%q err=%v", data, err)
	}

	first, err := acquireInstallLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireInstallLock(dir); !errors.Is(err, ErrPatchInProgress) {
		t.Fatalf("second lock error = %v, want ErrPatchInProgress", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireInstallLock(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCacheRoundTripAndManifestInvalidation(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, verifyCacheFilename)
	target := "Data/cached.bin"
	abs := filepath.Join(dir, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("cache me")
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		t.Fatal(err)
	}
	entry := testEntry("http://unused.invalid/cached.bin", target, content)

	cache := newVerifyCache("manifest-a")
	if err := cache.remember(abs, entry); err != nil {
		t.Fatal(err)
	}
	if err := cache.save(filename); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadVerifyCache(filename, "manifest-a")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.matches(abs, entry) {
		t.Fatal("round-tripped cache did not match unchanged file")
	}
	invalidated, err := loadVerifyCache(filename, "manifest-b")
	if err != nil {
		t.Fatal(err)
	}
	if invalidated.matches(abs, entry) {
		t.Fatal("cache survived a manifest change")
	}
}

func TestManifestPathValidation(t *testing.T) {
	p := &patcher{}
	for _, target := range []string{`..\outside.bin`, `C:relative.bin`, `file.bin:stream`, `C:\outside.bin`} {
		record := testRecord(target, []byte("x"))
		if _, err := p.entryFromRecord("http://example.invalid/base", record); err == nil {
			t.Errorf("target %q was accepted", target)
		}
	}
	for _, table := range []string{`..\outside`, `nested/table`, `C:table`} {
		if safeTableName(table) {
			t.Errorf("table name %q was accepted", table)
		}
	}
}

func testEntry(rawURL, target string, content []byte) *fileEntry {
	return &fileEntry{
		url:    rawURL,
		source: filepath.ToSlash(target),
		target: filepath.FromSlash(target),
		size:   uint32(len(content)),
		crc:    sourceCRCBytes(content),
		typ:    1,
	}
}

func testRecord(target string, content []byte) dml.Record {
	return dml.Record{
		"SrcFileName": target,
		"TarFileName": target,
		"CRC":         sourceCRCBytes(content),
		"Size":        uint32(len(content)),
		"FileType":    uint32(1),
	}
}

func sourceCRC(source string) uint32 {
	return sourceCRCBytes([]byte(source))
}

func sourceCRCBytes(data []byte) uint32 {
	hasher := newReverseHasher()
	_, _ = hasher.Write(data)
	return hasher.Sum32()
}
