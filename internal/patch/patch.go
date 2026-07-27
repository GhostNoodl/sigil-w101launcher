// Package patch implements a fast incremental patcher for Wizard101.
//
// It talks to the KingsIsle patch server to obtain the latest file list,
// verifies local files against the published CRCs (only for packages the
// install has actually downloaded, per LocalPackagesList.txt), and fetches
// stale or missing files over HTTP with concurrent workers. This mirrors
// what the official patcher does, minus the slow full-tree scan.
package patch

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"math/bits"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cedws/w101-client-go/dml"
	"github.com/cedws/w101-client-go/proto"
	"github.com/cedws/w101-proto-go/pkg/patch"
	"golang.org/x/sync/errgroup"
)

// File types in the patch manifest that need special handling.
const fileTypeDynamicWAD = 5

// Tables in the file list that are metadata, not downloadable packages.
var metaTables = []string{"_TableList", "About", "PatchClient"}

type Options struct {
	Dir         string // Wizard101 install directory
	Server      string // patch server host:port
	Full        bool   // verify/download every package, not just installed ones
	Concurrency int    // parallel verify/download workers
	DialTimeout time.Duration
	// DownloadTimeout caps a single file download attempt; Retries is the
	// total number of attempts per file. Together they keep a stalled CDN
	// connection from hanging the patch run forever.
	DownloadTimeout time.Duration
	Retries         int
	HTTPClient      *http.Client
	ProgressFunc    func(msg string) // optional status callback; defaults to slog
}

func (o *Options) defaults() {
	if o.Concurrency <= 0 {
		o.Concurrency = 8
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 10 * time.Second
	}
	if o.DownloadTimeout <= 0 {
		o.DownloadTimeout = 10 * time.Minute
	}
	if o.Retries <= 0 {
		o.Retries = 3
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		}
	}
	if o.ProgressFunc == nil {
		o.ProgressFunc = func(msg string) { slog.Info(msg) }
	}
}

// Run verifies and repairs the install according to opts.
func Run(ctx context.Context, opts Options) error {
	opts.defaults()

	p := &patcher{opts: opts}
	if err := p.openPackagesList(); err != nil {
		return err
	}
	defer p.packagesFile.Close()

	fileList, err := p.fetchFileList(ctx)
	if err != nil {
		return fmt.Errorf("fetching file list: %w", err)
	}
	opts.ProgressFunc(fmt.Sprintf("Latest version: %d", fileList.LatestVersion))

	tables, err := p.fetchTables(ctx, fileList.ListFileURL)
	if err != nil {
		return fmt.Errorf("downloading file list: %w", err)
	}

	return p.processTables(ctx, fileList.URLPrefix, *tables)
}

type patcher struct {
	opts         Options
	packages     []string
	packagesFile *os.File
}

// --- package list (LocalPackagesList.txt) ---

