package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpsertSkipsUnchangedRecord(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing authorization")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  []Record{{ID: "record-id", Type: "A", Name: "host.example.com", Content: "192.0.2.10", TTL: 1, Proxied: false}},
		})
	}))
	defer server.Close()
	client := NewWithEndpoint("secret", server.URL, server.Client())
	result, err := client.Upsert(context.Background(), "zone-id", Record{Type: "A", Name: "host.example.com", Content: "192.0.2.10", TTL: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || requests != 1 {
		t.Fatalf("result=%+v requests=%d", result, requests)
	}
}

func TestResolveZone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "site.com" || r.URL.Query().Get("status") != "active" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  []Zone{{ID: "zone-id", Name: "site.com"}},
		})
	}))
	defer server.Close()
	client := NewWithEndpoint("secret", server.URL, server.Client())
	zoneID, err := client.ResolveZone(context.Background(), "site.com")
	if err != nil {
		t.Fatal(err)
	}
	if zoneID != "zone-id" {
		t.Fatalf("zone ID = %q", zoneID)
	}
}

func TestUpsertCreatesMissingRecord(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []Record{}})
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": Record{ID: "new"}})
	}))
	defer server.Close()
	client := NewWithEndpoint("secret", server.URL, server.Client())
	result, err := client.Upsert(context.Background(), "zone-id", Record{Type: "A", Name: "host.example.com", Content: "192.0.2.10", TTL: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || !result.Created || requests != 2 {
		t.Fatalf("result=%+v requests=%d", result, requests)
	}
}

func TestUpsertChangesOnlyExistingRecordAddress(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result":  []Record{{ID: "record-id", Type: "A", Name: "host.example.com", Content: "192.0.2.1", TTL: 3600, Proxied: true}},
			})
			return
		}
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		if _, present := body["ttl"]; present {
			t.Errorf("PATCH unexpectedly changes TTL: %s", raw)
		}
		if _, present := body["proxied"]; present {
			t.Errorf("PATCH unexpectedly changes proxy setting: %s", raw)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": Record{ID: "record-id"}})
	}))
	defer server.Close()
	client := NewWithEndpoint("secret", server.URL, server.Client())
	result, err := client.Upsert(context.Background(), "zone-id", Record{Type: "A", Name: "host.example.com", Content: "192.0.2.10", TTL: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Created || requests != 2 {
		t.Fatalf("result=%+v requests=%d", result, requests)
	}
}
