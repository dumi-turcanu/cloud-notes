package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// userCreator is what create-user needs from the auth service.
type userCreator interface {
	CreateUser(ctx context.Context, email, displayName, password string) (*models.UserAccount, error)
}

// creatorFactory opens the database and returns the creator plus a cleanup func.
// It is only called after flags and the password were read successfully.
type creatorFactory func(ctx context.Context) (userCreator, func(), error)

const maxPasswordInput = 1024

// createUser implements `create-user --email E [--display-name N]
// [--if-not-exists] [--password-stdin]`. The password comes from
// NEW_USER_PASSWORD / NEW_USER_PASSWORD_FILE or stdin — never from a flag, so
// it cannot leak through the process list or shell history.
func createUser(ctx context.Context, args []string, getenv func(string) string,
	readFile func(string) ([]byte, error), stdin io.Reader, stdout, stderr io.Writer, open creatorFactory) int {

	fs := flag.NewFlagSet("create-user", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "email address of the new user (required)")
	displayName := fs.String("display-name", "", "optional display name")
	ifNotExists := fs.Bool("if-not-exists", false, "exit 0 if a user with this email already exists")
	fromStdin := fs.Bool("password-stdin", false, "read the password from the first line of stdin")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return exitUsage
	}
	if strings.TrimSpace(*email) == "" {
		fmt.Fprintln(stderr, "create-user: --email is required")
		return exitUsage
	}

	// Passwords belong to the identity provider in the external modes. An
	// unrecognised value is refused too: only an explicit local deployment
	// may create password users.
	if provider := strings.ToLower(strings.TrimSpace(getenv("AUTH_PROVIDER"))); provider != "" && provider != config.AuthLocal {
		fmt.Fprintf(stderr, "create-user: not available when AUTH_PROVIDER=%s: users sign in through the identity provider and are created on first login\n", provider)
		return exitError
	}

	var password string
	if *fromStdin {
		line, err := bufio.NewReader(io.LimitReader(stdin, maxPasswordInput)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintf(stderr, "create-user: read password from stdin: %v\n", err)
			return exitError
		}
		password = strings.TrimRight(line, "\r\n")
	} else {
		p, err := config.NewUserPassword(getenv, readFile)
		if err != nil {
			fmt.Fprintf(stderr, "create-user: %v\n", err)
			return exitError
		}
		password = p
	}
	if password == "" {
		fmt.Fprintln(stderr, "create-user: no password: set NEW_USER_PASSWORD (or NEW_USER_PASSWORD_FILE) or use --password-stdin")
		return exitUsage
	}

	svc, closeFn, err := open(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "create-user: %v\n", err)
		return exitError
	}
	defer closeFn()

	user, err := svc.CreateUser(ctx, *email, *displayName, password)
	var verr *auth.ValidationError
	switch {
	case err == nil:
		fmt.Fprintf(stdout, "created user %s (%s)\n", user.ID, user.Email)
		return exitOK
	case errors.Is(err, models.ErrEmailTaken) && *ifNotExists:
		fmt.Fprintln(stdout, "user already exists, nothing to do")
		return exitOK
	case errors.Is(err, models.ErrEmailTaken):
		fmt.Fprintln(stderr, "create-user: a user with this email already exists")
		return exitError
	case errors.As(err, &verr):
		for field, msg := range verr.Fields {
			fmt.Fprintf(stderr, "create-user: invalid %s: %s\n", field, msg)
		}
		return exitError
	default:
		fmt.Fprintf(stderr, "create-user: %v\n", err)
		return exitError
	}
}
