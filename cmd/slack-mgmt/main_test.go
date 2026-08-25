package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/relux-works/skill-slack-management/internal/attachments"
	"github.com/relux-works/skill-slack-management/internal/config"
	"github.com/relux-works/skill-slack-management/internal/provider"
	"github.com/relux-works/skill-slack-management/internal/query"
	"github.com/relux-works/skill-slack-management/internal/redact"
	"github.com/relux-works/skill-slack-management/internal/slack"
)

const entrypointHelperEnv = "SLACK_MGMT_ENTRYPOINT_HELPER"
const signalContextHelperEnv = "SLACK_MGMT_SIGNAL_CONTEXT_HELPER"

type browserRunnerFunc func(context.Context, slack.BrowserCommand) (slack.BrowserCommandResult, error)

func (f browserRunnerFunc) Run(ctx context.Context, command slack.BrowserCommand) (slack.BrowserCommandResult, error) {
	return f(ctx, command)
}

func browserCommandJSON(t *testing.T, value any) slack.BrowserCommandResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return slack.BrowserCommandResult{Stdout: data}
}

func TestEntrypoint(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStdout []string
		wantStderr []string
	}{
		{
			name:       "help",
			args:       []string{"--help"},
			wantStderr: []string{"Usage:", "slack-mgmt version", "slack-mgmt q '<query>'"},
		},
		{
			name:       "version",
			args:       []string{"version"},
			wantStdout: []string{"slack-mgmt dev", "commit=unknown", "build_date=unknown", runtime.GOOS + "/" + runtime.GOARCH},
		},
		{
			name:       "query schema",
			args:       []string{"q", "schema()", "--format", "compact"},
			wantStdout: []string{"operations:", "attachments", "schema"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runEntrypoint(t, tt.args...)
			if err != nil {
				t.Fatalf("main(%q) error = %v\nstdout:\n%s\nstderr:\n%s", tt.args, err, stdout, stderr)
			}
			assertContainsAll(t, "stdout", stdout, tt.wantStdout)
			assertContainsAll(t, "stderr", stderr, tt.wantStderr)
		})
	}
}

func TestMutationDryRunAndConfirmationGateBeforeCredentialsOrHTTP(t *testing.T) {
	opts := entrypointOptions{env: map[string]string{
		"HOME": t.TempDir(), "XDG_CONFIG_HOME": filepath.Join(t.TempDir(), ".config"),
	}}
	for _, raw := range []string{
		`post_message(C123, text="hello", thread_ts="1710000000.000100", reply_broadcast=true)`,
		`delete_message(C123, ts="1710000005.000600")`,
		`remove_reaction(C123, ts="1710000005.000600", name="wave")`,
	} {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, "m", raw, "--dry-run", "--format", "json")
		if err != nil {
			t.Fatalf("dry-run %q failed: %v\nstdout=%s\nstderr=%s", raw, err, stdout, stderr)
		}
		for _, want := range []string{`"dry_run": true`, `"would_execute": false`, `"requested_target": "C123"`} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("dry-run output missing %q: %s", want, stdout)
			}
		}
		if strings.Contains(stderr, "access token") {
			t.Fatalf("dry-run reached credential resolution: %s", stderr)
		}
	}
	for _, raw := range []string{
		`delete_message(C123, ts="1710000005.000600")`,
		`remove_reaction(C123, ts="1710000005.000600", name="wave")`,
	} {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, "m", raw, "--format", "json")
		if err == nil || stdout != "" || !strings.Contains(stderr, "mutation confirmation required") {
			t.Fatalf("missing confirmation gate raw=%q err=%v stdout=%q stderr=%q", raw, err, stdout, stderr)
		}
		if strings.Contains(stderr, "access token") {
			t.Fatalf("missing confirmation reached credential resolution: %s", stderr)
		}
	}
}

func TestMutationValidationFailsBeforeCredentialsOrHTTP(t *testing.T) {
	opts := entrypointOptions{env: map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": filepath.Join(t.TempDir(), ".config")}}
	invalid := []string{
		`post_message(C123, channel=C456, text="hello")`,
		`post_message(C123, text="first", text="second")`,
		`post_message(C123, text="hello") { full }`,
		`post_message(C123, text="hello", reply_broadcast=true)`,
		`post_message(C123, text="hello"); add_reaction(C123, ts="1710000005.000600", name="wave")`,
		`delete_message(C123, ts="1710000005.000600", unknown=true)`,
	}
	for _, raw := range invalid {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, "m", raw, "--confirm", "--format", "json")
		if err == nil || stdout != "" || strings.Contains(stderr, "access token") {
			t.Fatalf("local validation raw=%q err=%v stdout=%q stderr=%q", raw, err, stdout, stderr)
		}
	}
}

func TestQueryInvalidProjectionFailsBeforeCredentialsOrHTTP(t *testing.T) {
	opts := entrypointOptions{env: map[string]string{
		"HOME": t.TempDir(), "XDG_CONFIG_HOME": filepath.Join(t.TempDir(), ".config"), "SLACK_ACCESS_TOKEN": "",
	}}
	stdout, stderr, err := runEntrypointWithOptions(t, opts,
		"q", `auth_test() { team_id user_i }`, "--source", "env", "--format", "json",
	)
	if err == nil || stdout != "" || !strings.Contains(stderr, `unsupported field "user_i" for auth_test`) {
		t.Fatalf("invalid projection err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if strings.Contains(stderr, "access token") {
		t.Fatalf("invalid projection reached credential resolution: %s", stderr)
	}
}

func TestMutationWriteSuiteActivelyRefusesLiveSlackOrigin(t *testing.T) {
	previousFactory := resolvedSlackClientFactory
	previousExit := exitProcess
	previousStderr := cliStderr
	defer func() {
		resolvedSlackClientFactory = previousFactory
		exitProcess = previousExit
		cliStderr = previousStderr
	}()
	var attemptedHost string
	guardClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attemptedHost = request.URL.Hostname()
		return nil, errors.New("test guard refused live Slack mutation origin")
	})}
	resolvedSlackClientFactory = func(*config.Resolver, config.Source, string, string) (*slack.Client, error) {
		return slack.NewClient(slack.DefaultBaseURL, "xoxb-synthetic", guardClient)
	}
	exitCode := 0
	exitProcess = func(code int) { exitCode = code }
	stderr := &bytes.Buffer{}
	cliStderr = stderr
	t.Setenv("SLACK_ACCESS_TOKEN", "xoxb-synthetic")
	runMutate([]string{`delete_message(C123, ts="1710000005.000600")`, "--source", "env", "--confirm"})
	if attemptedHost != "slack.com" || exitCode != 1 || !strings.Contains(stderr.String(), "test guard refused live Slack mutation origin") {
		t.Fatalf("guard host=%q exit=%d stderr=%q", attemptedHost, exitCode, stderr.String())
	}
}

func TestQueryAndMutationEntrypointsUseCancellableCommandContext(t *testing.T) {
	previousFactory := commandContextFactory
	previousStdout := cliStdout
	previousExit := exitProcess
	defer func() {
		commandContextFactory = previousFactory
		cliStdout = previousStdout
		exitProcess = previousExit
	}()

	calls := 0
	commandContextFactory = func() (context.Context, context.CancelFunc) {
		calls++
		return context.WithCancel(context.Background())
	}
	cliStdout = &bytes.Buffer{}
	exitCode := 0
	exitProcess = func(code int) { exitCode = code }

	runQuery([]string{"schema()", "--format", "json"})
	runMutate([]string{"schema()", "--format", "json"})
	if calls != 2 {
		t.Fatalf("command context factory calls = %d, want 2", calls)
	}
	if exitCode != 0 {
		t.Fatalf("entrypoint exit code = %d, want 0", exitCode)
	}
}

