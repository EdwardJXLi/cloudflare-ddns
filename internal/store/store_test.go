package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
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
