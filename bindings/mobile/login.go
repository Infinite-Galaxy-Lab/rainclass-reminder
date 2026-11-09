package mobile

import "github.com/Infinite-Galaxy-Lab/rainclass-reminder/internal/application"

type Login struct{ core *application.Login }

func NewLogin() (*Login, error) {
	core, err := application.NewLogin()
	if err != nil {
		return nil, err
	}
	return &Login{core: core}, nil
}

func (l *Login) Start() ([]byte, error)           { return l.core.Start() }
func (l *Login) Await() (string, error)           { return l.core.Wait() }
func (l *Login) Cancel()                          { l.core.Cancel() }
func (l *Login) ConfirmationURL() (string, error) { return l.core.ConfirmationURL() }
