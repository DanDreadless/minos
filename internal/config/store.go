package config

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// Store owns the on-disk config file and the current in-memory snapshot.
// Get is lock-free; Update serializes writers.
type Store struct {
	path string

	mu       sync.Mutex // guards Update (clone-validate-persist-swap) and onChange
	cur      atomic.Pointer[Config]
	onChange []func(*Config)
}

// Open loads the config at path, creating it with defaults if it is missing.
func Open(path string) (*Store, error) {
	c, err := load(path)
	if errors.Is(err, os.ErrNotExist) {
		c = Default()
		if saveErr := save(path, c); saveErr != nil {
			return nil, fmt.Errorf("write default config: %w", saveErr)
		}
	} else if err != nil {
		return nil, err
	}
	s := &Store{path: path}
	s.cur.Store(c)
	return s, nil
}

// Get returns the current snapshot. Callers must treat it as read-only.
func (s *Store) Get() *Config { return s.cur.Load() }

// Path returns the location of the config file on disk.
func (s *Store) Path() string { return s.path }

// OnChange registers fn to run (synchronously, in Update) after every
// successful config change. Register during startup, before serving.
func (s *Store) OnChange(fn func(*Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, fn)
}

// Update clones the current config, applies mutate, validates, persists to
// disk, then atomically swaps the snapshot and notifies OnChange handlers.
// If any step fails the running config is untouched.
func (s *Store) Update(mutate func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur.Load().Clone()
	if err := mutate(next); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if err := save(s.path, next); err != nil {
		return err
	}
	s.cur.Store(next)
	for _, fn := range s.onChange {
		fn(next)
	}
	return nil
}
