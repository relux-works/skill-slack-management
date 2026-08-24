package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	browserOrigin            = "https://app.slack.com"
	browserEnvelopeVersion   = 1
	browserListOutputLimit   = 512 * 1024
	browserReadOutputLimit   = 512 * 1024
	browserStderrOutputLimit = 32 * 1024
	defaultOpenPollAttempts  = 20
	defaultOpenPollInterval  = 250 * time.Millisecond
	browserListTimeout       = 15 * time.Second
	browserOpenTimeout       = 15 * time.Second
	browserReadTimeout       = 35 * time.Second
)

var (
	ErrBrowserUnsupportedPlatform = errors.New("browser_transport_unsupported_platform")
	ErrBrowserCapability          = errors.New("browser_transport_capability_unavailable")
	ErrBrowserTabUnavailable      = errors.New("browser_workspace_tab_unavailable")
	ErrBrowserOriginDrift         = errors.New("browser_workspace_origin_mismatch")
	ErrBrowserWorkspaceDrift      = errors.New("browser_workspace_identity_mismatch")
	ErrBrowserUnauthenticated     = errors.New("browser_workspace_unauthenticated")
	ErrBrowserRequestRefused      = errors.New("browser_transport_request_refused")
	ErrBrowserMalformedResponse   = errors.New("browser_transport_malformed_response")
	ErrBrowserReadTimeout         = errors.New("browser_transport_timeout")
	ErrBrowserWriteUnsupported    = errors.New("browser_write_unsupported")
)

type BrowserCommand struct {
	Name  string
	Args  []string
	Stdin []byte
}

type BrowserCommandResult struct {
	Stdout    []byte
	ExitCode  int
	Truncated bool
}

type BrowserCommandRunner interface {
	Run(ctx context.Context, command BrowserCommand) (BrowserCommandResult, error)
}

// BrowserRuntime keeps command execution, platform, and readiness polling
// injectable. Production uses only silent mac-chrome-session commands and
// /usr/bin/open -g; neither path focuses or selects a Chrome tab.
type BrowserRuntime struct {
	GOOS             string
	Runner           BrowserCommandRunner
	OpenPollAttempts int
	OpenPollInterval time.Duration
}

type BrowserJSONTransport struct {
	workspace        string
	workspaceURL     string
	workspaceID      string
	runner           BrowserCommandRunner
	openPollAttempts int
	openPollInterval time.Duration
}

type browserArgumentKind uint8

const (
	browserStringArgument browserArgumentKind = iota
	browserBoolArgument
	browserIntArgument
)

type browserMethodSpec struct {
	httpMethod string
	arguments  map[string]browserArgumentKind
}

