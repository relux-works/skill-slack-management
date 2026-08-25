package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/relux-works/skill-slack-management/internal/slack"
)

const (
	defaultSlackdumpTimeout      = 30 * time.Second
	defaultSlackdumpProbeTimeout = 5 * time.Second
	defaultSlackdumpOutputBytes  = 8 << 20
	defaultSlackdumpRecords      = 10_000
	compatibleSlackdumpMajor     = 4
)

var slackdumpVersionPattern = regexp.MustCompile(`(?i)\bslackdump\s+v?(\d+)\.(\d+)\.(\d+)\b`)

var (
	ErrSlackdumpAuthorization = errors.New("slackdump_authorization_required")
	ErrSlackdumpExecutable    = errors.New("slackdump_executable_invalid")
	ErrSlackdumpVersion       = errors.New("slackdump_version_unsupported")
	ErrSlackdumpTimeout       = errors.New("slackdump_execution_timeout")
	ErrSlackdumpOutputBound   = errors.New("slackdump_output_too_large")
	ErrSlackdumpRecordBound   = errors.New("slackdump_record_limit_exceeded")
	ErrSlackdumpMalformedJSON = errors.New("slackdump_malformed_json")
	ErrSlackdumpExecution     = errors.New("slackdump_execution_failed")
)

type Sanitizer interface {
	Sanitize(any) (any, error)
	SanitizeText(string) string
}

type SlackdumpCommand struct {
	Executable string
	Args       []string
}

type SlackdumpCommandResult struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	Truncated bool
}

type SlackdumpRunner interface {
	Run(context.Context, SlackdumpCommand, int64) (SlackdumpCommandResult, error)
}

type SlackdumpRuntime struct {
	Runner       SlackdumpRunner
	LookPath     func(string) (string, error)
	GOOS         string
	Sanitizer    Sanitizer
	Timeout      time.Duration
	ProbeTimeout time.Duration
	MaxOutput    int64
	MaxRecords   int
}

type SlackdumpProvider struct {
	executable string
	workspace  string
	runtime    SlackdumpRuntime

	versionOnce sync.Once
	version     string
	versionErr  error

	conversationsMu     sync.Mutex
	conversations       []Conversation
	conversationsErr    error
	conversationsLoaded bool

	usersMu     sync.Mutex
	users       []User
	usersErr    error
	usersLoaded bool
}

func NewSlackdumpProvider(spec SlackdumpSpec, rt SlackdumpRuntime) (*SlackdumpProvider, error) {
	if spec.Authorization != SlackdumpExternalOptIn {
		return nil, fmt.Errorf("%w: set authorization=%q only after authorizing Slackdump itself", ErrSlackdumpAuthorization, SlackdumpExternalOptIn)
	}
	if rt.Sanitizer == nil {
		return nil, fmt.Errorf("Slackdump sanitizer is required")
	}
	if rt.LookPath == nil {
		rt.LookPath = exec.LookPath
	}
	if rt.GOOS == "" {
		rt.GOOS = runtime.GOOS
	}
	resolved, err := resolveSlackdumpExecutable(spec.Executable, rt.LookPath, rt.GOOS)
	if err != nil {
		return nil, err
	}
	if rt.Runner == nil {
		rt.Runner = execSlackdumpRunner{}
	}
	if rt.Timeout <= 0 {
		rt.Timeout = defaultSlackdumpTimeout
	}
	if rt.ProbeTimeout <= 0 {
		rt.ProbeTimeout = defaultSlackdumpProbeTimeout
	}
	if rt.MaxOutput <= 0 {
		rt.MaxOutput = defaultSlackdumpOutputBytes
	}
	if rt.MaxRecords <= 0 {
		rt.MaxRecords = defaultSlackdumpRecords
	}
	return &SlackdumpProvider{
		executable: resolved,
		workspace:  strings.TrimSpace(spec.Workspace),
		runtime:    rt,
	}, nil
}

func resolveSlackdumpExecutable(value string, lookPath func(string) (string, error), goos string) (string, error) {
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("%w: executable path contains control characters", ErrSlackdumpExecutable)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = "slackdump"
	}
	resolved := value
	var err error
	if !filepath.IsAbs(resolved) {
		resolved, err = lookPath(resolved)
		if err != nil {
			return "", fmt.Errorf("%w: slackdump was not found on PATH", ErrSlackdumpExecutable)
		}
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: resolve absolute executable", ErrSlackdumpExecutable)
	}
	info, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: executable path is unavailable", ErrSlackdumpExecutable)
	}
	if !filepath.IsAbs(info) {
		return "", fmt.Errorf("%w: executable did not resolve absolutely", ErrSlackdumpExecutable)
	}
	stat, err := os.Stat(info)
	if err != nil || !stat.Mode().IsRegular() || (goos != "windows" && stat.Mode().Perm()&0o111 == 0) {
		return "", fmt.Errorf("%w: executable must be an executable regular file", ErrSlackdumpExecutable)
	}
	return info, nil
}

