package app

import (
	"fmt"

	"github.com/sentinelb51/revoltgo"
)

// startWithToken opens a session using an existing token.
func (a *App) startWithToken(token string) error {
	session := revoltgo.New(token)
	if err := a.openSession(session); err != nil {
		return err
	}
	return nil
}

// startWithLogin opens a session using credentials and returns the new token.
func (a *App) startWithLogin(email, password string) (string, error) {
	session, resp, err := revoltgo.NewWithLogin(revoltgo.LoginParams{Email: email, Password: password})
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	if err := a.openSession(session); err != nil {
		return "", err
	}
	return resp.Token, nil
}

// openSession registers handlers and opens the websocket for session.
func (a *App) openSession(session *revoltgo.Session) error {
	session.HTTP.Debug = true
	a.session = session

	revoltgo.AddHandler(session, a.onReady)
	revoltgo.AddHandler(session, a.onMessage)
	revoltgo.AddHandler(session, a.onError)

	if err := session.Open(); err != nil {
		return fmt.Errorf("open session: %w", err)
	}
	return nil
}
