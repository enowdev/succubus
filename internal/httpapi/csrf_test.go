package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests cover the hole reported in issue #1, verified by driving a real
// browser at the daemon before fixing it.
//
// Binding to loopback is not the protection it looks like. Any page the user
// visits can POST to 127.0.0.1, and a POST whose content type is "simple"
// (text/plain, form-encoded, multipart) is sent *without* a CORS preflight — so
// the browser never asks permission and the write lands. The attacker cannot
// read the response, which is why this is easy to miss.
//
// It matters here more than in most local daemons: a forged room message is
// injected into agent context by the hooks, making this a path from any web
// page the user visits into what their coding agents are told to do.

// prod is a server with the dev escape hatch off — how it runs for real users.
func prod() *Server { return &Server{Dev: false} }

// dev is a server started with --dev, where the Vite server on another port is
// a deliberately trusted origin.
func dev() *Server { return &Server{Dev: true} }

func TestCrossOriginWriteIsRefused(t *testing.T) {
	// The exact request shape from the report: a simple request, so no preflight.
	r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/tasks",
		strings.NewReader(`{"title":"csrf-created"}`))
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "text/plain")

	if err := prod().checkCrossOriginWrite(r); err == nil {
		t.Fatal("a cross-origin write was allowed; this is the reported CSRF hole")
	}
}

// TestNonBrowserClientsAreUnaffected is the constraint that makes an Origin
// check the right fix instead of a token: hooks, the CLI, MCP and curl send no
// Origin at all, so none of them need to change.
func TestNonBrowserClientsAreUnaffected(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		r := httptest.NewRequest(method, "/api/projects/p1/tasks", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		// deliberately no Origin
		if err := prod().checkCrossOriginWrite(r); err != nil {
			t.Errorf("%s from a non-browser client was refused: %v", method, err)
		}
	}
}

// TestDashboardCanStillWrite: the dashboard is served by the daemon itself, so
// its Origin is the daemon's. A fix that locked out the real UI would be worse
// than the bug.
func TestDashboardCanStillWrite(t *testing.T) {
	cases := []struct{ origin, host string }{
		{"http://127.0.0.1:7801", "127.0.0.1:7801"},
		{"http://localhost:7801", "127.0.0.1:7801"}, // reached by the other name
		{"http://127.0.0.1:7801", "localhost:7801"},
		{"http://[::1]:7801", "127.0.0.1:7801"}, // IPv6 loopback
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/tasks", strings.NewReader(`{}`))
		r.Host = c.host
		r.Header.Set("Origin", c.origin)
		if err := prod().checkCrossOriginWrite(r); err != nil {
			t.Errorf("the dashboard at %s could not write to %s: %v", c.origin, c.host, err)
		}
	}
}

// TestSafeMethodsAreNotBlocked: reads are already protected by the browser,
// which will not hand the response to a cross-origin page without CORS headers.
// Blocking them would break nothing useful and confuse anyone reading a trace.
func TestSafeMethodsAreNotBlocked(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		r := httptest.NewRequest(method, "/api/projects", nil)
		r.Header.Set("Origin", "https://evil.example")
		if err := prod().checkCrossOriginWrite(r); err != nil {
			t.Errorf("%s was refused: %v", method, err)
		}
	}
}

// TestAttackerCannotSpoofOriginBySuffix guards the string-matching trap: an
// attacker who registers evil-127.0.0.1.com must not be treated as local.
func TestAttackerCannotSpoofOriginBySuffix(t *testing.T) {
	for _, origin := range []string{
		"http://127.0.0.1.evil.example",
		"http://localhost.evil.example",
		"http://evil.example/127.0.0.1:7801",
		"http://127.0.0.1:9999", // right host, wrong port: a different origin
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/tasks", strings.NewReader(`{}`))
		r.Host = "127.0.0.1:7801"
		r.Header.Set("Origin", origin)
		if err := prod().checkCrossOriginWrite(r); err == nil {
			t.Errorf("%q was accepted as this daemon's own origin", origin)
		}
	}
}

