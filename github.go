package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// GitHub device flow
//
// Device flow is the only OAuth flow that ships safely in a public binary: it
// uses a client id and NO client secret, so nothing extractable from a release
// tarball grants anything. The reporter authenticates as themselves, which
// also means issues are correctly attributed and repliable.
// ---------------------------------------------------------------------------

// ghClientID identifies serve's GitHub OAuth App. It is public by design:
// the device flow uses no client secret, which is exactly what makes it safe
// to ship in an open-source binary. Set it here once the app is registered, or
// override with SERVE_GITHUB_CLIENT_ID.
//
// While it is empty, filing degrades to `serve report export`, which needs no
// credentials at all.
var ghClientID = "Ov23liJT7RhGN8e3MumS"

var (
	ghOwner = "ericbryant24"
	ghRepo  = "serve"
)

const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

func githubClientID() string {
	if v := strings.TrimSpace(os.Getenv("SERVE_GITHUB_CLIENT_ID")); v != "" {
		return v
	}
	return strings.TrimSpace(ghClientID)
}

func githubTarget() (owner, repo string) {
	owner, repo = ghOwner, ghRepo
	if v := strings.TrimSpace(os.Getenv("SERVE_GITHUB_REPO")); v != "" {
		if o, r, ok := strings.Cut(v, "/"); ok && o != "" && r != "" {
			owner, repo = o, r
		}
	}
	return
}

// errNoClientID is returned when no OAuth App has been registered yet.
var errNoClientID = errors.New(
	"GitHub filing is not configured: no OAuth client id was built in.\n" +
		"Set SERVE_GITHUB_CLIENT_ID, or use `serve report export <id>` to get the\n" +
		"markdown and file it yourself.")

// ghClient carries the endpoints so tests can point the whole flow at an
// httptest.Server, and a sleep hook so polling tests do not actually wait.
type ghClient struct {
	deviceCodeURL  string
	accessTokenURL string
	apiBase        string
	http           *http.Client
	sleep          func(time.Duration)
}

// newGHClientFn is the constructor handlers call. Tests replace it so a code
// path that reaches GitHub is impossible to hit accidentally from a test.
var newGHClientFn = newGHClient

func newGHClient() *ghClient {
	c := &ghClient{
		deviceCodeURL:  "https://github.com/login/device/code",
		accessTokenURL: "https://github.com/login/oauth/access_token",
		apiBase:        "https://api.github.com",
		// No timeout on the client: DevicePoll deliberately holds a request
		// open while the reporter approves in their browser.
		http:  &http.Client{},
		sleep: time.Sleep,
	}
	// Endpoint overrides, so the flow can be driven against a stub instead of
	// github.com. The integration suite uses these to exercise authorization
	// and issue creation end to end without touching the network.
	if v := os.Getenv("SERVE_GITHUB_DEVICE_URL"); v != "" {
		c.deviceCodeURL = v
	}
	if v := os.Getenv("SERVE_GITHUB_TOKEN_URL"); v != "" {
		c.accessTokenURL = v
	}
	if v := os.Getenv("SERVE_GITHUB_API"); v != "" {
		c.apiBase = v
	}
	return c
}

type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

func (c *ghClient) postForm(ctx context.Context, endpoint string, form url.Values, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("github returned %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// DeviceStart requests a user code the reporter types into github.com.
func (c *ghClient) DeviceStart(ctx context.Context, clientID string) (*DeviceCode, error) {
	var out struct {
		DeviceCode
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	form := url.Values{"client_id": {clientID}, "scope": {"public_repo"}}
	if err := c.postForm(ctx, c.deviceCodeURL, form, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("github: %s", firstNonEmpty(out.Description, out.Error))
	}
	if out.DeviceCode.DeviceCode == "" || out.UserCode == "" {
		return nil, errors.New("github returned an incomplete device code response")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	if out.VerificationURI == "" {
		out.VerificationURI = "https://github.com/login/device"
	}
	return &out.DeviceCode, nil
}

// DevicePoll blocks until the reporter approves, declines, or the code expires.
func (c *ghClient) DevicePoll(ctx context.Context, clientID string, dc *DeviceCode) (string, error) {
	interval := time.Duration(dc.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(max(dc.ExpiresIn, 60)) * time.Second)

	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if time.Now().After(deadline) {
			return "", errors.New("the device code expired before it was approved")
		}
		c.sleep(interval)

		var out struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
			Description string `json:"error_description"`
			Interval    int    `json:"interval"`
		}
		form := url.Values{
			"client_id":   {clientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {deviceGrantType},
		}
		if err := c.postForm(ctx, c.accessTokenURL, form, &out); err != nil {
			return "", err
		}

		switch out.Error {
		case "":
			if out.AccessToken == "" {
				return "", errors.New("github returned an empty access token")
			}
			return out.AccessToken, nil
		case "authorization_pending":
			// keep waiting
		case "slow_down":
			// GitHub asks us to back off; honour the interval it returns.
			if out.Interval > 0 {
				interval = time.Duration(out.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
		case "expired_token":
			return "", errors.New("the device code expired before it was approved")
		case "access_denied":
			return "", errors.New("authorization was declined on github.com")
		default:
			return "", fmt.Errorf("github: %s", firstNonEmpty(out.Description, out.Error))
		}
	}
}

// CreateIssue posts the report and returns the new issue URL.
func (c *ghClient) CreateIssue(ctx context.Context, token, owner, repo string, r *Report) (string, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"title":  r.IssueTitle(),
		"body":   r.Markdown(),
		"labels": r.IssueLabels(),
	})
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues", strings.TrimSuffix(c.apiBase, "/"), owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		HTMLURL string `json:"html_url"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("github rejected the token (%s). Run `serve report login` to re-authorize", resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("github returned %s: %s", resp.Status, out.Message)
	}
	if out.HTMLURL == "" {
		return "", errors.New("github accepted the issue but returned no URL")
	}
	return out.HTMLURL, nil
}

// ---------------------------------------------------------------------------
// Token cache
// ---------------------------------------------------------------------------

type ghToken struct {
	Token   string `json:"token"`
	Scope   string `json:"scope"`
	SavedAt string `json:"saved_at"`
}

func ghTokenPath() string { return filepath.Join(homeDir(), ".serve", "github.json") }

func loadGHToken() string {
	data, err := os.ReadFile(ghTokenPath())
	if err != nil {
		return ""
	}
	var t ghToken
	if json.Unmarshal(data, &t) != nil {
		return ""
	}
	return t.Token
}

func saveGHToken(token string) error {
	if err := os.MkdirAll(filepath.Dir(ghTokenPath()), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ghToken{
		Token:   token,
		Scope:   "public_repo",
		SavedAt: time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ghTokenPath(), data, 0600)
}

func clearGHToken() error {
	err := os.Remove(ghTokenPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
