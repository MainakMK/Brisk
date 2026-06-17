package api

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"brisk-control/internal/adminauth"
	"brisk-control/internal/identity"
	"brisk-control/internal/store"
)

// Human (dashboard/API) authentication — Phase 3.7 Step 3. Two caller types share
// one identity core (internal/identity):
//   - Dashboard UI: session COOKIE (HttpOnly/Secure/SameSite) + double-submit CSRF.
//   - Scripts/automation: bearer ADMIN TOKEN (Authorization: Bearer brisk_admin_...).
// The agent path (/agent/*, internal/auth per-agent tokens) is UNTOUCHED.

const (
	sessionCookie = "brisk_session"
	csrfCookie    = "brisk_csrf"
	csrfHeader    = "X-CSRF-Token"
	sessionTTL    = 12 * time.Hour
)

// resolveIdentity authenticates the caller via bearer admin token OR session
// cookie. Returns the identity, the session's CSRF hash ("" for token auth), and
// ok=false when unauthenticated.
func (a *API) resolveIdentity(r *http.Request) (id identity.Identity, csrfHash string, ok bool) {
	// 1) Bearer admin token (programmatic).
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimPrefix(h, "Bearer ")
		if adminauth.IsAdminToken(tok) {
			cands, err := a.store.ActiveAdminTokensByPrefix(r.Context(), adminauth.AdminPrefix(tok))
			if err == nil {
				want := adminauth.Hash(tok)
				for _, c := range cands {
					if subtleEqual(c.Hash, want) {
						_ = a.store.TouchAdminToken(r.Context(), c.ID)
						return identity.Identity{AccountID: c.AccountID, Role: c.Role, Method: "token"}, "", true
					}
				}
			}
		}
		return identity.Identity{}, "", false // a Bearer header that isn't a valid admin token
	}
	// 2) Session cookie (dashboard UI).
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return identity.Identity{}, "", false
	}
	sess, err := a.store.GetSession(r.Context(), adminauth.Hash(c.Value))
	if err != nil {
		return identity.Identity{}, "", false
	}
	return identity.Identity{AccountID: sess.AccountID, Role: sess.Role, Method: "session"}, sess.CSRFHash, true
}

// requireAuth resolves the caller to an identity context, enforcing CSRF for
// cookie-based state-changing requests. 401 when unauthenticated.
func (a *API) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, csrfHash, ok := a.resolveIdentity(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// CSRF: cookie sessions on state-changing verbs must echo the CSRF token
		// (double-submit, bound to the session). Bearer-token callers are exempt
		// (no ambient cookie -> not subject to CSRF).
		if id.Method == "session" && isStateChanging(r.Method) {
			got := r.Header.Get(csrfHeader)
			if got == "" || !subtleEqual(adminauth.Hash(got), csrfHash) {
				writeError(w, http.StatusForbidden, "csrf token missing or invalid")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(identity.NewContext(r.Context(), id)))
	})
}

