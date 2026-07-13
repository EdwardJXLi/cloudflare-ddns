package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AutomaticTTL asks Cloudflare to manage the record's TTL automatically.
const AutomaticTTL = 1

type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied bool   `json:"proxied,omitempty"`
}

type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Result struct {
	Changed bool
	Created bool
}

type envelope struct {
	Success bool            `json:"success"`
	Errors  []responseError `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func New(token string) *Client {
	return &Client{
		token:      token,
		baseURL:    "https://api.cloudflare.com/client/v4",
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func NewWithEndpoint(token, endpoint string, httpClient *http.Client) *Client {
	return &Client{token: token, baseURL: strings.TrimRight(endpoint, "/"), httpClient: httpClient}
}

func (c *Client) ResolveZone(ctx context.Context, name string) (string, error) {
	params := url.Values{"name": {name}, "status": {"active"}}

	var zones []Zone
	if err := c.request(ctx, http.MethodGet, "/zones?"+params.Encode(), nil, &zones); err != nil {
		return "", fmt.Errorf("find Cloudflare zone %q: %w", name, err)
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("Cloudflare zone %q was not found or is not active", name)
	}
	if len(zones) > 1 {
		return "", fmt.Errorf("multiple Cloudflare zones named %q were returned", name)
	}
	if !strings.EqualFold(zones[0].Name, name) {
		return "", fmt.Errorf("Cloudflare returned an unexpected zone %q", zones[0].Name)
	}

	return zones[0].ID, nil
}

func (c *Client) Upsert(ctx context.Context, zoneID string, desired Record) (Result, error) {
	params := url.Values{"type": {desired.Type}, "name": {desired.Name}}
	path := fmt.Sprintf("/zones/%s/dns_records?%s", url.PathEscape(zoneID), params.Encode())

	var existing []Record
	if err := c.request(ctx, http.MethodGet, path, nil, &existing); err != nil {
		return Result{}, fmt.Errorf("find DNS record: %w", err)
	}
	if len(existing) > 1 {
		return Result{}, fmt.Errorf("found %d %s records named %s; refusing ambiguous update", len(existing), desired.Type, desired.Name)
	}

	if len(existing) == 0 {
		var created Record
		if err := c.request(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", url.PathEscape(zoneID)), desired, &created); err != nil {
			return Result{}, fmt.Errorf("create DNS record: %w", err)
		}
		return Result{Changed: true, Created: true}, nil
	}

	current := existing[0]
	if current.Content == desired.Content {
		return Result{}, nil
	}

	var updated Record
	path = fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(zoneID), url.PathEscape(current.ID))
	patch := struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}{Type: desired.Type, Name: desired.Name, Content: desired.Content}
	if err := c.request(ctx, http.MethodPatch, path, patch, &updated); err != nil {
		return Result{}, fmt.Errorf("update DNS record: %w", err)
	}

	return Result{Changed: true}, nil
}

func (c *Client) request(ctx context.Context, method, path string, body any, output any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var wrapped envelope
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return fmt.Errorf("Cloudflare returned HTTP %d with an invalid response", resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !wrapped.Success {
		messages := make([]string, 0, len(wrapped.Errors))
		for _, item := range wrapped.Errors {
			messages = append(messages, fmt.Sprintf("%d: %s", item.Code, item.Message))
		}
		if len(messages) == 0 {
			messages = append(messages, http.StatusText(resp.StatusCode))
		}
		return errors.New(strings.Join(messages, "; "))
	}

	if output != nil && len(wrapped.Result) != 0 && string(wrapped.Result) != "null" {
		if err := json.Unmarshal(wrapped.Result, output); err != nil {
			return fmt.Errorf("decode Cloudflare result: %w", err)
		}
	}

	return nil
}
