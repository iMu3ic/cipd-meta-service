package mirror

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/iMu3ic/cipd-meta-service/internal/index"
)

// Runner runs an external command and returns its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// CLI is an Upstream that drives the real cipd client.
type CLI struct {
	// Binary is the path to the cipd client; "cipd" if empty.
	Binary string
	// ServiceURL is the upstream CIPD backend; the client's default if empty.
	ServiceURL string
	// Run runs commands; a real exec if nil. Tests replace it.
	Run Runner
}

func (c *CLI) run(ctx context.Context, args ...string) ([]byte, error) {
	bin := c.Binary
	if bin == "" {
		bin = "cipd"
	}
	if c.ServiceURL != "" {
		args = append(args, "-service-url", c.ServiceURL)
	}
	run := c.Run
	if run == nil {
		run = execRunner
	}
	return run(ctx, bin, args...)
}

// Resolve runs "cipd resolve <pkg> -version <version>".
func (c *CLI) Resolve(ctx context.Context, pkg, version string) (string, error) {
	out, err := c.run(ctx, "resolve", pkg, "-version", version)
	if err != nil {
		return "", err
	}
	return parseResolve(out)
}

// Fetch runs "cipd pkg-fetch <pkg> -version <instance ID> -out <dst>". The
// instance ID, not the original version, is used so that the file is exactly
// the instance that Resolve returned.
func (c *CLI) Fetch(ctx context.Context, pkg, instanceID, dst string) error {
	_, err := c.run(ctx, "pkg-fetch", pkg, "-version", instanceID, "-out", dst)
	return err
}

// parseResolve extracts the instance ID from the last line of the output of
// "cipd resolve", which looks like "<package>:<instance ID>".
func parseResolve(out []byte) (string, error) {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", fmt.Errorf("cipd resolve printed nothing")
	}
	lines := strings.Split(text, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	i := strings.LastIndex(last, ":")
	if i < 0 {
		return "", fmt.Errorf("cannot parse cipd resolve output %q", last)
	}
	id := last[i+1:]
	if _, ok := index.HexFromInstanceID(id); !ok {
		return "", fmt.Errorf("cipd resolve returned %q, which is not a SHA-256 instance ID", id)
	}
	return id, nil
}
