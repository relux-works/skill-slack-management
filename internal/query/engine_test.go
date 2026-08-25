package query

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relux-works/skill-slack-management/internal/attachments"
	"github.com/relux-works/skill-slack-management/internal/provider"
)

type fakeSlackClient struct {
	capabilities          provider.Capabilities
	authResult            provider.AuthTestResult
	conversationInfo      provider.Conversation
	conversationResult    provider.ConversationPage
	historyResult         provider.ConversationHistoryPage
	repliesResult         provider.ConversationHistoryPage
	searchInfoResult      provider.SearchInfoResult
	searchContextResult   provider.SearchContextPage
	searchResult          provider.SearchMessagesPage
	userResult            provider.UserPage
	authCalls             int
	conversationInfoCalls int
	conversationCalls     int
	historyCalls          int
	repliesCalls          int
	searchInfoCalls       int
	searchContextCalls    int
	searchCalls           int
	userCalls             int
}

func (f *fakeSlackClient) Capabilities() provider.Capabilities {
	if f.capabilities.Provider != "" {
		return f.capabilities
	}
	return provider.NewCapabilities(provider.KindWebAPI,
		provider.OperationAuthTest,
		provider.OperationSearchInfo,
		provider.OperationConversations,
		provider.OperationConversation,
		provider.OperationHistory,
		provider.OperationReplies,
		provider.OperationSearchMessages,
		provider.OperationSearchContext,
		provider.OperationUsers,
	)
}

func TestEngineProviderCapabilitiesAndUnsupportedGatePrecedeProviderSideEffects(t *testing.T) {
	client := &fakeSlackClient{capabilities: provider.NewCapabilities(provider.KindSlackdump,
		provider.OperationConversations,
		provider.OperationConversation,
		provider.OperationUsers,
	)}
	providerCalls := 0
	engine := NewEngine(Runtime{ReadProvider: func() (provider.ReadProvider, error) {
		providerCalls++
		return client, nil
	}})

	results, err := engine.Execute(context.Background(), "provider_capabilities()")
	if err != nil {
		t.Fatalf("Execute(provider_capabilities) error = %v", err)
	}
	if len(results) != 1 || results[0].Object["provider"] != string(provider.KindSlackdump) {
		t.Fatalf("provider capabilities = %#v", results)
	}
	_, err = engine.Execute(context.Background(), "history(C0123456789)")
	if !errors.Is(err, provider.ErrCapabilityUnsupported) {
		t.Fatalf("Execute(history) error = %v, want stable capability refusal", err)
	}
	if client.historyCalls != 0 || client.conversationCalls != 0 || client.conversationInfoCalls != 0 {
		t.Fatalf("Engine.executeOne reached provider side effects: %#v", client)
	}
	if providerCalls != 2 {
		t.Fatalf("ReadProvider factory calls = %d, want one per Execute call", providerCalls)
	}
}

func (f *fakeSlackClient) AuthTest(context.Context) (provider.AuthTestResult, error) {
	f.authCalls++
	return f.authResult, nil
}

func (f *fakeSlackClient) GetConversation(context.Context, provider.GetConversationOptions) (provider.Conversation, error) {
	f.conversationInfoCalls++
	return f.conversationInfo, nil
}

func (f *fakeSlackClient) GetConversationHistory(context.Context, provider.GetConversationHistoryOptions) (provider.ConversationHistoryPage, error) {
	f.historyCalls++
	return f.historyResult, nil
}

func (f *fakeSlackClient) GetConversationReplies(context.Context, provider.GetConversationRepliesOptions) (provider.ConversationHistoryPage, error) {
	f.repliesCalls++
	return f.repliesResult, nil
}

func (f *fakeSlackClient) ListConversations(context.Context, provider.ListConversationsOptions) (provider.ConversationPage, error) {
	f.conversationCalls++
	return f.conversationResult, nil
}

func (f *fakeSlackClient) ListUsers(context.Context, provider.ListUsersOptions) (provider.UserPage, error) {
	f.userCalls++
	return f.userResult, nil
}

func (f *fakeSlackClient) SearchMessages(context.Context, provider.SearchMessagesOptions) (provider.SearchMessagesPage, error) {
	f.searchCalls++
	return f.searchResult, nil
}

func (f *fakeSlackClient) SearchInfo(context.Context) (provider.SearchInfoResult, error) {
	f.searchInfoCalls++
	return f.searchInfoResult, nil
}

func (f *fakeSlackClient) SearchContext(context.Context, provider.SearchContextOptions) (provider.SearchContextPage, error) {
	f.searchContextCalls++
	return f.searchContextResult, nil
}