func (p *SlackdumpProvider) Capabilities() Capabilities {
	capabilities := NewCapabilities(KindSlackdump,
		OperationConversations,
		OperationConversation,
		OperationUsers,
	)
	capabilities.ExternalAuth = true
	capabilities.Authorization = SlackdumpExternalOptIn
	capabilities.CompatibleWith = ">=4.0.0,<5.0.0"
	return capabilities
}

func (p *SlackdumpProvider) Diagnose(ctx context.Context) Diagnostic {
	diagnostic := Diagnostic{
		Provider:     KindSlackdump,
		Executable:   p.executable,
		Capabilities: p.Capabilities(),
	}
	version, err := p.ensureCompatible(ctx)
	if err != nil {
		diagnostic.Code = slackdumpErrorCode(err)
		diagnostic.Message = p.runtime.Sanitizer.SanitizeText(err.Error())
		return diagnostic
	}
	diagnostic.Ready = true
	diagnostic.Version = version
	return diagnostic
}

func (p *SlackdumpProvider) ensureCompatible(ctx context.Context) (string, error) {
	p.versionOnce.Do(func() {
		probeCtx, cancel := context.WithTimeout(ctx, p.runtime.ProbeTimeout)
		defer cancel()
		result, err := p.runtime.Runner.Run(probeCtx, SlackdumpCommand{Executable: p.executable, Args: []string{"version"}}, p.runtime.MaxOutput)
		if err != nil {
			if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
				p.versionErr = ErrSlackdumpTimeout
				return
			}
			p.versionErr = fmt.Errorf("%w: version probe could not run", ErrSlackdumpExecution)
			return
		}
		if result.Truncated {
			p.versionErr = ErrSlackdumpOutputBound
			return
		}
		if result.ExitCode != 0 {
			detail := p.runtime.Sanitizer.SanitizeText(strings.TrimSpace(string(result.Stderr)))
			p.versionErr = fmt.Errorf("%w: version probe exited %d: %s", ErrSlackdumpExecution, result.ExitCode, detail)
			return
		}
		matches := slackdumpVersionPattern.FindSubmatch(result.Stdout)
		if len(matches) != 4 {
			p.versionErr = fmt.Errorf("%w: version probe returned an unrecognized version", ErrSlackdumpVersion)
			return
		}
		major, _ := strconv.Atoi(string(matches[1]))
		if major != compatibleSlackdumpMajor {
			p.versionErr = fmt.Errorf("%w: require >=4.0.0,<5.0.0", ErrSlackdumpVersion)
			return
		}
		p.version = string(matches[1]) + "." + string(matches[2]) + "." + string(matches[3])
	})
	return p.version, p.versionErr
}

func (p *SlackdumpProvider) runList(ctx context.Context, entity string, destination any) error {
	if _, err := p.ensureCompatible(ctx); err != nil {
		return err
	}
	args := []string{"list", entity, "-format", "json", "-no-json"}
	if p.workspace != "" {
		args = append(args, "-workspace", p.workspace)
	}
	runCtx, cancel := context.WithTimeout(ctx, p.runtime.Timeout)
	defer cancel()
	result, err := p.runtime.Runner.Run(runCtx, SlackdumpCommand{Executable: p.executable, Args: args}, p.runtime.MaxOutput)
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return ErrSlackdumpTimeout
		}
		return fmt.Errorf("%w: list %s could not run", ErrSlackdumpExecution, entity)
	}
	if result.Truncated {
		return ErrSlackdumpOutputBound
	}
	if result.ExitCode != 0 {
		detail := p.runtime.Sanitizer.SanitizeText(strings.TrimSpace(string(result.Stderr)))
		return fmt.Errorf("%w: list %s exited %d: %s", ErrSlackdumpExecution, entity, result.ExitCode, detail)
	}
	jsonStart := bytes.IndexByte(result.Stdout, '[')
	if jsonStart < 0 {
		return fmt.Errorf("%w: list %s did not return a JSON array", ErrSlackdumpMalformedJSON, entity)
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Stdout[jsonStart:]))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: list %s response", ErrSlackdumpMalformedJSON, entity)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%w: list %s response has trailing data", ErrSlackdumpMalformedJSON, entity)
	}
	return nil
}

