package client

// Signing in, including the second factor.
//
// revoltgo cannot express an MFA login at all — three gaps on one route: no
// ticket or allowed methods in its response, no mfa_ticket in its params, and an
// MFAResponse carrying only a password. Hence the types below, which are
// Revolt's own shapes. What this file produces is a token, which Open takes like
// any other.

import (
	"fmt"
	"net/http"
	"os"

	"github.com/sentinelb51/revoltgo"
)

// MFAMethod is a second factor Revolt will accept for a login it is holding.
// The values are Revolt's own, sent back verbatim.
type MFAMethod string

const (
	MFATOTP     MFAMethod = "Totp"
	MFARecovery MFAMethod = "Recovery"
	MFAPassword MFAMethod = "Password"
)

// Label names a method for somebody who has to choose one.
func (m MFAMethod) Label() string {
	switch m {
	case MFATOTP:
		return "Authenticator app"
	case MFARecovery:
		return "Recovery code"
	case MFAPassword:
		return "Password"
	}

	return string(m)
}

// Login is what an attempt to sign in came back with. Exactly one of the two
// halves is filled in: a Token means the session is already open, and a Ticket
// means the server has accepted the password and is holding the login until a
// second factor answers it — nothing is logged in at that point.
type Login struct {
	Token string

	// SessionID is the ID of the session the token belongs to, which arrives here
	// and **nowhere else**: no route answers "which of this account's sessions am
	// I". Worth persisting beside the token — a login restored without it cannot
	// mark itself in the session list or tell its own revocation from anybody
	// else's.
	SessionID string

	Ticket  string
	Methods []MFAMethod
}

// Pending reports a login the server is holding, waiting on a second factor.
func (l Login) Pending() bool { return l.Token == "" && l.Ticket != "" }

// friendlyName is what this login is called in the account's session list, which
// is the only place somebody sees which device a session belongs to.
const friendlyName = "RGOClient"

// ErrLoginRefused is returned for a login the server neither completed nor
// challenged — an account it has disabled, or a result added after this was
// written. It is distinct from a transport failure: retrying will not help.
var ErrLoginRefused = fmt.Errorf("login refused")

/* Revolt's own shapes for the two login stages */

// loginRequest is Revolt's DataLogin, whose two forms share one route: an email
// and password opens a login, and a ticket plus a response finishes one.
type loginRequest struct {
	Email    string `json:"email,omitempty"`
	Password string `json:"password,omitempty"`

	MFATicket   string       `json:"mfa_ticket,omitempty"`
	MFAResponse *mfaResponse `json:"mfa_response,omitempty"`

	FriendlyName string `json:"friendly_name,omitempty"`
}

// mfaResponse carries exactly one of its fields — which one is the method.
type mfaResponse struct {
	Password     string `json:"password,omitempty"`
	RecoveryCode string `json:"recovery_code,omitempty"`
	TOTPCode     string `json:"totp_code,omitempty"`
}

// loginResponse is Revolt's ResponseLogin, one struct over its three forms:
// Success carries the token, MFA carries the challenge, Disabled carries neither.
type loginResponse struct {
	Result string `json:"result"`

	// ID is the new session's own, which Revolt sends here and on no other route.
	ID    string `json:"_id"`
	Token string `json:"token"`

	Ticket         string      `json:"ticket"`
	AllowedMethods []MFAMethod `json:"allowed_methods"`
}

/* The two stages */

// Login exchanges credentials for a session, opening the gateway when the
// account has no second factor. An account that does comes back Pending, and the
// caller finishes with AnswerMFA — the password alone is not a login, and
// nothing here is logged in until it is.
func (c *Client) Login(email, password string) (Login, error) {
	return c.login(loginRequest{Email: email, Password: password})
}

// AnswerMFA finishes a login the server is holding. The ticket is the one Login
// handed back; it is short-lived, and an expired one fails here rather than
// anywhere later.
func (c *Client) AnswerMFA(ticket string, method MFAMethod, code string) (Login, error) {
	answer, err := answerFor(method, code)
	if err != nil {
		return Login{}, err
	}

	return c.login(loginRequest{MFATicket: ticket, MFAResponse: answer})
}

// answerFor puts the code in the field naming the method it answers. Revolt
// reads the method off which field is set rather than off a name, so a code in
// the wrong one is a different factor and is refused.
func answerFor(method MFAMethod, code string) (*mfaResponse, error) {
	switch method {
	case MFATOTP:
		return &mfaResponse{TOTPCode: code}, nil
	case MFARecovery:
		return &mfaResponse{RecoveryCode: code}, nil
	case MFAPassword:
		return &mfaResponse{Password: code}, nil
	}

	return nil, fmt.Errorf("unsupported second factor %q", method)
}

// login makes one login request and opens the gateway if it produced a token.
//
// The request goes through a session with no token of its own: the route is
// unauthenticated, and the session that ends up serving the account is built from
// the token afterwards by Open — which is what makes both stages land on exactly
// the path a saved login already takes.
func (c *Client) login(request loginRequest) (Login, error) {
	request.FriendlyName = fmt.Sprintf("%s (%s)", friendlyName, hostname())

	var response loginResponse
	if err := revoltgo.New("").HTTP.Request(
		http.MethodPost, revoltgo.EndpointAuthSession("login"), request, &response,
	); err != nil {
		return Login{}, err
	}

	switch {
	case response.Token != "":
		// OpenAs rather than Open: the ID is cleared by opening a session, so it has
		// to be recorded on the far side of it.
		return Login{Token: response.Token, SessionID: response.ID},
			c.OpenAs(response.Token, response.ID)
	case response.Ticket != "":
		return Login{Ticket: response.Ticket, Methods: response.AllowedMethods}, nil
	}

	return Login{}, fmt.Errorf("%w: %s", ErrLoginRefused, response.Result)
}

// hostname names this computer in the account's session list. It is the whole
// reason the name is composed rather than a constant: the list is read to decide
// which session to revoke, and several rows saying only "RGOClient" would not
// tell anybody which one is the machine in front of them.
func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown device"
	}

	return name
}