var browserReadMethods = map[string]browserMethodSpec{
	"auth.test": {
		httpMethod: http.MethodPost,
		arguments:  map[string]browserArgumentKind{},
	},
	"assistant.search.context": {
		httpMethod: http.MethodPost,
		arguments: map[string]browserArgumentKind{
			"after": browserIntArgument, "before": browserIntArgument,
			"channel_types": browserStringArgument, "content_types": browserStringArgument,
			"context_channel_id": browserStringArgument, "cursor": browserStringArgument,
			"disable_semantic_search": browserBoolArgument, "highlight": browserBoolArgument,
			"include_archived_channels": browserBoolArgument, "include_bots": browserBoolArgument,
			"include_context_messages": browserBoolArgument, "include_deleted_users": browserBoolArgument,
			"include_message_blocks": browserBoolArgument, "limit": browserIntArgument,
			"modifiers": browserStringArgument, "query": browserStringArgument,
			"sort": browserStringArgument, "sort_dir": browserStringArgument,
			"term_clauses": browserStringArgument,
		},
	},
	"assistant.search.info": {
		httpMethod: http.MethodPost,
		arguments:  map[string]browserArgumentKind{},
	},
	"conversations.history": {
		httpMethod: http.MethodGet,
		arguments: map[string]browserArgumentKind{
			"channel": browserStringArgument, "cursor": browserStringArgument,
			"include_all_metadata": browserBoolArgument, "inclusive": browserBoolArgument,
			"latest": browserStringArgument, "limit": browserIntArgument,
			"oldest": browserStringArgument,
		},
	},
	"conversations.info": {
		httpMethod: http.MethodGet,
		arguments: map[string]browserArgumentKind{
			"channel": browserStringArgument, "include_locale": browserBoolArgument,
			"include_num_members": browserBoolArgument,
		},
	},
	"conversations.list": {
		httpMethod: http.MethodGet,
		arguments: map[string]browserArgumentKind{
			"cursor": browserStringArgument, "exclude_archived": browserBoolArgument,
			"limit": browserIntArgument, "team_id": browserStringArgument,
			"types": browserStringArgument,
		},
	},
	"conversations.replies": {
		httpMethod: http.MethodGet,
		arguments: map[string]browserArgumentKind{
			"channel": browserStringArgument, "cursor": browserStringArgument,
			"include_all_metadata": browserBoolArgument, "inclusive": browserBoolArgument,
			"latest": browserStringArgument, "limit": browserIntArgument,
			"oldest": browserStringArgument, "ts": browserStringArgument,
		},
	},
	"search.messages": {
		httpMethod: http.MethodGet,
		arguments: map[string]browserArgumentKind{
			"count": browserIntArgument, "cursor": browserStringArgument,
			"highlight": browserBoolArgument, "page": browserIntArgument,
			"query": browserStringArgument, "sort": browserStringArgument,
			"sort_dir": browserStringArgument, "team_id": browserStringArgument,
		},
	},
	"users.list": {
		httpMethod: http.MethodGet,
		arguments: map[string]browserArgumentKind{
			"cursor": browserStringArgument, "include_locale": browserBoolArgument,
			"limit": browserIntArgument, "team_id": browserStringArgument,
		},
	},
}

func NewBrowserReadClient(workspace, workspaceURL string, rt BrowserRuntime) (*Client, error) {
	transport, err := NewBrowserJSONTransport(workspace, workspaceURL, rt)
	if err != nil {
		return nil, err
	}
	client, err := NewReadClient(transport)
	if err != nil {
		return nil, err
	}
	client.writeTransport = browserWriteRefusalTransport{}
	return client, nil
}

func NewBrowserJSONTransport(workspace, workspaceURL string, rt BrowserRuntime) (*BrowserJSONTransport, error) {
	configuredURL, configuredErr := url.Parse(strings.TrimSpace(workspaceURL))
	if configuredErr != nil || configuredURL.RawQuery != "" || configuredURL.Fragment != "" {
		return nil, fmt.Errorf("invalid_browser_workspace_url")
	}
	canonicalURL, workspaceID, err := parseBrowserWorkspaceURL(workspaceURL)
	if err != nil {
		return nil, err
	}
	if rt.GOOS == "" {
		rt.GOOS = runtime.GOOS
	}
	if rt.GOOS != "darwin" {
		return nil, fmt.Errorf("%w: browser reads currently require macOS and Google Chrome", ErrBrowserUnsupportedPlatform)
	}
	if rt.Runner == nil {
		rt.Runner = execBrowserCommandRunner{}
	}
	if rt.OpenPollAttempts <= 0 {
		rt.OpenPollAttempts = defaultOpenPollAttempts
	}
	if rt.OpenPollInterval < 0 {
		return nil, fmt.Errorf("invalid browser poll interval")
	}
	if rt.OpenPollInterval == 0 {
		rt.OpenPollInterval = defaultOpenPollInterval
	}
	return &BrowserJSONTransport{
		workspace:        strings.TrimSpace(strings.ToLower(workspace)),
		workspaceURL:     canonicalURL,
		workspaceID:      workspaceID,
		runner:           rt.Runner,
		openPollAttempts: rt.OpenPollAttempts,
		openPollInterval: rt.OpenPollInterval,
	}, nil
}

