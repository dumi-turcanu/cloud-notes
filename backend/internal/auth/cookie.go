package auth

import (
	"math"
	"net/http"
	"time"
)

// CookieName returns the session cookie name: the __Host- prefix is only
// valid (and only used) when the cookie is Secure.
func CookieName(secure bool) string {
	if secure {
		return "__Host-cn_session"
	}
	return "cn_session"
}

// SetSessionCookie sends the session cookie with Max-Age equal to the
// remaining absolute lifetime.
func SetSessionCookie(w http.ResponseWriter, secure bool, token string, expiresAt, now time.Time) {
	maxAge := int(math.Ceil(expiresAt.Sub(now).Seconds()))
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName(secure),
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie instructs the browser to drop the session cookie.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName(secure),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// TokenFromRequest returns the session token from the request cookie, or "".
func TokenFromRequest(r *http.Request, secure bool) string {
	c, err := r.Cookie(CookieName(secure))
	if err != nil {
		return ""
	}
	return c.Value
}
