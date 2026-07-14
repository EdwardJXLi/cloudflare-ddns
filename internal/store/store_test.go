package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCredentialLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	s := New(path)
	token, err := s.Add("host")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("empty token")
	}
	credential, err := s.Authenticate(token)
	if err != nil || credential.ID != "host" {
		t.Fatalf("authenticate: credential=%+v err=%v", credential, err)
	}
	if _, err := s.Authenticate("wrong"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong token error = %v", err)
	}
	rotated, err := s.Rotate("host")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(token); !errors.Is(err, ErrNotFound) {
		t.Fatal("old token still works")
	}
	if _, err := s.Authenticate(rotated); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("host"); err != nil {
		t.Fatal(err)
	}
	var database Database
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &database); err != nil {
		t.Fatalf("stored database is invalid JSON: %v", err)
	}
}

func TestEmptyDatabaseIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := New(path).Initialize(); err == nil {
		t.Fatal("Initialize accepted an empty credential database")
	}
}

func TestDatabaseStoresClientsAlphabetically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	s := New(path)
	for _, id := range []string{"zulu", "alpha", "mike"} {
		if _, err := s.Add(id); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var database Database
	if err := json.Unmarshal(raw, &database); err != nil {
		t.Fatal(err)
	}

	want := []string{"alpha", "mike", "zulu"}
	for i, id := range want {
		if database.Clients[i].ID != id {
			t.Fatalf("clients[%d] = %q, want %q", i, database.Clients[i].ID, id)
		}
	}
}

func TestRecordPingPersistsLatestTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	s := New(path)
	if _, err := s.Add("host"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("never-seen"); err != nil {
		t.Fatal(err)
	}

	lastPing := time.Date(2026, time.July, 13, 18, 30, 0, 0, time.FixedZone("EDT", -4*60*60))
	if err := s.RecordPing("host", lastPing); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPing("host", lastPing.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	clients, err := New(path).List()
	if err != nil {
		t.Fatal(err)
	}
	if clients[0].ID != "host" || clients[0].LastPing == nil || !clients[0].LastPing.Equal(lastPing) {
		t.Fatalf("recorded client = %+v, want last ping %s", clients[0], lastPing)
	}
	if clients[0].LastPing.Location() != time.UTC {
		t.Fatalf("last ping location = %v, want UTC", clients[0].LastPing.Location())
	}
	if clients[1].ID != "never-seen" || clients[1].LastPing != nil {
		t.Fatalf("unseen client = %+v, want no last ping", clients[1])
	}
	if err := s.RecordPing("missing", lastPing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing client error = %v, want %v", err, ErrNotFound)
	}
}

func TestConcurrentWritesRemainSerializedAcrossRenames(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "clients.json"))
	const count = 20
	var wg sync.WaitGroup
	errors := make(chan error, count)
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Add(fmt.Sprintf("host-%02d", i))
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	clients, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != count {
		t.Fatalf("stored %d clients, want %d", len(clients), count)
	}
}
