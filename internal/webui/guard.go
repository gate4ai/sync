package webui

import (
	"net"
	"net/http"
	"strings"
)

// guard keeps every page other than this client's own off the settings
// server. Loopback alone does not do that: any web page the person has open
// can make their browser send requests to 127.0.0.1, and the settings page
// sits on a well-known port.
//
//   - Host must name this server: 127.0.0.1:<port> or localhost:<port>. A
//     DNS-rebinding page — its own domain re-pointed at 127.0.0.1 — arrives
//     with that domain as Host, and would otherwise read /browse as if it
//     were same-origin.
//   - A POST must come from this server's own pages: the standard library's
//     CrossOriginProtection rejects it when Sec-Fetch-Site or Origin says it
//     came from another site, so a form on someone else's page cannot add or
//     remove a folder. Requests carrying neither header are not from a
//     browser and are let through; a local program can reach the files
//     directly anyway.
//   - No page may be framed, so the "Sync it anyway" button of a confirm
//     page cannot be clicked through someone else's page laid over it.
//
// Every GET handler only reads, which is what lets safe methods through
// CrossOriginProtection unchecked.
func (s *Server) guard(next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(s.Addr())
	allowed := []string{"127.0.0.1:" + port, "localhost:" + port}
	csrf := http.NewCrossOriginProtection()
	next = csrf.Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, allowed) {
			http.Error(w, "this page is only served at http://"+allowed[0]+"/", http.StatusMisdirectedRequest)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func hostAllowed(host string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(host, a) {
			return true
		}
	}
	return false
}
