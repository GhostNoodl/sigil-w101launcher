package patch

// Live integration test against the real KingsIsle patch server.
// Only runs when W101_LIVE_TEST=1 is set. Read-only: it fetches the file
// manifest and decodes it, but downloads no game files.

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"
)

func TestLiveFileList(t *testing.T) {
	if os.Getenv("W101_LIVE_TEST") != "1" {
		t.Skip("set W101_LIVE_TEST=1 to run against the live patch server")
	}

	p := &patcher{opts: Options{
		Dir:          t.TempDir(),
		Server:       "patch.us.wizard101.com:12500",
		Concurrency:  4,
		DialTimeout:  15 * time.Second,
		ProgressFunc: func(msg string) { t.Log(msg) },
	}}
	p.opts.defaults()

	ctx := context.Background()

	fileList, err := p.fetchFileList(ctx)
	if err != nil {
		t.Fatalf("fetchFileList: %v", err)
	}
	t.Logf("latest version %d, list URL %s, prefix %s", fileList.LatestVersion, fileList.ListFileURL, fileList.URLPrefix)
	if fileList.ListFileURL == "" || fileList.URLPrefix == "" {
		t.Fatal("empty URLs in file list response")
	}

	tables, err := p.fetchTables(ctx, fileList.ListFileURL)
	if err != nil {
		t.Fatalf("fetchTables: %v", err)
	}
	if len(*tables) == 0 {
		t.Fatal("no tables in manifest")
	}

	var base *struct{ records int }
	for _, table := range *tables {
		t.Logf("table %q: %d records", table.Name, len(table.Records))
		if slices.Contains(metaTables, table.Name) {
			continue // meta tables have a different record schema
		}
		if table.Name == "Base" {
			b := &struct{ records int }{len(table.Records)}
			base = b
		}
		for i, record := range table.Records {
			if i >= 1 {
				break
			}
			for _, key := range []string{"SrcFileName", "CRC", "Size", "FileType"} {
				if _, ok := record[key]; !ok {
					t.Errorf("table %q record missing field %q", table.Name, key)
				}
			}
		}
	}
	if base == nil {
		t.Error("manifest has no Base table")
	}
}
