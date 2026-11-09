package application

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/auth"
)

type Vault interface {
	Load() (string, error)
	Save(string) error
}

type session struct {
	mu      sync.Mutex
	cookie  auth.Cookie
	vault   Vault
	account string
}

type credentials struct {
	Cookie  auth.Cookie `json:"cookie"`
	Account string      `json:"account"`
}

func (s *session) Valid() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookie.Valid()
}

func (s *session) Apply(req *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookie.Apply(req)
}

func (s *session) ObserveCookie(name, value string) error {
	if value == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cookie.Valid() {
		return nil
	}
	old := ""
	switch name {
	case "sessionid":
		old, s.cookie.SessionID = s.cookie.SessionID, value
	case "csrftoken":
		old, s.cookie.CSRF = s.cookie.CSRF, value
	default:
		return nil
	}
	if old == value || s.vault == nil {
		return nil
	}
	s.cookie.RefreshedAt = time.Now()
	return s.persist()
}

func (s *session) persist() error {
	value, err := json.Marshal(credentials{Cookie: s.cookie, Account: s.account})
	if err != nil {
		return err
	}
	return s.vault.Save(string(value))
}
