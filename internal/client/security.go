package client

// What an account is secured with: its logins, its password and email, and the
// second factor guarding all of it. Filed apart from actions.go because it is a
// different kind of request — most of these change how the account is *reached*
// rather than what is in it, and Revolt gates them behind a proof of identity
// nothing else here needs.
//
// **That proof is an MFA ticket.** A ticket is minted by answering a challenge —
// a password, a TOTP code or a recovery code — and is then presented in the
// `x-mfa-ticket` header, for a few minutes, on the routes that take one. Which
// routes those are is not guessable and is not symmetric: the spec's own guard
// lists say reading is free, renaming a session is free, and *enabling* TOTP is
// free because its answer rides in the body — while revoking a session, changing
// a password or an email, disabling TOTP and every recovery-code route are
// gated. Sending a ticket where none is wanted is ignored, so the split is worth
// getting right for the reverse case only: a gated route asked without one is
// refused with nothing to say why.
//
// revoltgo's REST layer has no per-request headers, so the ticket is set on the
// session's shared header map for the length of one call and taken off again.
// That map is guarded by the HTTP client's own lock, so this is safe; what it
// cannot promise is that no *other* request overlaps and carries the header too.
// Harmless — it is this account's own ticket, and a route that does not declare
// the guard never reads it — but it is why every gated call goes through
// withTicket rather than setting the header where it is convenient.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sentinelb51/revoltgo"

	"RGOClient/internal/domain"
)

// mfaHeader is the header Revolt takes a ticket in. Lower-case because that is
// how the spec names it; Go canonicalises it either way.
const mfaHeader = "x-mfa-ticket"

// ErrNoTicket reports a gated call made without a ticket. It is a programming
// error rather than a refusal — the caller answers a challenge first — so it is
// caught here instead of being sent to be refused.
var ErrNoTicket = errors.New("this action needs a second-factor ticket")

// ErrPasswordTooShort reports a new password Revolt would refuse. Its floor is
// 8 characters; anything else it dislikes (a breached password, which Revolt
// checks against HIBP) comes back as a status code and is not predictable here.
var ErrPasswordTooShort = errors.New("password is shorter than 8 characters")

// MinPassword is that floor.
const MinPassword = 8

/* This session */

// SessionID is this login's own session ID, or "" where it was never recorded —
// see Client.sessionID. Safe from any goroutine.
func (c *Client) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.sessionID
}

// setSessionID records it. Called by a login that answered with one, and by
// OpenAs for a token saved beside one.
func (c *Client) setSessionID(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sessionID = id
}

/* The ticket */

// MFAMethods is what this account will accept as an answer to a challenge.
// Revolt derives it from what the account has: a password alone for most, and
// TOTP and recovery beside it for an account that has set one up.
func (c *Client) MFAMethods() ([]MFAMethod, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}

	methods, err := session.AuthMFAMethods()
	if err != nil {
		return nil, err
	}

	out := make([]MFAMethod, 0, len(methods))
	for _, method := range methods {
		out = append(out, MFAMethod(method))
	}

	return out, nil
}

// CreateMFATicket answers a challenge and returns the ticket the answer is worth.
// Short-lived: it is minted for one action and a stale one is refused, so it is
// never held between them.
//
// The answer goes in the field naming the method, exactly as a login's does —
// Revolt reads which factor is being answered off *which field* carries the
// code, so the wrong one is a refusal with nothing to say why.
func (c *Client) CreateMFATicket(method MFAMethod, code string) (string, error) {
	session := c.session.Load()
	if session == nil {
		return "", ErrNoSession
	}

	params, err := ticketAnswer(method, code)
	if err != nil {
		return "", err
	}

	ticket, err := session.AuthMFACreateTicket(params)
	if err != nil {
		return "", err
	}

	return ticket.Token, nil
}

