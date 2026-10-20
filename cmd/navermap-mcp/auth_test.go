package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBasicAuth(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := basicAuth(ok, "user", "pass")

	cases := []struct {
		name       string
		user, pass string
		setAuth    bool
		want       int
	}{
		{"no header", "", "", false, http.StatusUnauthorized},
		{"wrong pass", "user", "nope", true, http.StatusUnauthorized},
		{"wrong user", "nope", "pass", true, http.StatusUnauthorized},
		{"ok", "user", "pass", true, http.StatusOK},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if c.setAuth {
			req.SetBasicAuth(c.user, c.pass)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestBasicAuthFromEnv(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	t.Setenv("MCP_BASIC_USER", "")
	t.Setenv("MCP_BASIC_PASS", "")
	if _, enabled, err := basicAuthFromEnv(ok); err != nil || enabled {
		t.Fatalf("unset: enabled=%v err=%v, want disabled", enabled, err)
	}

	t.Setenv("MCP_BASIC_USER", "user")
	if _, _, err := basicAuthFromEnv(ok); err == nil {
		t.Fatal("only user set: want error")
	}

	t.Setenv("MCP_BASIC_PASS", "pass")
	if _, enabled, err := basicAuthFromEnv(ok); err != nil || !enabled {
		t.Fatalf("both set: enabled=%v err=%v, want enabled", enabled, err)
	}
}
