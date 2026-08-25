package app

// The Security section's half of the controller: proving who you are, and the
// half-dozen things that proof unlocks.
//
// **Everything here begins with a challenge.** Revolt gates every route that
// changes how an account is *reached* behind an MFA ticket — see
// internal/client/security.go for which and why — so the shape is always the
// same: raise a card, mint a ticket from what is typed into it, then spend the
// ticket on one request. The ticket is never held between two of them: it is
// short-lived by design, and an action that reused one would be an action nobody
// confirmed.
//
// Two of them do **not** take that shape, and for a reason rather than by
// accident. Changing a password and changing an email each need the current
// password in the request *as well as* a ticket, so asking for the password in a
// challenge and then again in the card would be asking twice for one answer —
// those two are one card that mints its own ticket. Turning an authenticator on
// is the opposite: its confirmation is a code from the authenticator itself,
// which is the only proof that means anything there, so it takes no ticket at
// all.

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"RGOClient/internal/client"
	"RGOClient/internal/ui"
)

/* Proving who you are */

// challenge asks the account to confirm itself and hands the ticket that answer
// is worth to then, on the UI thread. purpose is the line under the title saying
// what it is for — the card is otherwise identical whichever action raised it.
//
// The methods are asked for rather than assumed: which factors an account will
// accept is Revolt's answer, and a picker offering one it will refuse is a
// refusal with nothing to say why.
func (a *App) challenge(purpose string, then func(ticket string)) {
	epoch := a.epoch

	go func() {
		methods, err := a.client.MFAMethods()

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("second factors: %v", err)
				a.notify(ui.ToneDanger, "Could not ask you to confirm it's you.")

				return
			}

			a.showChallenge(purpose, answerable(methods), then)
		}, false)
	}()
}

// mfaField names the challenge card's one field. Short where the login screen's
// mfaPrompt is a sentence, and for a reason: that screen is a card of its own
// with room to explain, where this one has already said what the proof is for on
// the line above the picker — and the label sits where every other field label on
// a card does, upper-cased and two words wide.
func mfaField(method client.MFAMethod) string {
	switch method {
	case client.MFARecovery:
		return "Recovery code"
	case client.MFAPassword:
		return "Password"
	}

	return "Code from your app"
}

// answerable drops the factors this client has no way to answer. Revolt names a
// security key among them for an account that has one, and there is no WebAuthn
// here — an option that cannot be used is worse than one that is missing.
func answerable(methods []client.MFAMethod) []client.MFAMethod {
	kept := make([]client.MFAMethod, 0, len(methods))
	for _, method := range methods {
		switch method {
		case client.MFAPassword, client.MFATOTP, client.MFARecovery:
			kept = append(kept, method)
		}
	}

	return kept
}

// showChallenge raises the card. Call on the UI thread.
func (a *App) showChallenge(purpose string, methods []client.MFAMethod, then func(ticket string)) {
	if len(methods) == 0 {
		a.notify(ui.ToneDanger, "This account is secured in a way this client can't answer.")
		return
	}

	offered := make([]ui.ChallengeMethod, len(methods))
	for i, method := range methods {
		offered[i] = ui.ChallengeMethod{Label: method.Label(), Prompt: mfaField(method)}
	}

	dialog := ui.NewChallengeDialog(purpose, offered, func(picked int, code string) {
		a.submitChallenge(methods[picked], code, then)
	}, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.challengeCard = dialog // after showOverlay, which clears the field
	a.window.Canvas().Focus(dialog.Entry)
}

// submitChallenge mints the ticket and, if it takes, closes the card and runs
// what was waiting on it. A refusal leaves the card up: a mistyped code is
// corrected in the field it came from.
func (a *App) submitChallenge(method client.MFAMethod, code string, then func(ticket string)) {
	epoch := a.epoch

	go func() {
		ticket, err := a.client.CreateMFATicket(method, code)

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("mfa ticket: %v", err)
				if a.challengeCard != nil {
					a.challengeCard.Fail("That was not accepted. Try again.")
				}

				return
			}

			a.closeOverlay()
			then(ticket)
		}, false)
	}()
}

/* What the section is drawn from */

