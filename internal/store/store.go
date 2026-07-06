// Package store persiste, em um pequeno arquivo JSON, a referência da mensagem
// do painel (canal + id da mensagem). Assim, após um restart, o bot volta a
// editar a MESMA mensagem em vez de criar uma nova a cada boot.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// DashboardRef aponta para a mensagem-painel no Discord.
type DashboardRef struct {
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
}

// Store lê/grava a referência de forma thread-safe.
type Store struct {
	path string
	mu   sync.Mutex
}

// New garante que o diretório exista e retorna o store.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, "dashboard.json")}, nil
}

// Load lê a referência salva. Arquivo inexistente devolve ref vazia (sem erro).
func (s *Store) Load() (DashboardRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var ref DashboardRef
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return ref, nil
	}
	if err != nil {
		return ref, err
	}
	err = json.Unmarshal(b, &ref)
	return ref, err
}

// Save grava a referência (escrita atômica via arquivo temporário + rename).
func (s *Store) Save(ref DashboardRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := json.MarshalIndent(ref, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