// TestDevModeTrustsOnlyLoopback: --dev has to let the Vite server on port 5273
// write, since that is the whole development workflow. It must not become a
// blanket "allow everything" — a dev flag should widen what the developer's own
// machine can do, not what the internet can.
func TestDevModeTrustsOnlyLoopback(t *testing.T) {
	allowed := []string{
		"http://localhost:5273", // Vite
		"http://127.0.0.1:5273",
		"http://127.0.0.1:3000", // any other local dev server
	}
	for _, origin := range allowed {
		r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/tasks", strings.NewReader(`{}`))
		r.Host = "127.0.0.1:7801"
		r.Header.Set("Origin", origin)
		if err := dev().checkCrossOriginWrite(r); err != nil {
			t.Errorf("--dev refused the local dev server at %s: %v", origin, err)
		}
	}

	// Still refused in dev: a remote page is not made trustworthy by a flag
	// meant for local development.
	for _, origin := range []string{"https://evil.example", "http://127.0.0.1.evil.example"} {
		r := httptest.NewRequest(http.MethodPost, "/api/projects/p1/tasks", strings.NewReader(`{}`))
		r.Host = "127.0.0.1:7801"
		r.Header.Set("Origin", origin)
		if err := dev().checkCrossOriginWrite(r); err == nil {
			t.Errorf("--dev accepted a write from %s", origin)
		}
	}
}

// TestDecodeRequiresJSONContentType is the second half of the defence. A browser
// can only skip the CORS preflight when the content type is a "simple" one, so
// requiring application/json forces any browser write to ask permission first.
func TestDecodeRequiresJSONContentType(t *testing.T) {
	refuse := []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data"}
	for _, ct := range refuse {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"title":"x"}`))
		r.Header.Set("Content-Type", ct)
		var v map[string]any
		if err := decode(r, &v); err == nil {
			t.Errorf("decode accepted a body sent as %s; that is the preflight bypass", ct)
		}
	}

	accept := []string{"application/json", "application/json; charset=utf-8", "application/merge-patch+json"}
	for _, ct := range accept {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"title":"x"}`))
		r.Header.Set("Content-Type", ct)
		var v map[string]any
		if err := decode(r, &v); err != nil {
			t.Errorf("decode refused a legitimate %s body: %v", ct, err)
		}
	}

	// An absent header stays tolerated: some clients omit it on a body they
	// always send as JSON, and it is not the browser bypass being guarded.
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"title":"x"}`))
	var v map[string]any
	if err := decode(r, &v); err != nil {
		t.Errorf("decode refused a body with no Content-Type: %v", err)
	}
}

// TestUnmatchedAPIPathsAnswerInJSON guards a failure that hides its own cause.
//
// The SPA is mounted at "/", so any /api path that matches no route fell
// through to it: the client got 200 and an HTML page, tried to parse it as
// JSON, and reported `invalid character '<' looking for beginning of value` —
// which says nothing about the endpoint being wrong. It surfaced as an MCP
// tool failure, and finding the real cause took far longer than it should.
//
// A path with an empty id (/api/plans/) is the common way to hit this, since
// it happens whenever a caller interpolates a variable that turned out empty.
func TestUnmatchedAPIPathsAnswerInJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.MountDevNotice("http://localhost:5273")

	for _, path := range []string{
		"/api/",
		"/api/plans/",
		"/api/tasks/",
		"/api/nosuchendpoint",
		"/api/projects/p1/nosuchthing",
	} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)

		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("%s: Content-Type %q, want JSON — an API client cannot parse HTML", path, ct)
		}
		if body := w.Body.String(); strings.Contains(body, "<") {
			t.Errorf("%s: body looks like markup: %.60s", path, body)
		}
	}
}

// TestRealEndpointsAreUnaffected: the 404 rule must not shadow a route that
// does exist.
func TestRealEndpointsAreUnaffected(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.MountDevNotice("http://localhost:5273")

	r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code == http.StatusNotFound {
		t.Error("/api/health was swallowed by the unmatched-path rule")
	}

	// And a page route still gets the SPA, not a JSON error.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code == http.StatusNotFound {
		t.Error("the dashboard root now 404s")
	}
}
