package patch

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const verifyCacheFilename = "SigilVerifyCache.json"

type cachedFile struct {
	Size            uint32 `json:"size"`
	CRC             uint32 `json:"crc"`
	ModTimeUnixNano int64  `json:"modTimeUnixNano"`
}

// verifyCache remembers files whose content has already passed a CRC check.
// A cache hit still stats the file; any size, mtime, manifest, or expected-CRC
// change falls back to reading and hashing the content again.
type verifyCache struct {
	Manifest string                `json:"manifest"`
	Files    map[string]cachedFile `json:"files"`

	mu sync.RWMutex `json:"-"`
}

func newVerifyCache(manifest string) *verifyCache {
	return &verifyCache{
		Manifest: manifest,
		Files:    make(map[string]cachedFile),
	}
}

func loadVerifyCache(filename, manifest string) (*verifyCache, error) {
	data, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return newVerifyCache(manifest), nil
	}
	if err != nil {
		return nil, err
	}

	cache := newVerifyCache(manifest)
	if err := json.Unmarshal(data, cache); err != nil {
		return nil, err
	}
	if cache.Manifest != manifest {
		return newVerifyCache(manifest), nil
	}
	if cache.Files == nil {
		cache.Files = make(map[string]cachedFile)
	}
	return cache, nil
}

func (c *verifyCache) matches(filename string, entry *fileEntry) bool {
	key := cacheKey(entry.target)
	c.mu.RLock()
	cached, ok := c.Files[key]
	c.mu.RUnlock()
	if !ok || cached.Size != entry.size || cached.CRC != entry.crc {
		return false
	}

	info, err := os.Stat(filename)
	return err == nil && !info.IsDir() &&
		info.Size() == int64(entry.size) &&
		info.ModTime().UnixNano() == cached.ModTimeUnixNano
}

func (c *verifyCache) remember(filename string, entry *fileEntry) error {
	info, err := os.Stat(filename)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("expected file, found directory")
	}

	c.mu.Lock()
	c.Files[cacheKey(entry.target)] = cachedFile{
		Size:            entry.size,
		CRC:             entry.crc,
		ModTimeUnixNano: info.ModTime().UnixNano(),
	}
	c.mu.Unlock()
	return nil
}

func (c *verifyCache) marshal() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return json.MarshalIndent(c, "", "  ")
}

func (c *verifyCache) save(filename string) error {
	data, err := c.marshal()
	if err != nil {
		return err
	}
	return writeAtomic(filename, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

func cacheKey(target string) string {
	// Wizard101 is Windows-only, so paths are case-insensitive.
	return strings.ToLower(filepath.ToSlash(filepath.Clean(target)))
}
