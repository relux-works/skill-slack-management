package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBrowserReadClientUsesDeterministicExactTabAndAtomicSealedRead(t *testing.T) {
	runner := &recordingBrowserRunner{}
	runner.run = func(call int, command BrowserCommand) (BrowserCommandResult, error) {
		switch call {
		case 0:
			return browserJSONResult(t, []browserTab{
				{WindowID: "10", TabID: "9", URL: "https://app.slack.com/client/T01234567/C9"},
				{WindowID: "1", TabID: "1", URL: "https://app.slack.com/client/T99999999/C1"},
				{WindowID: "2", TabID: "7", URL: "https://app.slack.com/client/T01234567/C7"},
			}), nil
		case 1:
			return sealedSuccess(t, "auth.test", map[string]any{
				"ok": true, "team": "Fixture", "team_id": "T01234567", "user_id": "U1",
			}), nil
		default:
			t.Fatalf("unexpected command %d: %#v", call, command)
			return BrowserCommandResult{}, nil
		}
	}
	client := newFakeBrowserClient(t, runner)

	result, err := client.AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TeamID != "T01234567" || result.UserID != "U1" {
		t.Fatalf("AuthTest() = %#v", result)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("commands = %d, want list + sealed read", len(runner.calls))
	}
	assertCommand(t, runner.calls[0], "mac-chrome-session", "list")
	assertCommand(t, runner.calls[1], "mac-chrome-session",
		"slack-read", "--window-id", "2", "--tab-id", "7",
		"--origin", browserOrigin, "--workspace-id", "T01234567", "--request-stdin")
	assertSilentCommands(t, runner.calls)

	var request map[string]any
	if err := json.Unmarshal(runner.calls[1].Stdin, &request); err != nil {
		t.Fatal(err)
	}
	if request["version"] != float64(1) || request["method"] != "auth.test" || len(request["args"].(map[string]any)) != 0 {
		t.Fatalf("sealed request = %#v", request)
	}
	for _, forbidden := range []string{"xox", "cookie", "localstorage", "authorization"} {
		if strings.Contains(strings.ToLower(string(runner.calls[1].Stdin)), forbidden) {
			t.Fatalf("sealed request exported browser credentials: %s", runner.calls[1].Stdin)
		}
	}
}

func TestBrowserReadOpensMissingWorkspaceInBackgroundAndPollsExactTab(t *testing.T) {
	runner := &recordingBrowserRunner{}
	runner.run = func(call int, command BrowserCommand) (BrowserCommandResult, error) {
		switch call {
		case 0:
			return browserJSONResult(t, []browserTab{{WindowID: "1", TabID: "2", URL: "https://example.com"}}), nil
		case 1:
			return BrowserCommandResult{}, nil
		case 2:
			return browserJSONResult(t, []browserTab{{WindowID: "3", TabID: "4", URL: "https://app.slack.com/client/T99999999"}}), nil
		case 3:
			return browserJSONResult(t, []browserTab{{WindowID: "8", TabID: "9", URL: "https://app.slack.com/client/T01234567"}}), nil
		case 4:
			return sealedSuccess(t, "auth.test", map[string]any{"ok": true, "team_id": "T01234567"}), nil
		default:
			t.Fatalf("unexpected command %d: %#v", call, command)
			return BrowserCommandResult{}, nil
		}
	}
	client := newFakeBrowserClient(t, runner)
	if _, err := client.AuthTest(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCommand(t, runner.calls[1], "/usr/bin/open", "-g", "-a", "Google Chrome", "https://app.slack.com/client/T01234567")
	assertCommand(t, runner.calls[4], "mac-chrome-session",
		"slack-read", "--window-id", "8", "--tab-id", "9",
		"--origin", browserOrigin, "--workspace-id", "T01234567", "--request-stdin")
	assertSilentCommands(t, runner.calls)
}

func TestBrowserDiscoveryFailureNeverBecomesMissingTabFallback(t *testing.T) {
	tests := []struct {
		name   string
		result BrowserCommandResult
		want   error
	}{
		{name: "list command failure", result: BrowserCommandResult{ExitCode: 1}, want: ErrBrowserCapability},
		{name: "malformed list", result: BrowserCommandResult{Stdout: []byte(`{"windowId":"1"}`)}, want: ErrBrowserMalformedResponse},
		{name: "truncated list", result: BrowserCommandResult{Stdout: []byte(`[]`), Truncated: true}, want: ErrBrowserMalformedResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingBrowserRunner{run: func(int, BrowserCommand) (BrowserCommandResult, error) {
				return test.result, nil
			}}
			client := newFakeBrowserClient(t, runner)
			_, err := client.AuthTest(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("AuthTest() error = %v, want %v", err, test.want)
			}
			if len(runner.calls) != 1 {
				t.Fatalf("discovery failure triggered fallback commands: %#v", runner.calls)
			}
		})
	}
}

