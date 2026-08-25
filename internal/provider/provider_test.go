package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/relux-works/skill-slack-management/internal/redact"
	"github.com/relux-works/skill-slack-management/internal/slack"
)

type fakeSlackdumpRunner struct {
	commands []SlackdumpCommand
	results  []SlackdumpCommandResult
	errors   []error
}

type contractTransport struct {
	auth          slack.AuthTestResult
	conversation  slack.Conversation
	history       slack.ConversationHistoryPage
	replies       slack.ConversationHistoryPage
	conversations slack.ConversationPage
	users         slack.UserPage
	searchContext slack.SearchContextPage
	searchInfo    slack.SearchInfoResult
	search        slack.SearchMessagesPage
}

func (c *contractTransport) AuthTest(context.Context) (slack.AuthTestResult, error) {
	return c.auth, nil
}
func (c *contractTransport) GetConversation(context.Context, slack.GetConversationOptions) (slack.Conversation, error) {
	return c.conversation, nil
}
func (c *contractTransport) GetConversationHistory(context.Context, slack.GetConversationHistoryOptions) (slack.ConversationHistoryPage, error) {
	return c.history, nil
}
func (c *contractTransport) GetConversationReplies(context.Context, slack.GetConversationRepliesOptions) (slack.ConversationHistoryPage, error) {
	return c.replies, nil
}
func (c *contractTransport) ListConversations(context.Context, slack.ListConversationsOptions) (slack.ConversationPage, error) {
	return c.conversations, nil
}
func (c *contractTransport) ListUsers(context.Context, slack.ListUsersOptions) (slack.UserPage, error) {
	return c.users, nil
}
func (c *contractTransport) SearchContext(context.Context, slack.SearchContextOptions) (slack.SearchContextPage, error) {
	return c.searchContext, nil
}
func (c *contractTransport) SearchInfo(context.Context) (slack.SearchInfoResult, error) {
	return c.searchInfo, nil
}
func (c *contractTransport) SearchMessages(context.Context, slack.SearchMessagesOptions) (slack.SearchMessagesPage, error) {
	return c.search, nil
}

func (r *fakeSlackdumpRunner) Run(_ context.Context, command SlackdumpCommand, _ int64) (SlackdumpCommandResult, error) {
	r.commands = append(r.commands, command)
	idx := len(r.commands) - 1
	var result SlackdumpCommandResult
	if idx < len(r.results) {
		result = r.results[idx]
	}
	if idx < len(r.errors) {
		return result, r.errors[idx]
	}
	return result, nil
}

func newSlackdumpTestProvider(t *testing.T, runner SlackdumpRunner, maxRecords int) *SlackdumpProvider {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "slackdump")
	if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	sanitizer, err := redact.New([]byte("slackdump-provider-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewSlackdumpProvider(SlackdumpSpec{
		Executable: executable, Authorization: SlackdumpExternalOptIn, Workspace: "acme",
	}, SlackdumpRuntime{Runner: runner, Sanitizer: sanitizer, MaxRecords: maxRecords})
	if err != nil {
		t.Fatalf("NewSlackdumpProvider() error = %v", err)
	}
	return provider
}