func (p *patcher) openPackagesList() error {
	path := filepath.Join(p.opts.Dir, "LocalPackagesList.txt")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening packages list: %w", err)
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		p.packages = append(p.packages, strings.TrimSpace(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		f.Close()
		return err
	}

	p.packagesFile = f
	return nil
}

func (p *patcher) hasPackage(name string) bool {
	// The Base package is always present and always verified, but is never
	// written to the packages list.
	if name == "Base" {
		return true
	}
	return slices.Contains(p.packages, name)
}

// packageOnDisk reports whether any of the table's files exist in the
// install. Steam installs ship fully populated but without a
// LocalPackagesList.txt, so on-disk presence is the only signal that a
// package needs verifying.
func (p *patcher) packageOnDisk(table dml.Table) bool {
	for _, record := range table.Records {
		source, _ := record["SrcFileName"].(string)
		target, _ := record["TarFileName"].(string)
		if target == "" {
			target = source
		}
		if target == "" {
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(target))
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		if _, err := os.Stat(filepath.Join(p.opts.Dir, clean)); err == nil {
			return true
		}
	}
	return false
}

func (p *patcher) addPackage(name string) {
	if name == "Base" || p.hasPackage(name) {
		return
	}
	p.packages = append(p.packages, name)
	fmt.Fprintln(p.packagesFile, name)
}

// --- file list retrieval ---

type fileListHandler struct {
	patch.Service
	ch chan patch.LatestFileListV2
}

func (h *fileListHandler) LatestFileListV2(m patch.LatestFileListV2) { h.ch <- m }

func (p *patcher) fetchFileList(ctx context.Context) (*patch.LatestFileListV2, error) {
	handler := &fileListHandler{ch: make(chan patch.LatestFileListV2, 1)}
	router := proto.NewMessageRouter()
	patch.RegisterService(&router, handler)

	ctx, cancel := context.WithTimeout(ctx, p.opts.DialTimeout)
	defer cancel()

	client, err := proto.Dial(ctx, p.opts.Server, &router)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	if err := patch.NewClient(client).LatestFileListV2(&patch.LatestFileListV2{}); err != nil {
		return nil, err
	}

	select {
	case m := <-handler.ch:
		return &m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *patcher) fetchTables(ctx context.Context, listURL string) (*[]dml.Table, error) {
	body, err := p.get(ctx, listURL)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	// Cache the manifest locally, matching the official patcher's layout.
	if err := os.MkdirAll(filepath.Join(p.opts.Dir, "PatchInfo"), 0o755); err != nil {
		return nil, err
	}
	cache, err := os.Create(filepath.Join(p.opts.Dir, "PatchInfo", "LatestFileList.bin"))
	if err != nil {
		return nil, err
	}
	defer cache.Close()

	return dml.DecodeTable(io.TeeReader(body, cache))
}

// --- verification and download ---

type fileEntry struct {
	url    string
	source string
	target string
	crc    uint32
	size   uint32
	typ    uint32
}

func (p *patcher) processTables(ctx context.Context, urlPrefix string, tables []dml.Table) error {
	for _, table := range tables {
		if slices.Contains(metaTables, table.Name) {
			continue
		}
		if !p.opts.Full && !p.hasPackage(table.Name) && !p.packageOnDisk(table) {
			continue
		}

		p.opts.ProgressFunc(fmt.Sprintf("Verifying package %s (%d files)", table.Name, len(table.Records)))

		crcPath := filepath.Join(p.opts.Dir, "PatchInfo", "CRC_"+table.Name+".dat")
		crcFile, err := os.Create(crcPath)
		if err != nil {
			return err
		}

		group, ctx := errgroup.WithContext(ctx)
		group.SetLimit(p.opts.Concurrency)

		var crcMu sync.Mutex

		for _, record := range table.Records {
			group.Go(func() error {
				entry, err := p.entryFromRecord(urlPrefix, record)
				if err != nil {
					return err
				}
				if entry == nil {
					return nil // skipped (dynamic WAD in incremental mode)
				}

				if err := p.verifyOrDownload(ctx, entry); err != nil {
					return fmt.Errorf("%s: %w", entry.target, err)
				}

				crcMu.Lock()
				defer crcMu.Unlock()
				return writeCRCRecord(crcFile, entry)
			})
		}

		err = group.Wait()
		crcFile.Close()
		if err != nil {
			return err
		}

		p.addPackage(table.Name)
	}

	return nil
}

func (p *patcher) entryFromRecord(urlPrefix string, record dml.Record) (*fileEntry, error) {
	source, _ := record["SrcFileName"].(string)
	target, _ := record["TarFileName"].(string)
	fileCRC, _ := record["CRC"].(uint32)
	size, _ := record["Size"].(uint32)
	typ, _ := record["FileType"].(uint32)

	if target == "" {
		target = source
	}

	// Dynamic WADs are streamed in segments at runtime, so their on-disk
	// content never matches the manifest CRC. Only touch them in full mode.
	if typ == fileTypeDynamicWAD && !p.opts.Full {
		return nil, nil
	}

	// Guard against path traversal from a hostile/manipulated manifest.
	cleanTarget := filepath.Clean(filepath.FromSlash(target))
	if strings.HasPrefix(cleanTarget, "..") || filepath.IsAbs(cleanTarget) {
		return nil, fmt.Errorf("unsafe target path in manifest: %q", target)
	}

	fileURL, err := url.JoinPath(urlPrefix, source)
	if err != nil {
		return nil, err
	}

	return &fileEntry{
		url:    fileURL,
		source: path.Clean(source),
		target: cleanTarget,
		crc:    fileCRC,
		size:   size,
		typ:    typ,
	}, nil
}

func (p *patcher) verifyOrDownload(ctx context.Context, e *fileEntry) error {
	abs := filepath.Join(p.opts.Dir, e.target)

	ok, err := verifyFile(abs, e.size, e.crc)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}

	var dlErr error
	for attempt := 1; attempt <= p.opts.Retries; attempt++ {
		if attempt > 1 {
			p.opts.ProgressFunc(fmt.Sprintf("Retrying %s (attempt %d/%d)", e.target, attempt, p.opts.Retries))
		}
		if dlErr = p.download(ctx, abs, e); dlErr == nil {
			return nil
		}
	}
	return dlErr
}

func (p *patcher) download(ctx context.Context, abs string, e *fileEntry) error {
	p.opts.ProgressFunc(fmt.Sprintf("Downloading %s (%.1f MB)", e.target, float64(e.size)/(1<<20)))

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, p.opts.DownloadTimeout)
	defer cancel()

	body, err := p.get(ctx, e.url)
	if err != nil {
		return err
	}
	defer body.Close()

	// Write to a temp file and rename only after the CRC checks out, so an
	// interrupted run never leaves a corrupt file behind for the official
	// patcher (or us) to trip over.
	tmp := abs + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	hasher := newReverseHasher()
	var src io.Reader = io.TeeReader(io.LimitReader(body, int64(e.size)), hasher)
	if e.size >= 5<<20 {
		src = &progressReader{r: src, report: func(done int64) {
			p.opts.ProgressFunc(fmt.Sprintf("Downloading %s (%.0f%%)", e.target, 100*float64(done)/float64(e.size)))
		}}
	}

	n, err := io.Copy(f, src)
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	if uint32(n) != e.size {
		os.Remove(tmp)
		return fmt.Errorf("size mismatch: got %d bytes, want %d", n, e.size)
	}
	if got := hasher.Sum32(); got != e.crc {
		os.Remove(tmp)
		return fmt.Errorf("CRC mismatch after download: got %08x, want %08x", got, e.crc)
	}

	return os.Rename(tmp, abs)
}

