package userhelp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	oblog "github.com/ivantit66/onebase/internal/logging"
)

type ConfluenceOptions struct {
	// BaseURL includes any context path, e.g. https://host/confluence or /wiki.
	BaseURL, Space, User, Token string
	Cloud                       bool
	Client                      *http.Client
}

type Confluence struct {
	opts   ConfluenceOptions
	api    string
	client *http.Client
}

var remoteID = regexp.MustCompile(`^[0-9]+$`)

func NewConfluence(opts ConfluenceOptions) (*Confluence, error) {
	u, err := url.Parse(opts.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("Confluence URL must be an HTTP(S) base URL without credentials, query or fragment")
	}
	if opts.Space == "" || opts.Token == "" {
		return nil, fmt.Errorf("Confluence space and token are required")
	}
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	api := opts.BaseURL + "/rest/api"
	if opts.Cloud {
		// Cloud v2 uses numeric space IDs; Server/DC uses a space key.
		if !remoteID.MatchString(opts.Space) {
			return nil, fmt.Errorf("Confluence Cloud requires a numeric space ID")
		}
		api = opts.BaseURL + "/api/v2"
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	// Do not forward credentials to another origin or silently rewrite POSTs.
	copyClient := *client
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Confluence{opts: opts, api: api, client: &copyClient}, nil
}

func (c *Confluence) Target(root string) string {
	return fmt.Sprintf("%s|%t|%s|%s", c.opts.BaseURL, c.opts.Cloud, c.opts.Space, root)
}

func (c *Confluence) PageURL(id string) string { return escapedPageURL(c.opts.BaseURL, id) }

func (c *Confluence) endpoint(id string) (string, error) {
	if !remoteID.MatchString(id) {
		return "", fmt.Errorf("invalid page ID %q", id)
	}
	if c.opts.Cloud {
		return c.api + "/pages/" + id, nil
	}
	return c.api + "/content/" + id, nil
}

func (c *Confluence) ReadPage(ctx context.Context, id string) (RemotePage, error) {
	u, err := c.endpoint(id)
	if err != nil {
		return RemotePage{}, err
	}
	var response pageResponse
	err = c.request(ctx, http.MethodGet, u, nil, &response)
	return response.remote(), err
}

func (c *Confluence) CreatePage(ctx context.Context, parent, title string) (RemotePage, error) {
	if !remoteID.MatchString(parent) {
		return RemotePage{}, fmt.Errorf("invalid parent page ID")
	}
	var payload map[string]any
	var endpoint string
	if c.opts.Cloud {
		endpoint = c.api + "/pages"
		payload = map[string]any{"spaceId": c.opts.Space, "parentId": parent, "status": "current", "title": title,
			"body": map[string]any{"representation": "storage", "value": "<p>OneBase</p>"}}
	} else {
		endpoint = c.api + "/content"
		payload = map[string]any{"type": "page", "title": title, "space": map[string]string{"key": c.opts.Space},
			"ancestors": []map[string]string{{"id": parent}}, "body": map[string]any{"storage": map[string]string{"representation": "storage", "value": "<p>OneBase</p>"}}}
	}
	var response pageResponse
	err := c.request(ctx, http.MethodPost, endpoint, payload, &response)
	return response.remote(), err
}

func (c *Confluence) UpdatePage(ctx context.Context, page RemotePage, title, body string) (RemotePage, error) {
	u, err := c.endpoint(page.ID)
	if err != nil {
		return RemotePage{}, err
	}
	payload := map[string]any{"id": page.ID, "title": title, "version": map[string]int{"number": page.Version + 1}}
	if c.opts.Cloud {
		payload["status"] = "current"
		payload["body"] = map[string]string{"representation": "storage", "value": body}
	} else {
		payload["type"] = "page"
		payload["body"] = map[string]any{"storage": map[string]string{"representation": "storage", "value": body}}
	}
	var response pageResponse
	err = c.request(ctx, http.MethodPut, u, payload, &response)
	return response.remote(), err
}

func (c *Confluence) PutImage(ctx context.Context, pageID, name string, asset Asset) error {
	if !remoteID.MatchString(pageID) || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid attachment target")
	}
	// Attachments use the v1 API on both Cloud and Server/DC. Content-hash
	// filenames let retries reuse existing attachments without new versions.
	endpoint := c.opts.BaseURL + "/rest/api/content/" + pageID + "/child/attachment"
	var existing struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := c.request(ctx, http.MethodGet, endpoint+"?filename="+url.QueryEscape(name), nil, &existing); err != nil {
		return err
	}
	if len(existing.Results) > 0 {
		return nil
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(asset.Data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "nocheck")
	return c.send(req, nil)
}

type pageResponse struct {
	ID      string `json:"id"`
	Version struct {
		Number int `json:"number"`
	} `json:"version"`
}

func (p pageResponse) remote() RemotePage { return RemotePage{ID: p.ID, Version: p.Version.Number} }

func (c *Confluence) request(ctx context.Context, method, endpoint string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.send(req, out)
}

func (c *Confluence) send(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	if c.opts.User != "" {
		req.SetBasicAuth(c.opts.User, c.opts.Token)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.opts.Token)
	}
	resp, err := c.client.Do(req) //nolint:gosec // G704: destination is the explicit administrator-selected Confluence base URL; this client never handles incoming web requests and rejects redirects.
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			oblog.Component("userhelp").Warn("close Confluence response", "error", err)
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not include response bodies: upstream errors may echo credentials.
		return fmt.Errorf("Confluence %s %s: HTTP %d", req.Method, req.URL.Path, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
			return err
		}
		if p, ok := out.(*pageResponse); ok && (!remoteID.MatchString(p.ID) || p.Version.Number < 1) {
			return fmt.Errorf("Confluence returned an invalid page ID or version")
		}
	}
	return nil
}
