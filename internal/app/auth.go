package app

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type loginWindow struct {
	Count int
	Until time.Time
}

// RemoteAddr is intentional: forwarded headers are untrusted. Behind a proxy
// the origin budget is shared, while the account budget remains independent.
func (a *App) allowLogin(r *http.Request, now time.Time) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	account := fmt.Sprintf("account:%x", sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(r.Form.Get("username"))))))
	keys := []string{"origin:" + host, account}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.loginLimits) >= 10000 {
		for k, v := range a.loginLimits {
			if !now.Before(v.Until) {
				delete(a.loginLimits, k)
			}
		}
	}
	for i, k := range keys {
		v := a.loginLimits[k]
		limit := 60
		if i == 1 {
			limit = 10
		}
		if now.Before(v.Until) && v.Count >= limit {
			return false
		}
		if _, exists := a.loginLimits[k]; !exists && len(a.loginLimits) >= 10000 {
			return false
		}
	}
	for _, k := range keys {
		v := a.loginLimits[k]
		if !now.Before(v.Until) {
			v = loginWindow{Until: now.Add(time.Minute)}
		}
		v.Count++
		a.loginLimits[k] = v
	}
	return true
}

// Compare a fixed hash for unknown accounts too, avoiding the fast failure path.
const dummyPasswordHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

func validLogin(rows []Row, password string) bool {
	hash := dummyPasswordHash
	if len(rows) == 1 {
		hash = str(rows[0]["password"])
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return len(rows) == 1 && err == nil
}
func (a *App) revokeSessions(uid string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, s := range a.sessions {
		if s.UserID == uid {
			delete(a.sessions, id)
		}
	}
}
