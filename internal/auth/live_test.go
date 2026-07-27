package auth

// Live integration test for Steam-linked account login (IsSteamPatcher=1).
// Only runs when W101_LIVE_TEST=1 is set. Uses the account stored in the OS
// credential store; never prints credentials or tokens.

import (
	"context"
	"os"
	"testing"

	"github.com/GhostNoodl/sigil/internal/config"
	"github.com/GhostNoodl/sigil/internal/creds"
)

func TestLiveSteamLogin(t *testing.T) {
	if os.Getenv("W101_LIVE_TEST") != "1" {
		t.Skip("set W101_LIVE_TEST=1 to run against the live login server")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	username, err := cfg.ResolveAccount("")
	if err != nil {
		t.Fatalf("resolving account: %v", err)
	}
	password, err := creds.Get(username)
	if err != nil {
		t.Fatalf("no stored credential for %s: %v", username, err)
	}

	session, err := Login(context.Background(), Options{
		Server:       cfg.LoginServer,
		Username:     username,
		Password:     password,
		Locale:       "English",
		SteamPatcher: true,
	})
	if err != nil {
		t.Fatalf("Login with IsSteamPatcher=1 failed: %v", err)
	}
	t.Logf("login OK, userID %d, CK2 length %d", session.UserID, len(session.CK2))
}