// loadSecurity fetches the three answers the Security section needs, together.
// Three requests rather than one route, so they go in parallel: the section
// cannot draw without any of them and three waits in a row is three times the
// blank card.
//
// One error covers the lot. They come from one server over one session, so a
// partial failure is a route being down rather than anything about the account —
// and a section saying "could not be read" for everything is honest where one
// saying it for a third would leave the reader guessing which third.
func (a *App) loadSecurity(onLoaded func(ui.SecurityState, error)) {
	epoch := a.epoch

	go func() {
		var (
			state ui.SecurityState
			mu    sync.Mutex
			first error
			wg    sync.WaitGroup
		)

		fail := func(what string, err error) {
			log.Printf("%s: %v", what, err)

			mu.Lock()
			defer mu.Unlock()
			if first == nil {
				first = err
			}
		}

		wg.Add(3)
		go func() {
			defer wg.Done()

			email, err := a.client.AccountEmail()
			if err != nil {
				fail("account email", err)
				return
			}
			state.Email = email
		}()
		go func() {
			defer wg.Done()

			status, err := a.client.MFAStatus()
			if err != nil {
				fail("second factors", err)
				return
			}
			state.MFA = status
		}()
		go func() {
			defer wg.Done()

			logins, err := a.client.Sessions()
			if err != nil {
				fail("account sessions", err)
				return
			}
			state.Logins = logins
		}()
		wg.Wait()

		state.SelfKnown = a.client.SessionID() != ""

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			onLoaded(state, first)
		}, false)
	}()
}

// refreshSecurity drops what the section is holding and redraws it. Called by
// every action here once the server has agreed, and by the gateway when another
// client revokes something. Call on the UI thread.
func (a *App) refreshSecurity() {
	if a.settings != nil {
		a.settings.RefreshSecurity()
	}
}

// onSessionsChanged follows a login revoked from somewhere else. Only the page
// cares: this session surviving is what the event *not* being about us means, so
// there is nothing to say and nothing to tear down.
func (a *App) onSessionsChanged() {
	a.refreshSecurity()
}

/* The password and the email */

// changePassword is one card rather than a challenge and then a card. Revolt
// takes the current password in the request as well as the ticket, so a challenge
// first would ask for the same answer twice — this mints its own ticket from what
// is typed in the first field.
func (a *App) changePassword() {
	a.showPrompt(ui.Prompt{
		Title:  "Change password",
		Action: "Change",
		Busy:   "Changing...",
		Fields: []ui.PromptField{
			{Label: "Current password", Placeholder: "Your password now", Password: true},
			{Label: "New password", Placeholder: "At least 8 characters", Password: true},
		},
		OnSubmit: func(values []string) { a.submitPassword(values[0], values[1]) },
	})
}

func (a *App) submitPassword(current, next string) {
	epoch := a.epoch

	go func() {
		err := a.spend(current, func(ticket string) error {
			return a.client.ChangePassword(current, next, ticket)
		})

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("change password: %v", err)
				a.failPrompt(passwordFailure(err))

				return
			}

			a.closeOverlay()
			a.notify(ui.ToneInfo, "Your password was changed.")
		}, false)
	}()
}

// passwordFailure is what the card says. Only the length is the client's own to
// answer for; Revolt refuses a password it has seen in a breach with the same
// status code it refuses a wrong current one with, so the notice names both.
func passwordFailure(err error) string {
	switch {
	case errors.Is(err, client.ErrPasswordTooShort):
		return "A password needs at least 8 characters."
	case errors.Is(err, errPasswordRefused):
		return "That password was not accepted."
	}

	return "That did not take. Try a password you have not used elsewhere."
}

// changeEmail is changePassword's shape for the other credential, and for the
// same reason: the route takes the password with it.
func (a *App) changeEmail() {
	a.showPrompt(ui.Prompt{
		Title:  "Change email",
		Action: "Change",
		Busy:   "Changing...",
		Fields: []ui.PromptField{
			{Label: "New email", Placeholder: "you@example.com"},
			{Label: "Current password", Password: true},
		},
		OnSubmit: func(values []string) { a.submitEmail(values[0], values[1]) },
	})
}