func TestBrowserSealedRefusalsMapToActionableSafeErrorsWithoutRetry(t *testing.T) {
	tests := []struct {
		kind string
		want error
	}{
		{kind: "target-missing", want: ErrBrowserTabUnavailable},
		{kind: "origin-mismatch", want: ErrBrowserOriginDrift},
		{kind: "workspace-mismatch", want: ErrBrowserWorkspaceDrift},
		{kind: "capability-unavailable", want: ErrBrowserUnauthenticated},
		{kind: "method-not-allowed", want: ErrBrowserCapability},
		{kind: "invalid-arguments", want: ErrBrowserRequestRefused},
		{kind: "invalid-request", want: ErrBrowserRequestRefused},
		{kind: "request-too-large", want: ErrBrowserRequestRefused},
		{kind: "invalid-target", want: ErrBrowserRequestRefused},
		{kind: "invalid-origin", want: ErrBrowserRequestRefused},
		{kind: "invalid-workspace", want: ErrBrowserRequestRefused},
		{kind: "automation-unavailable", want: ErrBrowserCapability},
		{kind: "unavailable", want: ErrBrowserCapability},
		{kind: "timeout", want: ErrBrowserReadTimeout},
		{kind: "response-invalid", want: ErrBrowserMalformedResponse},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			runner := &recordingBrowserRunner{}
			runner.run = func(call int, _ BrowserCommand) (BrowserCommandResult, error) {
				if call == 0 {
					return exactWorkspaceList(t), nil
				}
				return sealedFailure(t, test.kind, "xoxb-secret-bearing-adapter-message"), nil
			}
			client := newFakeBrowserClient(t, runner)
			_, err := client.AuthTest(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("AuthTest() error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), "xoxb-") {
				t.Fatalf("adapter message leaked through error: %v", err)
			}
			if len(runner.calls) != 2 {
				t.Fatalf("sealed refusal was retried or rerouted: %#v", runner.calls)
			}
		})
	}
}