func (p *SlackdumpProvider) loadConversations(ctx context.Context) ([]Conversation, error) {
	p.conversationsMu.Lock()
	defer p.conversationsMu.Unlock()
	if !p.conversationsLoaded {
		p.conversationsLoaded = true
		var raw []slack.Conversation
		p.conversationsErr = p.runList(ctx, "channels", &raw)
		if p.conversationsErr == nil && len(raw) > p.runtime.MaxRecords {
			p.conversationsErr = fmt.Errorf("%w: channels=%d max=%d", ErrSlackdumpRecordBound, len(raw), p.runtime.MaxRecords)
		}
		if p.conversationsErr == nil {
			p.conversationsErr = sanitizeInto(p.runtime.Sanitizer, normalizeConversations(raw), &p.conversations)
		}
	}
	return append([]Conversation(nil), p.conversations...), p.conversationsErr
}

func (p *SlackdumpProvider) loadUsers(ctx context.Context) ([]User, error) {
	p.usersMu.Lock()
	defer p.usersMu.Unlock()
	if !p.usersLoaded {
		p.usersLoaded = true
		var raw []slack.User
		p.usersErr = p.runList(ctx, "users", &raw)
		if p.usersErr == nil && len(raw) > p.runtime.MaxRecords {
			p.usersErr = fmt.Errorf("%w: users=%d max=%d", ErrSlackdumpRecordBound, len(raw), p.runtime.MaxRecords)
		}
		if p.usersErr == nil {
			p.usersErr = sanitizeInto(p.runtime.Sanitizer, normalizeUsers(raw), &p.users)
		}
	}
	return append([]User(nil), p.users...), p.usersErr
}

func sanitizeInto(sanitizer Sanitizer, input, destination any) error {
	safe, err := sanitizer.Sanitize(input)
	if err != nil {
		return fmt.Errorf("sanitize Slackdump result: %w", err)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		return fmt.Errorf("encode sanitized Slackdump result: %w", err)
	}
	if err := json.Unmarshal(encoded, destination); err != nil {
		return fmt.Errorf("decode sanitized Slackdump result: %w", err)
	}
	return nil
}

func (p *SlackdumpProvider) ListConversations(ctx context.Context, opts ListConversationsOptions) (ConversationPage, error) {
	items, err := p.loadConversations(ctx)
	if err != nil {
		return ConversationPage{}, err
	}
	filtered := make([]Conversation, 0, len(items))
	for _, item := range items {
		if opts.ExcludeArchived && item.IsArchived {
			continue
		}
		if opts.TeamID != "" && item.ContextTeamID != "" && item.ContextTeamID != opts.TeamID {
			continue
		}
		if !conversationTypeAllowed(item, opts.Types) {
			continue
		}
		filtered = append(filtered, item)
	}
	page, next, err := paginate(filtered, opts.Cursor, opts.Limit)
	return ConversationPage{Items: page, NextCursor: next}, err
}

func conversationTypeAllowed(item Conversation, rawTypes string) bool {
	rawTypes = strings.TrimSpace(rawTypes)
	if rawTypes == "" {
		return true
	}
	for _, value := range strings.Split(rawTypes, ",") {
		switch strings.TrimSpace(value) {
		case "public_channel":
			if item.IsChannel && !item.IsPrivate {
				return true
			}
		case "private_channel":
			if (item.IsChannel || item.IsGroup) && item.IsPrivate && !item.IsMPIM {
				return true
			}
		case "im":
			if item.IsIM {
				return true
			}
		case "mpim":
			if item.IsMPIM {
				return true
			}
		}
	}
	return false
}

func (p *SlackdumpProvider) GetConversation(ctx context.Context, opts GetConversationOptions) (Conversation, error) {
	items, err := p.loadConversations(ctx)
	if err != nil {
		return Conversation{}, err
	}
	for _, item := range items {
		if strings.EqualFold(item.ID, strings.TrimSpace(opts.Channel)) {
			return item, nil
		}
	}
	return Conversation{}, fmt.Errorf("conversation %q not found", opts.Channel)
}