func (a *App) submitEmail(email, password string) {
	epoch := a.epoch

	go func() {
		err := a.spend(password, func(ticket string) error {
			return a.client.ChangeEmail(email, password, ticket)
		})

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("change email: %v", err)
				if errors.Is(err, errPasswordRefused) {
					a.failPrompt("That password was not accepted.")
					return
				}
				a.failPrompt("That did not take. Check the address is a real one and not already in use.")

				return
			}

			a.closeOverlay()
			a.refreshSecurity()

			// Revolt sends a mail to the new address and the change lands when it is
			// followed, so the section still says the old one — which is true, and would
			// read as a bug without this.
			a.notify(ui.ToneInfo, "Check %s for a message confirming the change.", email)
		}, false)
	}()
}

// errPasswordRefused marks the *first* half of spend failing: Revolt would not
// take the password as proof. Named apart because the two halves fail for
// different reasons and the card can only say one of them — a wrong password
// here, where the route's own refusal past it is about the new value.
var errPasswordRefused = errors.New("password refused as proof")

// spend mints a ticket from a password and uses it once. The two password cards
// are the only ones holding an answer a ticket can be made from without asking
// for it again, which is the whole reason they are cards rather than challenges.
// Runs on a worker: both halves are requests.
func (a *App) spend(password string, do func(ticket string) error) error {
	ticket, err := a.client.CreateMFATicket(client.MFAPassword, password)
	if err != nil {
		return fmt.Errorf("%w: %w", errPasswordRefused, err)
	}

	return do(ticket)
}

/* The second factor */

// enableTOTP walks the two steps Revolt splits setting up an authenticator into:
// the secret, which is gated and therefore behind a challenge, and the code
// proving it was stored, which is not gated at all — that code is the proof.
func (a *App) enableTOTP() {
	a.challenge("Setting up an authenticator needs you to confirm it's you.", func(ticket string) {
		epoch := a.epoch

		go func() {
			secret, err := a.client.GenerateTOTPSecret(ticket)

			a.doOnUI(func() {
				if a.stale(epoch) {
					return
				}
				if err != nil {
					log.Printf("totp secret: %v", err)
					a.notify(ui.ToneDanger, "Could not start setting up an authenticator.")

					return
				}

				a.showSecret(secret)
			}, false)
		}()
	})
}

// showSecret raises the card holding it. Call on the UI thread.
func (a *App) showSecret(secret string) {
	dialog := ui.NewSecretDialog(secret, a.submitTOTP, a.closeOverlay)

	a.showOverlay(dialog.Content)
	a.secretCard = dialog
	a.window.Canvas().Focus(dialog.Entry)
}

func (a *App) submitTOTP(code string) {
	epoch := a.epoch

	go func() {
		err := a.client.EnableTOTP(strings.TrimSpace(code))

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("enable totp: %v", err)
				if a.secretCard != nil {
					a.secretCard.Fail("That code was not accepted. Check the clock on your device.")
				}

				return
			}

			a.closeOverlay()
			a.refreshSecurity()
			a.notify(ui.ToneInfo, "Your authenticator is on. Generate recovery codes next.")
		}, false)
	}()
}

// disableTOTP takes it off, confirmed and then challenged: the question is what
// it *means*, and the challenge is who is asking.
func (a *App) disableTOTP() {
	a.confirm(ui.Confirm{
		Title:  "Turn off your authenticator",
		Body:   "Your password becomes the only thing between somebody and this account.",
		Action: "Turn off",
		Tone:   ui.ToneDanger,
		OnConfirm: func() {
			a.challenge("Turning your authenticator off needs you to confirm it's you.", func(ticket string) {
				a.backgroundThen(
					func() error { return a.client.DisableTOTP(ticket) },
					a.notifyFailure("disable totp", "Could not turn your authenticator off."),
					func() {
						a.refreshSecurity()
						a.notify(ui.ToneWarning, "Your authenticator is off.")
					},
				)
			})
		},
	})
}

// recoveryCodes shows the set that exists. Reading them is gated exactly as
// replacing them is — they are the way past every other factor — so it takes the
// same challenge.
func (a *App) recoveryCodes() {
	a.challenge("Seeing your recovery codes needs you to confirm it's you.", func(ticket string) {
		a.fetchCodes("Keep these somewhere other than this computer. Each one signs you in once.",
			func() ([]string, error) { return a.client.RecoveryCodes(ticket) })
	})
}