func TestBrowserHistoryMapsSealedArgumentRefusalAtClientCallSite(t *testing.T) {
	runner := &recordingBrowserRunner{}
	runner.run = func(call int, _ BrowserCommand) (BrowserCommandResult, error) {
		if call == 0 {
			return exactWorkspaceList(t), nil
		}
		return sealedFailure(t, "invalid-arguments", "xoxb-secret-bearing-adapter-message"), nil
	}
	client := newFakeBrowserClient(t, runner)
	_, err := client.GetConversationHistory(context.Background(), GetConversationHistoryOptions{
		Channel: "C0123456789",
		Limit:   200,
	})
	if !errors.Is(err, ErrBrowserRequestRefused) {
		t.Fatalf("GetConversationHistory() error = %v, want ErrBrowserRequestRefused", err)
	}
	if errors.Is(err, ErrBrowserMalformedResponse) {
		t.Fatalf("sealed argument refusal was mislabeled as malformed response: %v", err)
	}
	if strings.Contains(err.Error(), "xoxb-") {
		t.Fatalf("sealed refusal leaked adapter details: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("sealed argument refusal was retried or rerouted: %#v", runner.calls)
	}
}

func TestBrowserSlackAuthenticationErrorsAreActionableAndSanitized(t *testing.T) {
	runner := &recordingBrowserRunner{}
	runner.run = func(call int, _ BrowserCommand) (BrowserCommandResult, error) {
		if call == 0 {
			return exactWorkspaceList(t), nil
		}
		return sealedSuccess(t, "auth.test", map[string]any{
			"ok": false, "error": "invalid_auth", "detail": "xoxb-must-not-leak",
		}), nil
	}
	client := newFakeBrowserClient(t, runner)
	_, err := client.AuthTest(context.Background())
	if !errors.Is(err, ErrBrowserUnauthenticated) || !strings.Contains(err.Error(), "authenticate") {
		t.Fatalf("AuthTest() error = %v", err)
	}
	if strings.Contains(err.Error(), "xoxb-") || strings.Contains(err.Error(), "invalid_auth") {
		t.Fatalf("authentication error leaked adapter details: %v", err)
	}
}

func TestBrowserHistoryAndRepliesUseTypedBoundedSealedArguments(t *testing.T) {
	runner := &recordingBrowserRunner{}
	methods := []string{"conversations.history", "conversations.replies"}
	runner.run = func(call int, command BrowserCommand) (BrowserCommandResult, error) {
		if call%2 == 0 {
			return exactWorkspaceList(t), nil
		}
		method := methods[call/2]
		var request struct {
			Version int            `json:"version"`
			Method  string         `json:"method"`
			Args    map[string]any `json:"args"`
		}
		if err := json.Unmarshal(command.Stdin, &request); err != nil {
			t.Fatal(err)
		}
		if request.Version != 1 || request.Method != method {
			t.Fatalf("request = %#v, want %s", request, method)
		}
		if _, ok := request.Args["limit"].(float64); !ok {
			t.Fatalf("limit was not encoded as a JSON number: %#v", request.Args)
		}
		if _, ok := request.Args["inclusive"].(bool); !ok {
			t.Fatalf("inclusive was not encoded as a JSON boolean: %#v", request.Args)
		}
		return sealedSuccess(t, method, map[string]any{
			"ok": true, "messages": []map[string]any{{"ts": "1.0", "text": method}},
		}), nil
	}
	client := newFakeBrowserClient(t, runner)
	history, err := client.GetConversationHistory(context.Background(), GetConversationHistoryOptions{
		Channel: "C0123456789", Limit: 7, Inclusive: true,
	})
	if err != nil || len(history.Items) != 1 || history.Items[0].Text != "conversations.history" {
		t.Fatalf("GetConversationHistory() = %#v, %v", history, err)
	}
	replies, err := client.GetConversationReplies(context.Background(), GetConversationRepliesOptions{
		Channel: "C0123456789", Ts: "1.0", Limit: 3, Inclusive: true,
	})
	if err != nil || len(replies.Items) != 1 || replies.Items[0].Text != "conversations.replies" {
		t.Fatalf("GetConversationReplies() = %#v, %v", replies, err)
	}
}

func TestBrowserConversationsListValidatesThenOmitsRedundantTeamID(t *testing.T) {
	runner := &recordingBrowserRunner{}
	runner.run = func(call int, command BrowserCommand) (BrowserCommandResult, error) {
		if call == 0 {
			return exactWorkspaceList(t), nil
		}
		var request struct {
			Method string         `json:"method"`
			Args   map[string]any `json:"args"`
		}
		if err := json.Unmarshal(command.Stdin, &request); err != nil {
			t.Fatal(err)
		}
		if request.Method != "conversations.list" {
			t.Fatalf("method = %q", request.Method)
		}
		if _, exists := request.Args["team_id"]; exists {
			t.Fatalf("redundant pinned team_id reached sealed reader: %#v", request.Args)
		}
		return sealedSuccess(t, request.Method, map[string]any{"ok": true, "channels": []any{}}), nil
	}
	client := newFakeBrowserClient(t, runner)
	if _, err := client.ListConversations(context.Background(), ListConversationsOptions{TeamID: "T01234567"}); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserGatesRejectWritesTokensCrossWorkspaceAndMalformedRequestsBeforeChrome(t *testing.T) {
	runner := &recordingBrowserRunner{run: func(_ int, command BrowserCommand) (BrowserCommandResult, error) {
		t.Fatalf("refused request reached Chrome: %#v", command)
		return BrowserCommandResult{}, nil
	}}
	client := newFakeBrowserClient(t, runner)
	if _, err := client.PostMessage(context.Background(), PostMessageOptions{Channel: "C1", Text: "must not send"}); !errors.Is(err, ErrBrowserWriteUnsupported) {
		t.Fatalf("PostMessage() error = %v", err)
	}
	transport, err := NewBrowserJSONTransport("acme", "https://app.slack.com/client/T01234567", fakeBrowserRuntime(runner))
	if err != nil {
		t.Fatal(err)
	}
	tests := []JSONRequest{
		{HTTPMethod: http.MethodPost, Method: "assistant.search.context", Body: url.Values{"action_token": {"secret"}}},
		{HTTPMethod: http.MethodPost, Method: "chat.postMessage"},
		{HTTPMethod: http.MethodDelete, Method: "auth.test"},
		{HTTPMethod: http.MethodGet, Method: "users.list", Query: url.Values{"team_id": {"T99999999"}}},
		{HTTPMethod: http.MethodGet, Method: "users.list", Query: url.Values{"unknown": {"value"}}},
		{HTTPMethod: http.MethodGet, Method: "users.list", Query: url.Values{"limit": {"1", "2"}}},
		{HTTPMethod: http.MethodGet, Method: "users.list", Body: url.Values{"limit": {"1"}}},
		{HTTPMethod: http.MethodPost, Method: "auth.test", Query: url.Values{"query": {"x"}}},
	}
	for idx, request := range tests {
		if err := transport.DoJSON(context.Background(), request, &map[string]any{}); err == nil {
			t.Fatalf("request %d unexpectedly passed: %#v", idx, request)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("refused requests reached command runner: %#v", runner.calls)
	}
}

func TestBrowserTransportRejectsUnsupportedPlatformBeforeCapabilityClaim(t *testing.T) {
	_, err := NewBrowserReadClient("acme", "https://app.slack.com/client/T01234567", BrowserRuntime{GOOS: "linux"})
	if !errors.Is(err, ErrBrowserUnsupportedPlatform) {
		t.Fatalf("NewBrowserReadClient() error = %v, want ErrBrowserUnsupportedPlatform", err)
	}
}

func TestBrowserTransportWorkspaceURLValidation(t *testing.T) {
	runner := &recordingBrowserRunner{}
	valid, err := NewBrowserJSONTransport("acme", "https://app.slack.com/client/T01234567/C111", fakeBrowserRuntime(runner))
	if err != nil {
		t.Fatal(err)
	}
	if valid.workspaceURL != "https://app.slack.com/client/T01234567" || valid.workspaceID != "T01234567" {
		t.Fatalf("canonical browser target = %#v", valid)
	}

	invalid := []string{
		"http://app.slack.com/client/T01234567",
		"https://evil.example/client/T01234567",
		"https://app.slack.com/client/T01234567?token=secret",
		"https://app.slack.com/client/not_valid",
		"https://app.slack.com/client/C01234567",
		"https://app.slack.com/client/T012abc67",
		"https://app.slack.com/",
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			if _, err := NewBrowserJSONTransport("acme", raw, fakeBrowserRuntime(runner)); err == nil {
				t.Fatalf("NewBrowserJSONTransport(%q) unexpectedly succeeded", raw)
			}
		})
	}
}

func TestBrowserSealedEnvelopeRejectsMethodDriftTrailingDataAndSecretBearingMalformedOutput(t *testing.T) {
	tests := map[string]BrowserCommandResult{
		"method drift":  sealedSuccess(t, "users.list", map[string]any{"ok": true}),
		"trailing data": {Stdout: []byte(`{"version":1,"ok":true,"method":"auth.test","data":{"ok":true}} {}`)},
		"raw secret":    {Stdout: []byte(`xoxb-this-is-not-json`)},
		"truncated":     {Stdout: []byte(`{"version":1`), Truncated: true},
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			runner := &recordingBrowserRunner{}
			runner.run = func(call int, _ BrowserCommand) (BrowserCommandResult, error) {
				if call == 0 {
					return exactWorkspaceList(t), nil
				}
				return response, nil
			}
			client := newFakeBrowserClient(t, runner)
			_, err := client.AuthTest(context.Background())
			if !errors.Is(err, ErrBrowserMalformedResponse) {
				t.Fatalf("AuthTest() error = %v", err)
			}
			if strings.Contains(err.Error(), "xoxb-") {
				t.Fatalf("malformed response leaked raw output: %v", err)
			}
		})
	}
}

