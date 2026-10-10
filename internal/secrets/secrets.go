// Package secrets keeps the secrets of the pipelines (API keys of the LLM
// providers and of the plugins, IMP-167/168, ADR-035) in a file of the
// state dir, each value sealed with AES-256-GCM under a key that comes
// from the environment (GOSIDIAN_SECRETS_KEY), never from the state dir:
// a backup of the state dir does not carry the keys in clear.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// EnvVar is the variable that holds the key: 32 bytes in base64
// (`openssl rand -base64 32`).
const EnvVar = "GOSIDIAN_SECRETS_KEY"

// FileName is the file of the secrets in the state dir.
const FileName = "secrets.json"

// ErrNoKey: the server has no key, so no secret can be read or written.
var ErrNoKey = errors.New("secrets need a key: set " + EnvVar + " (32 bytes in base64, openssl rand -base64 32)")

// ErrNotFound: no secret with that name.
var ErrNotFound = errors.New("no such secret")

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// ParseKey reads the key: 32 bytes, base64 (standard or URL alphabet).
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrNoKey
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			if len(b) != 32 {
				return nil, fmt.Errorf("%s is %d bytes, it must be 32 (openssl rand -base64 32)", EnvVar, len(b))
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("%s is not base64 (openssl rand -base64 32)", EnvVar)
}

type sealed struct {
	Nonce   string    `json:"nonce"`
	Data    string    `json:"data"`
	Updated time.Time `json:"updated"`
}

type file struct {
	Version int               `json:"version"`
	Secrets map[string]sealed `json:"secrets"`
}

// Store is the secrets file.
type Store struct {
	path string
	aead cipher.AEAD
	mu   sync.Mutex
}

// Open opens the secrets of path with key (from ParseKey).
func Open(path string, key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, ErrNoKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, aead: aead}
	// A file sealed with another key is an error now, not at the first
	// pipeline that needs a secret.
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	for name, v := range f.Secrets {
		if _, err := s.open(name, v); err != nil {
			return nil, fmt.Errorf("secret %q does not open with this key: is %s the one it was written with? %w", name, EnvVar, err)
		}
	}
	return s, nil
}

func (s *Store) load() (file, error) {
	f := file{Version: 1, Secrets: map[string]sealed{}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("%s: %w", s.path, err)
	}
	if f.Secrets == nil {
		f.Secrets = map[string]sealed{}
	}
	return f, nil
}

func (s *Store) save(f file) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".secrets-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func (s *Store) open(name string, v sealed) (string, error) {
	nonce, err := base64.StdEncoding.DecodeString(v.Nonce)
	if err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(v.Data)
	if err != nil {
		return "", err
	}
	// The name is the additional data: a value moved under another name
	// does not open.
	plain, err := s.aead.Open(nil, nonce, data, []byte(name))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Get returns a secret's value.
func (s *Store) Get(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return "", err
	}
	v, ok := f.Secrets[name]
	if !ok {
		return "", fmt.Errorf("%w: %q (gosidian secret set %s)", ErrNotFound, name, name)
	}
	return s.open(name, v)
}

// Set stores a secret, replacing one with the same name.
func (s *Store) Set(name, value string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("secret name %q: lowercase letters, digits, _ . -, starting with a letter", name)
	}
	if value == "" {
		return errors.New("an empty secret")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	f.Secrets[name] = sealed{
		Nonce:   base64.StdEncoding.EncodeToString(nonce),
		Data:    base64.StdEncoding.EncodeToString(s.aead.Seal(nil, nonce, []byte(value), []byte(name))),
		Updated: time.Now().UTC(),
	}
	return s.save(f)
}

// Delete removes a secret.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := f.Secrets[name]; !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	delete(f.Secrets, name)
	return s.save(f)
}

// Entry is a secret as a listing shows it: never its value.
type Entry struct {
	Name    string    `json:"name"`
	Updated time.Time `json:"updated"`
}

// List lists the secrets, by name.
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(f.Secrets))
	for n, v := range f.Secrets {
		out = append(out, Entry{Name: n, Updated: v.Updated})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
