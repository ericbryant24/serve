package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// testGH points a client at a stub server and makes polling instant, so the
// device-flow state machine can be exercised without waiting on real seconds.
func testGH(t *testing.T, h http.Handler) (*ghClient, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	var slept []time.Duration
	c := &ghClient{
		deviceCodeURL:  srv.URL + "/login/device/code",
		accessTokenURL: srv.URL + "/login/oauth/access_token",
		apiBase:        srv.URL,
		http:           srv.Client(),
		sleep:          func(d time.Duration) { slept = append(slept, d) },
	}
	return c, &slept
}

func writeJSONResp(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func TestDeviceStart(t *testing.T) {
	c, _ := testGH(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if got := r.Form.Get("scope"); got != "public_repo" {
			t.Errorf("scope = %q, want public_repo", got)
		}
		if r.Form.Get("client_id") != "cid" {
			t.Errorf("client_id = %q", r.Form.Get("client_id"))
		}
		// Device flow must never carry a client secret; that is what makes it
		// safe to ship the client id in a public binary.
		if r.Form.Get("client_secret") != "" {
			t.Error("a client secret was sent")
		}
		writeJSONResp(w, map[string]interface{}{
			"device_code": "dc", "user_code": "WDJB-MJHT",
			"verification_uri": "https://github.com/login/device",
			"expires_in":       900, "interval": 5,
		})
	}))

	dc, err := c.DeviceStart(context.Background(), "cid")
	if err != nil {
		t.Fatal(err)
	}
	if dc.UserCode != "WDJB-MJHT" || dc.DeviceCode != "dc" || dc.Interval != 5 {
		t.Fatalf("unexpected device code: %+v", dc)
	}
}

func TestDeviceStartError(t *testing.T) {
	c, _ := testGH(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONResp(w, map[string]string{
			"error": "unauthorized_client", "error_description": "device flow is not enabled",
		})
	}))
	_, err := c.DeviceStart(context.Background(), "cid")
	if err == nil || !strings.Contains(err.Error(), "device flow is not enabled") {
		t.Fatalf("expected the description to surface, got %v", err)
	}
}

func TestDevicePollPendingThenSuccess(t *testing.T) {
	calls := 0
	c, slept := testGH(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			writeJSONResp(w, map[string]string{"error": "authorization_pending"})
			return
		}
		writeJSONResp(w, map[string]string{"access_token": "gho_token"})
	}))

	tok, err := c.DevicePoll(context.Background(), "cid",
		&DeviceCode{DeviceCode: "dc", Interval: 5, ExpiresIn: 900})
	if err != nil {
		t.Fatal(err)
	}
	if tok != "gho_token" {
		t.Fatalf("token = %q", tok)
	}
	if len(*slept) != 3 {
		t.Errorf("slept %d times, want 3", len(*slept))
	}
}

// GitHub asks callers to back off; ignoring slow_down gets the client blocked.
func TestDevicePollHonoursSlowDown(t *testing.T) {
	calls := 0
	c, slept := testGH(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			writeJSONResp(w, map[string]interface{}{"error": "slow_down", "interval": 12})
		default:
			writeJSONResp(w, map[string]string{"access_token": "t"})
		}
	}))

	if _, err := c.DevicePoll(context.Background(), "cid",
		&DeviceCode{DeviceCode: "dc", Interval: 5, ExpiresIn: 900}); err != nil {
		t.Fatal(err)
	}
	if len(*slept) < 2 {
		t.Fatalf("expected at least two sleeps, got %v", *slept)
	}
	if (*slept)[0] != 5*time.Second {
		t.Errorf("first interval = %v, want 5s", (*slept)[0])
	}
	if (*slept)[1] != 12*time.Second {
		t.Errorf("interval after slow_down = %v, want 12s", (*slept)[1])
	}
}

func TestDevicePollTerminalErrors(t *testing.T) {
	cases := map[string]string{
		"access_denied": "declined",
		"expired_token": "expired",
	}
	for ghErr, want := range cases {
		t.Run(ghErr, func(t *testing.T) {
			c, _ := testGH(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSONResp(w, map[string]string{"error": ghErr})
			}))
			_, err := c.DevicePoll(context.Background(), "cid",
				&DeviceCode{DeviceCode: "dc", Interval: 1, ExpiresIn: 900})
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to mention %q", err, want)
			}
		})
	}
}

func TestCreateIssue(t *testing.T) {
	c, _ := testGH(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/ericbryant24/serve/issues" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth header = %q", got)
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["title"] != "Overflow" {
			t.Errorf("title = %v", body["title"])
		}
		labels, _ := body["labels"].([]interface{})
		if len(labels) != 1 || labels[0] != "bug" {
			t.Errorf("labels = %v", labels)
		}
		w.WriteHeader(201)
		writeJSONResp(w, map[string]string{"html_url": "https://github.com/x/y/issues/7"})
	}))

	url, err := c.CreateIssue(context.Background(), "tok", "ericbryant24", "serve",
		&Report{Kind: ReportKindBug, Title: "Overflow", Body: "runs over"})
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/x/y/issues/7" {
		t.Fatalf("url = %q", url)
	}
}

func TestCreateIssueAuthFailureIsActionable(t *testing.T) {
	c, _ := testGH(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		writeJSONResp(w, map[string]string{"message": "Bad credentials"})
	}))
	_, err := c.CreateIssue(context.Background(), "tok", "o", "r", &Report{Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "serve report login") {
		t.Fatalf("error should tell the user how to recover, got %v", err)
	}
}

func TestGHTokenRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if loadGHToken() != "" {
		t.Fatal("expected no token in a fresh home")
	}
	if err := saveGHToken("gho_secret"); err != nil {
		t.Fatal(err)
	}
	if got := loadGHToken(); got != "gho_secret" {
		t.Fatalf("token = %q", got)
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(home, ".serve", "github.json"))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Errorf("token file mode = %o, want 600", perm)
		}
	}

	if err := clearGHToken(); err != nil {
		t.Fatal(err)
	}
	if loadGHToken() != "" {
		t.Error("token survived logout")
	}
	if err := clearGHToken(); err != nil {
		t.Errorf("clearing an absent token should be a no-op, got %v", err)
	}
}

func TestGithubTargetOverride(t *testing.T) {
	t.Setenv("SERVE_GITHUB_REPO", "acme/widgets")
	o, r := githubTarget()
	if o != "acme" || r != "widgets" {
		t.Fatalf("target = %s/%s", o, r)
	}
	t.Setenv("SERVE_GITHUB_REPO", "garbage-without-slash")
	o, r = githubTarget()
	if o != "ericbryant24" || r != "serve" {
		t.Fatalf("malformed override should fall back, got %s/%s", o, r)
	}
}
