// Package nexus is a minimal client for a Nexus raw repository. It implements
// storage.Storage over plain HTTP GET, HEAD and PUT.
package nexus

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/iMu3ic/cipd-meta-service/internal/storage"
)

// Client talks to one raw repository, for example
// https://nexus.example/repository/cipd-packages.
type Client struct {
	base     string
	user     string
	password string
	hc       *http.Client
}

// New returns a client for the repository at baseURL. Empty user means
// anonymous access.
func New(baseURL, user, password string) *Client {
	return &Client{
		base:     strings.TrimRight(baseURL, "/"),
		user:     user,
		password: password,
		hc:       &http.Client{Timeout: 15 * time.Minute},
	}
}

// URL returns the absolute URL of a file in the repository.
func (c *Client) URL(rel string) (string, error) {
	if !storage.ValidPath(rel) {
		return "", fmt.Errorf("invalid storage path %q", rel)
	}
	u := url.URL{Path: "/" + rel}
	return c.base + u.EscapedPath(), nil
}

func (c *Client) do(ctx context.Context, method, rel string, body io.Reader, size int64) (*http.Response, error) {
	target, err := c.URL(rel)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.password)
	}
	return c.hc.Do(req)
}

func statusError(method, rel string, resp *http.Response) error {
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("nexus %s %s: %s: %s", method, rel, resp.Status, strings.TrimSpace(string(msg)))
}

func (c *Client) Get(ctx context.Context, rel string) (io.ReadCloser, error) {
	resp, err := c.do(ctx, http.MethodGet, rel, nil, 0)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, storage.ErrNotFound
	default:
		return nil, statusError(http.MethodGet, rel, resp)
	}
}

func (c *Client) Exists(ctx context.Context, rel string) (bool, error) {
	resp, err := c.do(ctx, http.MethodHead, rel, nil, 0)
	if err != nil {
		return false, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		resp.Body.Close()
		return true, nil
	case http.StatusNotFound:
		resp.Body.Close()
		return false, nil
	default:
		return false, statusError(http.MethodHead, rel, resp)
	}
}

// Put uploads a file. A repository that forbids redeploy rejects an upload to
// an existing path; that error is returned as is.
func (c *Client) Put(ctx context.Context, rel string, r io.Reader, size int64) error {
	resp, err := c.do(ctx, http.MethodPut, rel, r, size)
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		resp.Body.Close()
		return nil
	default:
		return statusError(http.MethodPut, rel, resp)
	}
}
