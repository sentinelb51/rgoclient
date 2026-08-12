package client

import (
	"encoding/json"
	"testing"
)

// TestLoginRequestShape covers the half of the login that nothing else can: the
// bodies are written against Revolt's spec by hand, revoltgo modelling neither
// stage, so a wrong key is a login that fails with no clue why. Revolt reads the
// method off *which* field carries the code, not off a name, so the mapping is
// the whole of what a second factor is.
func TestLoginRequestShape(t *testing.T) {
	first, err := json.Marshal(loginRequest{Email: "a@b.c", Password: "hunter2", FriendlyName: "RGOClient"})
	if err != nil {
		t.Fatalf("marshal the first stage: %v", err)
	}

	body := decode(t, first)
	for _, key := range []string{"email", "password", "friendly_name"} {
		if _, ok := body[key]; !ok {
			t.Errorf("the first stage sent no %q", key)
		}
	}
	if _, ok := body["mfa_ticket"]; ok {
		t.Error("the first stage sent an mfa_ticket, which names a login already open")
	}

	cases := []struct {
		method MFAMethod
		field  string
	}{
		{MFATOTP, "totp_code"},
		{MFARecovery, "recovery_code"},
		{MFAPassword, "password"},
	}

	for _, c := range cases {
		answer, err := answerFor(c.method, "123456")
		if err != nil {
			t.Fatalf("%s: %v", c.method, err)
		}

		second, err := json.Marshal(loginRequest{MFATicket: "ticket", MFAResponse: answer})
		if err != nil {
			t.Fatalf("%s: marshal the second stage: %v", c.method, err)
		}

		body := decode(t, second)
		if body["mfa_ticket"] != "ticket" {
			t.Errorf("%s: the second stage carried no ticket", c.method)
		}

		response, ok := body["mfa_response"].(map[string]any)
		if !ok {
			t.Fatalf("%s: the second stage carried no mfa_response", c.method)
		}
		if response[c.field] != "123456" {
			t.Errorf("%s: the code is not in %q — it reads as a different factor", c.method, c.field)
		}
		if len(response) != 1 {
			t.Errorf("%s: sent %d fields, want only the one that names the method", c.method, len(response))
		}
	}

	if _, err := answerFor(MFAMethod("SecurityKey"), "x"); err == nil {
		t.Error("a factor the client cannot answer was accepted")
	}
}

// TestLoginResponseForms covers the three answers the route gives back through
// one struct: a token, a challenge, or neither.
func TestLoginResponseForms(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		token    string
		ticket   string
		methods  int
		refusing bool
	}{
		{
			name:  "success",
			body:  `{"result":"Success","_id":"s","user_id":"u","token":"tok","name":"pc"}`,
			token: "tok",
		},
		{
			name:    "challenge",
			body:    `{"result":"MFA","ticket":"tkt","allowed_methods":["Totp","Recovery"]}`,
			ticket:  "tkt",
			methods: 2,
		},
		{
			name:     "disabled",
			body:     `{"result":"Disabled","user_id":"u"}`,
			refusing: true,
		},
	}

	for _, c := range cases {
		var response loginResponse
		if err := json.Unmarshal([]byte(c.body), &response); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}

		if response.Token != c.token {
			t.Errorf("%s: token %q, want %q", c.name, response.Token, c.token)
		}
		if response.Ticket != c.ticket {
			t.Errorf("%s: ticket %q, want %q", c.name, response.Ticket, c.ticket)
		}
		if len(response.AllowedMethods) != c.methods {
			t.Errorf("%s: %d methods, want %d", c.name, len(response.AllowedMethods), c.methods)
		}
		if refusing := response.Token == "" && response.Ticket == ""; refusing != c.refusing {
			t.Errorf("%s: refused=%v, want %v", c.name, refusing, c.refusing)
		}
	}
}

// TestPendingIsOnlyAChallenge covers what the login screen branches on: a token
// is a session, and only a ticket with no token is somebody still to be asked.
func TestPendingIsOnlyAChallenge(t *testing.T) {
	if (Login{Token: "tok"}).Pending() {
		t.Error("a completed login reported as pending")
	}
	if !(Login{Ticket: "tkt"}).Pending() {
		t.Error("a held login did not report as pending")
	}
	if (Login{}).Pending() {
		t.Error("an empty login reported as pending")
	}
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	return out
}
