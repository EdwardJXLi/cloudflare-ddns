package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	HubURL            string
	Token             string
	Subdomain         string
	IPv4Provider      string
	Interval          time.Duration
	AllowInsecureHTTP bool
}

type Agent struct {
	config Config
	client *http.Client
	logger *slog.Logger
}

func New(config Config, logger *slog.Logger) (*Agent, error) {
	if config.Token == "" {
		return nil, errors.New("CLIENT_TOKEN is required")
	}
	config.Subdomain = strings.ToLower(strings.TrimSpace(config.Subdomain))
	if config.Subdomain == "" {
		return nil, errors.New("SUBDOMAIN is required")
	}
	hub, err := url.Parse(config.HubURL)
	if err != nil || hub.Host == "" {
		return nil, errors.New("HUB_URL must be a valid absolute URL")
	}

	if hub.Scheme != "https" && !(config.AllowInsecureHTTP && hub.Scheme == "http") {
		return nil, errors.New("HUB_URL must use https (or explicitly set ALLOW_INSECURE_HTTP=true)")
	}

	config.HubURL = strings.TrimRight(config.HubURL, "/")
	if config.Interval <= 0 {
		return nil, errors.New("UPDATE_INTERVAL must be positive")
	}

	return &Agent{
		config: config,
		client: &http.Client{Timeout: 20 * time.Second},
		logger: logger,
	}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	a.runAndLog(ctx)

	ticker := time.NewTicker(a.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.runAndLog(ctx)
		}
	}
}

func (a *Agent) runAndLog(ctx context.Context) {
	if err := a.Update(ctx); err != nil && !errors.Is(err, context.Canceled) {
		a.logger.Error("DDNS update failed", "error", err)
	}
}

func (a *Agent) Update(ctx context.Context) error {
	address, err := a.discover(ctx, a.config.IPv4Provider)
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]string{
		"address":   address,
		"subdomain": a.config.Subdomain,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.HubURL+"/v1/update", bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+a.config.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("contact hub: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hub returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var result struct {
		Result struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return errors.New("hub returned an invalid response")
	}

	a.logger.Info("DNS record checked", "record", result.Result.Name, "status", result.Result.Status)

	return nil
}

func (a *Agent) discover(ctx context.Context, endpoint string) (string, error) {
	if endpoint == "" {
		return "", errors.New("IPV4_PROVIDER is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(a.config.AllowInsecureHTTP && parsed.Scheme == "http")) {
		return "", errors.New("invalid IPv4 provider URL")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "text/plain")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("discover IPv4 address: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discover IPv4 address: provider returned HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}

	ip := net.ParseIP(strings.TrimSpace(string(raw)))
	if ip == nil || ip.To4() == nil {
		return "", errors.New("IPv4 provider returned an invalid address")
	}

	return ip.To4().String(), nil
}
