package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const healthcheckTimeout = 3 * time.Second

// defaultHealthURL points at /readyz of the local main listener, derived from
// HTTP_ADDR (or PORT) exactly like the server does.
func defaultHealthURL(getenv func(string) string) string {
	addr := strings.TrimSpace(getenv("HTTP_ADDR"))
	if addr == "" {
		port := strings.TrimSpace(getenv("PORT"))
		if port == "" {
			port = "8080"
		}
		addr = ":" + port
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8080"
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/readyz"
}

// healthcheck implements `cloud-notes-api healthcheck [--url URL]`: exit 0 when
// the endpoint answers 200 within 3 seconds, 1 when it does not, 2 on bad usage.
// It exists because distroless images have no curl/wget.
func healthcheck(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "", "URL to probe (default: http://127.0.0.1:<port from HTTP_ADDR>/readyz)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return exitUsage
	}
	target := *url
	if target == "" {
		target = defaultHealthURL(getenv)
	}

	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: invalid URL: %v\n", err)
		return exitUsage
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return exitError
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck: %s returned %d\n", target, resp.StatusCode)
		return exitError
	}
	return exitOK
}
