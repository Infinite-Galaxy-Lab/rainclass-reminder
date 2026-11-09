package mobile

import (
	"context"
	_ "time/tzdata"

	"github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/application"
)

type Vault interface {
	Load() (string, error)
	Save(value string) error
}

type Notifications interface {
	Notify(title, body string) error
}

type Engine struct{ core *application.Mobile }

type Refresh struct {
	core   *application.Mobile
	ctx    context.Context
	cancel context.CancelFunc
}

func (e *Engine) NewRefresh() *Refresh {
	ctx, cancel := context.WithCancel(context.Background())
	return &Refresh{core: e.core, ctx: ctx, cancel: cancel}
}

func (r *Refresh) Run() (string, error) { return r.core.RefreshContext(r.ctx) }
func (r *Refresh) Cancel()              { r.cancel() }

func NewEngine(directory string, vault Vault, notifications Notifications, zone string) (*Engine, error) {
	core, err := application.OpenMobile(directory, vault, notifications, zone)
	if err != nil {
		return nil, err
	}
	return &Engine{core: core}, nil
}

func (e *Engine) Authenticate(cookie string) error { return e.core.Authenticate(cookie) }
func (e *Engine) Refresh() (string, error)         { return e.core.Refresh() }
func (e *Engine) Snapshot() (string, error)        { return e.core.Snapshot() }
func (e *Engine) SignOut() error                   { return e.core.SignOut() }
func (e *Engine) Cancel()                          { e.core.Cancel() }
func (e *Engine) Close() error                     { return e.core.Close() }
func (e *Engine) SetTimeZone(zone string) error    { return e.core.SetTimeZone(zone) }
