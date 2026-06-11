package app

import (
	"fmt"

	"github.com/sentinelb51/revoltgo"
)

// startWithToken opens a session using an existing token.
func (a *App) startWithToken(token string) error {
	return a.openSession(revoltgo.New(token))
}

// startWithLogin opens a session using credentials. The new token is stashed
// in pendingToken before the gateway opens — onReady persists it, and Ready
// can arrive before the login goroutine would otherwise get back onto the UI
// thread to record it.
func (a *App) startWithLogin(email, password string) error {
	session, resp, err := revoltgo.NewWithLogin(revoltgo.LoginParams{Email: email, Password: password})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	a.doOnUI(func() { a.pendingToken = resp.Token }, true)
	if err := a.openSession(session); err != nil {
		a.doOnUI(func() { a.pendingToken = "" }, true)
		return err
	}
	return nil
}

// openSession registers handlers and opens the websocket for session.
// openSession itself runs on a login goroutine, so the a.session write goes
// through the UI thread — the only place that field is ever written (the other
// writer is onError's teardown), keeping every UI-thread read race-free.
func (a *App) openSession(session *revoltgo.Session) error {
	a.doOnUI(func() { a.session = session }, true)

	revoltgo.AddHandler(session, a.onReady)
	revoltgo.AddHandler(session, a.onMessage)
	revoltgo.AddHandler(session, a.onServerMemberJoin)
	revoltgo.AddHandler(session, a.onServerMemberLeave)
	revoltgo.AddHandler(session, a.onServerMemberUpdate)
	revoltgo.AddHandler(session, a.onError)

	if err := session.Open(); err != nil {
		a.doOnUI(func() { a.session = nil }, true)
		return fmt.Errorf("open session: %w", err)
	}
	return nil
}