func (t *BrowserJSONTransport) DoJSON(ctx context.Context, request JSONRequest, out any) error {
	if out == nil {
		return fmt.Errorf("%w: response destination is required", ErrBrowserMalformedResponse)
	}
	args, err := t.validateRequest(request)
	if err != nil {
		return err
	}
	target, err := t.resolveExactTab(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"version": browserEnvelopeVersion,
		"method":  request.Method,
		"args":    args,
	})
	if err != nil {
		return fmt.Errorf("%w: encode sealed browser request", ErrBrowserMalformedResponse)
	}

	readCtx, cancel := boundedBrowserContext(ctx, browserReadTimeout)
	defer cancel()
	result, runErr := t.runner.Run(readCtx, BrowserCommand{
		Name: "mac-chrome-session",
		Args: []string{
			"slack-read",
			"--window-id", target.WindowID,
			"--tab-id", target.TabID,
			"--origin", browserOrigin,
			"--workspace-id", t.workspaceID,
			"--request-stdin",
		},
		Stdin: append(payload, '\n'),
	})
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled) || readCtx.Err() != nil {
			return fmt.Errorf("%w: sealed Chrome read exceeded its deadline", ErrBrowserReadTimeout)
		}
		return fmt.Errorf("%w: mac-chrome-session could not run", ErrBrowserCapability)
	}
	if result.Truncated || len(result.Stdout) > browserReadOutputLimit {
		return fmt.Errorf("%w: sealed Chrome response exceeded its bound", ErrBrowserMalformedResponse)
	}
	return t.decodeSealedResponse(request.Method, result, out)
}

func (t *BrowserJSONTransport) validateRequest(request JSONRequest) (map[string]any, error) {
	spec, ok := browserReadMethods[request.Method]
	if !ok {
		return nil, fmt.Errorf("%w: method %q is not a browser read", ErrBrowserWriteUnsupported, safeMethodName(request.Method))
	}
	if request.HTTPMethod != spec.httpMethod {
		return nil, fmt.Errorf("%w: invalid HTTP method for browser read", ErrBrowserWriteUnsupported)
	}
	if spec.httpMethod == http.MethodGet && len(request.Body) != 0 {
		return nil, fmt.Errorf("%w: GET browser reads do not accept a body", ErrBrowserWriteUnsupported)
	}
	if spec.httpMethod == http.MethodPost && len(request.Query) != 0 {
		return nil, fmt.Errorf("%w: POST browser reads do not accept query arguments", ErrBrowserWriteUnsupported)
	}
	values := request.Query
	if spec.httpMethod == http.MethodPost {
		values = request.Body
	}
	args := make(map[string]any, len(values))
	for key, items := range values {
		normalizedKey := strings.TrimSpace(strings.ToLower(key))
		if normalizedKey == "action_token" {
			return nil, fmt.Errorf("browser read transport does not accept action_token")
		}
		kind, allowed := spec.arguments[normalizedKey]
		if !allowed || normalizedKey != key || len(items) != 1 {
			return nil, fmt.Errorf("browser read transport refused malformed or unknown arguments")
		}
		value := items[0]
		if len(value) > 8*1024 {
			return nil, fmt.Errorf("browser read transport refused oversized arguments")
		}
		if normalizedKey == "team_id" && value != t.workspaceID {
			return nil, fmt.Errorf("%w: team_id does not match configured workspace", ErrBrowserWorkspaceDrift)
		}
		// conversations.list is already pinned to the configured workspace by
		// the atomic page guard. Older compatible sealed readers do not expose
		// this redundant argument, so validate it above and omit it on the wire.
		if normalizedKey == "team_id" && request.Method == "conversations.list" {
			continue
		}
		switch kind {
		case browserBoolArgument:
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return nil, fmt.Errorf("browser read transport refused malformed boolean argument")
			}
			args[normalizedKey] = parsed
		case browserIntArgument:
			parsed, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || parsed < 0 {
				return nil, fmt.Errorf("browser read transport refused malformed integer argument")
			}
			args[normalizedKey] = parsed
		default:
			args[normalizedKey] = value
		}
	}
	return args, nil
}

