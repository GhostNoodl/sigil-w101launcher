package webui

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailswindows "github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend-news
var newsAssets embed.FS

// RunNewsWindow opens a decorated browser window showing the official
// Wizard101 news page. Runs as the "news-window" subcommand, spawned by
// OpenNewsWindow — a separate process because the main window is frameless
// and the site's CSP blocks iframing it from non-wizard101.com origins.
func RunNewsWindow() error {
	return wails.Run(&options.App{
		Title:  "Wizard101 News",
		Width:  1150,
		Height: 820,
		AssetServer: &assetserver.Options{
			Assets: newsAssets,
		},
		Windows: &wailswindows.Options{
			// Separate user-data dir so this instance can run alongside
			// the main launcher window.
			WebviewUserDataPath: filepath.Join(os.TempDir(), "w101client-news-webview"),
		},
	})
}
