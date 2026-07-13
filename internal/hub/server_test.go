package hub

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cloudflare-ddns/internal/cloudflare"
	"cloudflare-ddns/internal/store"
)

type fakeUpdater struct {
	records []cloudflare.Record
}

func (f *fakeUpdater) Upsert(_ context.Context, _ string, record cloudflare.Record) (cloudflare.Result, error) {
	f.records = append(f.records, record)
	return cloudflare.Result{Changed: true}, nil
}

func TestUpdateCanOnlyUseConfiguredRecords(t *testing.T) {
	credentials := store.New(filepath.Join(t.TempDir(), "clients.json"))
	token, err := credentials.Add("server-a")
	if err != nil {
		t.Fatal(err)
	}
	updater := &fakeUpdater{}
	server := httptest.NewServer(New(credentials, updater, "zone-id", "example.com", slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer server.Close()

	body := `{"address":"192.0.2.40","hostname":"server-b.example.com"}`
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/update", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest || len(updater.records) != 0 {
		t.Fatalf("status=%d records=%+v", response.StatusCode, updater.records)
	}

	body = `{"address":"192.0.2.40"}`
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/update", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	response, err = server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var output map[string]any
	json.NewDecoder(response.Body).Decode(&output)
	if response.StatusCode != http.StatusOK || len(updater.records) != 1 {
		t.Fatalf("status=%d records=%+v body=%+v", response.StatusCode, updater.records, output)
	}
	if updater.records[0].Name != "server-a.example.com" || updater.records[0].Content != "192.0.2.40" {
		t.Fatalf("record=%+v", updater.records[0])
	}
}

func TestUpdateRejectsBadToken(t *testing.T) {
	credentials := store.New(filepath.Join(t.TempDir(), "clients.json"))
	token, _ := credentials.Add("server-a")
	updater := &fakeUpdater{}
	server := httptest.NewServer(New(credentials, updater, "zone-id", "example.com", slog.Default()).Handler())
	defer server.Close()

	request := func(token string) int {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/update", strings.NewReader(`{"address":"192.0.2.1"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := request("wrong"); got != http.StatusUnauthorized {
		t.Fatalf("bad token status=%d", got)
	}
	if got := request(token); got != http.StatusOK {
		t.Fatalf("valid token status=%d", got)
	}
}

func TestValidateAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		valid   bool
	}{
		{name: "public IPv4", address: "8.8.8.8", valid: true},
		{name: "mapped IPv6", address: "::ffff:192.0.2.40"},
		{name: "IPv6", address: "2001:db8::1"},
		{name: "private", address: "192.168.1.10"},
		{name: "loopback", address: "127.0.0.1"},
		{name: "link local", address: "169.254.10.20"},
		{name: "unspecified", address: "0.0.0.0"},
		{name: "multicast", address: "224.0.0.1"},
		{name: "invalid", address: "not-an-address"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAddress(test.address)
			if (err == nil) != test.valid {
				t.Fatalf("validateAddress(%q) error = %v, valid = %t", test.address, err, test.valid)
			}
		})
	}
}