func TestNewCommandContextCancelsOnInterruptAndTermination(t *testing.T) {
	if os.Getenv(signalContextHelperEnv) == "1" {
		ctx, stop := newCommandContext()
		defer stop()
		_, _ = fmt.Fprintln(os.Stdout, "ready")
		<-ctx.Done()
		if !errors.Is(ctx.Err(), context.Canceled) {
			os.Exit(2)
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal is not implemented on Windows")
	}

	for _, tt := range []struct {
		name   string
		signal os.Signal
	}{
		{name: "interrupt", signal: os.Interrupt},
		{name: "termination", signal: syscall.SIGTERM},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestNewCommandContextCancelsOnInterruptAndTermination$")
			command.Env = append(os.Environ(), signalContextHelperEnv+"=1")
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(stdout)
			if !scanner.Scan() || scanner.Text() != "ready" {
				_ = command.Process.Kill()
				t.Fatalf("signal helper did not become ready: %q error=%v", scanner.Text(), scanner.Err())
			}
			if err := command.Process.Signal(tt.signal); err != nil {
				_ = command.Process.Kill()
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("signal helper error = %v", err)
				}
			case <-time.After(5 * time.Second):
				_ = command.Process.Kill()
				t.Fatal("signal helper did not cancel command context")
			}
		})
	}
}

func TestEntrypointProcess(t *testing.T) {
	if os.Getenv(entrypointHelperEnv) != "1" {
		return
	}

	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator == -1 {
		t.Fatal("helper arguments are missing -- separator")
	}

	os.Args = append([]string{"slack-mgmt"}, os.Args[separator+1:]...)
	main()
}

