package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// runHealthcheckCmd is the `gosidian healthcheck` subcommand. It performs a
// GET against /healthz on the local server and exits 0/1 depending on the
// status field. Used as the container HEALTHCHECK since distroless images
// have no shell/curl available.
func runHealthcheckCmd(args []string) {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080/healthz", "healthcheck URL")
	timeout := fs.Duration("timeout", 2*time.Second, "request timeout")
	_ = fs.Parse(args)

	client := &http.Client{Timeout: *timeout}
	resp, err := client.Get(*url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d, body=%s\n", resp.StatusCode, body)
		os.Exit(1)
	}

	var out struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck: invalid json:", err)
		os.Exit(1)
	}
	if out.Status != "ok" {
		fmt.Fprintf(os.Stderr, "healthcheck: status=%q\n", out.Status)
		os.Exit(1)
	}
}
