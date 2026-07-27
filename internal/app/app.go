// Package app holds the launch sequence shared by the CLI and the GUI:
// patch check, login-server authentication, and starting the game client.
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/GhostNoodl/sigil/internal/auth"
	"github.com/GhostNoodl/sigil/internal/config"
	"github.com/GhostNoodl/sigil/internal/launch"
	"github.com/GhostNoodl/sigil/internal/patch"
)

type PlayOptions struct {
	Username string
	Password string
	NoPatch  bool
	Full     bool
	// Log receives progress lines. Nil-safe; defaults to discarding.
	Log func(string)
}

func (o *PlayOptions) logf(format string, args ...any) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(format, args...))
	}
}

// Patch runs only the patch stage (no login, no launch).
func Patch(ctx context.Context, cfg *config.Config, full bool, log func(string)) error {
	if cfg.InstallDir == "" {
		return errors.New("install directory not found; set it with 'sigil config set-dir <path>'")
	}
	opts := PlayOptions{Log: log}
	opts.logf("Checking for updates...")
	err := patch.Run(ctx, patch.Options{
		Dir:          cfg.InstallDir,
		Server:       cfg.PatchServer,
		Full:         full,
		ProgressFunc: log,
	})
	if err != nil {
		return fmt.Errorf("patch failed: %w", err)
	}
	opts.logf("Patch complete.")
	return nil
}

// Play runs the full launch sequence against cfg's servers and install dir.
//
// Steam installs authenticate with IsSteamPatcher set (Steam-linked
// accounts reject plain password logins with AccountClientMismatch) and
// launch the client with -ST plus the Steam app-id environment so it can
// complete Steam ticket validation.
func Play(ctx context.Context, cfg *config.Config, opts PlayOptions) error {
	if cfg.InstallDir == "" {
		return errors.New("install directory not found; set it with 'sigil config set-dir <path>'")
	}
	if opts.Username == "" {
		return errors.New("no username")
	}

	if !opts.NoPatch {
		opts.logf("Checking for updates...")
		err := patch.Run(ctx, patch.Options{
			Dir:          cfg.InstallDir,
			Server:       cfg.PatchServer,
			Full:         opts.Full,
			ProgressFunc: opts.Log,
		})
		if err != nil {
			return fmt.Errorf("patch failed: %w", err)
		}
		opts.logf("All files up to date.")
	}

	steam := launch.IsSteamInstall(cfg.InstallDir)

	opts.logf("Logging in as %s...", opts.Username)
	session, err := auth.Login(ctx, auth.Options{
		Server:       cfg.LoginServer,
		Username:     opts.Username,
		Password:     opts.Password,
		Locale:       "English", // auth handshake locale; the client's -A arg uses cfg.Locale
		SteamPatcher: steam,
	})
	if err != nil {
		return err
	}

	if steam {
		opts.logf("Steam install detected; launching with Steam ticket support (Steam must be running).")
	}

	opts.logf("Launching game...")
	return launch.Game(cfg.InstallDir, cfg.LoginServer, cfg.Locale, opts.Username, session)
}
