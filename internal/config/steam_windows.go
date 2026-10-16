//go:build windows

package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// libraryPathRe matches `"path"  "C:\\SteamLibrary"` lines in
// libraryfolders.vdf.
var libraryPathRe = regexp.MustCompile(`"path"\s+"([^"]+)"`)

// steamInstallDirs returns candidate Wizard101 folders in every Steam
// library on this machine. Steam can live anywhere (C:\Steam, D:\Games\Steam,
// ...) and each install can have extra library folders on other drives, so
// the registry and libraryfolders.vdf are the only reliable way to find them.
func steamInstallDirs() []string {
	var roots []string
	for _, key := range []struct {
		root  registry.Key
		path  string
		value string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath"},
		{registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`, "InstallPath"},
		{registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath"},
	} {
		k, err := registry.OpenKey(key.root, key.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if v, _, err := k.GetStringValue(key.value); err == nil && v != "" {
			roots = append(roots, filepath.Clean(v))
		}
		k.Close()
	}

	var libraries []string
	for _, root := range roots {
		libraries = append(libraries, root)
		data, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range libraryPathRe.FindAllStringSubmatch(string(data), -1) {
			libraries = append(libraries, filepath.Clean(strings.ReplaceAll(m[1], `\\`, `\`)))
		}
	}

	var dirs []string
	for _, lib := range libraries {
		dirs = append(dirs, filepath.Join(lib, "steamapps", "common", "Wizard101"))
	}
	return dirs
}