// renewRecovery replaces them, which is the one action here that destroys
// something the reader may be relying on — so it is confirmed before the
// challenge, in the words of what it costs.
func (a *App) renewRecovery() {
	a.confirm(ui.Confirm{
		Title:  "Generate new recovery codes",
		Body:   "Every code you have written down stops working. The new set is shown once.",
		Action: "Generate",
		Tone:   ui.ToneWarning,
		OnConfirm: func() {
			a.challenge("Replacing your recovery codes needs you to confirm it's you.", func(ticket string) {
				a.fetchCodes("This set is shown once. Write it down before closing this card.",
					func() ([]string, error) { return a.client.RegenerateRecoveryCodes(ticket) })
			})
		},
	})
}

// fetchCodes asks and puts what comes back on the modal layer. Shared by both
// ways in, which differ only in the request and in what the card says about it.
func (a *App) fetchCodes(purpose string, ask func() ([]string, error)) {
	epoch := a.epoch

	go func() {
		codes, err := ask()

		a.doOnUI(func() {
			if a.stale(epoch) {
				return
			}
			if err != nil {
				log.Printf("recovery codes: %v", err)
				a.notify(ui.ToneDanger, "Could not read your recovery codes.")

				return
			}

			a.refreshSecurity() // generating a set is what turns the row's Show button on
			a.showOverlay(ui.NewCodesDialog(purpose, codes, a.closeOverlay))
		}, false)
	}()
}

/* The account's logins */

// renameLogin changes what a device is called in the list, which is the only
// thing telling two apart. Ungated, and the one action here with no challenge in
// front of it: a name is not a credential.
func (a *App) renameLogin(sessionID, name string) {
	a.showPrompt(ui.Prompt{
		Title:  "Rename login",
		Action: "Rename",
		Busy:   "Renaming...",
		Fields: []ui.PromptField{{Label: "Name", Placeholder: "Laptop", Value: name}},
		OnSubmit: func(values []string) {
			a.backgroundThen(
				func() error { return a.client.RenameSession(sessionID, values[0]) },
				func(err error) {
					log.Printf("rename session %s: %v", sessionID, err)
					a.failPrompt("Could not rename that login.")
				},
				func() {
					a.closeOverlay()
					a.refreshSecurity()
				},
			)
		},
	})
}

// revokeLogin signs one device out. Revoking the one in front of you is allowed
// and is what the confirmation has to say plainly — the gateway announces it and
// this window goes back to the login screen.
func (a *App) revokeLogin(sessionID, name string) {
	body := name + " will be signed out at once and will need the password to sign back in."
	if sessionID == a.client.SessionID() {
		body = "This is the device you are using. You will be returned to the login screen."
	}

	a.confirm(ui.Confirm{
		Title:  "Revoke login",
		Body:   body,
		Action: "Revoke",
		Tone:   ui.ToneDanger,
		OnConfirm: func() {
			a.challenge("Revoking a login needs you to confirm it's you.", func(ticket string) {
				a.backgroundThen(
					func() error { return a.client.RevokeSession(sessionID, ticket) },
					a.notifyFailure("revoke session "+sessionID, "Could not revoke that login."),
					func() {
						a.refreshSecurity()
						a.notify(ui.ToneWarning, "%s was signed out.", name)
					},
				)
			})
		},
	})
}

// revokeOthers is the one that leaves this device signed in, which is what
// somebody who thinks a login has been taken actually wants. Not the same as
// "Log out everywhere" on the Account section, which includes this one — the two
// are worded apart because they read alike.
func (a *App) revokeOthers() {
	a.confirm(ui.Confirm{
		Title:  "Sign out everywhere else",
		Body:   "Every other device signed in as this account is signed out. This one stays signed in.",
		Action: "Sign out others",
		Tone:   ui.ToneDanger,
		OnConfirm: func() {
			a.challenge("Signing your other devices out needs you to confirm it's you.", func(ticket string) {
				a.backgroundThen(
					func() error { return a.client.RevokeOtherSessions(ticket) },
					a.notifyFailure("revoke other sessions", "Could not sign your other devices out."),
					func() {
						a.refreshSecurity()
						a.notify(ui.ToneWarning, "Your other devices were signed out.")
					},
				)
			})
		},
	})
}
