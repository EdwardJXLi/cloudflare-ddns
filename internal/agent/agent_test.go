package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUpdateDiscoversAddressAndCallsHub(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "192.0.2.55\n")
	}))
	defer provider.Close()
	var gotAddress string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer client-secret" {
			t.Error("wrong authorization")
		}
		var request struct {
			Address string `json:"address"`
		}
		json.NewDecoder(r.Body).Decode(&request)
		gotAddress = request.Address
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]string{"name": "host.example.com", "type": "A", "status": "updated"}})
	}))
	defer hub.Close()
	agent, err := New(Config{
		HubURL: hub.URL, Token: "client-secret",
		IPv4Provider: provider.URL, Interval: time.Minute, AllowInsecureHTTP: true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAddress != "192.0.2.55" {
		t.Fatalf("address = %q", gotAddress)
	}
}

func TestRequiresHTTPSByDefault(t *testing.T) {
	_, err := New(Config{HubURL: "http://hub:8080", Token: "secret", Interval: time.Minute}, slog.Default())
	if err == nil {
		t.Fatal("expected insecure URL error")
	}
}