func TestEngineSchema(t *testing.T) {
	engine := NewEngine(Runtime{})

	results, err := engine.Execute(context.Background(), "schema()")
	if err != nil {
		t.Fatalf("Execute(schema) error = %v", err)
	}
	if len(results) != 1 || results[0].Kind != ResultKindObject {
		t.Fatalf("results = %#v", results)
	}
}

func TestInvalidFieldProjectionFailsBeforeProviderInitialization(t *testing.T) {
	providerCalls := 0
	engine := NewEngine(Runtime{ReadProvider: func() (provider.ReadProvider, error) {
		providerCalls++
		return &fakeSlackClient{}, nil
	}})

	for _, raw := range []string{
		`schema() { operations }`,
		`provider_capabilities() { provider }`,
		`attachments() { bogus }`,
		`attachment(att-1) { bogus }`,
		`auth_test() { team_id user_i }`,
		`search_info() { enabled }`,
		`conversations(limit=1) { id channel_nam }`,
		`conversation(C0123456789) { id channel_nam }`,
		`history(C0123456789, limit=1) { ts tex }`,
		`replies(C0123456789, ts="1710000000.000100") { ts tex }`,
		`search_messages("incident") { ts tex }`,
		`search_context("incident") { content conten }`,
		`users(limit=1) { id emai }`,
	} {
		_, err := engine.Execute(context.Background(), raw)
		if err == nil || !strings.Contains(err.Error(), "field") {
			t.Errorf("Execute(%q) error = %v, want field projection refusal", raw, err)
		}
	}
	if providerCalls != 0 {
		t.Fatalf("invalid projections initialized provider %d times", providerCalls)
	}

	_, err := engine.Execute(context.Background(), `auth_test() { default }; users(limit=1) { id emai }`)
	if err == nil || !strings.Contains(err.Error(), `unsupported field "emai" for user`) {
		t.Fatalf("invalid batch projection error = %v", err)
	}
	if providerCalls != 0 {
		t.Fatalf("invalid batch projection executed earlier request; provider calls = %d", providerCalls)
	}
}

