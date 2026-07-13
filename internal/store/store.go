package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const CurrentVersion = 1

var ErrNotFound = errors.New("credential not found")

type Database struct {
	Version int          `json:"version"`
	Clients []Credential `json:"clients"`
}

type Credential struct {
	ID        string `json:"id"`
	TokenHash string `json:"token_hash"`
}

type Store struct {
	path string
}

func New(path string) *Store { return &Store{path: path} }

func (s *Store) Initialize() error {
	return s.withLock(true, func(db *Database) error { return nil })
}

func (s *Store) List() ([]Credential, error) {
	var clients []Credential
	err := s.withLock(false, func(db *Database) error {
		clients = append(clients, db.Clients...)
		return nil
	})
	sort.Slice(clients, func(i, j int) bool { return clients[i].ID < clients[j].ID })
	return clients, err
}

func (s *Store) Authenticate(token string) (Credential, error) {
	wanted := sha256.Sum256([]byte(token))
	var found Credential
	err := s.withLock(false, func(db *Database) error {
		for _, client := range db.Clients {
			stored, err := hex.DecodeString(client.TokenHash)
			if err != nil || len(stored) != sha256.Size {
				continue
			}
			if subtle.ConstantTimeCompare(stored, wanted[:]) == 1 {
				found = client
				return nil
			}
		}
		return ErrNotFound
	})
	return found, err
}

func (s *Store) Add(id string) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	err = s.withLock(true, func(db *Database) error {
		for _, client := range db.Clients {
			if client.ID == id {
				return fmt.Errorf("credential %q already exists", id)
			}
		}
		db.Clients = append(db.Clients, Credential{ID: id, TokenHash: hash})
		return nil
	})
	return token, err
}

func (s *Store) Rotate(id string) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	err = s.withLock(true, func(db *Database) error {
		for i := range db.Clients {
			if db.Clients[i].ID == id {
				db.Clients[i].TokenHash = hash
				return nil
			}
		}
		return ErrNotFound
	})
	return token, err
}

func (s *Store) Remove(id string) error {
	return s.withLock(true, func(db *Database) error {
		for i := range db.Clients {
			if db.Clients[i].ID == id {
				db.Clients = append(db.Clients[:i], db.Clients[i+1:]...)
				return nil
			}
		}
		return ErrNotFound
	})
}

func (s *Store) withLock(exclusive bool, fn func(*Database) error) error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	// The database inode is replaced on every write, so lock a stable sidecar.
	lockFile, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open credential database lock: %w", err)
	}
	defer lockFile.Close()
	lock := syscall.LOCK_SH
	if exclusive {
		lock = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(lockFile.Fd()), lock); err != nil {
		return fmt.Errorf("lock credential database: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) //nolint:errcheck

	db := Database{Version: CurrentVersion, Clients: []Credential{}}
	raw, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read credential database: %w", err)
	}
	if err == nil {
		if len(strings.TrimSpace(string(raw))) == 0 {
			return errors.New("credential database is empty")
		}
		if err := json.Unmarshal(raw, &db); err != nil {
			return fmt.Errorf("decode credential database: %w", err)
		}
		if db.Version != CurrentVersion {
			return fmt.Errorf("unsupported credential database version %d", db.Version)
		}
	}
	if err := fn(&db); err != nil {
		return err
	}
	if !exclusive {
		return nil
	}
	return writeDatabase(directory, s.path, db)
}

func writeDatabase(directory, path string, db Database) error {
	encoded, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary credential database: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath) //nolint:errcheck

	if err := temporary.Chmod(0600); err != nil {
		temporary.Close() //nolint:errcheck
		return fmt.Errorf("set temporary credential database permissions: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close() //nolint:errcheck
		return fmt.Errorf("write credential database: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close() //nolint:errcheck
		return fmt.Errorf("sync credential database: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close credential database: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace credential database: %w", err)
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open credential database directory: %w", err)
	}
	defer directoryFile.Close()
	if err := directoryFile.Sync(); err != nil {
		return fmt.Errorf("sync credential database directory: %w", err)
	}
	return nil
}

func newToken() (token, hash string, err error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = "hnddns_" + base64.RawURLEncoding.EncodeToString(random)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}
