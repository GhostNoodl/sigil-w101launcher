// Package webui is the graphical launcher: a Wails v2 app with an embedded
// HTML/CSS/JS frontend (WebView2, no cgo). It is the default entry point
// when w101client is started without arguments.
package webui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	launcherapp "github.com/GhostNoodl/sigil/internal/app"
	"github.com/GhostNoodl/sigil/internal/config"
	"github.com/GhostNoodl/sigil/internal/creds"
	"github.com/GhostNoodl/sigil/internal/launch"
	"github.com/GhostNoodl/sigil/internal/news"
)

//go:embed all:frontend
var assets embed.FS

// App is the bound object whose methods the frontend calls.
type App struct {
	ctx context.Context
	cfg *config.Config

	mu   sync.Mutex
	busy bool
}

// State is the snapshot the frontend loads on startup.
type State struct {
	Accounts         []string `json:"accounts"`
	DefaultAccount   string   `json:"defaultAccount"`
	InstallDir       string   `json:"installDir"`
	Steam            bool     `json:"steam"`
	SkipPatch        bool     `json:"skipPatch"`
	RememberMe       bool     `json:"rememberMe"`
	Theme            string   `json:"theme"`
	Version          string   `json:"version"`
	MinimizeOnLaunch bool     `json:"minimizeOnLaunch"`
}

// Version is shown in the settings panel.
const Version = "1.0.0"

// Run opens the launcher window and blocks until it is closed.
func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	a := &App{cfg: cfg}

	return wails.Run(&options.App{
		Title:     "Sigil",
		Width:     460,
		Height:    720,
		MinWidth:  400,
		MinHeight: 600,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: a.startup,
		Bind:      []interface{}{a},
	})
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// GetState returns accounts and install info for initial render.
func (a *App) GetState() State {
	theme := a.cfg.Theme
	if theme == "" {
		theme = "dark"
	}
	return State{
		Accounts:         append([]string{}, a.cfg.Accounts...),
		DefaultAccount:   a.cfg.DefaultAccount,
		InstallDir:       a.cfg.InstallDir,
		Steam:            launch.IsSteamInstall(a.cfg.InstallDir),
		SkipPatch:        a.cfg.SkipPatchCheck,
		RememberMe:       a.cfg.RememberMeOrDefault(),
		Theme:            theme,
		Version:          Version,
		MinimizeOnLaunch: a.cfg.MinimizeOnLaunch,
	}
}

// BrowseInstallDir opens a native folder picker and returns the chosen
// directory ("" if cancelled). Does not save; the frontend passes the
// result back through SaveSettings.
func (a *App) BrowseInstallDir() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select your Wizard101 installation folder",
	})
}

// SaveSettings validates and persists the settings form. It returns an
// error message ("" on success) so the frontend can show it inline.
func (a *App) SaveSettings(installDir, theme string, minimizeOnLaunch bool) string {
	installDir = strings.TrimSpace(installDir)

	if installDir == "" {
		return "install directory is required"
	}
	if _, err := os.Stat(filepath.Join(installDir, "Bin", "WizardGraphicalClient.exe")); err != nil {
		return "no Wizard101 installation found at " + installDir
	}
	if theme != "dark" && theme != "light" {
		theme = "dark"
	}

	a.cfg.InstallDir = installDir
	a.cfg.Theme = theme
	a.cfg.MinimizeOnLaunch = minimizeOnLaunch
	if err := a.cfg.Save(); err != nil {
		return "saving settings: " + err.Error()
	}
	return ""
}

// GetPassword returns the stored password for an account ("" if none),
// letting the frontend prefill when an account chip is selected.
func (a *App) GetPassword(username string) string {
	pw, err := creds.Get(username)
	if err != nil {
		return ""
	}
	return pw
}

// Play starts the patch-login-launch sequence in the background. Progress
// and errors are delivered as "log", "busy", and "failed" events.
func (a *App) Play(username, password string, remember, skipPatch bool) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		a.fail(errors.New("enter a username and password"))
		return
	}
	if !a.setBusy(true) {
		return
	}

	// Persist the toggle states so they survive restarts (best-effort;
	// a config write failure shouldn't block launching).
	a.cfg.SkipPatchCheck = skipPatch
	a.cfg.RememberMe = &remember
	a.cfg.Save()

	go func() {
		defer a.setBusy(false)

		if remember {
			if err := creds.Set(username, password); err != nil {
				a.fail(fmt.Errorf("storing password: %w", err))
				return
			}
			a.cfg.AddAccount(username)
			if err := a.cfg.Save(); err != nil {
				a.fail(fmt.Errorf("saving config: %w", err))
				return
			}
			runtime.EventsEmit(a.ctx, "accounts", a.GetState().Accounts)
		}

		err := launcherapp.Play(context.Background(), a.cfg, launcherapp.PlayOptions{
			Username: username,
			Password: password,
			NoPatch:  skipPatch,
			Log:      a.log,
		})
		if err != nil {
			a.fail(err)
			return
		}
		a.log("Game launched.")
		if a.cfg.MinimizeOnLaunch {
			runtime.WindowMinimise(a.ctx)
		}
		runtime.EventsEmit(a.ctx, "launched")
	}()
}

// Patch runs only the patch stage in the background.
func (a *App) Patch() {
	if !a.setBusy(true) {
		return
	}
	go func() {
		defer a.setBusy(false)
		if err := launcherapp.Patch(context.Background(), a.cfg, false, a.log); err != nil {
			a.fail(err)
		}
	}()
}

// GetNews returns recent Wizard101 news items for the news panel.
func (a *App) GetNews() ([]news.Item, error) {
	return news.Fetch(a.ctx)
}

// OpenURL opens a link in the system browser (news items, etc).
func (a *App) OpenURL(url string) {
	runtime.BrowserOpenURL(a.ctx, url)
}

// OpenNewsWindow launches a separate window showing the official news page.
func (a *App) OpenNewsWindow() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return exec.Command(exe, "news-window").Start()
}

// Minimize / Close back the custom title bar buttons.
func (a *App) Minimize() { runtime.WindowMinimise(a.ctx) }
func (a *App) Close()    { runtime.Quit(a.ctx) }

func (a *App) setBusy(busy bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if busy && a.busy {
		return false
	}
	a.busy = busy
	runtime.EventsEmit(a.ctx, "busy", busy)
	return true
}

func (a *App) log(msg string) {
	runtime.EventsEmit(a.ctx, "log", msg)
}

func (a *App) fail(err error) {
	runtime.EventsEmit(a.ctx, "failed", err.Error())
	a.log("error: " + err.Error())
}
