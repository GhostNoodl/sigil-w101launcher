// Package launch starts WizardGraphicalClient.exe with the same arguments
// the official launcher uses, skipping the patcher and login UI.
//
// On Steam installs it additionally passes -ST and the Steam app-id
// environment so the client can perform Steam ticket auth — without these,
// the login server rejects the session with an "account mismatch" error.
package launch

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/GhostNoodl/sigil-w101launcher/internal/auth"
)

// steamAppID is Wizard101's app id on Steam (US / KingsIsle build).
const steamAppID = "799960"

// IsSteamInstall reports whether the install is the Steam build of the game.
func IsSteamInstall(installDir string) bool {
	for _, rel := range []string{
		filepath.Join("Bin", "steam_api64.dll"),
		filepath.Join("Bin", "steam_api.dll"),
		"steam_api.dll",
	} {
		if _, err := os.Stat(filepath.Join(installDir, rel)); err == nil {
			return true
		}
	}
	return false
}

// Game starts the client for an authenticated session.
//
//	installDir: Wizard101 install root (containing Bin\)
//	loginServer: host:port the client should connect to
//	locale: client locale arg (e.g. "en-US")
//	username: account username (used only with -U)
//	session: token material from auth.Login; nil for Steam installs,
//	         which authenticate via Steam ticket instead
func Game(installDir, loginServer, locale, username string, session *auth.Session) error {
	host, port, err := net.SplitHostPort(loginServer)
	if err != nil {
		return fmt.Errorf("invalid login server address %q: %w", loginServer, err)
	}

	binDir := filepath.Join(installDir, "Bin")
	exe := filepath.Join(binDir, "WizardGraphicalClient.exe")
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("game client not found at %s (install dir wrong, or patch needed)", exe)
	}

	// Mirrors the official launcher:
	//   -L <host> <port> -A <locale> [-ST -P 0 -PT 0] [-U ..<userID> <ck2> <username>]
	// -ST enables Steam integration; -P/-PT are platform flags the official
	// launcher always sets. -U is only passed for password-authenticated
	// sessions; Steam installs authenticate via ticket instead.
	args := []string{"-L", host, port, "-A", locale}
	steam := IsSteamInstall(installDir)
	if steam {
		args = append(args, "-ST", "-P", "0", "-PT", "0")
	}
	if session != nil {
		args = append(args, "-U", ".."+fmt.Sprint(session.UserID), session.CK2, username)
	}

	cmd := exec.Command(exe, args...)
	cmd.Dir = binDir // the client resolves its data files relative to Bin
	if steam {
		// Steam-launched processes carry these in their environment; the
		// client needs them to initialize steam_api for ticket auth.
		cmd.Env = append(os.Environ(),
			"SteamAppId="+steamAppID,
			"SteamGameId="+steamAppID,
		)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting game client: %w", err)
	}

	// Detach: the game keeps running after the launcher exits.
	return cmd.Process.Release()
}