// ticketAnswer is answerFor for the ticket route, which takes the same three
// alternatives in a shape of its own.
func ticketAnswer(method MFAMethod, code string) (revoltgo.AuthMFAParams, error) {
	switch method {
	case MFATOTP:
		return revoltgo.AuthMFAParams{TOTPCode: code}, nil
	case MFARecovery:
		return revoltgo.AuthMFAParams{RecoveryCode: code}, nil
	case MFAPassword:
		return revoltgo.AuthMFAParams{Password: code}, nil
	}

	return revoltgo.AuthMFAParams{}, fmt.Errorf("unsupported second factor %q", method)
}

// withTicket runs one gated request with the ticket in the header, and takes it
// off again whatever the request did. Serialised on ticketMu so two of them
// cannot leave the header holding the other's ticket.
func (c *Client) withTicket(ticket string, do func(*revoltgo.Session) error) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return c.withTicketOn(session, ticket, do)
}

// withTicketOn is withTicket against a session the caller already holds, which
// LogoutEverywhere does: it drops the session before spending it, so by the time
// the request goes out there is nothing left to load.
func (c *Client) withTicketOn(session *revoltgo.Session, ticket string,
	do func(*revoltgo.Session) error) error {

	if ticket == "" {
		return ErrNoTicket
	}

	c.ticketMu.Lock()
	defer c.ticketMu.Unlock()

	session.HTTP.SetHeader(mfaHeader, ticket)
	defer session.HTTP.RemoveHeader(mfaHeader)

	return do(session)
}

/* The account's logins */

// Sessions lists every login Revolt is holding open for this account. The one
// this client is using is marked where its ID was recorded — see
// domain.AccountSession.
func (c *Client) Sessions() ([]domain.AccountSession, error) {
	session := c.session.Load()
	if session == nil {
		return nil, ErrNoSession
	}

	raw, err := session.Sessions()
	if err != nil {
		return nil, err
	}

	current := c.SessionID()

	out := make([]domain.AccountSession, 0, len(raw))
	for _, entry := range raw {
		if entry == nil {
			continue
		}

		out = append(out, domain.AccountSession{
			ID:      entry.ID,
			Name:    entry.Name,
			Current: entry.ID != "" && entry.ID == current,
		})
	}

	return out, nil
}

// RenameSession changes what a login is called in that list, which is the only
// thing telling two of them apart. Ungated: a name is not a credential.
func (c *Client) RenameSession(sessionID, name string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	clean := strings.TrimSpace(name)
	if clean == "" {
		return errors.New("a session needs a name")
	}

	_, err := session.SessionEdit(sessionID, revoltgo.SessionEditParams{FriendlyName: clean})

	return err
}

// RevokeSession signs one device out. Revoking *this* one is allowed and is the
// same thing as logging out, except that the gateway announces it — see the
// EventAuth handler.
func (c *Client) RevokeSession(sessionID, ticket string) error {
	return c.withTicket(ticket, func(session *revoltgo.Session) error {
		return session.SessionsDelete(sessionID)
	})
}

// RevokeOtherSessions signs every *other* device out and leaves this one signed
// in, which is what somebody who thinks a login has been taken wants: they are
// on the device they trust.
func (c *Client) RevokeOtherSessions(ticket string) error {
	return c.withTicket(ticket, func(session *revoltgo.Session) error {
		return session.SessionsDeleteAll(false)
	})
}

/* The account itself */

// AccountEmail is the address this account signs in with. It is not on the user
// record and no event announces a change, so it is a request of its own — the
// same shape the profile takes.
func (c *Client) AccountEmail() (string, error) {
	session := c.session.Load()
	if session == nil {
		return "", ErrNoSession
	}

	account, err := session.Account()
	if err != nil {
		return "", err
	}

	return account.Email, nil
}

