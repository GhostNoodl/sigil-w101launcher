// sigil is a custom Wizard101 launcher: fast incremental patching and
// one-click login without the official launcher's full file validation.
//
// Run without arguments to open the GUI launcher window; subcommands give
// the same functionality from a terminal.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/GhostNoodl/sigil/internal/app"
	"github.com/GhostNoodl/sigil/internal/config"
	"github.com/GhostNoodl/sigil/internal/creds"
	"github.com/GhostNoodl/sigil/internal/webui"
)

const usage = `sigil - custom Wizard101 launcher (US servers)

Usage:
  sigil                                 open the launcher window
  sigil     play [username] [--no-patch] [--full]
  sigil     patch [--full]
  sigil     accounts add <username>
  sigil     accounts remove <username>
  sigil     accounts list
  sigil     config show
  sigil     config set-dir <install dir>

Passwords are stored in Windows Credential Manager, never on disk.
`

func main() {
	// No arguments (e.g. double-clicked): open the launcher window.
	// With arguments, we're in CLI mode: reclaim the parent console so
	// output is visible despite the windowsgui subsystem build.
	if len(os.Args) < 2 {
		runGUI()
		return
	}
	attachParentConsole()

	var err error
	switch os.Args[1] {
	case "gui":
		runGUI()
	case "news-window":
		err = webui.RunNewsWindow()
	case "play":
		err = cmdPlay(os.Args[2:])
	case "patch":
		err = cmdPatch(os.Args[2:])
	case "accounts":
		err = cmdAccounts(os.Args[2:])
	case "config":
		err = cmdConfig(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runGUI() {
	if err := webui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdPlay(args []string) error {
	fs := flag.NewFlagSet("play", flag.ExitOnError)
	noPatch := fs.Bool("no-patch", false, "skip the patch check entirely")
	full := fs.Bool("full", false, "verify/download all packages, not just installed ones")
	fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	username, err := cfg.ResolveAccount(fs.Arg(0))
	if err != nil {
		return err
	}

	password, err := creds.Get(username)
	if errors.Is(err, creds.ErrNotFound) {
		return fmt.Errorf("no stored password for %s; run 'sigil accounts add %s' first (or use the GUI)", username, username)
	}
	if err != nil {
		return err
	}

	return app.Play(context.Background(), cfg, app.PlayOptions{
		Username: username,
		Password: password,
		NoPatch:  *noPatch,
		Full:     *full,
		Log:      func(msg string) { fmt.Println(msg) },
	})
}

func cmdPatch(args []string) error {
	fs := flag.NewFlagSet("patch", flag.ExitOnError)
	full := fs.Bool("full", false, "verify/download all packages, not just installed ones")
	fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	return app.Patch(context.Background(), cfg, *full, func(msg string) { fmt.Println(msg) })
}

func cmdAccounts(args []string) error {
	if len(args) < 1 {
		return errors.New("expected 'add', 'remove', or 'list'")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			return errors.New("usage: sigil accounts add <username>")
		}
		username := args[1]

		password, err := promptPassword(fmt.Sprintf("Password for %s: ", username))
		if err != nil {
			return err
		}
		if password == "" {
			return errors.New("empty password")
		}

		if err := creds.Set(username, password); err != nil {
			return fmt.Errorf("storing password: %w", err)
		}
		cfg.AddAccount(username)
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("Account %s saved (password stored in Windows Credential Manager).\n", username)

	case "remove":
		if len(args) < 2 {
			return errors.New("usage: sigil accounts remove <username>")
		}
		username := args[1]

		if err := creds.Delete(username); err != nil {
			return fmt.Errorf("deleting password: %w", err)
		}
		cfg.RemoveAccount(username)
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("Account %s removed.\n", username)

	case "list":
		if len(cfg.Accounts) == 0 {
			fmt.Println("No accounts configured.")
			return nil
		}
		for _, u := range cfg.Accounts {
			if u == cfg.DefaultAccount {
				fmt.Printf("* %s (default)\n", u)
			} else {
				fmt.Printf("  %s\n", u)
			}
		}

	default:
		return fmt.Errorf("unknown accounts subcommand %q", args[0])
	}
	return nil
}

func cmdConfig(args []string) error {
	if len(args) < 1 {
		return errors.New("expected 'show' or 'set-dir'")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch args[0] {
	case "show":
		path, _ := config.Path()
		fmt.Println("Config file:", path)
		fmt.Println("Install dir:", cfg.InstallDir)
		fmt.Println("Login server:", cfg.LoginServer)
		fmt.Println("Patch server:", cfg.PatchServer)
		fmt.Println("Locale:", cfg.Locale)

	case "set-dir":
		if len(args) < 2 {
			return errors.New("usage: sigil config set-dir <install dir>")
		}
		dir := args[1]
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("directory not accessible: %w", err)
		}
		cfg.InstallDir = dir
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println("Install dir set to", dir)

	default:
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
	return nil
}

// promptPassword reads a password without echo when attached to a terminal,
// falling back to a plain line read for piped stdin.
func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