func TestWorkspaceCLIConfiguresPerWorkspaceReadTransport(t *testing.T) {
	home := t.TempDir()
	opts := entrypointOptions{env: map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}

	stdout, stderr, err := runEntrypointWithOptions(t, opts,
		"workspace", "set",
		"--workspace", "Acme",
		"--read-transport", "browser",
		"--browser-url", "https://app.slack.com/client/T01234567/C999",
	)
	if err != nil {
		t.Fatalf("workspace set error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, want := range []string{`"workspace": "acme"`, `"read_transport": "browser"`, `"browser_url": "https://app.slack.com/client/T01234567"`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("workspace set output missing %q: %s", want, stdout)
		}
	}

	stdout, stderr, err = runEntrypointWithOptions(t, opts, "workspace", "show", "--workspace", "acme")
	if err != nil {
		t.Fatalf("workspace show error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, `"read_transport": "browser"`) || !strings.Contains(stdout, `"browser_url": "https://app.slack.com/client/T01234567"`) {
		t.Fatalf("workspace show output = %s", stdout)
	}

	stdout, stderr, err = runEntrypointWithOptions(t, opts, "q", "provider_capabilities()", "--workspace", "acme", "--format", "json")
	if err != nil {
		t.Fatalf("provider_capabilities error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, `"transport": "browser"`) || !strings.Contains(stdout, `"read_only": true`) {
		t.Fatalf("browser provider capabilities output = %s", stdout)
	}
}

func TestWorkspaceSlackdumpProviderCapabilitiesAndUnsupportedGateAvoidExecution(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "Slackdump Tools")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(binDir, "executed")
	executable := filepath.Join(binDir, "slackdump")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"" + marker + "\"\nprintf '%s\\n' 'Slackdump v4.4.4 (commit: fixture) built on: now'\n"
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	opts := entrypointOptions{env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}

	stdout, stderr, err := runEntrypointWithOptions(t, opts,
		"workspace", "set", "--workspace", "acme",
		"--provider", "slackdump",
		"--slackdump-executable", executable,
		"--slackdump-workspace", "corp",
		"--slackdump-authorize-external",
	)
	if err != nil {
		t.Fatalf("workspace set error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, want := range []string{`"provider": "slackdump"`, `"authorization_mode": "external_opt_in"`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("workspace set output missing %q: %s", want, stdout)
		}
	}

	stdout, stderr, err = runEntrypointWithOptions(t, opts,
		"q", "provider_capabilities()", "--workspace", "acme", "--format", "json",
	)
	if err != nil {
		t.Fatalf("provider_capabilities error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, `"provider": "slackdump"`) || !strings.Contains(stdout, `"compatible_with": ">=4.0.0,<5.0.0"`) {
		t.Fatalf("provider capabilities output = %s", stdout)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capability discovery executed Slackdump: %v", err)
	}

	_, stderr, err = runEntrypointWithOptions(t, opts,
		"q", "history(C0123456789)", "--workspace", "acme", "--format", "json",
	)
	if err == nil || !strings.Contains(stderr, provider.ErrCapabilityUnsupported.Error()) {
		t.Fatalf("unsupported history refusal = error:%v stderr:%q", err, stderr)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported query executed Slackdump: %v", err)
	}

	_, stderr, err = runEntrypointWithOptions(t, opts,
		"m", `post_message(C0123456789, text="must not send")`, "--workspace", "acme", "--source", "env", "--format", "json",
	)
	if err == nil || !strings.Contains(stderr, "read_only_provider") || strings.Contains(stderr, "access token not found") {
		t.Fatalf("Slackdump mutation refusal = error:%v stderr:%q", err, stderr)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mutation gate executed Slackdump: %v", err)
	}

	stdout, stderr, err = runEntrypointWithOptions(t, opts, "workspace", "diagnose", "--workspace", "acme")
	if err != nil {
		t.Fatalf("workspace diagnose error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, `"ready": true`) || !strings.Contains(stdout, `"version": "4.4.4"`) {
		t.Fatalf("workspace diagnose output = %s", stdout)
	}
	markerData, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(markerData)) != "version" {
		t.Fatalf("diagnostic argv marker = %q, %v", markerData, err)
	}
}

func TestRunQueryConstructsProviderThroughInjectableFactory(t *testing.T) {
	previous := providerFactoryBuilder
	factoryCalls := 0
	providerFactoryBuilder = func(_ *config.Resolver, source config.Source, workspace, baseURL string) provider.Factory {
		if source != config.SourceAuto || workspace != "acme" || baseURL != "" {
			t.Fatalf("factory args = source:%q workspace:%q baseURL:%q", source, workspace, baseURL)
		}
		return provider.FactoryFunc(func(spec provider.ProviderSpec) (provider.ReadProvider, error) {
			factoryCalls++
			if spec.Kind != provider.KindWebAPI || spec.Workspace != "acme" || spec.WebAPI.Transport != provider.TransportAPI {
				t.Fatalf("resolved ProviderSpec = %#v", spec)
			}
			return provider.NewWebAPIProvider(provider.TransportAPI, func() (provider.WebAPITransport, error) {
				t.Fatal("provider_capabilities initialized Web API transport")
				return nil, nil
			})
		})
	}
	t.Cleanup(func() { providerFactoryBuilder = previous })

	stdout := installInProcessCLIRuntime(t, defaultPublishAlias)
	runQuery([]string{"provider_capabilities()", "--workspace", "acme", "--format", "json"})
	if factoryCalls != 1 || !strings.Contains(stdout.String(), `"provider": "web-api"`) {
		t.Fatalf("injected factory calls=%d output=%q", factoryCalls, stdout.String())
	}
}

func TestNewReadProviderFromResolverRefusesInvalidSlackdumpAuthorizationEvidenceBeforeFactory(t *testing.T) {
	tests := map[string]string{
		"present but wrong authorization mode": `{"workspaces":{"acme":{"provider":"slackdump","slackdump":{"executable":"slackdump","authorization_mode":"external"}}}}`,
		"missing required settings block":      `{"workspaces":{"acme":{"provider":"slackdump"}}}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			configRoot := t.TempDir()
			resolver := config.NewResolver(config.Runtime{
				GOOS:          runtime.GOOS,
				UserConfigDir: func() (string, error) { return configRoot, nil },
				Getenv:        func(string) string { return "" },
			}, nil)
			configPath, err := resolver.WorkspaceConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}

			factoryCalls := 0
			factory := provider.FactoryFunc(func(provider.ProviderSpec) (provider.ReadProvider, error) {
				factoryCalls++
				return nil, errors.New("factory must not receive invalid Slackdump config")
			})
			_, err = newReadProviderFromResolver(resolver, factory, "acme")
			if !errors.Is(err, config.ErrInvalidProviderConfig) {
				t.Fatalf("newReadProviderFromResolver() error = %v, want %v", err, config.ErrInvalidProviderConfig)
			}
			if factoryCalls != 0 {
				t.Fatalf("newReadProviderFromResolver() called factory %d times", factoryCalls)
			}
		})
	}
}

func TestRunQuerySelectsBrowserTransportWithoutResolvingAPICredentialsAndRedactsOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	resolver := newResolver()
	if _, err := resolver.SetWorkspace(config.SetWorkspaceOptions{
		Workspace:     "acme",
		ReadTransport: config.ReadTransportBrowser,
		BrowserURL:    "https://app.slack.com/client/T01234567",
	}); err != nil {
		t.Fatalf("SetWorkspace() error = %v", err)
	}

	secrets := []string{
		"person@example.test",
		"+14155552671",
		"hunter2",
		"xoxb-browser-response-secret",
	}
	browserCommands := 0
	runner := browserRunnerFunc(func(_ context.Context, command slack.BrowserCommand) (slack.BrowserCommandResult, error) {
		browserCommands++
		switch browserCommands {
		case 1:
			if command.Name != "mac-chrome-session" || !reflect.DeepEqual(command.Args, []string{"list"}) {
				t.Fatalf("browser discovery command = %#v", command)
			}
			return browserCommandJSON(t, []map[string]any{{
				"windowId": "11", "tabId": "22", "title": "Fixture",
				"url": "https://app.slack.com/client/T01234567/C1",
			}}), nil
		case 2:
			wantArgs := []string{
				"slack-read", "--window-id", "11", "--tab-id", "22",
				"--origin", "https://app.slack.com", "--workspace-id", "T01234567", "--request-stdin",
			}
			if command.Name != "mac-chrome-session" || !reflect.DeepEqual(command.Args, wantArgs) {
				t.Fatalf("sealed browser command = %#v", command)
			}
			if strings.Contains(strings.ToLower(string(command.Stdin)), "token") {
				t.Fatalf("browser request exported credential-shaped input: %s", command.Stdin)
			}
			return browserCommandJSON(t, map[string]any{
				"version": 1, "ok": true, "method": "users.list",
				"data": map[string]any{
					"ok": true,
					"members": []map[string]any{{
						"id": "U0123456789",
						"profile": map[string]any{
							"email": secrets[0], "display_name": secrets[1],
							"real_name": "password=" + secrets[2] + " token=" + secrets[3],
						},
					}},
				},
			}), nil
		default:
			t.Fatalf("unexpected browser command: %#v", command)
			return slack.BrowserCommandResult{}, nil
		}
	})

	previousBrowserFactory := browserReadTransportFactory
	previousAPIFactory := resolvedSlackClientFactory
	browserCalls := 0
	apiCalls := 0
	browserReadTransportFactory = func(workspace, browserURL string) (query.ReadTransport, error) {
		browserCalls++
		if workspace != "acme" || browserURL != "https://app.slack.com/client/T01234567" {
			t.Fatalf("browser factory route = %q %q", workspace, browserURL)
		}
		return slack.NewBrowserReadClient(workspace, browserURL, slack.BrowserRuntime{
			GOOS: "darwin", Runner: runner,
		})
	}
	resolvedSlackClientFactory = func(*config.Resolver, config.Source, string, string) (*slack.Client, error) {
		apiCalls++
		return nil, errors.New("API factory must not be called for browser reads")
	}
	t.Cleanup(func() {
		browserReadTransportFactory = previousBrowserFactory
		resolvedSlackClientFactory = previousAPIFactory
	})

	stdout := installInProcessCLIRuntime(t, defaultPublishAlias)
	runQuery([]string{"users() { id email display_name real_name }", "--workspace", "acme", "--format", "json"})
	if browserCalls != 1 || apiCalls != 0 {
		t.Fatalf("transport factory calls = browser:%d api:%d", browserCalls, apiCalls)
	}
	for _, secret := range secrets {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("browser query output leaked %q: %q", secret, stdout.String())
		}
	}
	for _, want := range []string{"U0123456789", "<email:", "<phone:", "<secret:", "<token:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("browser query output missing %q after redaction: %q", want, stdout.String())
		}
	}
	if browserCommands != 2 {
		t.Fatalf("browser query output was not safely redacted: %q", stdout.String())
	}
}

func TestBrowserProviderCapabilitiesRefuseUnavailableSearchBeforeSubprocess(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	marker := filepath.Join(binDir, "browser-invoked")
	fakeChromeSession := filepath.Join(binDir, "mac-chrome-session")
	script := "#!/bin/sh\nprintf invoked > \"" + marker + "\"\nexit 70\n"
	if err := os.WriteFile(fakeChromeSession, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	opts := entrypointOptions{env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"PATH": binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}}
	_, stderr, err := runEntrypointWithOptions(t, opts,
		"workspace", "set", "--workspace", "acme", "--read-transport", "browser",
		"--browser-url", "https://app.slack.com/client/T01234567",
	)
	if err != nil {
		t.Fatalf("workspace set error = %v, stderr=%q", err, stderr)
	}

	stdout, stderr, err := runEntrypointWithOptions(t, opts,
		"q", "provider_capabilities()", "--workspace", "acme", "--format", "json",
	)
	if err != nil {
		t.Fatalf("provider_capabilities error = %v, stderr=%q", err, stderr)
	}
	for _, supported := range []string{`"search_messages"`, `"history"`, `"replies"`} {
		if !strings.Contains(stdout, supported) {
			t.Fatalf("browser capabilities omitted %s: %s", supported, stdout)
		}
	}
	for _, unsupported := range []string{`"search_info"`, `"search_context"`} {
		if strings.Contains(stdout, unsupported) {
			t.Fatalf("browser capabilities advertised %s: %s", unsupported, stdout)
		}
	}

	for _, queryText := range []string{"search_info()", `search_context("incident")`} {
		_, stderr, err = runEntrypointWithOptions(t, opts,
			"q", queryText, "--workspace", "acme", "--format", "json",
		)
		if err == nil || !strings.Contains(stderr, provider.ErrCapabilityUnsupported.Error()) {
			t.Fatalf("%s refusal = error:%v stderr:%q", queryText, err, stderr)
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported browser search initialized subprocess: %v", err)
	}
}

func TestRunQueryRefusesAPICredentialOverridesForBrowserWorkspace(t *testing.T) {
	fakeBinDir := t.TempDir()
	fakeChromeSession := filepath.Join(fakeBinDir, "mac-chrome-session")
	fakeChromeSessionScript := `#!/bin/sh
case "$1" in
  list)
    printf '%s\n' '[{"windowId":"11","tabId":"22","url":"https://app.slack.com/client/T01234567"}]'
    ;;
  slack-read)
    printf '%s\n' '{"version":1,"ok":true,"method":"auth.test","data":{"ok":true,"team_id":"T01234567","user_id":"U1"}}'
    ;;
  *)
    exit 64
    ;;
esac
`
	if err := os.WriteFile(fakeChromeSession, []byte(fakeChromeSessionScript), 0o700); err != nil {
		t.Fatalf("WriteFile(fake mac-chrome-session) error = %v", err)
	}
	fakePath := fakeBinDir + string(os.PathListSeparator) + os.Getenv("PATH")

	tests := []struct {
		name        string
		override    []string
		wantRefusal string
	}{
		{
			name:        "source",
			override:    []string{"--source", "env"},
			wantRefusal: "browser read transport does not accept --source",
		},
		{
			name:        "base URL",
			override:    []string{"--base-url", "https://slack.com/api"},
			wantRefusal: "browser read transport does not accept --base-url",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			opts := entrypointOptions{env: map[string]string{
				"HOME":            home,
				"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
				"PATH":            fakePath,
			}}
			_, stderr, err := runEntrypointWithOptions(t, opts,
				"workspace", "set",
				"--workspace", "acme",
				"--read-transport", "browser",
				"--browser-url", "https://app.slack.com/client/T01234567",
			)
			if err != nil {
				t.Fatalf("workspace set error = %v\nstderr:\n%s", err, stderr)
			}

			args := []string{"q", "auth_test()", "--workspace", "acme", "--format", "json"}
			args = append(args, test.override...)
			stdout, stderr, err := runEntrypointWithOptions(t, opts, args...)
			if err == nil {
				t.Fatalf("runQuery accepted browser API override: %s", stdout)
			}
			if !strings.Contains(stderr, test.wantRefusal) {
				t.Fatalf("runQuery refusal = %q, want %q", stderr, test.wantRefusal)
			}
			for _, wrongPath := range []string{"access token not found", slack.ErrBrowserUnsupportedPlatform.Error()} {
				if strings.Contains(stderr, wrongPath) {
					t.Fatalf("runQuery reached a path beyond the override gate: %q", stderr)
				}
			}
		})
	}
}

func TestMutationCLIRefusesBrowserWorkspaceBeforeCredentialResolution(t *testing.T) {
	home := t.TempDir()
	opts := entrypointOptions{env: map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}
	_, stderr, err := runEntrypointWithOptions(t, opts,
		"workspace", "set",
		"--workspace", "acme",
		"--read-transport", "browser",
		"--browser-url", "https://app.slack.com/client/T01234567",
	)
	if err != nil {
		t.Fatalf("workspace set error = %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := runEntrypointWithOptions(t, opts,
		"m", `post_message(C0123456789, text="must not send")`,
		"--workspace", "acme",
		"--source", "env",
		"--format", "json",
	)
	if err == nil {
		t.Fatalf("browser workspace mutation unexpectedly succeeded: %s", stdout)
	}
	if !strings.Contains(stderr, slack.ErrBrowserWriteUnsupported.Error()) || strings.Contains(stderr, "access token not found") {
		t.Fatalf("mutation refusal did not precede credential resolution: %q", stderr)
	}
}

func TestGitIgnoreBoundary(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	tests := []struct {
		name        string
		path        string
		wantIgnored bool
	}{
		{name: "entrypoint is admitted", path: "cmd/slack-mgmt/main.go", wantIgnored: false},
		{name: "future entrypoint file is admitted", path: "cmd/slack-mgmt/future.go", wantIgnored: false},
		{name: "root binary remains ignored", path: "slack-mgmt", wantIgnored: true},
		{name: "root Windows binary remains ignored", path: "slack-mgmt.exe", wantIgnored: true},
		{name: "generated AGENTS remains ignored", path: "AGENTS.md", wantIgnored: true},
		{name: "agent runtime remains ignored", path: ".agents/.instructions/AGENTS.project.md", wantIgnored: true},
		{name: "Claude runtime remains ignored", path: ".claude/CLAUDE.md", wantIgnored: true},
		{name: "Codex runtime remains ignored", path: ".codex/config.toml", wantIgnored: true},
		{name: "local tool runtime remains ignored", path: ".local/bin/agents-infra", wantIgnored: true},
		{name: "planning scratch remains ignored", path: ".planning/plan.md", wantIgnored: true},
		{name: "research scratch remains ignored", path: ".research/notes.md", wantIgnored: true},
		{name: "spec scratch remains ignored", path: ".spec/local.md", wantIgnored: true},
		{name: "task board remains ignored", path: ".task-board/local.md", wantIgnored: true},
		{name: "task board config remains ignored", path: "task-board.config.json", wantIgnored: true},
		{name: "agent logbook remains ignored", path: "LOGBOOK.md", wantIgnored: true},
		{name: "local agent verifier remains ignored", path: "scripts/verify-agent-safety-contract.sh", wantIgnored: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ignored, output, err := gitCheckIgnored(repoRoot, tt.path)
			if err != nil {
				t.Fatalf("git check-ignore --no-index %q failed: %v\n%s", tt.path, err, output)
			}
			if ignored != tt.wantIgnored {
				t.Fatalf("git check-ignore --no-index %q ignored = %v, want %v\n%s", tt.path, ignored, tt.wantIgnored, output)
			}
		})
	}
}

func TestCLIPhoneRedactionMatrixRejectsNarrowedRecognizer(t *testing.T) {
	positive := []string{
		"+14155552671",
		"+44 20 7946 0958",
		"+1 (415) 555-2671",
		"415-555-2671",
		`+14155552671\next 9`,
		`+14155552671\rext 9`,
		`+14155552671\text 9`,
		`+14155552671\nx123`,
		`+14155552671\rx123`,
		`+14155552671\tx123`,
	}
	negative := []string{
		"C0123456789",
		"D0123456789",
		"T0123456789",
		"U0123456789",
		"W0123456789",
		"B0123456789",
		"1710000005.000600",
		"1234567",
		"+01234567890",
		"x+14155552671",
		"+14155552671x",
		"1-23-456",
		"+14155552671 ext 9",
		"+14155552671 x123",
		"+14155552671\text 9",
		"+14155552671\rext 9",
		"+14155552671\next 9",
		"+14155552671\tx123",
		"+14155552671\rx123",
		"+14155552671\nx123",
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	source := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	items := make([]map[string]any, 0, len(positive)+len(negative))
	for idx, value := range append(append([]string{}, positive...), negative...) {
		items = append(items, map[string]any{
			"id":         fmt.Sprintf("att-%02d", idx),
			"name":       value,
			"local_path": source,
		})
	}
	manifestBytes, err := json.Marshal(map[string]any{"attachments": items})
	if err != nil {
		t.Fatalf("Marshal(manifest) error = %v", err)
	}
	manifest := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifest, manifestBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	opts := entrypointOptions{dir: root, env: map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config")}}

	jsonOut, jsonErr, err := runEntrypointWithOptions(t, opts, "q", "attachments() { id name }", "--manifest", manifest, "--format", "json")
	if err != nil {
		t.Fatalf("JSON CLI error = %v\nstdout:\n%s\nstderr:\n%s", err, jsonOut, jsonErr)
	}
	var jsonRows []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &jsonRows); err != nil {
		t.Fatalf("Unmarshal(JSON output) error = %v\n%s", err, jsonOut)
	}
	jsonNames := map[string]string{}
	for _, row := range jsonRows {
		jsonNames[row["id"].(string)] = row["name"].(string)
	}

	compactOut, compactErr, err := runEntrypointWithOptions(t, opts, "q", "attachments() { id name }", "--manifest", manifest, "--format", "compact")
	if err != nil {
		t.Fatalf("compact CLI error = %v\nstdout:\n%s\nstderr:\n%s", err, compactOut, compactErr)
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimSpace(compactOut))).ReadAll()
	if err != nil {
		t.Fatalf("parse compact CSV error = %v\n%s", err, compactOut)
	}
	compactNames := map[string]string{}
	for _, row := range records[1:] {
		compactNames[row[0]] = row[1]
	}

	for idx, value := range positive {
		id := fmt.Sprintf("att-%02d", idx)
		for mode, got := range map[string]string{"json": jsonNames[id], "compact": compactNames[id]} {
			if got == value || !strings.HasPrefix(got, "<phone:") {
				t.Errorf("%s positive row %q = %q, want phone marker", mode, value, got)
			}
		}
		if jsonNames[id] != compactNames[id] {
			t.Errorf("marker for %q differs across JSON/compact: %q vs %q", value, jsonNames[id], compactNames[id])
		}
		_, stderr, err := runEntrypointWithOptions(t, opts, "invalid "+value)
		if err == nil {
			t.Errorf("stderr positive row %q unexpectedly succeeded", value)
		} else if strings.Contains(stderr, value) || !strings.Contains(stderr, "<phone:") {
			t.Errorf("stderr positive row %q not redacted: %q", value, stderr)
		}
	}
	for idx, value := range negative {
		id := fmt.Sprintf("att-%02d", len(positive)+idx)
		compactValue := strings.ReplaceAll(value, "\n", `\n`)
		if jsonNames[id] != value || compactNames[id] != compactValue {
			t.Errorf("negative row %q changed: json=%q compact=%q, want compact=%q", value, jsonNames[id], compactNames[id], compactValue)
		}
		_, stderr, err := runEntrypointWithOptions(t, opts, "invalid "+value)
		if err == nil {
			t.Errorf("stderr negative row %q unexpectedly succeeded", value)
		} else if !strings.Contains(stderr, value) {
			t.Errorf("stderr negative row %q was not preserved: %q", value, stderr)
		}
	}
}

func TestCLIAttachmentPathPrivacyRejectsRawPathBypass(t *testing.T) {
	root := t.TempDir()
	rawFixtures := []string{"+14155552671", "person@example.test", "xoxb-path-fixture"}
	rawDir := filepath.Join(root, strings.Join(rawFixtures, "-"))
	if err := os.MkdirAll(rawDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(rawDir) error = %v", err)
	}
	source := filepath.Join(rawDir, "payload.bin")
	want := []byte{0x00, 0x42, 0xff}
	if err := os.WriteFile(source, want, 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	manifest := filepath.Join(root, "manifest.json")
	manifestBytes, _ := json.Marshal(map[string]any{"attachments": []map[string]any{{
		"id": "att-private", "name": "payload.bin", "mime_type": "application/octet-stream", "local_path": source,
	}}})
	if err := os.WriteFile(manifest, manifestBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	home := filepath.Join(root, "home")
	opts := entrypointOptions{dir: root, env: map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config")}}

	jsonOut, stderr, err := runEntrypointWithOptions(t, opts, "q", "attachments() { id local_path }", "--manifest", manifest, "--format", "json")
	if err != nil {
		t.Fatalf("attachments JSON error = %v\n%s", err, stderr)
	}
	assertNoRawFixtures(t, jsonOut+stderr, rawFixtures)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatalf("Unmarshal(attachments) error = %v", err)
	}
	assertUsableAlias(t, root, rows[0]["local_path"].(string), want)

	compactOut, stderr, err := runEntrypointWithOptions(t, opts, "q", "attachment(att-private) { local_path }", "--manifest", manifest, "--format", "compact")
	if err != nil {
		t.Fatalf("attachment compact error = %v\n%s", err, stderr)
	}
	assertNoRawFixtures(t, compactOut+stderr, rawFixtures)
	compactAlias := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(compactOut), "local_path:"))
	assertUsableAlias(t, root, compactAlias, want)

	destination := filepath.Join(rawDir, "stage", "copied.bin")
	stageOut, stderr, err := runEntrypointWithOptions(t, opts, "attachment", "stage", source, "--destination", destination, "--format", "json")
	if err != nil {
		t.Fatalf("attachment stage error = %v\n%s", err, stderr)
	}
	assertNoRawFixtures(t, stageOut+stderr, rawFixtures)
	var stage map[string]any
	if err := json.Unmarshal([]byte(stageOut), &stage); err != nil {
		t.Fatalf("Unmarshal(stage) error = %v", err)
	}
	assertUsableAlias(t, root, stage["local_path"].(string), want)
	if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("staged destination bytes = %v error=%v", got, err)
	}

	before := aliasEntryCount(t, root)
	stdout, stderr, err := runEntrypointWithOptions(t, opts, "attachment", "stage", source, "--destination", destination, "--format", "compact")
	if err == nil {
		t.Fatal("existing destination without --force unexpectedly succeeded")
	}
	assertNoRawFixtures(t, stdout+stderr+err.Error(), rawFixtures)
	if after := aliasEntryCount(t, root); after != before {
		t.Fatalf("failed stage published alias count %d -> %d", before, after)
	}
	if got, _ := os.ReadFile(source); !bytes.Equal(got, want) {
		t.Fatalf("source was mutated: %v", got)
	}
}

func TestCLIAttachmentStagePreservesDestinationAndPrivateAlias(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	opts := entrypointOptions{dir: root, env: map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config")}}
	source := filepath.Join(root, "source.bin")
	destination := filepath.Join(root, "destination.bin")
	if err := os.WriteFile(source, []byte("source-bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}

	stdout, stderr, err := runEntrypointWithOptions(t, opts, "attachment", "stage", source, "--destination", destination, "--format", "json")
	if err != nil {
		t.Fatalf("stage success error = %v\n%s", err, stderr)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("Unmarshal(stage) error = %v", err)
	}
	alias := result["local_path"].(string)
	assertUsableAlias(t, root, alias, []byte("source-bytes"))
	if alias == destination {
		t.Fatal("private alias equals requested destination")
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "source-bytes" {
		t.Fatalf("initial staged destination = %q error=%v, want source bytes", got, err)
	}

	if err := os.WriteFile(source, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("WriteFile(replacement) error = %v", err)
	}
	stdout, stderr, err = runEntrypointWithOptions(t, opts, "attachment", "stage", source, "--destination", destination, "--format", "json")
	if err == nil {
		t.Fatal("no-force stage unexpectedly replaced destination")
	}
	if got, _ := os.ReadFile(destination); string(got) != "source-bytes" {
		t.Fatalf("no-force destination = %q, want original bytes", got)
	}

	stdout, stderr, err = runEntrypointWithOptions(t, opts, "attachment", "stage", source, "--destination", destination, "--force", "--format", "json")
	if err != nil {
		t.Fatalf("force stage error = %v\n%s", err, stderr)
	}
	if got, _ := os.ReadFile(destination); string(got) != "replacement" {
		t.Fatalf("force destination = %q, want replacement", got)
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("Unmarshal(force stage) error = %v", err)
	}
	assertUsableAlias(t, root, result["local_path"].(string), []byte("replacement"))
}

func TestCLIAttachmentAliasCollisionRetriesWithoutReplacement(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.bin")
	destination := filepath.Join(root, "destination.bin")
	if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	aliasRoot := filepath.Join(root, filepath.FromSlash(attachments.AliasRelativeRoot))
	if err := os.MkdirAll(aliasRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(aliasRoot) error = %v", err)
	}
	firstPath := filepath.Join(aliasRoot, "attachment-"+strings.Repeat("11", 16))
	if err := os.WriteFile(firstPath, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile(sentinel) error = %v", err)
	}
	randomBytes := append(bytes.Repeat([]byte{0x11}, 16), bytes.Repeat([]byte{0x22}, 16)...)

	stdout := installInProcessCLIRuntime(t, func(path string) (attachments.Alias, error) {
		return attachments.PublishAlias(path, attachments.AliasRuntime{
			WorkingDir: root,
			Random:     bytes.NewReader(randomBytes),
		})
	})
	if err := runAttachmentStage([]string{source, "--destination", destination, "--format", "json"}); err != nil {
		t.Fatalf("runAttachmentStage() error = %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal(stage output) error = %v\n%s", err, stdout.String())
	}
	if got := result["local_path"].(string); !strings.HasSuffix(got, strings.Repeat("22", 16)) {
		t.Fatalf("local_path = %q, want second generated name", got)
	}
	if got, _ := os.ReadFile(firstPath); string(got) != "sentinel" {
		t.Fatalf("collision sentinel = %q, want unchanged", got)
	}
	if got, _ := os.ReadFile(destination); string(got) != "payload" {
		t.Fatalf("production stage destination = %q, want payload", got)
	}
}

func TestCLIAttachmentAliasPublicationFailuresAreAtomic(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*attachments.AliasRuntime)
	}{
		{
			name: "copy",
			mutate: func(runtime *attachments.AliasRuntime) {
				runtime.Copy = func(io.Writer, io.Reader) (int64, error) { return 0, errors.New("copy failure") }
			},
		},
		{
			name: "sync",
			mutate: func(runtime *attachments.AliasRuntime) {
				runtime.Sync = func(*os.File) error { return errors.New("sync failure") }
			},
		},
		{
			name: "chmod",
			mutate: func(runtime *attachments.AliasRuntime) {
				runtime.Chmod = func(*os.File, os.FileMode) error { return errors.New("chmod failure") }
			},
		},
		{
			name: "rename",
			mutate: func(runtime *attachments.AliasRuntime) {
				runtime.Publish = func(string, string) error { return errors.New("publish failure") }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			rawFixtures := []string{"+14155552671", "person@example.test", "xoxb-path-fixture"}
			rawDir := filepath.Join(root, strings.Join(rawFixtures, "-"))
			if err := os.MkdirAll(rawDir, 0o700); err != nil {
				t.Fatalf("MkdirAll(rawDir) error = %v", err)
			}
			source := filepath.Join(rawDir, "source.bin")
			destination := filepath.Join(rawDir, "destination.bin")
			if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
				t.Fatalf("WriteFile(source) error = %v", err)
			}
			aliasRoot := filepath.Join(root, filepath.FromSlash(attachments.AliasRelativeRoot))
			if err := os.MkdirAll(aliasRoot, 0o700); err != nil {
				t.Fatalf("MkdirAll(aliasRoot) error = %v", err)
			}
			sentinel := filepath.Join(aliasRoot, "attachment-"+strings.Repeat("44", 16))
			if err := os.WriteFile(sentinel, []byte("sentinel"), 0o600); err != nil {
				t.Fatalf("WriteFile(sentinel) error = %v", err)
			}

			runtime := attachments.AliasRuntime{
				WorkingDir: root,
				Random:     bytes.NewReader(bytes.Repeat([]byte{0x55}, 16)),
			}
			tt.mutate(&runtime)
			stdout := installInProcessCLIRuntime(t, func(path string) (attachments.Alias, error) {
				return attachments.PublishAlias(path, runtime)
			})

			err := runAttachmentStage([]string{source, "--destination", destination, "--format", "json"})
			if !errors.Is(err, attachments.ErrAttachmentAlias) {
				t.Fatalf("runAttachmentStage() error = %v, want attachment_alias_error", err)
			}
			assertNoRawFixtures(t, err.Error()+stdout.String(), rawFixtures)
			if stdout.Len() != 0 {
				t.Fatalf("failed publication wrote stdout: %q", stdout.String())
			}
			if got, _ := os.ReadFile(destination); string(got) != "payload" {
				t.Fatalf("successful stage side effect = %q, want payload", got)
			}
			if got, _ := os.ReadFile(sentinel); string(got) != "sentinel" {
				t.Fatalf("collision sentinel = %q, want unchanged", got)
			}
			entries, err := os.ReadDir(aliasRoot)
			if err != nil {
				t.Fatalf("ReadDir(aliasRoot) error = %v", err)
			}
			if len(entries) != 1 || entries[0].Name() != filepath.Base(sentinel) {
				t.Fatalf("failure left partial/temp aliases: %v", entries)
			}
		})
	}
}

func TestCLIAttachmentBatchFailureRollsBackNewAliases(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	aliasRoot := filepath.Join(root, filepath.FromSlash(attachments.AliasRelativeRoot))
	if err := os.MkdirAll(aliasRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(alias root) error = %v", err)
	}
	sentinel := filepath.Join(aliasRoot, "attachment-"+strings.Repeat("44", 16))
	if err := os.WriteFile(sentinel, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile(sentinel) error = %v", err)
	}
	valid := filepath.Join(root, "valid.bin")
	if err := os.WriteFile(valid, []byte("valid"), 0o600); err != nil {
		t.Fatalf("WriteFile(valid) error = %v", err)
	}
	missing := filepath.Join(root, "missing-person@example.test.bin")
	manifestBytes, err := json.Marshal(map[string]any{"attachments": []map[string]any{
		{"id": "att-valid", "name": "a-valid.bin", "local_path": valid},
		{"id": "att-missing", "name": "z-missing.bin", "local_path": missing},
	}})
	if err != nil {
		t.Fatalf("Marshal(manifest) error = %v", err)
	}
	manifest := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifest, manifestBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	opts := entrypointOptions{dir: root, env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}

	for _, format := range []string{"json", "compact"} {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, "q", "attachments() { id local_path }", "--manifest", manifest, "--format", format)
		if err == nil {
			t.Fatalf("attachments %s batch unexpectedly succeeded", format)
		}
		assertNoRawFixtures(t, stdout+stderr, []string{missing, "person@example.test"})
		entries, readErr := os.ReadDir(aliasRoot)
		if readErr != nil {
			t.Fatalf("ReadDir(alias root) error = %v", readErr)
		}
		if len(entries) != 1 || entries[0].Name() != filepath.Base(sentinel) {
			t.Fatalf("attachments %s failure left partial aliases: %v", format, entries)
		}
		if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "sentinel" {
			t.Fatalf("attachments %s changed sentinel to %q, error=%v", format, got, readErr)
		}
	}
}

func TestCLIAttachmentAliasRootRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privileges on Windows")
	}

	root := t.TempDir()
	external := t.TempDir()
	aliasRoot := filepath.Join(root, filepath.FromSlash(attachments.AliasRelativeRoot))
	if err := os.MkdirAll(filepath.Dir(aliasRoot), 0o700); err != nil {
		t.Fatalf("MkdirAll(alias parent) error = %v", err)
	}
	if err := os.Symlink(external, aliasRoot); err != nil {
		t.Fatalf("Symlink(alias root) error = %v", err)
	}
	rawDir := filepath.Join(root, "person@example.test")
	if err := os.MkdirAll(rawDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(raw source parent) error = %v", err)
	}
	source := filepath.Join(rawDir, "source.bin")
	destination := filepath.Join(rawDir, "destination.bin")
	if err := os.WriteFile(source, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	home := filepath.Join(root, "home")
	opts := entrypointOptions{dir: root, env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}

	stdout, stderr, err := runEntrypointWithOptions(t, opts, "attachment", "stage", source, "--destination", destination, "--format", "json")
	if err == nil {
		t.Fatal("stage through symlinked alias root unexpectedly succeeded")
	}
	if stdout != "" || !strings.Contains(stderr, attachments.ErrAttachmentAlias.Error()) {
		t.Fatalf("stage symlink refusal stdout=%q stderr=%q", stdout, stderr)
	}
	assertNoRawFixtures(t, stdout+stderr, []string{source, destination, external, "person@example.test"})
	if strings.Contains(strings.ToLower(stderr), "rollback") {
		t.Fatalf("stderr falsely claimed destination rollback: %q", stderr)
	}
	if got, readErr := os.ReadFile(destination); readErr != nil || string(got) != "payload" {
		t.Fatalf("successful stage side effect = %q error=%v, want payload", got, readErr)
	}
	entries, readErr := os.ReadDir(external)
	if readErr != nil {
		t.Fatalf("ReadDir(external) error = %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink escape wrote external entries: %v", entries)
	}
}

func TestCLIRedactsEveryEgressWithoutChangingOutboundSlackPayload(t *testing.T) {
	rawText := "send xoxb-request-secret person@example.test +14155552671 password=hunter2 action_token=action-fixture https://example.test/?api_key=query-secret Authorization: Bearer bearer-fixture"
	authToken := "xoxb-auth-secret"
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/chat.postMessage":
			if got := request.Header.Get("Authorization"); got != "Bearer "+authToken {
				t.Errorf("Authorization header = %q", got)
			}
			if err := request.ParseForm(); err != nil {
				t.Errorf("ParseForm() error = %v", err)
			}
			requests <- request.Form.Get("text")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "channel": "C0123456789", "ts": "1710000005.000600",
				"message": map[string]any{"text": rawText, "ts": "1710000005.000600"},
			})
		case "/users.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"members": []map[string]any{{
					"id": "U0123456789", "name": "agent", "profile": map[string]any{"email": "opaque-email-value", "display_name": rawText},
				}},
				"response_metadata": map[string]any{"next_cursor": ""},
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("leak "+rawText+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(README) error = %v", err)
	}
	opts := entrypointOptions{dir: root, env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "SLACK_ACCESS_TOKEN": authToken,
	}}
	sensitive := []string{"xoxb-request-secret", "person@example.test", "opaque-email-value", "+14155552671", "hunter2", "action-fixture", "query-secret", "bearer-fixture", authToken}
	mutation := `post_message(C0123456789, text="` + rawText + `")`
	previousFactory := resolvedSlackClientFactory
	resolvedSlackClientFactory = func(*config.Resolver, config.Source, string, string) (*slack.Client, error) {
		return slack.NewClient(server.URL, authToken, server.Client())
	}
	t.Cleanup(func() { resolvedSlackClientFactory = previousFactory })
	inProcessStdout := installInProcessCLIRuntime(t, defaultPublishAlias)
	t.Setenv("SLACK_ACCESS_TOKEN", authToken)

	for _, format := range []string{"json", "compact"} {
		inProcessStdout.Reset()
		runMutate([]string{mutation, "--source", "env", "--format", format})
		stdout := inProcessStdout.String()
		assertNoRawFixtures(t, stdout, sensitive)
		for _, structural := range []string{"C0123456789", "1710000005.000600"} {
			if !strings.Contains(stdout, structural) {
				t.Errorf("mutation %s removed structural value %q from %q", format, structural, stdout)
			}
		}
	}
	for idx := 0; idx < 2; idx++ {
		if got := <-requests; got != rawText {
			t.Errorf("outbound fake Slack payload = %q, want exact %q", got, rawText)
		}
	}

	for _, format := range []string{"json", "compact"} {
		inProcessStdout.Reset()
		runQuery([]string{"users() { id email display_name }", "--source", "env", "--format", format})
		stdout := inProcessStdout.String()
		assertNoRawFixtures(t, stdout, sensitive)
		if !strings.Contains(stdout, "U0123456789") || !strings.Contains(stdout, "<email:") {
			t.Errorf("users %s output lost safe ID or email marker: %q", format, stdout)
		}
	}

	for _, format := range []string{"json", "compact"} {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, "grep", "leak", "--format", format)
		if err != nil {
			t.Fatalf("grep %s error = %v\n%s", format, err, stderr)
		}
		assertNoRawFixtures(t, stdout+stderr, sensitive)
	}

	stdout, stderr, err := runEntrypointWithOptions(t, opts, "q", "invalid "+rawText, "--format", "json")
	if err == nil {
		t.Fatal("invalid query unexpectedly succeeded")
	}
	assertNoRawFixtures(t, stdout+stderr+err.Error(), sensitive)

	stdout, stderr, err = runEntrypointWithOptions(t, opts, "auth", "set-access", "--source", "env_or_file", "--workspace", "fixture", "--token", authToken)
	if err != nil {
		t.Fatalf("auth set-access error = %v\n%s", err, stderr)
	}
	assertNoRawFixtures(t, stdout+stderr, sensitive)
	stdout, stderr, err = runEntrypointWithOptions(t, opts, "auth", "resolve", "--source", "invalid", "--workspace", authToken)
	if err == nil {
		t.Fatal("auth resolve with invalid source unexpectedly succeeded")
	}
	assertNoRawFixtures(t, stdout+stderr+err.Error(), sensitive)
}

func TestCLIExplicitSensitiveContextsRejectSelfMintedMarkers(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	forged := fmt.Sprintf("<token:%010x>", 0x12345)
	ordinary := fmt.Sprintf("<email:%010x>", 0x67890)
	manifestBytes, err := json.Marshal(map[string]any{"attachments": []map[string]any{
		{"id": "att-param", "name": "https://example.test/?token=" + forged, "local_path": source},
		{"id": "att-password", "name": `password="` + forged + `"`, "local_path": source},
		{"id": "att-ordinary", "name": ordinary, "local_path": source},
	}})
	if err != nil {
		t.Fatalf("Marshal(manifest) error = %v", err)
	}
	manifest := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifest, manifestBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	opts := entrypointOptions{dir: root, env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}

	for _, format := range []string{"json", "compact"} {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, "q", "attachments() { id name }", "--manifest", manifest, "--format", format)
		if err != nil {
			t.Fatalf("attachments %s error = %v\nstdout:\n%s\nstderr:\n%s", format, err, stdout, stderr)
		}
		assertNoRawFixtures(t, stdout+stderr, []string{forged})
		for _, visible := range []string{"<param:", "<secret:", ordinary} {
			if !strings.Contains(stdout, visible) {
				t.Errorf("attachments %s output lost %q: %q", format, visible, stdout)
			}
		}
	}

	stdout, stderr, err := runEntrypointWithOptions(t, opts, `invalid password="`+forged+`"`)
	if err == nil {
		t.Fatal("self-minted marker stderr attack unexpectedly succeeded")
	}
	assertNoRawFixtures(t, stdout+stderr, []string{forged})
	if !strings.Contains(stderr, "<secret:") {
		t.Fatalf("stderr attack omitted replacement secret marker: %q", stderr)
	}
}

func TestCLIRedactsSensitiveNestedMapKeysInJSONAndCompact(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	rawKey := "person@example.test"
	manifestBytes, err := json.Marshal(map[string]any{"attachments": []map[string]any{{
		"id":         "att-key",
		"name":       "payload.bin",
		"local_path": source,
		"metadata": map[string]any{
			rawKey:    "safe value",
			"channel": "C0123456789",
			"ts":      "1710000005.000600",
		},
	}}})
	if err != nil {
		t.Fatalf("Marshal(manifest) error = %v", err)
	}
	manifest := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifest, manifestBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	opts := entrypointOptions{dir: root, env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}

	outputs := map[string]string{}
	for _, format := range []string{"json", "compact"} {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, "q", "attachments() { id metadata }", "--manifest", manifest, "--format", format)
		if err != nil {
			t.Fatalf("attachments %s error = %v\nstdout:\n%s\nstderr:\n%s", format, err, stdout, stderr)
		}
		outputs[format] = stdout
		assertNoRawFixtures(t, stdout+stderr, []string{rawKey})
		for _, structural := range []string{"att-key", "C0123456789", "1710000005.000600", "<email:"} {
			if !strings.Contains(stdout, structural) {
				t.Errorf("attachments %s output lost required structural fixture index; output_len=%d", format, len(stdout))
			}
		}
	}
	markerPattern := regexp.MustCompile(`<email:[0-9a-f]{10}>`)
	jsonMarker := markerPattern.FindString(outputs["json"])
	compactMarker := markerPattern.FindString(outputs["compact"])
	if jsonMarker == "" || jsonMarker != compactMarker {
		t.Fatalf("sensitive key marker differs across JSON/compact: json=%q compact=%q", jsonMarker, compactMarker)
	}
}

func TestCLICompoundStructuredCredentialsAndEncodedQueryNamesAreRedacted(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	raw := map[string]string{
		"client_secret":       "opaque client secret value",
		"API key":             "opaque spaced api key value",
		"database-password":   "opaque password value",
		"oauth refresh-token": "opaque refresh token value",
		"action-token":        "opaque action token value",
	}
	encodedURL := "https://example.test/path?api%5Fkey=encoded-api-fixture&%61ccess_token=encoded-access-fixture&safe=yes"
	metadata := map[string]any{
		"channel":    "C0123456789",
		"ts":         "1710000005.000600",
		"safe_label": "public value",
		"url":        encodedURL,
	}
	fixtures := []string{"encoded-api-fixture", "encoded-access-fixture"}
	for key, value := range raw {
		metadata[key] = value
		fixtures = append(fixtures, value)
	}
	manifestBytes, err := json.Marshal(map[string]any{"attachments": []map[string]any{{
		"id": "att-structured", "name": "payload.bin", "local_path": source, "metadata": metadata,
	}}})
	if err != nil {
		t.Fatalf("Marshal(manifest) error = %v", err)
	}
	manifest := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifest, manifestBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	opts := entrypointOptions{dir: root, env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}

	for _, format := range []string{"json", "compact"} {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, "q", "attachments() { id metadata }", "--manifest", manifest, "--format", format)
		if err != nil {
			t.Fatalf("attachments %s error = %v\nstdout:\n%s\nstderr:\n%s", format, err, stdout, stderr)
		}
		assertNoRawFixtures(t, stdout+stderr, fixtures)
		for _, visible := range []string{"C0123456789", "1710000005.000600", "public value", "<secret:", "<token:", "<action_token:", "<param:"} {
			if !strings.Contains(stdout, visible) {
				t.Errorf("attachments %s output lost %q: %q", format, visible, stdout)
			}
		}
	}

	for _, encoded := range []string{
		"https://example.test/?api%5Fkey=encoded-api-fixture",
		"https://example.test/?%61ccess_token=encoded-access-fixture",
	} {
		stdout, stderr, err := runEntrypointWithOptions(t, opts, encoded)
		if err == nil {
			t.Fatalf("encoded query-name command %q unexpectedly succeeded", encoded)
		}
		assertNoRawFixtures(t, stdout+stderr, fixtures)
		if !strings.Contains(stderr, "<param:") {
			t.Fatalf("final stderr omitted param marker: %q", stderr)
		}
	}
}

func TestCLIRedactsNonStringSensitiveFieldsInJSONAndCompact(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, "payload.bin")
	if err := os.WriteFile(source, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	manifestBytes, err := json.Marshal(map[string]any{"attachments": []map[string]any{{
		"id": "att-non-string", "name": "payload.bin", "local_path": source,
		"metadata": map[string]any{
			"api_key":    json.Number("8675309"),
			"safe_count": json.Number("42"),
			"channel":    "C0123456789",
			"ts":         "1710000005.000600",
		},
	}}})
	if err != nil {
		t.Fatalf("Marshal(manifest) error = %v", err)
	}
	manifest := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifest, manifestBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	opts := entrypointOptions{dir: root, env: map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}

	markers := map[string]string{}
	for _, format := range []string{"json", "compact"} {
		// This drives main -> runQuery -> writeQueryResults ->
		// sanitizeQueryResults -> redact.Redactor.Sanitize.
		stdout, stderr, err := runEntrypointWithOptions(t, opts, "q", "attachments() { id metadata }", "--manifest", manifest, "--format", format)
		if err != nil {
			t.Fatalf("attachments %s error = %v\nstdout:\n%s\nstderr:\n%s", format, err, stdout, stderr)
		}
		assertNoRawFixtures(t, stdout+stderr, []string{"8675309"})
		for _, visible := range []string{"att-non-string", "42", "C0123456789", "1710000005.000600", "<secret:"} {
			if !strings.Contains(stdout, visible) {
				t.Errorf("attachments %s output lost %q: %q", format, visible, stdout)
			}
		}
		markers[format] = regexp.MustCompile(`<secret:[0-9a-f]{10}>`).FindString(stdout)
	}
	if markers["json"] == "" || markers["json"] != markers["compact"] {
		t.Fatalf("numeric sensitive marker differs across JSON/compact: json=%q compact=%q", markers["json"], markers["compact"])
	}
}

func TestCLISpacedCredentialLabelsAreRedactedInFinalStderr(t *testing.T) {
	positive := []struct {
		label string
		value string
		want  string
	}{
		{label: "API key", value: "api-secret", want: "<secret:"},
		{label: "access token", value: "access-secret", want: "<token:"},
		{label: "refresh token", value: "refresh-secret", want: "<token:"},
		{label: "action token", value: "action-secret", want: "<action_token:"},
	}
	for _, tt := range positive {
		t.Run(tt.label, func(t *testing.T) {
			fixture := tt.label + ": " + tt.value
			stdout, stderr, err := runEntrypoint(t, "grep", "needle", "--limit="+fixture)
			if err == nil {
				t.Fatal("invalid --limit unexpectedly succeeded")
			}
			assertNoRawFixtures(t, stdout+stderr, []string{tt.value})
			if !strings.Contains(stderr, tt.want) {
				t.Fatalf("fatalErr production writer omitted required marker class; stderr_len=%d", len(stderr))
			}
		})
	}

	for _, fixture := range []string{"API keynote: public-value", "access tokenization: public-value"} {
		t.Run(fixture, func(t *testing.T) {
			_, stderr, err := runEntrypoint(t, "grep", "needle", "--limit="+fixture)
			if err == nil {
				t.Fatal("invalid --limit unexpectedly succeeded")
			}
			if !strings.Contains(stderr, fixture) {
				t.Fatalf("stderr = %q, want bounded adjacent negative %q unchanged", stderr, fixture)
			}
		})
	}
}

func TestCLIAttachmentStageRefusesSourceDestinationIdentity(t *testing.T) {
	tests := []struct {
		name        string
		sourceArg   func(root, source string) string
		destination func(t *testing.T, root, source string) string
	}{
		{
			name:      "relative source and absolute destination",
			sourceArg: func(_, _ string) string { return "source.bin" },
			destination: func(_ *testing.T, _, source string) string {
				return source
			},
		},
		{
			name:      "symlink destination",
			sourceArg: func(_, source string) string { return source },
			destination: func(t *testing.T, root, source string) string {
				path := filepath.Join(root, "source-link.bin")
				if err := os.Symlink(source, path); err != nil {
					t.Fatalf("Symlink() error = %v", err)
				}
				return path
			},
		},
		{
			name:      "hard-link destination",
			sourceArg: func(_, source string) string { return source },
			destination: func(t *testing.T, root, source string) string {
				path := filepath.Join(root, "source-hard-link.bin")
				if err := os.Link(source, path); err != nil {
					t.Fatalf("Link() error = %v", err)
				}
				return path
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			source := filepath.Join(root, "source.bin")
			want := []byte("source-must-survive")
			if err := os.WriteFile(source, want, 0o600); err != nil {
				t.Fatalf("WriteFile(source) error = %v", err)
			}
			destination := tt.destination(t, root, source)
			opts := entrypointOptions{dir: root, env: map[string]string{
				"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
			}}

			stdout, stderr, err := runEntrypointWithOptions(t, opts, "attachment", "stage", tt.sourceArg(root, source), "--destination", destination, "--force", "--format", "json")
			if err == nil {
				t.Fatalf("same-file stage unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			if !strings.Contains(stderr, "same file") {
				t.Fatalf("stderr = %q, want same-file refusal from runAttachmentStage -> attachments.Stage", stderr)
			}
			got, readErr := os.ReadFile(source)
			if readErr != nil || !bytes.Equal(got, want) {
				t.Fatalf("source after CLI refusal = %q error=%v, want unchanged", got, readErr)
			}
			aliases, globErr := filepath.Glob(filepath.Join(root, filepath.FromSlash(attachments.AliasRelativeRoot), "attachment-*"))
			if globErr != nil || len(aliases) != 0 {
				t.Fatalf("same-file refusal published aliases = %v error=%v", aliases, globErr)
			}
		})
	}
}

func runEntrypoint(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return runEntrypointWithOptions(t, entrypointOptions{}, args...)
}

type entrypointOptions struct {
	dir string
	env map[string]string
}

func runEntrypointWithOptions(t *testing.T, opts entrypointOptions, args ...string) (string, string, error) {
	t.Helper()

	commandArgs := []string{"-test.run=^TestEntrypointProcess$", "--"}
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command(os.Args[0], commandArgs...)
	if opts.dir != "" {
		cmd.Dir = opts.dir
	}
	home := t.TempDir()
	env := map[string]string{
		entrypointHelperEnv: "1",
		"HOME":              home,
		"XDG_CONFIG_HOME":   filepath.Join(home, ".config"),
	}
	for key, value := range opts.env {
		env[key] = value
	}
	cmd.Env = environmentWithOverrides(os.Environ(), env)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stripTestProcessTrailer(stdout.String()), stderr.String(), err
}

func stripTestProcessTrailer(output string) string {
	if index := strings.LastIndex(output, "\nPASS\n"); index >= 0 {
		return output[:index+1]
	}
	return strings.TrimSuffix(output, "PASS\n")
}

func environmentWithOverrides(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if _, overridden := overrides[key]; !overridden {
			result = append(result, item)
		}
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func installInProcessCLIRuntime(t *testing.T, publisher func(string) (attachments.Alias, error)) *bytes.Buffer {
	t.Helper()
	previousStdout := cliStdout
	previousStderr := cliStderr
	previousLoader := redactorLoader
	previousPublisher := publishAliasPath
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	testRedactor, err := redact.New([]byte("cli-in-process-test-salt"))
	if err != nil {
		t.Fatalf("redact.New() error = %v", err)
	}
	cliStdout = stdout
	cliStderr = stderr
	redactorLoader = func() (*redact.Redactor, error) { return testRedactor, nil }
	publishAliasPath = publisher
	t.Cleanup(func() {
		cliStdout = previousStdout
		cliStderr = previousStderr
		redactorLoader = previousLoader
		publishAliasPath = previousPublisher
	})
	return stdout
}

func assertNoRawFixtures(t *testing.T, output string, fixtures []string) {
	t.Helper()
	for idx, fixture := range fixtures {
		if strings.Contains(output, fixture) {
			t.Errorf("output leaked raw fixture index=%d; output_len=%d", idx, len(output))
		}
	}
}

func assertUsableAlias(t *testing.T, root, localPath string, want []byte) {
	t.Helper()
	if filepath.IsAbs(localPath) {
		t.Fatalf("alias local_path = %q, want relative path", localPath)
	}
	matched, err := regexp.MatchString(`^\.temp/slack-mgmt/attachment-aliases/attachment-[0-9a-f]{32}$`, filepath.ToSlash(localPath))
	if err != nil {
		t.Fatalf("MatchString(alias) error = %v", err)
	}
	if !matched {
		t.Fatalf("alias local_path = %q, want opaque alias grammar", localPath)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(localPath)))
	if err != nil {
		t.Fatalf("ReadFile(alias %q) error = %v", localPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("alias bytes = %v, want %v", got, want)
	}
}

func aliasEntryCount(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(attachments.AliasRelativeRoot)))
	if err != nil {
		t.Fatalf("ReadDir(alias root) error = %v", err)
	}
	return len(entries)
}

func gitCheckIgnored(repoRoot, path string) (bool, string, error) {
	cmd := exec.Command("git", "check-ignore", "--no-index", "--verbose", path)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true, string(output), nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, string(output), nil
	}
	return false, string(output), err
}

func assertContainsAll(t *testing.T, streamName, got string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s = %q, want substring %q", streamName, got, want)
		}
	}
}