// requireAdmin gates infrastructure routes (servers, DNS, stats, health, purge):
// admin-only regardless of account. Runs after requireAuth.
func (a *API) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err := identity.RequireAdmin(id); err != nil {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func subtleEqual(a, b string) bool {
	// constant-time over equal-length hex strings (both are sha256 hex here)
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// --- handlers: login / logout / refresh / me ---

type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// login verifies admin credentials (argon2), rate-limits by IP+email, and on
// success establishes a session cookie + CSRF cookie. Error messages are uniform
// (no user enumeration).
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var in loginInput
	if !decode(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	key := clientIP(r) + "|" + email
	now := time.Now()
	if !a.loginLimiter.Allowed(key, now) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}

	acc, err := a.store.GetAccountByEmail(r.Context(), email)
	valid := err == nil && acc.PasswordHash != nil && adminauth.VerifyPassword(in.Password, *acc.PasswordHash)
	if !valid {
		a.loginLimiter.Fail(key, now)
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	a.loginLimiter.Reset(key)

	if err := a.establishSession(w, r, acc.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start session")
		return
	}
	writeJSON(w, http.StatusOK, a.identityView(acc))
}

// logout clears the session (idempotent — always clears the cookies).
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = a.store.DeleteSession(r.Context(), adminauth.Hash(c.Value))
	}
	a.clearSessionCookies(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// refresh rotates the session id + CSRF + expiry (sliding window with rotation).
func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	newID, e1 := adminauth.NewSessionID()
	newCSRF, e2 := adminauth.NewCSRFToken()
	if e1 != nil || e2 != nil {
		writeError(w, http.StatusInternalServerError, "could not rotate session")
		return
	}
	exp := time.Now().Add(sessionTTL)
	if err := a.store.RotateSession(r.Context(), adminauth.Hash(c.Value), adminauth.Hash(newID), adminauth.Hash(newCSRF), exp); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	a.setSessionCookies(w, newID, newCSRF, exp)
	writeJSON(w, http.StatusOK, map[string]string{"status": "refreshed"})
}

// authMe returns the current identity (for the SPA to know who is logged in + role).
func (a *API) authMe(w http.ResponseWriter, r *http.Request) {
	id, _ := identity.FromContext(r.Context())
	acc, err := a.store.GetAccountByID(r.Context(), id.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.identityView(acc))
}

func (a *API) identityView(acc store.Account) map[string]any {
	email := ""
	if acc.Email != nil {
		email = *acc.Email
	}
	return map[string]any{"account_id": acc.ID, "role": acc.Role, "name": acc.Name, "email": email}
}

// --- session cookie helpers ---

func (a *API) establishSession(w http.ResponseWriter, r *http.Request, accountID int64) error {
	id, err := adminauth.NewSessionID()
	if err != nil {
		return err
	}
	csrf, err := adminauth.NewCSRFToken()
	if err != nil {
		return err
	}
	exp := time.Now().Add(sessionTTL)
	if err := a.store.CreateSession(r.Context(), adminauth.Hash(id), adminauth.Hash(csrf), accountID, r.UserAgent(), exp); err != nil {
		return err
	}
	a.setSessionCookies(w, id, csrf, exp)
	return nil
}

func (a *API) setSessionCookies(w http.ResponseWriter, sessionID, csrf string, exp time.Time) {
	// Session id: HttpOnly (JS can't read it -> XSS-resistant).
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: sessionID, Path: "/", Expires: exp,
		HttpOnly: true, Secure: a.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
	// CSRF token: readable by the SPA so it can echo it in the X-CSRF-Token header.
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookie, Value: csrf, Path: "/", Expires: exp,
		HttpOnly: false, Secure: a.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (a *API) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", Expires: time.Unix(0, 0), MaxAge: -1,
			HttpOnly: name == sessionCookie, Secure: a.cookieSecure, SameSite: http.SameSiteLaxMode,
		})
	}
}

// --- handlers: change password + admin API tokens ---

type changePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password" validate:"min=10"`
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	id, _ := identity.FromContext(r.Context())
	var in changePasswordInput
	if !decode(w, r, &in) {
		return
	}
	acc, err := a.store.GetAccountByID(r.Context(), id.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if acc.PasswordHash == nil || !adminauth.VerifyPassword(in.CurrentPassword, *acc.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	hash, err := adminauth.HashPassword(in.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if err := a.store.SetAccountCredentials(r.Context(), acc.ID, nil, hash); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Log out other sessions, then re-establish this one (rotate after credential change).
	_ = a.store.DeleteAccountSessions(r.Context(), acc.ID)
	_ = a.establishSession(w, r, acc.ID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "password changed"})
}

type createTokenInput struct {
	Name string `json:"name"`
}

// createAdminToken mints a bearer token for the caller's account. The plaintext is
// returned ONCE; only its hash is stored.
func (a *API) createAdminToken(w http.ResponseWriter, r *http.Request) {
	id, _ := identity.FromContext(r.Context())
	var in createTokenInput
	_ = decodeOptional(r, &in)
	tok, err := adminauth.NewAdminToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate token")
		return
	}
	newID, err := a.store.CreateAdminToken(r.Context(), id.AccountID, strings.TrimSpace(in.Name), adminauth.AdminPrefix(tok), adminauth.Hash(tok))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// token shown once
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": newID, "name": in.Name, "prefix": adminauth.AdminPrefix(tok), "token": tok,
	})
}

func (a *API) listAdminTokens(w http.ResponseWriter, r *http.Request) {
	id, _ := identity.FromContext(r.Context())
	toks, err := a.store.ListAdminTokens(r.Context(), id.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toks)
}

func (a *API) revokeAdminToken(w http.ResponseWriter, r *http.Request) {
	id, _ := identity.FromContext(r.Context())
	tid, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if err := a.store.RevokeAdminToken(r.Context(), id.AccountID, tid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "token not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// deleteAdminToken permanently removes an ALREADY-REVOKED token from the list (tidy-up). An
// active token can't be deleted here — revoke it first. 404 if absent/not owned/still active.
func (a *API) deleteAdminToken(w http.ResponseWriter, r *http.Request) {
	id, _ := identity.FromContext(r.Context())
	tid, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if err := a.store.DeleteAdminToken(r.Context(), id.AccountID, tid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "token not found, not yours, or still active (revoke it first)")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