type browserTab struct {
	WindowID string `json:"windowId"`
	TabID    string `json:"tabId"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Origin   string `json:"origin,omitempty"`
}

func (t *BrowserJSONTransport) resolveExactTab(ctx context.Context) (browserTab, error) {
	tabs, err := t.listTabs(ctx)
	if err != nil {
		return browserTab{}, err
	}
	if target, ok := t.selectWorkspaceTab(tabs); ok {
		return target, nil
	}

	openCtx, cancel := boundedBrowserContext(ctx, browserOpenTimeout)
	result, openErr := t.runner.Run(openCtx, BrowserCommand{
		Name: "/usr/bin/open",
		Args: []string{"-g", "-a", "Google Chrome", t.workspaceURL},
	})
	cancel()
	if openErr != nil || result.ExitCode != 0 {
		return browserTab{}, fmt.Errorf("%w: could not open configured Slack workspace in background", ErrBrowserCapability)
	}

	for attempt := 0; attempt < t.openPollAttempts; attempt++ {
		if attempt > 0 {
			if err := waitBrowserPoll(ctx, t.openPollInterval); err != nil {
				return browserTab{}, fmt.Errorf("%w: background tab readiness deadline expired", ErrBrowserReadTimeout)
			}
		}
		tabs, err = t.listTabs(ctx)
		if err != nil {
			return browserTab{}, err
		}
		if target, ok := t.selectWorkspaceTab(tabs); ok {
			return target, nil
		}
	}
	return browserTab{}, fmt.Errorf("%w: Chrome opened %s in the background but no exact workspace tab became ready", ErrBrowserTabUnavailable, t.workspaceURL)
}

func (t *BrowserJSONTransport) listTabs(ctx context.Context) ([]browserTab, error) {
	listCtx, cancel := boundedBrowserContext(ctx, browserListTimeout)
	defer cancel()
	result, err := t.runner.Run(listCtx, BrowserCommand{Name: "mac-chrome-session", Args: []string{"list"}})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || listCtx.Err() != nil {
			return nil, fmt.Errorf("%w: Chrome tab discovery timed out", ErrBrowserReadTimeout)
		}
		return nil, fmt.Errorf("%w: run mac-chrome-session list", ErrBrowserCapability)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("%w: mac-chrome-session list failed; enable Chrome Automation access and retry", ErrBrowserCapability)
	}
	if result.Truncated || len(result.Stdout) > browserListOutputLimit {
		return nil, fmt.Errorf("%w: Chrome tab metadata exceeded its bound", ErrBrowserMalformedResponse)
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Stdout))
	var tabs []browserTab
	if err := decoder.Decode(&tabs); err != nil || requireBrowserJSONEOF(decoder) != nil {
		return nil, fmt.Errorf("%w: mac-chrome-session list returned invalid metadata", ErrBrowserMalformedResponse)
	}
	for _, tab := range tabs {
		if !validChromeTargetID(tab.WindowID) || !validChromeTargetID(tab.TabID) {
			return nil, fmt.Errorf("%w: Chrome tab metadata contained an invalid exact target", ErrBrowserMalformedResponse)
		}
	}
	return tabs, nil
}

func (t *BrowserJSONTransport) selectWorkspaceTab(tabs []browserTab) (browserTab, bool) {
	candidates := make([]browserTab, 0, len(tabs))
	for _, tab := range tabs {
		if tabMatchesWorkspace(tab.URL, t.workspaceID) {
			candidates = append(candidates, tab)
		}
	}
	if len(candidates) == 0 {
		return browserTab{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].WindowID != candidates[j].WindowID {
			return numericStringLess(candidates[i].WindowID, candidates[j].WindowID)
		}
		return numericStringLess(candidates[i].TabID, candidates[j].TabID)
	})
	return candidates[0], true
}

type sealedBrowserResponse struct {
	Version int                   `json:"version"`
	OK      bool                  `json:"ok"`
	Method  string                `json:"method,omitempty"`
	Data    json.RawMessage       `json:"data,omitempty"`
	Error   *sealedBrowserFailure `json:"error,omitempty"`
}

type sealedBrowserFailure struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
}

func (t *BrowserJSONTransport) decodeSealedResponse(method string, result BrowserCommandResult, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(result.Stdout))
	decoder.DisallowUnknownFields()
	var response sealedBrowserResponse
	if err := decoder.Decode(&response); err != nil || requireBrowserJSONEOF(decoder) != nil || response.Version != browserEnvelopeVersion {
		return fmt.Errorf("%w: mac-chrome-session returned an invalid sealed envelope", ErrBrowserMalformedResponse)
	}
	if !response.OK {
		if response.Error == nil || strings.TrimSpace(response.Error.Kind) == "" {
			return fmt.Errorf("%w: mac-chrome-session returned an invalid refusal", ErrBrowserMalformedResponse)
		}
		return t.mapSealedFailure(response.Error.Kind)
	}
	if result.ExitCode != 0 || response.Method != method || len(response.Data) == 0 || response.Error != nil {
		return fmt.Errorf("%w: inconsistent sealed success envelope", ErrBrowserMalformedResponse)
	}
	var meta struct {
		OK       *bool  `json:"ok"`
		Error    string `json:"error"`
		Needed   string `json:"needed"`
		Provided string `json:"provided"`
	}
	if err := json.Unmarshal(response.Data, &meta); err != nil || meta.OK == nil {
		return fmt.Errorf("%w: sealed Slack payload is not a valid API response", ErrBrowserMalformedResponse)
	}
	if err := json.Unmarshal(response.Data, out); err != nil {
		return fmt.Errorf("%w: decode sealed Slack payload", ErrBrowserMalformedResponse)
	}
	if !*meta.OK {
		if browserAuthenticationError(meta.Error) {
			return fmt.Errorf("%w: authenticate the configured workspace in its Chrome tab, then retry", ErrBrowserUnauthenticated)
		}
		return &APIError{StatusCode: http.StatusOK, Code: meta.Error, Needed: meta.Needed, Provided: meta.Provided}
	}
	return nil
}

func (t *BrowserJSONTransport) mapSealedFailure(kind string) error {
	switch strings.TrimSpace(kind) {
	case "target-missing":
		return fmt.Errorf("%w: the selected exact Chrome tab closed; retry to rediscover or reopen the configured workspace", ErrBrowserTabUnavailable)
	case "origin-mismatch":
		return fmt.Errorf("%w: the selected Chrome tab left %s; no Slack request was started", ErrBrowserOriginDrift, browserOrigin)
	case "workspace-mismatch":
		return fmt.Errorf("%w: the selected Chrome tab left workspace %s; no Slack request was started", ErrBrowserWorkspaceDrift, t.workspaceID)
	case "capability-unavailable":
		return fmt.Errorf("%w: authenticate the configured workspace in Chrome and install a compatible mac-infra sealed Slack reader, then retry", errors.Join(ErrBrowserUnauthenticated, ErrBrowserCapability))
	case "method-not-allowed":
		return fmt.Errorf("%w: installed mac-infra does not support this sealed Slack read; update mac-infra and retry", ErrBrowserCapability)
	case "invalid-arguments", "invalid-request", "request-too-large", "invalid-target", "invalid-origin", "invalid-workspace":
		return fmt.Errorf("%w: sealed Slack read rejected the request; verify query arguments and update mac-infra if the request is supported", ErrBrowserRequestRefused)
	case "automation-unavailable", "unavailable":
		return fmt.Errorf("%w: Chrome automation or the sealed Slack reader is unavailable; verify Chrome Automation permission and update mac-infra", ErrBrowserCapability)
	case "timeout":
		return fmt.Errorf("%w: sealed Chrome read exceeded its deadline", ErrBrowserReadTimeout)
	case "response-too-large", "response-invalid", "response-too-deep", "response-too-complex":
		return fmt.Errorf("%w: sealed Chrome response failed its bounded output policy", ErrBrowserMalformedResponse)
	case "api-unavailable":
		return fmt.Errorf("browser Slack request failed inside the authenticated Chrome page")
	default:
		return fmt.Errorf("%w: mac-chrome-session returned an unknown safe refusal", ErrBrowserMalformedResponse)
	}
}

func parseBrowserWorkspaceURL(raw string) (canonical, workspaceID string, err error) {
	parsed, parseErr := url.Parse(strings.TrimSpace(raw))
	if parseErr != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "app.slack.com") || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("invalid_browser_workspace_url")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) < 2 || parts[0] != "client" || !validBrowserWorkspaceID(parts[1]) {
		return "", "", fmt.Errorf("invalid_browser_workspace_url")
	}
	return browserOrigin + "/client/" + parts[1], parts[1], nil
}

func validBrowserWorkspaceID(value string) bool {
	if len(value) < 9 || len(value) > 32 || value[0] != 'T' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func safeMethodName(value string) string {
	if _, ok := browserReadMethods[value]; ok {
		return value
	}
	return "unsupported"
}

func tabMatchesWorkspace(raw, workspaceID string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "app.slack.com") || parsed.Port() != "" || parsed.User != nil {
		return false
	}
	prefix := "/client/" + workspaceID
	cleanPath := strings.TrimSuffix(parsed.EscapedPath(), "/")
	return cleanPath == prefix || strings.HasPrefix(cleanPath, prefix+"/")
}

func validChromeTargetID(value string) bool {
	if value == "" || len(value) > 20 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func numericStringLess(left, right string) bool {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	if len(left) != len(right) {
		return len(left) < len(right)
	}
	return left < right
}

func browserAuthenticationError(code string) bool {
	switch strings.TrimSpace(code) {
	case "not_authed", "invalid_auth", "account_inactive", "token_revoked":
		return true
	default:
		return false
	}
}

func requireBrowserJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return fmt.Errorf("unexpected trailing JSON value")
}

func boundedBrowserContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, timeout)
}

func waitBrowserPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type browserWriteRefusalTransport struct{}

func (browserWriteRefusalTransport) DoJSON(context.Context, JSONRequest, any) error {
	return ErrBrowserWriteUnsupported
}

type execBrowserCommandRunner struct{}

func (execBrowserCommandRunner) Run(ctx context.Context, command BrowserCommand) (BrowserCommandResult, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	if len(command.Stdin) != 0 {
		cmd.Stdin = bytes.NewReader(command.Stdin)
	}
	stdout := &boundedCommandBuffer{limit: browserReadOutputLimit}
	stderr := &boundedCommandBuffer{limit: browserStderrOutputLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	result := BrowserCommandResult{Stdout: stdout.Bytes(), Truncated: stdout.truncated || stderr.truncated}
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

type boundedCommandBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedCommandBuffer) Write(data []byte) (int, error) {
	originalLen := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || originalLen > 0
		return originalLen, nil
	}
	if len(data) > remaining {
		b.truncated = true
		data = data[:remaining]
	}
	_, _ = b.buffer.Write(data)
	return originalLen, nil
}

func (b *boundedCommandBuffer) Bytes() []byte {
	return append([]byte(nil), b.buffer.Bytes()...)
}
