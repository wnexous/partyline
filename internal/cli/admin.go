package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/app"
	"github.com/wnexous/partyline/internal/config"
	"github.com/wnexous/partyline/internal/users"
	"github.com/wnexous/partyline/internal/validation"
)

const adminUsage = `Usage:
  partyline admin create-user -username NAME [-display-name NAME] [-admin] < password
  partyline admin reset-password -username NAME < password

The password is the first line of stdin, so it never shows up in the process list.
`

// admin runs the account subcommands against the data directory, without the HTTP server.
func admin(ctx context.Context, args []string, io stdio) int {
	if len(args) == 0 || (args[0] != "create-user" && args[0] != "reset-password") {
		fmt.Fprint(io.stderr, adminUsage)
		return 2
	}
	fs := flag.NewFlagSet("admin "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	username := fs.String("username", "", "account username")
	display := fs.String("display-name", "", "display name (create-user)")
	isAdmin := fs.Bool("admin", false, "give the account the admin role (create-user)")
	if err := fs.Parse(args[1:]); err != nil || *username == "" {
		fmt.Fprint(io.stderr, adminUsage)
		return 2
	}
	password, err := readPassword(io.stdin)
	if err != nil {
		fmt.Fprintln(io.stderr, "read password from stdin:", err)
		return 1
	}

	cfg, err := config.Load(io.getenv)
	if err != nil {
		fmt.Fprintln(io.stderr, "config:", err)
		return 1
	}
	a, err := app.New(cfg, app.Options{Logger: newLogger(cfg, io.stderr).With(slog.String("command", "admin"))})
	if err != nil {
		fmt.Fprintln(io.stderr, "open:", err)
		return 1
	}
	defer func() { _ = a.Shutdown(context.Background()) }()
	if err := a.Store.Migrate(ctx); err != nil {
		fmt.Fprintln(io.stderr, "migrate:", err)
		return 1
	}

	switch args[0] {
	case "create-user":
		role := dto.RoleMember
		if *isAdmin {
			role = dto.RoleAdmin
		}
		u, err := a.Users.CreateUser(ctx, users.AccountInput{Username: *username, Password: password, DisplayName: *display}, role)
		if err != nil {
			fmt.Fprintln(io.stderr, "create-user:", describe(err))
			return 1
		}
		fmt.Fprintf(io.stdout, "created %s account %q\n", u.Role, u.Username)
	case "reset-password":
		if err := a.Users.ResetPassword(ctx, *username, password); err != nil {
			fmt.Fprintln(io.stderr, "reset-password:", describe(err))
			return 1
		}
		fmt.Fprintf(io.stdout, "password of %q reset; its sessions were ended\n", *username)
	}
	return 0
}

func readPassword(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// describe renders service errors for the terminal, naming rejected fields.
func describe(err error) string {
	var verr *validation.Error
	if errors.As(err, &verr) {
		parts := make([]string, 0, len(verr.Fields))
		for f, reason := range verr.Fields {
			parts = append(parts, f+" "+reason)
		}
		sort.Strings(parts)
		return strings.Join(parts, "; ")
	}
	return err.Error()
}