// progressReader reports cumulative bytes read, at most once every couple
// of seconds, so large downloads show visible progress instead of looking
// like a hang.
type progressReader struct {
	r      io.Reader
	done   int64
	last   time.Time
	report func(done int64)
}

func (pr *progressReader) Read(b []byte) (int, error) {
	n, err := pr.r.Read(b)
	pr.done += int64(n)
	if n > 0 && time.Since(pr.last) >= 2*time.Second {
		pr.last = time.Now()
		pr.report(pr.done)
	}
	return n, err
}

func verifyFile(abs string, size uint32, wantCRC uint32) (bool, error) {
	st, err := os.Stat(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case st.IsDir():
		return false, fmt.Errorf("expected file, found directory")
	case st.Size() != int64(size):
		return false, nil
	}

	f, err := os.Open(abs)
	if err != nil {
		return false, err
	}
	defer f.Close()

	hasher := newReverseHasher()
	if _, err := io.Copy(hasher, f); err != nil {
		return false, err
	}
	return hasher.Sum32() == wantCRC, nil
}

// --- CRC helpers ---
//
// KingsIsle uses a CRC-32 with the bit-reversed polynomial, matching the
// values in the manifest. PatchInfo/CRC_<table>.dat records are written in
// the official patcher's 24-byte format so the stock launcher can still
// read them later.

type reverseHasher struct {
	table *crc32.Table
	sum   uint32
}

var crcTable = crc32.MakeTable(bits.Reverse32(0x4C11DB7))

func newReverseHasher() *reverseHasher {
	return &reverseHasher{table: crcTable, sum: 0xFFFFFFFF}
}

func (h *reverseHasher) Write(b []byte) (int, error) {
	h.sum = crc32.Update(h.sum, h.table, b)
	return len(b), nil
}

func (h *reverseHasher) Sum32() uint32 { return h.sum ^ 0xFFFFFFFF }

func writeCRCRecord(w io.Writer, e *fileEntry) error {
	hasher := newReverseHasher()
	hasher.Write([]byte(e.source))

	var buf []byte
	buf = binary.LittleEndian.AppendUint32(buf, hasher.Sum32())
	buf = binary.LittleEndian.AppendUint32(buf, e.size)
	buf = binary.LittleEndian.AppendUint32(buf, e.crc)
	buf = binary.LittleEndian.AppendUint32(buf, 0)
	buf = binary.LittleEndian.AppendUint64(buf, uint64(time.Now().UnixMilli()))

	if len(buf) != 24 {
		return fmt.Errorf("internal error: CRC record is %d bytes, want 24", len(buf))
	}
	_, err := w.Write(buf)
	return err
}

func (p *patcher) get(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	// The official CDN keys off this user agent.
	req.Header.Set("User-Agent", "KingsIsle Patcher")

	resp, err := p.opts.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	return resp.Body, nil
}