// ChangePassword replaces the account password. Revolt takes the current one in
// the body **as well as** the ticket the challenge minted — the two are not the
// same proof: a ticket may have been answered with a TOTP code.
func (c *Client) ChangePassword(current, next, ticket string) error {
	if len(next) < MinPassword {
		return ErrPasswordTooShort
	}

	return c.withTicket(ticket, func(session *revoltgo.Session) error {
		return session.AccountChangePassword(revoltgo.AccountChangePasswordParams{
			Password:        next,
			CurrentPassword: current,
		})
	})
}

// ChangeEmail points the account at another address. Revolt sends a verification
// mail to it and the change lands when that is followed, which is why nothing
// here reads the new address back: this client cannot see the mail, and the
// account still answers with the old one until it is.
func (c *Client) ChangeEmail(email, current, ticket string) error {
	clean := strings.TrimSpace(email)
	if clean == "" {
		return errors.New("give an email address")
	}

	return c.withTicket(ticket, func(session *revoltgo.Session) error {
		return session.AccountChangeEmail(revoltgo.AccountChangeEmailParams{
			Email:           clean,
			CurrentPassword: current,
		})
	})
}

/* The second factor */

// MFAStatus is which factors the account has. Ungated, being a reading.
func (c *Client) MFAStatus() (domain.MFAStatus, error) {
	session := c.session.Load()
	if session == nil {
		return domain.MFAStatus{}, ErrNoSession
	}

	status, err := session.AuthMFA()
	if err != nil {
		return domain.MFAStatus{}, err
	}

	return domain.MFAStatus{
		TOTP:        status.TotpMFA,
		Recovery:    status.RecoveryActive,
		EmailOTP:    status.EmailOTP,
		SecurityKey: status.SecurityKeyMFA,
	}, nil
}

// GenerateTOTPSecret asks for the secret an authenticator app is set up with.
// Asking again before it is confirmed answers with a new one, so the secret on
// screen is the one to enter and nothing keeps an older one alive.
func (c *Client) GenerateTOTPSecret(ticket string) (string, error) {
	var secret string

	err := c.withTicket(ticket, func(session *revoltgo.Session) error {
		answer, err := session.AuthMFAGenerateTOTPSecret()
		secret = answer.Secret

		return err
	})

	return secret, err
}

// EnableTOTP confirms the secret by answering with a code from it. Alone among
// these it takes **no** ticket: the code in its body is the proof, which is the
// only proof that could mean anything here — a password says nothing about
// whether the authenticator was actually set up.
func (c *Client) EnableTOTP(code string) error {
	session := c.session.Load()
	if session == nil {
		return ErrNoSession
	}

	return session.AuthMFAEnable2FATOTP(revoltgo.AuthMFAParams{TOTPCode: code})
}

// DisableTOTP takes the authenticator off the account.
func (c *Client) DisableTOTP(ticket string) error {
	return c.withTicket(ticket, func(session *revoltgo.Session) error {
		return session.AuthMFADisable2FATOTP()
	})
}

// RecoveryCodes is the codes as they stand — the ones already generated, not new
// ones. Revolt spends one route on each and the difference is the method, which
// is why the two are named apart here rather than taking a flag.
func (c *Client) RecoveryCodes(ticket string) ([]string, error) {
	return c.recoveryCodes(ticket, func(session *revoltgo.Session) ([]string, error) {
		return session.AuthMFARecoveryCodes()
	})
}

// RegenerateRecoveryCodes replaces them, which invalidates every code already
// written down. That is what makes it the one action here worth confirming
// twice: the codes it answers with are the only copy there will be.
func (c *Client) RegenerateRecoveryCodes(ticket string) ([]string, error) {
	return c.recoveryCodes(ticket, func(session *revoltgo.Session) ([]string, error) {
		return session.AuthMFAGenerateRecoveryCodes()
	})
}

func (c *Client) recoveryCodes(ticket string, ask func(*revoltgo.Session) ([]string, error)) ([]string, error) {
	var codes []string

	err := c.withTicket(ticket, func(session *revoltgo.Session) error {
		answer, err := ask(session)
		codes = answer

		return err
	})

	return codes, err
}
