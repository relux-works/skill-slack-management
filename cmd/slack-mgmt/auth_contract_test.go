package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/relux-works/skill-slack-management/internal/config"
	"github.com/relux-works/skill-slack-management/internal/redact"
)

func TestCLIAuthLifecycleNeverPrintsCredentials(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	token := "xoxe.xoxb-1-auth-lifecycle-secret"
	opts := entrypointOptions{dir: root, env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}

	commands := [][]string{
		{"auth", "set-access", "--source", "file", "--workspace", "Acme", "--token", token},
		{"auth", "resolve", "--source", "file", "--workspace", "acme"},
		{"auth", "whoami", "--source", "file", "--workspace", "acme", "--check=false"},
		{"auth", "clear-access", "--source", "file", "--workspace", "acme"},
	}

	for _, args := range commands {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, args...)
		if err != nil {
			t.Fatalf("slack-mgmt %v error = %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, token) || strings.Contains(stdout+stderr, "auth-lifecycle-secret") {
			t.Fatalf("slack-mgmt %v leaked credential", args)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("slack-mgmt %v returned invalid JSON: %v\n%s", args, err, stdout)
		}
		if result["workspace"] != "acme" {
			t.Errorf("slack-mgmt %v workspace = %#v, want acme", args, result["workspace"])
		}
		if result["security"] != "lower" || result["plaintext_file_possible"] != true {
			t.Errorf("slack-mgmt %v lower-security metadata = %#v/%#v", args, result["security"], result["plaintext_file_possible"])
		}
	}
}

func TestCLIAuthFileReadsRejectInvalidProtectionAndPreserveEnvPrecedence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode attack is covered here; Windows DACL enforcement has a platform test")
	}

	tests := []struct {
		name      string
		chmodPath func(string) string
		mode      os.FileMode
	}{
		{name: "broad directory mode", chmodPath: filepath.Dir, mode: 0o755},
		{name: "broad file mode", chmodPath: func(path string) string { return path }, mode: 0o644},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			fileToken := "xoxb-permission-invalid-cli-secret"
			opts := entrypointOptions{dir: root, env: map[string]string{
				"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
			}}
			stdout, stderr, err := runEntrypointWithOptions(t, opts,
				"auth", "set-access", "--source", "file", "--workspace", "acme", "--token", fileToken)
			if err != nil {
				t.Fatalf("auth set-access error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}

			configPath := cliAuthConfigPath(home)
			if err := os.Chmod(tt.chmodPath(configPath), tt.mode); err != nil {
				t.Fatalf("Chmod() error = %v", err)
			}

			commands := [][]string{
				{"auth", "resolve", "--source", "file", "--workspace", "acme"},
				{"auth", "resolve", "--source", "env_or_file", "--workspace", "acme"},
				{"auth", "whoami", "--source", "file", "--workspace", "acme", "--check=false"},
			}
			for _, args := range commands {
				stdout, stderr, err = runEntrypointWithOptions(t, opts, args...)
				if err == nil {
					t.Fatalf("slack-mgmt %v accepted permission-invalid auth file\nstdout:\n%s\nstderr:\n%s", args, stdout, stderr)
				}
				if strings.Contains(stdout+stderr, fileToken) || strings.Contains(stdout+stderr, "permission-invalid-cli-secret") {
					t.Fatalf("slack-mgmt %v leaked rejected credential", args)
				}
			}

			envToken := "xoxp-valid-env-short-circuit-secret"
			envOpts := opts
			envOpts.env = map[string]string{
				"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
				config.AccessTokenEnvVar: envToken,
			}
			for _, args := range [][]string{
				{"auth", "resolve", "--source", "env_or_file", "--workspace", "acme"},
				{"auth", "whoami", "--source", "env_or_file", "--workspace", "acme", "--check=false"},
			} {
				stdout, stderr, err = runEntrypointWithOptions(t, envOpts, args...)
				if err != nil {
					t.Fatalf("slack-mgmt %v did not short-circuit to env: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
				}
				if strings.Contains(stdout+stderr, envToken) || strings.Contains(stdout+stderr, fileToken) {
					t.Fatalf("slack-mgmt %v leaked credential", args)
				}
				var result map[string]any
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatalf("slack-mgmt %v returned invalid JSON: %v\n%s", args, err, stdout)
				}
				if result["resolved_from"] != "env" || result["access_token_present"] != true {
					t.Fatalf("slack-mgmt %v result = %#v, want env credential presence", args, result)
				}
			}
		})
	}
}