func (p *SlackdumpProvider) ListUsers(ctx context.Context, opts ListUsersOptions) (UserPage, error) {
	items, err := p.loadUsers(ctx)
	if err != nil {
		return UserPage{}, err
	}
	filtered := make([]User, 0, len(items))
	for _, item := range items {
		if opts.TeamID != "" && item.TeamID != "" && item.TeamID != opts.TeamID {
			continue
		}
		filtered = append(filtered, item)
	}
	page, next, err := paginate(filtered, opts.Cursor, opts.Limit)
	return UserPage{Items: page, NextCursor: next}, err
}

func paginate[T any](items []T, cursor string, limit int) ([]T, string, error) {
	offset := 0
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		if !strings.HasPrefix(cursor, "offset:") {
			return nil, "", fmt.Errorf("invalid provider cursor")
		}
		parsed, err := strconv.Atoi(strings.TrimPrefix(cursor, "offset:"))
		if err != nil || parsed < 0 || parsed > len(items) {
			return nil, "", fmt.Errorf("invalid provider cursor")
		}
		offset = parsed
	}
	if limit <= 0 {
		limit = 100
	}
	end := len(items)
	if limit < len(items)-offset {
		end = offset + limit
	}
	next := ""
	if end < len(items) {
		next = fmt.Sprintf("offset:%d", end)
	}
	return append([]T(nil), items[offset:end]...), next, nil
}

func (p *SlackdumpProvider) AuthTest(context.Context) (AuthTestResult, error) {
	return AuthTestResult{}, Unsupported(p, OperationAuthTest)
}
func (p *SlackdumpProvider) GetConversationHistory(context.Context, GetConversationHistoryOptions) (ConversationHistoryPage, error) {
	return ConversationHistoryPage{}, Unsupported(p, OperationHistory)
}
func (p *SlackdumpProvider) GetConversationReplies(context.Context, GetConversationRepliesOptions) (ConversationHistoryPage, error) {
	return ConversationHistoryPage{}, Unsupported(p, OperationReplies)
}
func (p *SlackdumpProvider) SearchContext(context.Context, SearchContextOptions) (SearchContextPage, error) {
	return SearchContextPage{}, Unsupported(p, OperationSearchContext)
}
func (p *SlackdumpProvider) SearchInfo(context.Context) (SearchInfoResult, error) {
	return SearchInfoResult{}, Unsupported(p, OperationSearchInfo)
}
func (p *SlackdumpProvider) SearchMessages(context.Context, SearchMessagesOptions) (SearchMessagesPage, error) {
	return SearchMessagesPage{}, Unsupported(p, OperationSearchMessages)
}

func slackdumpErrorCode(err error) string {
	for code, target := range map[string]error{
		ErrSlackdumpAuthorization.Error(): ErrSlackdumpAuthorization,
		ErrSlackdumpExecutable.Error():    ErrSlackdumpExecutable,
		ErrSlackdumpVersion.Error():       ErrSlackdumpVersion,
		ErrSlackdumpTimeout.Error():       ErrSlackdumpTimeout,
		ErrSlackdumpOutputBound.Error():   ErrSlackdumpOutputBound,
		ErrSlackdumpRecordBound.Error():   ErrSlackdumpRecordBound,
		ErrSlackdumpMalformedJSON.Error(): ErrSlackdumpMalformedJSON,
		ErrSlackdumpExecution.Error():     ErrSlackdumpExecution,
	} {
		if errors.Is(err, target) {
			return code
		}
	}
	return "provider_diagnostic_failed"
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	max       int64
	written   int64
	truncated bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.max - b.written
	if remaining > 0 {
		if int64(len(value)) > remaining {
			value = value[:remaining]
			b.truncated = true
		}
		written, err := b.buffer.Write(value)
		b.written += int64(written)
		if err != nil {
			return written, err
		}
	}
	if int64(original) > remaining {
		b.truncated = true
	}
	return original, nil
}

type execSlackdumpRunner struct{}

func (execSlackdumpRunner) Run(ctx context.Context, command SlackdumpCommand, maxOutput int64) (SlackdumpCommandResult, error) {
	stdout := &boundedBuffer{max: maxOutput}
	stderr := &boundedBuffer{max: maxOutput}
	cmd := exec.CommandContext(ctx, command.Executable, command.Args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	result := SlackdumpCommandResult{
		Stdout:    append([]byte(nil), stdout.buffer.Bytes()...),
		Stderr:    append([]byte(nil), stderr.buffer.Bytes()...),
		ExitCode:  0,
		Truncated: stdout.truncated || stderr.truncated,
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}