func TestProviderContractNormalizesWebAPIAndSlackdumpFixtures(t *testing.T) {
	wantConversations := ConversationPage{Items: []Conversation{{ID: "C1", Name: "general", IsChannel: true}}}
	wantUsers := UserPage{Items: []User{{ID: "U1", Name: "alice", Profile: UserProfile{DisplayName: "Alice"}}}}
	webAPIFactoryCalls := 0
	webAPI, err := NewWebAPIProvider(TransportAPI, func() (WebAPITransport, error) {
		webAPIFactoryCalls++
		return &contractTransport{
			conversations: slack.ConversationPage{Items: []slack.Conversation{{ID: "C1", Name: "general", IsChannel: true}}},
			users:         slack.UserPage{Items: []slack.User{{ID: "U1", Name: "alice", Profile: slack.UserProfile{DisplayName: "Alice"}}}},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if webAPI.Capabilities().Transport != TransportAPI || webAPIFactoryCalls != 0 {
		t.Fatalf("Web API capability discovery initialized transport: calls=%d", webAPIFactoryCalls)
	}

	slackdumpRunner := &fakeSlackdumpRunner{results: []SlackdumpCommandResult{
		{Stdout: []byte("Slackdump v4.4.4\n")},
		{Stdout: []byte("Slackdump v4.4.4\n" + `[{"id":"C1","name":"general","is_channel":true}]`)},
		{Stdout: []byte("Slackdump v4.4.4\n" + `[{"id":"U1","name":"alice","profile":{"display_name":"Alice"}}]`)},
	}}
	slackdump := newSlackdumpTestProvider(t, slackdumpRunner, 10)

	for name, readProvider := range map[string]ReadProvider{"web-api": webAPI, "slackdump": slackdump} {
		t.Run(name, func(t *testing.T) {
			conversations, err := readProvider.ListConversations(context.Background(), ListConversationsOptions{})
			if err != nil {
				t.Fatalf("ListConversations() error = %v", err)
			}
			users, err := readProvider.ListUsers(context.Background(), ListUsersOptions{})
			if err != nil {
				t.Fatalf("ListUsers() error = %v", err)
			}
			if !reflect.DeepEqual(conversations, wantConversations) || !reflect.DeepEqual(users, wantUsers) {
				t.Fatalf("normalized fixtures = conversations:%#v users:%#v", conversations, users)
			}
		})
	}
	if webAPIFactoryCalls != 1 {
		t.Fatalf("Web API lazy transport factory calls = %d, want 1", webAPIFactoryCalls)
	}
}

func TestWebAPIProviderCapabilitiesReflectTransport(t *testing.T) {
	tests := []struct {
		name      string
		transport TransportKind
		readOnly  bool
		supported []Operation
		refused   []Operation
	}{
		{
			name:      "api",
			transport: TransportAPI,
			supported: allReadOperations,
		},
		{
			name:      "browser",
			transport: TransportBrowser,
			readOnly:  true,
			supported: []Operation{
				OperationAuthTest, OperationConversations, OperationConversation,
				OperationHistory, OperationReplies, OperationSearchMessages, OperationUsers,
			},
			refused: []Operation{OperationSearchInfo, OperationSearchContext},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			factoryCalls := 0
			readProvider, err := NewWebAPIProvider(test.transport, func() (WebAPITransport, error) {
				factoryCalls++
				return &contractTransport{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			capabilities := readProvider.Capabilities()
			if capabilities.ReadOnly != test.readOnly {
				t.Fatalf("Capabilities().ReadOnly = %t, want %t", capabilities.ReadOnly, test.readOnly)
			}
			for _, operation := range test.supported {
				if !capabilities.Supports(operation) {
					t.Fatalf("Capabilities() omitted supported operation %q: %#v", operation, capabilities)
				}
			}
			for _, operation := range test.refused {
				if capabilities.Supports(operation) {
					t.Fatalf("Capabilities() advertised unsupported operation %q: %#v", operation, capabilities)
				}
			}
			if factoryCalls != 0 {
				t.Fatalf("Capabilities() initialized transport %d times", factoryCalls)
			}
		})
	}
}

func TestWebAPIProviderNormalizesEveryReadOperation(t *testing.T) {
	message := slack.Message{Type: "message", Subtype: "thread_broadcast", User: "U1", Username: "alice", Text: "hello", Ts: "2.0", ThreadTs: "1.0", ParentUserID: "U2", BotID: "B1", ReplyCount: 2, ReplyUsersCount: 1, LatestReply: "3.0", ReplyUsers: []string{"U2"}}
	transport := &contractTransport{
		auth:         slack.AuthTestResult{URL: "https://acme.slack.com", Team: "Acme", User: "alice", TeamID: "T1", UserID: "U1", BotID: "B1", EnterpriseID: "E1"},
		conversation: slack.Conversation{ID: "C1", Name: "general", NameNormalized: "general", IsChannel: true, IsMember: true, NumMembers: 2, Topic: slack.ConversationField{Value: "topic"}, Purpose: slack.ConversationField{Value: "purpose"}, ContextTeamID: "T1", Creator: "U1", Locale: "en-US", Created: 1, Latest: message},
		history:      slack.ConversationHistoryPage{Items: []slack.Message{message}, HasMore: true, Latest: "2.0", NextCursor: "history-next"},
		replies:      slack.ConversationHistoryPage{Items: []slack.Message{message}, NextCursor: "replies-next"},
		searchInfo:   slack.SearchInfoResult{IsAISearchEnabled: true},
		search: slack.SearchMessagesPage{
			Query: "hello", Total: 1, NextCursor: "search-next",
			Items:      []slack.SearchMessage{{Type: "message", User: "U1", Username: "alice", Text: "hello", Ts: "2.0", Team: "T1", IID: "iid", Permalink: "https://example.test/message", Channel: slack.SearchChannel{ID: "C1", Name: "general", IsPrivate: true, IsMPIM: false, IsShared: true}}},
			Pagination: slack.SearchPagination{Page: 1, PageCount: 2, PerPage: 20, TotalCount: 21},
		},
		searchContext: slack.SearchContextPage{
			Query: "hello", NextCursor: "context-next",
			Messages: []slack.SearchContextMessage{{AuthorName: "Alice", AuthorUserID: "U1", TeamID: "T1", ChannelID: "C1", ChannelName: "general", MessageTS: "2.0", Content: "hello", IsAuthorBot: true, Permalink: "https://example.test/message", Blocks: []any{"block"}, ContextMessages: slack.SearchContextMessageContexts{Before: []slack.SearchContextMessageContext{{Text: "before", UserID: "U2", Ts: "1.0", Blocks: "before-block"}}, After: []slack.SearchContextMessageContext{{Text: "after", UserID: "U3", Ts: "3.0", Blocks: "after-block"}}}}},
			Files:    []slack.SearchContextFile{{UploaderUserID: "U1", AuthorUserID: "U2", AuthorName: "Alice", TeamID: "T1", FileID: "F1", DateCreated: 1, DateUpdated: 2, Title: "file", FileType: "text", Permalink: "https://example.test/file", Content: "contents"}},
			Channels: []slack.SearchContextChannel{{TeamID: "T1", CreatorUserID: "U1", CreatorName: "Alice", DateCreated: 1, DateUpdated: 2, Name: "general", Topic: "topic", Purpose: "purpose", Permalink: "https://example.test/channel"}},
			Users:    []slack.SearchContextUser{{TeamID: "T1", UserID: "U1", Name: "alice", RealName: "Alice", Title: "Engineer", Email: "alice@example.test", Permalink: "https://example.test/user", IsBot: true, IsDeleted: true}},
		},
	}
	readProvider, err := NewWebAPIProvider(TransportBrowser, func() (WebAPITransport, error) { return transport, nil })
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic := readProvider.Diagnose(context.Background()); !diagnostic.Ready || diagnostic.Capabilities.Transport != TransportBrowser {
		t.Fatalf("Diagnose() = %#v", diagnostic)
	}
	auth, err := readProvider.AuthTest(context.Background())
	if err != nil || auth.TeamID != "T1" || auth.EnterpriseID != "E1" {
		t.Fatalf("AuthTest() = %#v, %v", auth, err)
	}
	conversation, err := readProvider.GetConversation(context.Background(), GetConversationOptions{Channel: "C1"})
	if err != nil || conversation.ID != "C1" || conversation.Latest.ThreadTs != "1.0" || conversation.Topic.Value != "topic" {
		t.Fatalf("GetConversation() = %#v, %v", conversation, err)
	}
	history, err := readProvider.GetConversationHistory(context.Background(), GetConversationHistoryOptions{Channel: "C1"})
	if err != nil || !history.HasMore || history.Items[0].ReplyUsers[0] != "U2" {
		t.Fatalf("GetConversationHistory() = %#v, %v", history, err)
	}
	replies, err := readProvider.GetConversationReplies(context.Background(), GetConversationRepliesOptions{Channel: "C1", Ts: "1.0"})
	if err != nil || replies.NextCursor != "replies-next" {
		t.Fatalf("GetConversationReplies() = %#v, %v", replies, err)
	}
	info, err := readProvider.SearchInfo(context.Background())
	if err != nil || !info.IsAISearchEnabled {
		t.Fatalf("SearchInfo() = %#v, %v", info, err)
	}
	search, err := readProvider.SearchMessages(context.Background(), SearchMessagesOptions{Query: "hello"})
	if err != nil || search.Items[0].Channel.ID != "C1" || search.Pagination.TotalCount != 21 {
		t.Fatalf("SearchMessages() = %#v, %v", search, err)
	}
	searchContext, err := readProvider.SearchContext(context.Background(), SearchContextOptions{Query: "hello"})
	if err != nil || searchContext.Messages[0].ContextMessages.After[0].Text != "after" || searchContext.Files[0].FileID != "F1" || searchContext.Channels[0].Name != "general" || searchContext.Users[0].UserID != "U1" {
		t.Fatalf("SearchContext() = %#v, %v", searchContext, err)
	}
}

func TestWebAPIProviderRefusesInvalidOrNilTransportFactories(t *testing.T) {
	if _, err := NewWebAPIProvider("slackdump", func() (WebAPITransport, error) { return &contractTransport{}, nil }); err == nil {
		t.Fatal("invalid transport kind unexpectedly accepted")
	}
	if _, err := NewWebAPIProvider(TransportAPI, nil); err == nil {
		t.Fatal("nil transport factory unexpectedly accepted")
	}
	readProvider, err := NewWebAPIProvider(TransportAPI, func() (WebAPITransport, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readProvider.AuthTest(context.Background()); err == nil {
		t.Fatal("nil transport unexpectedly accepted at production call site WebAPIProvider.AuthTest")
	}
}

func TestSlackdumpProviderCapabilitiesAreStaticAndUnsupportedOperationsHaveNoSideEffects(t *testing.T) {
	runner := &fakeSlackdumpRunner{}
	readProvider := newSlackdumpTestProvider(t, runner, 10)

	capabilities := readProvider.Capabilities()
	if capabilities.Provider != KindSlackdump || capabilities.ProtocolVersion != ProtocolVersion || !capabilities.ExternalAuth {
		t.Fatalf("Capabilities() = %#v", capabilities)
	}
	if capabilities.Supports(OperationHistory) || !capabilities.Supports(OperationConversations) {
		t.Fatalf("Capabilities() operations = %#v", capabilities.Operations)
	}
	_, err := readProvider.GetConversationHistory(context.Background(), GetConversationHistoryOptions{Channel: "C1"})
	if !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("GetConversationHistory() error = %v, want capability refusal", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unsupported operation executed subprocesses: %#v", runner.commands)
	}
}

func TestSlackdumpProviderRunsAbsoluteBoundedArgvChecksVersionAndRedacts(t *testing.T) {
	runner := &fakeSlackdumpRunner{results: []SlackdumpCommandResult{
		{Stdout: []byte("Slackdump v4.4.4 (commit: fixture) built on: now\n")},
		{Stdout: []byte("Slackdump v4.4.4 (commit: fixture) built on: now\n" + `[{"id":"C1","name":"general","is_channel":true,"topic":{"value":"person@example.test xoxb-secret"}},{"id":"C2","name":"private","is_channel":true,"is_private":true}]`)},
	}}
	readProvider := newSlackdumpTestProvider(t, runner, 10)

	page, err := readProvider.ListConversations(context.Background(), ListConversationsOptions{Limit: 1, Types: "public_channel"})
	if err != nil {
		t.Fatalf("ListConversations() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "C1" || page.NextCursor != "" {
		t.Fatalf("ListConversations() = %#v", page)
	}
	if strings.Contains(page.Items[0].Topic.Value, "person@example.test") || strings.Contains(page.Items[0].Topic.Value, "xoxb-secret") {
		t.Fatalf("ListConversations() leaked sensitive data: %#v", page.Items[0])
	}
	if !strings.Contains(page.Items[0].Topic.Value, "<email:") || !strings.Contains(page.Items[0].Topic.Value, "<token:") {
		t.Fatalf("ListConversations() did not use structured redaction: %#v", page.Items[0])
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	for _, command := range runner.commands {
		if !filepath.IsAbs(command.Executable) {
			t.Fatalf("command executable is not absolute: %#v", command)
		}
	}
	wantArgs := []string{"list", "channels", "-format", "json", "-no-json", "-workspace", "acme"}
	if !reflect.DeepEqual(runner.commands[1].Args, wantArgs) {
		t.Fatalf("data argv = %#v, want %#v", runner.commands[1].Args, wantArgs)
	}
}

func TestSlackdumpProviderPaginationAndRecordBounds(t *testing.T) {
	t.Run("pagination", func(t *testing.T) {
		runner := &fakeSlackdumpRunner{results: []SlackdumpCommandResult{
			{Stdout: []byte("Slackdump v4.1.0\n")},
			{Stdout: []byte("Slackdump v4.1.0\n" + `[{"id":"U1"},{"id":"U2"},{"id":"U3"}]`)},
		}}
		readProvider := newSlackdumpTestProvider(t, runner, 3)
		first, err := readProvider.ListUsers(context.Background(), ListUsersOptions{Limit: 2})
		if err != nil || len(first.Items) != 2 || first.NextCursor != "offset:2" {
			t.Fatalf("first page = %#v, %v", first, err)
		}
		second, err := readProvider.ListUsers(context.Background(), ListUsersOptions{Limit: 2, Cursor: first.NextCursor})
		if err != nil || len(second.Items) != 1 || second.Items[0].ID != "U3" || second.NextCursor != "" {
			t.Fatalf("second page = %#v, %v", second, err)
		}
		if len(runner.commands) != 2 {
			t.Fatalf("cached pagination reran Slackdump: %#v", runner.commands)
		}
		if _, err := readProvider.ListUsers(context.Background(), ListUsersOptions{Cursor: "forged:2"}); err == nil {
			t.Fatal("forged cursor unexpectedly accepted")
		}
	})

	t.Run("record bound", func(t *testing.T) {
		runner := &fakeSlackdumpRunner{results: []SlackdumpCommandResult{
			{Stdout: []byte("Slackdump v4.1.0\n")},
			{Stdout: []byte("Slackdump v4.1.0\n" + `[{"id":"U1"},{"id":"U2"}]`)},
		}}
		readProvider := newSlackdumpTestProvider(t, runner, 1)
		_, err := readProvider.ListUsers(context.Background(), ListUsersOptions{})
		if !errors.Is(err, ErrSlackdumpRecordBound) {
			t.Fatalf("ListUsers() error = %v, want record bound", err)
		}
	})
}

func TestSlackdumpProviderRefusesUnsafeConstructionAndExecutionEvidence(t *testing.T) {
	sanitizer, _ := redact.New([]byte("construction-test-salt"))
	for _, test := range []struct {
		name string
		spec SlackdumpSpec
		want error
	}{
		{name: "authorization absent", spec: SlackdumpSpec{Executable: "/bin/echo"}, want: ErrSlackdumpAuthorization},
		{name: "command string", spec: SlackdumpSpec{Executable: "/bin/echo --version", Authorization: SlackdumpExternalOptIn}, want: ErrSlackdumpExecutable},
		{name: "directory", spec: SlackdumpSpec{Executable: t.TempDir(), Authorization: SlackdumpExternalOptIn}, want: ErrSlackdumpExecutable},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSlackdumpProvider(test.spec, SlackdumpRuntime{Sanitizer: sanitizer})
			if !errors.Is(err, test.want) {
				t.Fatalf("NewSlackdumpProvider() error = %v, want %v", err, test.want)
			}
		})
	}

	t.Run("incompatible version", func(t *testing.T) {
		runner := &fakeSlackdumpRunner{results: []SlackdumpCommandResult{{Stdout: []byte("Slackdump v5.0.0\n")}}}
		readProvider := newSlackdumpTestProvider(t, runner, 10)
		_, err := readProvider.ListUsers(context.Background(), ListUsersOptions{})
		if !errors.Is(err, ErrSlackdumpVersion) || len(runner.commands) != 1 {
			t.Fatalf("version refusal = %v, commands=%#v", err, runner.commands)
		}
	})

	t.Run("truncated output", func(t *testing.T) {
		runner := &fakeSlackdumpRunner{results: []SlackdumpCommandResult{{Stdout: []byte("Slackdump v4.2.0\n")}, {Truncated: true}}}
		readProvider := newSlackdumpTestProvider(t, runner, 10)
		_, err := readProvider.ListUsers(context.Background(), ListUsersOptions{})
		if !errors.Is(err, ErrSlackdumpOutputBound) {
			t.Fatalf("output refusal = %v", err)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		runner := &fakeSlackdumpRunner{results: []SlackdumpCommandResult{{Stdout: []byte("Slackdump v4.2.0\n")}, {Stdout: []byte("Slackdump v4.2.0\n[{\"id\":")}}}
		readProvider := newSlackdumpTestProvider(t, runner, 10)
		_, err := readProvider.ListUsers(context.Background(), ListUsersOptions{})
		if !errors.Is(err, ErrSlackdumpMalformedJSON) {
			t.Fatalf("JSON refusal = %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		runner := &blockingSlackdumpRunner{}
		readProvider := newSlackdumpTestProvider(t, runner, 10)
		readProvider.runtime.ProbeTimeout = time.Millisecond
		_, err := readProvider.ListUsers(context.Background(), ListUsersOptions{})
		if !errors.Is(err, ErrSlackdumpTimeout) {
			t.Fatalf("timeout refusal = %v", err)
		}
	})
}

func TestSlackdumpProviderExecutablePlatformPolicy(t *testing.T) {
	sanitizer, err := redact.New([]byte("executable-platform-policy-test-salt"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("path containing spaces", func(t *testing.T) {
		toolDir := filepath.Join(t.TempDir(), "Slackdump Tools")
		if err := os.MkdirAll(toolDir, 0o700); err != nil {
			t.Fatal(err)
		}
		executable := filepath.Join(toolDir, "slackdump")
		if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
		got, err := NewSlackdumpProvider(SlackdumpSpec{
			Executable: executable, Authorization: SlackdumpExternalOptIn,
		}, SlackdumpRuntime{GOOS: "linux", Sanitizer: sanitizer})
		if err != nil {
			t.Fatalf("NewSlackdumpProvider() error = %v", err)
		}
		want, err := filepath.EvalSymlinks(executable)
		if err != nil {
			t.Fatal(err)
		}
		if got.executable != want {
			t.Fatalf("resolved executable = %q, want %q", got.executable, want)
		}
	})

	t.Run("Windows ignores Unix execute bits", func(t *testing.T) {
		executable := filepath.Join(t.TempDir(), "slackdump.exe")
		if err := os.WriteFile(executable, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewSlackdumpProvider(SlackdumpSpec{
			Executable: executable, Authorization: SlackdumpExternalOptIn,
		}, SlackdumpRuntime{GOOS: "windows", Sanitizer: sanitizer}); err != nil {
			t.Fatalf("NewSlackdumpProvider() Windows policy error = %v", err)
		}
	})

	t.Run("Unix requires execute bits", func(t *testing.T) {
		executable := filepath.Join(t.TempDir(), "slackdump")
		if err := os.WriteFile(executable, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := NewSlackdumpProvider(SlackdumpSpec{
			Executable: executable, Authorization: SlackdumpExternalOptIn,
		}, SlackdumpRuntime{GOOS: "linux", Sanitizer: sanitizer})
		if !errors.Is(err, ErrSlackdumpExecutable) {
			t.Fatalf("NewSlackdumpProvider() error = %v, want %v", err, ErrSlackdumpExecutable)
		}
	})

	for _, value := range []string{"slackdump\x00suffix", "slackdump\tsuffix", "slackdump\nsuffix"} {
		t.Run("control character", func(t *testing.T) {
			_, err := NewSlackdumpProvider(SlackdumpSpec{
				Executable: value, Authorization: SlackdumpExternalOptIn,
			}, SlackdumpRuntime{GOOS: "windows", Sanitizer: sanitizer})
			if !errors.Is(err, ErrSlackdumpExecutable) {
				t.Fatalf("NewSlackdumpProvider(%q) error = %v, want %v", value, err, ErrSlackdumpExecutable)
			}
		})
	}
}

func TestSlackdumpProviderDiagnoseConversationAndCapabilitySurface(t *testing.T) {
	t.Run("diagnose ready and conversation lookup", func(t *testing.T) {
		runner := &fakeSlackdumpRunner{results: []SlackdumpCommandResult{
			{Stdout: []byte("Slackdump v4.4.4\n")},
			{Stdout: []byte("Slackdump v4.4.4\n" + `[{"id":"C1","name":"general","is_channel":true}]`)},
		}}
		readProvider := newSlackdumpTestProvider(t, runner, 10)
		diagnostic := readProvider.Diagnose(context.Background())
		if !diagnostic.Ready || diagnostic.Version != "4.4.4" || diagnostic.Executable == "" {
			t.Fatalf("Diagnose() = %#v", diagnostic)
		}
		conversation, err := readProvider.GetConversation(context.Background(), GetConversationOptions{Channel: " c1 "})
		if err != nil || conversation.Name != "general" {
			t.Fatalf("GetConversation() = %#v, %v", conversation, err)
		}
		if _, err := readProvider.GetConversation(context.Background(), GetConversationOptions{Channel: "missing"}); err == nil {
			t.Fatal("missing conversation unexpectedly found")
		}
	})

	t.Run("diagnose sanitized failure", func(t *testing.T) {
		runner := &fakeSlackdumpRunner{results: []SlackdumpCommandResult{{ExitCode: 7, Stderr: []byte("person@example.test xoxb-secret")}}}
		readProvider := newSlackdumpTestProvider(t, runner, 10)
		diagnostic := readProvider.Diagnose(context.Background())
		if diagnostic.Ready || diagnostic.Code != ErrSlackdumpExecution.Error() {
			t.Fatalf("Diagnose() = %#v", diagnostic)
		}
		if strings.Contains(diagnostic.Message, "person@example.test") || strings.Contains(diagnostic.Message, "xoxb-secret") {
			t.Fatalf("Diagnose() leaked stderr: %#v", diagnostic)
		}
	})

	t.Run("every unsupported operation refuses before execution", func(t *testing.T) {
		runner := &fakeSlackdumpRunner{}
		readProvider := newSlackdumpTestProvider(t, runner, 10)
		checks := []func() error{
			func() error { _, err := readProvider.AuthTest(context.Background()); return err },
			func() error {
				_, err := readProvider.GetConversationHistory(context.Background(), GetConversationHistoryOptions{})
				return err
			},
			func() error {
				_, err := readProvider.GetConversationReplies(context.Background(), GetConversationRepliesOptions{})
				return err
			},
			func() error {
				_, err := readProvider.SearchContext(context.Background(), SearchContextOptions{})
				return err
			},
			func() error { _, err := readProvider.SearchInfo(context.Background()); return err },
			func() error {
				_, err := readProvider.SearchMessages(context.Background(), SearchMessagesOptions{})
				return err
			},
		}
		for idx, check := range checks {
			if err := check(); !errors.Is(err, ErrCapabilityUnsupported) {
				t.Fatalf("unsupported operation %d error = %v", idx, err)
			}
		}
		if len(runner.commands) != 0 {
			t.Fatalf("unsupported operations executed subprocesses: %#v", runner.commands)
		}
	})
}

func TestProviderHelpersExposeStableErrorsAndNormalization(t *testing.T) {
	if got := NormalizeWorkspace("  ACME  "); got != "acme" {
		t.Fatalf("NormalizeWorkspace() = %q", got)
	}
	readProvider := newSlackdumpTestProvider(t, &fakeSlackdumpRunner{}, 10)
	if err := Unsupported(readProvider, OperationConversations); err == nil || errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("declared-but-unimplemented error = %v", err)
	}
	for _, test := range []struct {
		err  error
		code string
	}{
		{ErrSlackdumpAuthorization, ErrSlackdumpAuthorization.Error()},
		{ErrSlackdumpExecutable, ErrSlackdumpExecutable.Error()},
		{ErrSlackdumpVersion, ErrSlackdumpVersion.Error()},
		{ErrSlackdumpTimeout, ErrSlackdumpTimeout.Error()},
		{ErrSlackdumpOutputBound, ErrSlackdumpOutputBound.Error()},
		{ErrSlackdumpRecordBound, ErrSlackdumpRecordBound.Error()},
		{ErrSlackdumpMalformedJSON, ErrSlackdumpMalformedJSON.Error()},
		{ErrSlackdumpExecution, ErrSlackdumpExecution.Error()},
		{errors.New("unknown"), "provider_diagnostic_failed"},
	} {
		if got := slackdumpErrorCode(test.err); got != test.code {
			t.Fatalf("slackdumpErrorCode(%v) = %q, want %q", test.err, got, test.code)
		}
	}
	var calls int
	factory := FactoryFunc(func(spec ProviderSpec) (ReadProvider, error) {
		calls++
		if spec.Kind != KindSlackdump {
			t.Fatalf("FactoryFunc spec = %#v", spec)
		}
		return readProvider, nil
	})
	if got, err := factory.New(ProviderSpec{Kind: KindSlackdump}); err != nil || got != readProvider || calls != 1 {
		t.Fatalf("FactoryFunc.New() = %#v, %v, calls=%d", got, err, calls)
	}
}

func TestConversationTypeFilteringCoversEverySupportedKind(t *testing.T) {
	for _, test := range []struct {
		name  string
		item  Conversation
		types string
		want  bool
	}{
		{"default", Conversation{}, "", true},
		{"public", Conversation{IsChannel: true}, "public_channel", true},
		{"private", Conversation{IsChannel: true, IsPrivate: true}, "private_channel", true},
		{"im", Conversation{IsIM: true}, "im", true},
		{"mpim", Conversation{IsMPIM: true}, "mpim", true},
		{"unknown", Conversation{IsChannel: true}, "unsupported", false},
		{"private excludes mpim", Conversation{IsChannel: true, IsPrivate: true, IsMPIM: true}, "private_channel", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := conversationTypeAllowed(test.item, test.types); got != test.want {
				t.Fatalf("conversationTypeAllowed() = %v, want %v", got, test.want)
			}
		})
	}
}

type blockingSlackdumpRunner struct{}

func (*blockingSlackdumpRunner) Run(ctx context.Context, _ SlackdumpCommand, _ int64) (SlackdumpCommandResult, error) {
	<-ctx.Done()
	return SlackdumpCommandResult{}, ctx.Err()
}