func cliAuthConfigPath(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "slack-mgmt", "auth.json")
	}
	return filepath.Join(home, ".config", "slack-mgmt", "auth.json")
}

func TestRunAuthSetAccessTokenStdinAndDualInputGate(t *testing.T) {
	tempDir := t.TempDir()
	resolver := config.NewResolver(config.Runtime{
		GOOS:          "linux",
		UserConfigDir: func() (string, error) { return tempDir, nil },
		Getenv:        func(string) string { return "" },
	}, nil)

	previousStdout := cliStdout
	previousStderr := cliStderr
	previousStdin := cliStdin
	previousExit := exitProcess
	previousLoader := redactorLoader
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	testRedactor, err := redact.New([]byte("auth-stdin-test-salt"))
	if err != nil {
		t.Fatalf("redact.New() error = %v", err)
	}
	cliStdout = stdout
	cliStderr = stderr
	redactorLoader = func() (*redact.Redactor, error) { return testRedactor, nil }
	exitCodes := []int{}
	exitProcess = func(code int) { exitCodes = append(exitCodes, code) }
	t.Cleanup(func() {
		cliStdout = previousStdout
		cliStderr = previousStderr
		cliStdin = previousStdin
		exitProcess = previousExit
		redactorLoader = previousLoader
	})

	token := "xoxb-stdin-secret"
	cliStdin = strings.NewReader(token + "\n")
	runAuthSetAccess([]string{"--source", "file", "--workspace", "acme", "--token-stdin"}, resolver)
	if len(exitCodes) != 0 {
		t.Fatalf("token-stdin success called exit: %v; stderr=%q", exitCodes, stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), token) {
		t.Fatalf("token-stdin success leaked credential")
	}
	resolved, err := resolver.ResolveToken(config.ResolveOptions{Source: config.SourceFile, Workspace: "acme"})
	if err != nil || resolved.Token != token {
		t.Fatalf("ResolveToken() token/error = %q/%v", resolved.Token, err)
	}

	stdout.Reset()
	stderr.Reset()
	cliStdin = strings.NewReader("xoxb-should-not-be-read\n")
	runAuthSetAccess([]string{"--source", "file", "--workspace", "beta", "--token", "xoxb-argument-secret", "--token-stdin"}, resolver)
	if len(exitCodes) != 1 || exitCodes[0] != 1 {
		t.Fatalf("dual-input gate exits = %v, want [1]", exitCodes)
	}
	if strings.Contains(stdout.String()+stderr.String(), "argument-secret") || strings.Contains(stdout.String()+stderr.String(), "should-not-be-read") {
		t.Fatalf("dual-input refusal leaked credential material")
	}
	if _, err := resolver.ResolveToken(config.ResolveOptions{Source: config.SourceFile, Workspace: "beta"}); !os.IsNotExist(err) && err != config.ErrAccessTokenNotFound {
		t.Fatalf("dual-input gate wrote beta profile; resolve error = %v", err)
	}
}