func TestExecBrowserCommandRunnerUsesStdinBoundsOutputAndKeepsStderrPrivate(t *testing.T) {
	runner := execBrowserCommandRunner{}
	dir := t.TempDir()
	success := filepath.Join(dir, "success")
	if err := os.WriteFile(success, []byte("#!/bin/sh\nset -eu\n[ \"$#\" -eq 1 ]\n[ \"$1\" = safe-arg ]\ncat\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"version":1,"method":"auth.test","args":{}}`)
	result, err := runner.Run(context.Background(), BrowserCommand{Name: success, Args: []string{"safe-arg"}, Stdin: input})
	if err != nil || result.ExitCode != 0 || result.Truncated || !reflect.DeepEqual(result.Stdout, input) {
		t.Fatalf("successful command = %#v, %v", result, err)
	}

	failure := filepath.Join(dir, "failure")
	if err := os.WriteFile(failure, []byte("#!/bin/sh\nprintf '%s' '{\"version\":1,\"ok\":false}'\nprintf '%s' 'xoxb-private-stderr' >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err = runner.Run(context.Background(), BrowserCommand{Name: failure})
	if err != nil || result.ExitCode != 7 || strings.Contains(string(result.Stdout), "xoxb-") {
		t.Fatalf("failed command = %#v, %v", result, err)
	}

	buffer := &boundedCommandBuffer{limit: 4}
	if count, err := buffer.Write([]byte("123456")); err != nil || count != 6 {
		t.Fatalf("bounded write = %d, %v", count, err)
	}
	if !buffer.truncated || string(buffer.Bytes()) != "1234" {
		t.Fatalf("bounded buffer = %q truncated=%v", buffer.Bytes(), buffer.truncated)
	}
}

func TestExecBrowserCommandRunnerHonorsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err := (execBrowserCommandRunner{}).Run(ctx, BrowserCommand{
		Name: "/bin/sh", Args: []string{"-c", "sleep 1"},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context deadline", err)
	}
}

type recordingBrowserRunner struct {
	calls []BrowserCommand
	run   func(call int, command BrowserCommand) (BrowserCommandResult, error)
}

func (r *recordingBrowserRunner) Run(_ context.Context, command BrowserCommand) (BrowserCommandResult, error) {
	copyCommand := BrowserCommand{
		Name:  command.Name,
		Args:  append([]string(nil), command.Args...),
		Stdin: append([]byte(nil), command.Stdin...),
	}
	r.calls = append(r.calls, copyCommand)
	if r.run == nil {
		return BrowserCommandResult{}, fmt.Errorf("unexpected command: %#v", command)
	}
	return r.run(len(r.calls)-1, copyCommand)
}

func newFakeBrowserClient(t *testing.T, runner BrowserCommandRunner) *Client {
	t.Helper()
	client, err := NewBrowserReadClient("acme", "https://app.slack.com/client/T01234567", fakeBrowserRuntime(runner))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func fakeBrowserRuntime(runner BrowserCommandRunner) BrowserRuntime {
	return BrowserRuntime{
		GOOS:             "darwin",
		Runner:           runner,
		OpenPollAttempts: 3,
		OpenPollInterval: time.Nanosecond,
	}
}

func exactWorkspaceList(t *testing.T) BrowserCommandResult {
	t.Helper()
	return browserJSONResult(t, []browserTab{{
		WindowID: "11", TabID: "22", URL: "https://app.slack.com/client/T01234567/C1",
	}})
}

func browserJSONResult(t *testing.T, value any) BrowserCommandResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return BrowserCommandResult{Stdout: data}
}

func sealedSuccess(t *testing.T, method string, data any) BrowserCommandResult {
	t.Helper()
	return browserJSONResult(t, map[string]any{
		"version": 1, "ok": true, "method": method, "data": data,
	})
}

func sealedFailure(t *testing.T, kind, message string) BrowserCommandResult {
	t.Helper()
	result := browserJSONResult(t, map[string]any{
		"version": 1, "ok": false,
		"error": map[string]any{"kind": kind, "message": message},
	})
	result.ExitCode = 1
	return result
}

func assertCommand(t *testing.T, got BrowserCommand, name string, args ...string) {
	t.Helper()
	if got.Name != name || !reflect.DeepEqual(got.Args, args) {
		t.Fatalf("command = %q %#v, want %q %#v", got.Name, got.Args, name, args)
	}
}

func assertSilentCommands(t *testing.T, commands []BrowserCommand) {
	t.Helper()
	for _, command := range commands {
		joined := strings.ToLower(command.Name + " " + strings.Join(command.Args, " "))
		for _, forbidden := range []string{" focus", "activate", "frontmost", "active-tab", "run-js", "system events", "osascript"} {
			if strings.Contains(joined, forbidden) {
				t.Fatalf("silent browser command contains %q: %s", forbidden, joined)
			}
		}
	}
}
