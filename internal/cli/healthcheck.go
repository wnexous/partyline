package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// healthcheck requests /healthz on the local server (PARTYLINE_ADDR) and exits 0 on 200,
// 1 otherwise. The container image uses it as its HEALTHCHECK (distroless has no curl).
func healthcheck(ctx context.Context, io stdio) int {
	addr := strings.TrimSpace(io.getenv("PARTYLINE_ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintln(io.stderr, "PARTYLINE_ADDR:", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		fmt.Fprintln(io.stderr, err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(io.stderr, "unhealthy:", err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(io.stderr, "unhealthy: status", resp.StatusCode)
		return 1
	}
	return 0
}