func TestNewSlackClientFromResolverRejectsCredentialExfiltrationOrigin(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	resolver := config.NewResolver(config.Runtime{
		GOOS:          "linux",
		UserConfigDir: func() (string, error) { return t.TempDir(), nil },
		Getenv: func(key string) string {
			if key == config.AccessTokenEnvVar {
				return "xoxb-origin-gate-secret"
			}
			return ""
		},
	}, nil)

	client, err := newSlackClientFromResolver(resolver, config.SourceEnv, "acme", server.URL)
	if !errors.Is(err, ErrCredentialOriginRefused) || client != nil {
		t.Fatalf("newSlackClientFromResolver() client/error = %#v/%v, want origin refusal", client, err)
	}
	if requests != 0 {
		t.Fatalf("production client factory contacted refused origin %d times", requests)
	}

	token := "xoxb-cli-origin-secret"
	opts := entrypointOptions{dir: t.TempDir(), env: map[string]string{
		"SLACK_ACCESS_TOKEN": token,
	}}
	commands := [][]string{
		{"q", "auth_test() { team_id }", "--source", "env", "--base-url", server.URL, "--format", "json"},
		{"auth", "whoami", "--source", "env", "--workspace", "acme", "--base-url", server.URL},
	}
	for _, args := range commands {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, args...)
		if err == nil || !strings.Contains(stderr, ErrCredentialOriginRefused.Error()) {
			t.Fatalf("CLI %v origin gate error = %v, stdout=%q stderr=%q", args, err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, token) || requests != 0 {
			t.Fatalf("CLI %v origin gate leaked/contacted refused origin; requests=%d", args, requests)
		}
	}
}

func TestStoredCredentialOriginAndRedirectGateRejectsNarrowedChecks(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		allowed bool
	}{
		{name: "canonical base", raw: "https://slack.com/api", allowed: true},
		{name: "canonical method", raw: "https://slack.com/api/auth.test", allowed: true},
		{name: "http downgrade", raw: "http://slack.com/api/auth.test"},
		{name: "lookalike host", raw: "https://slack.com.evil.test/api/auth.test"},
		{name: "userinfo host confusion", raw: "https://slack.com@evil.test/api/auth.test"},
		{name: "nonstandard port", raw: "https://slack.com:443/api/auth.test"},
		{name: "prefix confusion", raw: "https://slack.com/api.evil/auth.test"},
		{name: "normalized traversal", raw: "https://slack.com/api/%2e%2e/escape"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, tt.raw, nil)
			if err != nil {
				t.Fatalf("NewRequest() error = %v", err)
			}
			if got := isSlackAPIURL(request.URL); got != tt.allowed {
				t.Fatalf("isSlackAPIURL(%q) = %v, want %v", tt.raw, got, tt.allowed)
			}
		})
	}

	secret := "xoxb-redirect-gate-secret"
	canonicalRequests := 0
	foreignRequests := 0
	previousDefaultClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "slack.com" {
			canonicalRequests++
			if got := request.Header.Get("Authorization"); got != "Bearer "+secret {
				t.Fatalf("canonical request authorization = %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://foreign.example/steal"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		}
		foreignRequests++
		if got := request.Header.Get("Authorization"); got != "" {
			t.Fatalf("foreign redirect received authorization %q", got)
		}
		return nil, errors.New("foreign transport must not be reached")
	})}
	t.Cleanup(func() { http.DefaultClient = previousDefaultClient })

	resolver := config.NewResolver(config.Runtime{
		GOOS:          "linux",
		UserConfigDir: func() (string, error) { return t.TempDir(), nil },
		Getenv: func(key string) string {
			if key == config.AccessTokenEnvVar {
				return secret
			}
			return ""
		},
	}, nil)
	client, err := newSlackClientFromResolver(resolver, config.SourceEnv, "acme", "")
	if err != nil {
		t.Fatalf("newSlackClientFromResolver() error = %v", err)
	}
	if _, err := client.AuthTest(t.Context()); !errors.Is(err, ErrCredentialOriginRefused) {
		t.Fatalf("AuthTest() redirect error = %v, want credential origin refusal", err)
	}
	if canonicalRequests != 1 || foreignRequests != 0 {
		t.Fatalf("redirect transport calls = canonical:%d foreign:%d, want 1/0", canonicalRequests, foreignRequests)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