func TestEngineAttachmentsAndAttachment(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "files", "trace.log")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	manifestPath := filepath.Join(tempDir, "agents-attachments-manifest.json")
	manifest := `{"attachments":[{"id":"att-1","name":"trace.log","mime_type":"text/plain","size_bytes":5,"local_path":"files/trace.log","source":"ticket-123"}]}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	publishOne := func(item attachments.Attachment) (attachments.Attachment, error) {
		alias, err := attachments.PublishAlias(item.LocalPath, attachments.AliasRuntime{WorkingDir: tempDir})
		if err != nil {
			return attachments.Attachment{}, err
		}
		item.LocalPath = alias.LocalPath
		return item, nil
	}
	engine := NewEngine(Runtime{
		ManifestPath:      manifestPath,
		PublishAttachment: publishOne,
		PublishAttachments: func(items []attachments.Attachment) ([]attachments.Attachment, error) {
			published := make([]attachments.Attachment, 0, len(items))
			for _, item := range items {
				item, err := publishOne(item)
				if err != nil {
					return nil, err
				}
				published = append(published, item)
			}
			return published, nil
		},
	})

	results, err := engine.Execute(context.Background(), `attachments() { overview }; attachment(att-1) { full }`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if got := results[0].Items[0]["source"]; got != "ticket-123" {
		t.Fatalf("attachments source = %#v, want ticket-123", got)
	}
	if got := results[1].Object["path_exists"]; got != true {
		t.Fatalf("attachment path_exists = %#v, want true", got)
	}
	if got := results[1].Object["local_path"]; got == filePath || got == "files/trace.log" {
		t.Fatalf("attachment local_path = %#v, want opaque alias", got)
	}

	compact, err := RenderCompact(results)
	if err != nil {
		t.Fatalf("RenderCompact() error = %v", err)
	}
	if compact == "" {
		t.Fatal("compact output should not be empty")
	}
}

func TestEngineLiveSlackOperations(t *testing.T) {
	fake := &fakeSlackClient{
		authResult: provider.AuthTestResult{
			Team:   "Acme",
			TeamID: "T123",
			User:   "agent",
			UserID: "U123",
			BotID:  "B123",
		},
		conversationInfo: provider.Conversation{
			ID:             "C123",
			Name:           "general",
			NameNormalized: "general",
			IsChannel:      true,
			IsPrivate:      false,
			IsMember:       true,
			NumMembers:     42,
			Topic:          provider.ConversationField{Value: "Company-wide updates"},
			Purpose:        provider.ConversationField{Value: "General coordination"},
			Creator:        "U999",
			ContextTeamID:  "T123",
			Latest:         provider.Message{User: "U123", Text: "latest hello", Ts: "1710000001.000200"},
		},
		conversationResult: provider.ConversationPage{
			Items: []provider.Conversation{
				{ID: "C123", Name: "general", NameNormalized: "general", IsPrivate: false, IsMember: true, NumMembers: 42},
			},
			NextCursor: "cursor-2",
		},
		historyResult: provider.ConversationHistoryPage{
			Items: []provider.Message{
				{Type: "message", User: "U123", Text: "hello world", Ts: "1710000000.000100", ThreadTs: "1710000000.000100", ReplyCount: 2},
			},
			HasMore:    true,
			Latest:     "1710000000.000100",
			NextCursor: "cursor-history",
		},
		repliesResult: provider.ConversationHistoryPage{
			Items: []provider.Message{
				{Type: "message", User: "U123", Text: "root", Ts: "1710000000.000100", ThreadTs: "1710000000.000100", ReplyCount: 2, ReplyUsers: []string{"U234"}},
				{Type: "message", User: "U234", Text: "reply", Ts: "1710000001.000200", ThreadTs: "1710000000.000100", ParentUserID: "U123"},
			},
			HasMore:    false,
			Latest:     "1710000001.000200",
			NextCursor: "cursor-replies",
		},
		searchInfoResult: provider.SearchInfoResult{
			IsAISearchEnabled: true,
		},
		searchContextResult: provider.SearchContextPage{
			Query: "What is project gizmo?",
			Messages: []provider.SearchContextMessage{
				{AuthorName: "Jennifer", AuthorUserID: "U123", TeamID: "T123", ChannelID: "C123", ChannelName: "proj-gizmo", MessageTS: "1710000003.000400", Content: "Gizmo update", Permalink: "https://acme.provider.com/archives/C123/p1710000003000400", ContextMessages: provider.SearchContextMessageContexts{Before: []provider.SearchContextMessageContext{{Text: "before", UserID: "U999", Ts: "1710000002.000300"}}, After: []provider.SearchContextMessageContext{{Text: "after", UserID: "U888", Ts: "1710000004.000500"}}}},
			},
			Files: []provider.SearchContextFile{
				{AuthorName: "Jennifer", AuthorUserID: "U123", UploaderUserID: "U123", TeamID: "T123", FileID: "F123", Title: "Project tracker", FileType: "application/pdf", Content: "tracker", Permalink: "https://acme.provider.com/files/F123"},
			},
			Channels: []provider.SearchContextChannel{
				{TeamID: "T123", CreatorUserID: "U123", CreatorName: "Jennifer", Name: "project-gizmo", Topic: "Launch date", Purpose: "Ship it", Permalink: "https://acme.provider.com/archives/C999"},
			},
			Users: []provider.SearchContextUser{
				{TeamID: "T123", UserID: "U777", Name: "jane", RealName: "Jane Doe", Title: "PM", Email: "jane@example.com", Permalink: "https://acme.provider.com/team/U777"},
			},
			NextCursor: "cursor-rts",
		},
		searchResult: provider.SearchMessagesPage{
			Query: "error in:#alerts",
			Items: []provider.SearchMessage{
				{Type: "message", User: "U123", Username: "agent", Text: "error happened", Ts: "1710000002.000300", Team: "T123", IID: "iid-1", Permalink: "https://acme.provider.com/archives/C234/p1710000002000300", Channel: provider.SearchChannel{ID: "C234", Name: "alerts"}},
			},
			Total:      91,
			NextCursor: "cursor-search",
			Pagination: provider.SearchPagination{Page: 2, PageCount: 5, PerPage: 20, TotalCount: 91},
		},
		userResult: provider.UserPage{
			Items: []provider.User{
				{ID: "U123", Name: "agent", RealName: "Agent Smith", IsBot: false, Profile: provider.UserProfile{DisplayName: "agent", Email: "agent@example.com"}},
			},
			NextCursor: "cursor-users",
		},
	}

	engine := NewEngine(Runtime{
		ReadProvider: func() (provider.ReadProvider, error) {
			return fake, nil
		},
	})

	results, err := engine.Execute(context.Background(), `auth_test() { default }; search_info() { default }; conversations(limit=1) { overview }; conversation(general) { full }; history(general, limit=1) { overview }; replies(general, ts="1710000000.000100") { full }; search_messages("error in:#alerts", count=20, page=2) { overview }; search_context("What is project gizmo?", content_types="messages,files,channels,users", include_context_messages=true) { overview }; users(limit=1) { overview }`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(results) != 9 {
		t.Fatalf("len(results) = %d, want 9", len(results))
	}
	if fake.authCalls != 1 || fake.searchInfoCalls != 1 || fake.conversationCalls != 4 || fake.conversationInfoCalls != 1 || fake.historyCalls != 1 || fake.repliesCalls != 1 || fake.searchCalls != 1 || fake.searchContextCalls != 1 || fake.userCalls != 1 {
		t.Fatalf("slack calls = auth:%d search_info:%d list:%d info:%d history:%d replies:%d legacy_search:%d rts:%d users:%d", fake.authCalls, fake.searchInfoCalls, fake.conversationCalls, fake.conversationInfoCalls, fake.historyCalls, fake.repliesCalls, fake.searchCalls, fake.searchContextCalls, fake.userCalls)
	}
	if got := results[1].Object["is_ai_search_enabled"]; got != true {
		t.Fatalf("search info = %#v", results[1].Object)
	}
	if got := results[2].Page["next_cursor"]; got != "cursor-2" {
		t.Fatalf("conversation page = %#v", results[2].Page)
	}
	if got := results[3].Object["topic"]; got != "Company-wide updates" {
		t.Fatalf("conversation topic = %#v", got)
	}
	if got := results[4].Page["conversation_id"]; got != "C123" {
		t.Fatalf("history page = %#v", results[4].Page)
	}
	if got := results[4].Items[0]["reply_count"]; got != 2 {
		t.Fatalf("history item = %#v", results[4].Items[0])
	}
	if got := results[5].Page["thread_ts"]; got != "1710000000.000100" {
		t.Fatalf("replies page = %#v", results[5].Page)
	}
	if got := results[5].Items[1]["parent_user_id"]; got != "U123" {
		t.Fatalf("replies item = %#v", results[5].Items[1])
	}
	if got := results[6].Page["legacy"]; got != true {
		t.Fatalf("search page = %#v", results[6].Page)
	}
	if got := results[6].Items[0]["channel_name"]; got != "alerts" {
		t.Fatalf("search item = %#v", results[6].Items[0])
	}
	if got := results[7].Page["real_time_search"]; got != true {
		t.Fatalf("search context page = %#v", results[7].Page)
	}
	if got := results[7].Items[0]["content_type"]; got != "message" {
		t.Fatalf("search context first item = %#v", results[7].Items[0])
	}
	if got := results[7].Items[3]["name"]; got != "jane" {
		t.Fatalf("search context user item = %#v", results[7].Items[3])
	}
	if _, ok := results[8].Items[0]["email"]; ok {
		t.Fatalf("users overview unexpectedly exposed email: %#v", results[8].Items[0])
	}

	compact, err := RenderCompact(results)
	if err != nil {
		t.Fatalf("RenderCompact() error = %v", err)
	}
	if compact == "" {
		t.Fatal("compact output should not be empty")
	}
}

func TestResolveConversationRefSupportsHashPrefixAndID(t *testing.T) {
	if got := conversationRequestRef(Request{Positional: "#general"}); got != "general" {
		t.Fatalf("conversationRequestRef(#general) = %q, want general", got)
	}
	if !looksLikeConversationID("c123abc") {
		t.Fatal("looksLikeConversationID(c123abc) = false, want true")
	}
	if looksLikeConversationID("general") {
		t.Fatal("looksLikeConversationID(general) = true, want false")
	}
}

func TestRepliesRequiresThreadTS(t *testing.T) {
	fake := &fakeSlackClient{}
	engine := NewEngine(Runtime{
		ReadProvider: func() (provider.ReadProvider, error) {
			return fake, nil
		},
	})

	_, err := engine.Execute(context.Background(), `replies(general) { overview }`)
	if err == nil {
		t.Fatal("Execute(replies without ts) error = nil, want error")
	}
	if got := err.Error(); got != "replies() requires ts=..." {
		t.Fatalf("error = %q", got)
	}
}

func TestEngineAttachmentsReturnsEmptyListWithoutManifest(t *testing.T) {
	engine := NewEngine(Runtime{
		Getenv: func(string) string { return "" },
	})

	results, err := engine.Execute(context.Background(), `attachments() { overview }`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(results) != 1 || len(results[0].Items) != 0 {
		t.Fatalf("results = %#v", results)
	}
}
