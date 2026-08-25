package slack

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthTestUsesBearerToken(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth.test" {
			t.Fatalf("path = %q, want /auth.test", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"url":"https://acme.slack.com/","team":"Acme","user":"agent","team_id":"T123","user_id":"U123","bot_id":"B123"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.AuthTest(context.Background())
	if err != nil {
		t.Fatalf("AuthTest() error = %v", err)
	}
	if authHeader != "Bearer xoxb-test" {
		t.Fatalf("Authorization = %q, want Bearer xoxb-test", authHeader)
	}
	if got.TeamID != "T123" || got.UserID != "U123" || got.BotID != "B123" {
		t.Fatalf("AuthTest() = %#v", got)
	}
}

func TestWriteRateLimitRetriesOnceThenSucceeds(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error":"ratelimited"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1710000005.000600","message":{"text":"hello"}}`))
	}))
	defer server.Close()
	client, err := NewClientForWorkspace(server.URL, "xoxb-test", server.Client(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	sleeps := 0
	client.writeTransport.(*apiJSONTransport).sleep = func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}
	if _, err := client.PostMessage(context.Background(), PostMessageOptions{Channel: "C123", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || sleeps != 1 {
		t.Fatalf("calls=%d sleeps=%d, want 2/1", calls, sleeps)
	}
}

func TestWriteRateLimitExhaustionIsTypedAndBounded(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, _ := NewClientForWorkspace(server.URL, "xoxb-test", server.Client(), "acme")
	client.writeTransport.(*apiJSONTransport).sleep = func(context.Context, time.Duration) error { return nil }
	_, err := client.DeleteMessage(context.Background(), DeleteMessageOptions{Channel: "C123", TS: "1710000005.000600"})
	var rateErr *RateLimitError
	if !errors.As(err, &rateErr) || !rateErr.Exhausted || rateErr.Attempt != 2 || rateErr.RetryAfterSeconds != 0 || rateErr.Method != "chat.delete" || rateErr.Workspace != "acme" {
		t.Fatalf("rate error = %#v (%v)", rateErr, err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestRateLimitRejectsInvalidRetryAfterWithoutRetry(t *testing.T) {
	for _, header := range []string{"", "later", "-1", "999999999999999999999"} {
		t.Run(header, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if header != "" {
					w.Header().Set("Retry-After", header)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()
			client, _ := NewClientForWorkspace(server.URL, "xoxb-test", server.Client(), "acme")
			sleeps := 0
			client.writeTransport.(*apiJSONTransport).sleep = func(context.Context, time.Duration) error { sleeps++; return nil }
			_, err := client.PostMessage(context.Background(), PostMessageOptions{Channel: "C123", Text: "hello"})
			var decodeErr *RateLimitDecodeError
			if !errors.As(err, &decodeErr) || calls != 1 || sleeps != 0 {
				t.Fatalf("error=%v calls=%d sleeps=%d", err, calls, sleeps)
			}
		})
	}
}

func TestRateLimitRejectsRetryAfterAboveWaitBoundWithoutSleeping(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "61")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client, _ := NewClientForWorkspace(server.URL, "xoxb-test", server.Client(), "acme")
	sleeps := 0
	client.writeTransport.(*apiJSONTransport).sleep = func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}
	_, err := client.PostMessage(context.Background(), PostMessageOptions{Channel: "C123", Text: "hello"})
	var waitErr *RateLimitWaitError
	if !errors.As(err, &waitErr) {
		t.Fatalf("error = %v, want RateLimitWaitError", err)
	}
	if waitErr.Method != "chat.postMessage" || waitErr.Workspace != "acme" || waitErr.Attempt != 1 || waitErr.RetryAfterSeconds != 61 || waitErr.MaxRetryAfterSeconds != 60 {
		t.Fatalf("wait error = %#v", waitErr)
	}
	if calls != 1 || sleeps != 0 {
		t.Fatalf("calls=%d sleeps=%d, want 1/0", calls, sleeps)
	}
}

func TestRateLimitWaitIsContextCancellableWithoutAnotherRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, _ := NewClientForWorkspace(server.URL, "xoxb-test", server.Client(), "acme")
	ctx, cancel := context.WithCancel(context.Background())
	client.writeTransport.(*apiJSONTransport).sleep = func(ctx context.Context, duration time.Duration) error {
		cancel()
		return sleepWithContext(ctx, duration)
	}
	_, err := client.PostMessage(ctx, PostMessageOptions{Channel: "C123", Text: "hello"})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestReadRateLimitAllowsThreeAttemptsWithoutBlockingAnotherMethod(t *testing.T) {
	authCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth.test":
			authCalls++
			if authCalls < 3 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"team_id":"T123"}`))
		case "/conversations.list":
			_, _ = w.Write([]byte(`{"ok":true,"channels":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := NewClientForWorkspace(server.URL, "xoxb-test", server.Client(), "acme")
	enteredSleep := make(chan struct{}, 1)
	releaseSleep := make(chan struct{})
	client.readTransport.(*apiJSONTransport).sleep = func(ctx context.Context, _ time.Duration) error {
		enteredSleep <- struct{}{}
		select {
		case <-releaseSleep:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	authDone := make(chan error, 1)
	go func() {
		_, err := client.AuthTest(context.Background())
		authDone <- err
	}()
	<-enteredSleep
	if _, err := client.ListConversations(context.Background(), ListConversationsOptions{Limit: 1}); err != nil {
		t.Fatalf("unrelated method blocked or failed: %v", err)
	}
	close(releaseSleep)
	if err := <-authDone; err != nil {
		t.Fatal(err)
	}
	if authCalls != 3 {
		t.Fatalf("auth calls = %d, want 3", authCalls)
	}
}

func TestWriteDoesNotRetryServerOrSlackErrors(t *testing.T) {
	for _, body := range []string{`{"ok":false,"error":"server_error"}`, `not-json`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client, _ := NewClientForWorkspace(server.URL, "xoxb-test", server.Client(), "acme")
			_, _ = client.PostMessage(context.Background(), PostMessageOptions{Channel: "C123", Text: "hello"})
			if calls != 1 {
				t.Fatalf("calls = %d, want 1", calls)
			}
		})
	}
}

func TestListConversationsPassesQueryParams(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.list" {
			t.Fatalf("path = %q, want /conversations.list", r.URL.Path)
		}
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channels":[{"id":"C123","name":"general","is_channel":true,"is_private":false,"is_member":true,"is_archived":false,"num_members":42}],"response_metadata":{"next_cursor":"cursor-2"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.ListConversations(context.Background(), ListConversationsOptions{
		Cursor:          "cursor-1",
		Limit:           10,
		Types:           "public_channel,private_channel",
		ExcludeArchived: true,
	})
	if err != nil {
		t.Fatalf("ListConversations() error = %v", err)
	}
	if query == "" || got.NextCursor != "cursor-2" || len(got.Items) != 1 {
		t.Fatalf("query=%q result=%#v", query, got)
	}
}

func TestListUsersReturnsMembersAndCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users.list" {
			t.Fatalf("path = %q, want /users.list", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"members":[{"id":"U123","team_id":"T123","name":"agent","real_name":"Agent Smith","deleted":false,"is_bot":false,"is_app_user":false,"profile":{"display_name":"agent","real_name":"Agent Smith","email":"agent@example.com"}}],"response_metadata":{"next_cursor":"cursor-users"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.ListUsers(context.Background(), ListUsersOptions{
		Cursor:        "cursor-1",
		Limit:         20,
		IncludeLocale: true,
	})
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Profile.Email != "agent@example.com" || got.NextCursor != "cursor-users" {
		t.Fatalf("ListUsers() = %#v", got)
	}
}

func TestGetConversationUsesChannelParam(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.info" {
			t.Fatalf("path = %q, want /conversations.info", r.URL.Path)
		}
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":{"id":"C123","name":"general","name_normalized":"general","is_channel":true,"is_private":false,"is_member":true,"is_archived":false,"num_members":42,"creator":"U999","context_team_id":"T123","topic":{"value":"Topic"},"purpose":{"value":"Purpose"}}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.GetConversation(context.Background(), GetConversationOptions{
		Channel:           "C123",
		IncludeLocale:     true,
		IncludeNumMembers: true,
	})
	if err != nil {
		t.Fatalf("GetConversation() error = %v", err)
	}
	if query == "" || got.ID != "C123" || got.Creator != "U999" || got.NumMembers != 42 {
		t.Fatalf("query=%q result=%#v", query, got)
	}
}

func TestGetConversationHistoryPassesQueryParams(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.history" {
			t.Fatalf("path = %q, want /conversations.history", r.URL.Path)
		}
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U123","text":"hello","ts":"1710000000.000100","thread_ts":"1710000000.000100","reply_count":2,"reply_users_count":1,"latest_reply":"1710000001.000200"}],"has_more":true,"latest":"1710000000.000100","response_metadata":{"next_cursor":"cursor-history"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.GetConversationHistory(context.Background(), GetConversationHistoryOptions{
		Channel:            "C123",
		Cursor:             "cursor-1",
		Limit:              15,
		Oldest:             "1700000000.000000",
		Latest:             "1800000000.000000",
		Inclusive:          true,
		IncludeAllMetadata: true,
	})
	if err != nil {
		t.Fatalf("GetConversationHistory() error = %v", err)
	}
	if query == "" || len(got.Items) != 1 || !got.HasMore || got.NextCursor != "cursor-history" {
		t.Fatalf("query=%q result=%#v", query, got)
	}
	if got.Items[0].ReplyCount != 2 || got.Items[0].LatestReply != "1710000001.000200" {
		t.Fatalf("history item = %#v", got.Items[0])
	}
}

func TestGetConversationRepliesPassesQueryParams(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			t.Fatalf("path = %q, want /conversations.replies", r.URL.Path)
		}
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U123","text":"root","ts":"1710000000.000100","thread_ts":"1710000000.000100","reply_count":2,"reply_users":["U234"]},{"type":"message","user":"U234","text":"reply","ts":"1710000001.000200","thread_ts":"1710000000.000100","parent_user_id":"U123"}],"has_more":true,"latest":"1710000001.000200","response_metadata":{"next_cursor":"cursor-replies"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.GetConversationReplies(context.Background(), GetConversationRepliesOptions{
		Channel:            "C123",
		Ts:                 "1710000000.000100",
		Cursor:             "cursor-1",
		Limit:              10,
		Oldest:             "1700000000.000000",
		Latest:             "1800000000.000000",
		Inclusive:          true,
		IncludeAllMetadata: true,
	})
	if err != nil {
		t.Fatalf("GetConversationReplies() error = %v", err)
	}
	if query == "" || len(got.Items) != 2 || !got.HasMore || got.NextCursor != "cursor-replies" {
		t.Fatalf("query=%q result=%#v", query, got)
	}
	if got.Items[0].ReplyUsers[0] != "U234" || got.Items[1].ParentUserID != "U123" {
		t.Fatalf("replies items = %#v", got.Items)
	}
}

func TestSearchMessagesPassesQueryParams(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search.messages" {
			t.Fatalf("path = %q, want /search.messages", r.URL.Path)
		}
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"query":"error in:#alerts","messages":{"matches":[{"type":"message","user":"U123","username":"agent","text":"error happened","ts":"1710000000.000100","team":"T123","iid":"iid-1","permalink":"https://acme.slack.com/archives/C123/p1710000000000100","channel":{"id":"C123","name":"alerts","is_private":false}}],"pagination":{"page":2,"page_count":5,"per_page":20,"total_count":91},"total":91},"response_metadata":{"next_cursor":"cursor-search"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxp-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.SearchMessages(context.Background(), SearchMessagesOptions{
		Query:     "error in:#alerts",
		Count:     20,
		Page:      2,
		Cursor:    "cursor-1",
		Sort:      "timestamp",
		SortDir:   "asc",
		Highlight: true,
		TeamID:    "T123",
	})
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if query == "" || len(got.Items) != 1 || got.Total != 91 || got.NextCursor != "cursor-search" {
		t.Fatalf("query=%q result=%#v", query, got)
	}
	if got.Items[0].Channel.Name != "alerts" || got.Pagination.Page != 2 || got.Pagination.TotalCount != 91 {
		t.Fatalf("search page = %#v", got)
	}
}

func TestSearchInfoReturnsCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assistant.search.info" {
			t.Fatalf("path = %q, want /assistant.search.info", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"is_ai_search_enabled":true}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.SearchInfo(context.Background())
	if err != nil {
		t.Fatalf("SearchInfo() error = %v", err)
	}
	if !got.IsAISearchEnabled {
		t.Fatalf("SearchInfo() = %#v", got)
	}
}

func TestSearchContextPostsFormBody(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assistant.search.context" {
			t.Fatalf("path = %q, want /assistant.search.context", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"results":{"messages":[{"author_name":"Jennifer","author_user_id":"U123","team_id":"T123","channel_id":"C123","channel_name":"proj-gizmo","message_ts":"1710000000.000100","content":"Gizmo update","is_author_bot":false,"permalink":"https://acme.slack.com/archives/C123/p1710000000000100","context_messages":{"before":[{"text":"before","user_id":"U999","ts":"1709999999.000000"}],"after":[{"text":"after","user_id":"U888","ts":"1710000001.000000"}]}}],"files":[{"uploader_user_id":"U123","author_user_id":"U123","author_name":"Jennifer","team_id":"T123","file_id":"F123","date_created":1710000000,"date_updated":1710000001,"title":"Project tracker","file_type":"application/pdf","permalink":"https://acme.slack.com/files/F123","content":"tracker"}],"channels":[{"team_id":"T123","creator_user_id":"U123","creator_name":"Jennifer","date_created":1710000000,"date_updated":1710000001,"name":"project-gizmo","topic":"Launch date","purpose":"Ship it","permalink":"https://acme.slack.com/archives/C999"}],"users":[{"team_id":"T123","user_id":"U777","name":"jane","real_name":"Jane Doe","title":"PM","email":"jane@example.com","permalink":"https://acme.slack.com/team/U777"}]},"response_metadata":{"next_cursor":"cursor-rts"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxp-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.SearchContext(context.Background(), SearchContextOptions{
		Query:                  "What is project gizmo?",
		ActionToken:            "at-1",
		ChannelTypes:           "public_channel,private_channel",
		ContentTypes:           "messages,files,channels,users",
		IncludeContextMessages: true,
		Limit:                  10,
		Sort:                   "timestamp",
		SortDir:                "desc",
		Highlight:              true,
	})
	if err != nil {
		t.Fatalf("SearchContext() error = %v", err)
	}
	if !strings.Contains(body, "query=What+is+project+gizmo%3F") || !strings.Contains(body, "action_token=at-1") {
		t.Fatalf("body = %q", body)
	}
	if len(got.Messages) != 1 || len(got.Files) != 1 || len(got.Channels) != 1 || len(got.Users) != 1 || got.NextCursor != "cursor-rts" {
		t.Fatalf("SearchContext() = %#v", got)
	}
	if got.Messages[0].ContextMessages.Before[0].Text != "before" || got.Users[0].Name != "jane" {
		t.Fatalf("SearchContext() = %#v", got)
	}
}

func TestPostMessagePostsFormBody(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("path = %q, want /chat.postMessage", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1710000005.000600","message":{"type":"message","user":"UAPP","text":"hello world","ts":"1710000005.000600","thread_ts":"1710000000.000100"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.PostMessage(context.Background(), PostMessageOptions{
		Channel:        "C123",
		Text:           "hello world",
		ThreadTS:       "1710000000.000100",
		ReplyBroadcast: true,
	})
	if err != nil {
		t.Fatalf("PostMessage() error = %v", err)
	}
	if !strings.Contains(body, "channel=C123") || !strings.Contains(body, "text=hello+world") || !strings.Contains(body, "thread_ts=1710000000.000100") || !strings.Contains(body, "reply_broadcast=true") {
		t.Fatalf("body = %q", body)
	}
	if got.Channel != "C123" || got.TS != "1710000005.000600" || got.Message.ThreadTs != "1710000000.000100" {
		t.Fatalf("PostMessage() = %#v", got)
	}
}

func TestUpdateMessagePostsFormBody(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.update" {
			t.Fatalf("path = %q, want /chat.update", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1710000005.000600","text":"patched text","message":{"type":"message","user":"UAPP","text":"patched text","ts":"1710000005.000600"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.UpdateMessage(context.Background(), UpdateMessageOptions{
		Channel: "C123",
		TS:      "1710000005.000600",
		Text:    "patched text",
	})
	if err != nil {
		t.Fatalf("UpdateMessage() error = %v", err)
	}
	if !strings.Contains(body, "channel=C123") || !strings.Contains(body, "ts=1710000005.000600") || !strings.Contains(body, "text=patched+text") {
		t.Fatalf("body = %q", body)
	}
	if got.Channel != "C123" || got.TS != "1710000005.000600" || got.Text != "patched text" || got.Message.Text != "patched text" {
		t.Fatalf("UpdateMessage() = %#v", got)
	}
}

func TestDeleteMessagePostsFormBody(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.delete" {
			t.Fatalf("path = %q, want /chat.delete", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1710000005.000600"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := client.DeleteMessage(context.Background(), DeleteMessageOptions{
		Channel: "C123",
		TS:      "1710000005.000600",
	})
	if err != nil {
		t.Fatalf("DeleteMessage() error = %v", err)
	}
	if !strings.Contains(body, "channel=C123") || !strings.Contains(body, "ts=1710000005.000600") {
		t.Fatalf("body = %q", body)
	}
	if got.Channel != "C123" || got.TS != "1710000005.000600" {
		t.Fatalf("DeleteMessage() = %#v", got)
	}
}

func TestAddReactionPostsFormBody(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reactions.add" {
			t.Fatalf("path = %q, want /reactions.add", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if err := client.AddReaction(context.Background(), AddReactionOptions{
		Channel:   "C123",
		Timestamp: "1710000005.000600",
		Name:      "thumbsup",
	}); err != nil {
		t.Fatalf("AddReaction() error = %v", err)
	}
	if !strings.Contains(body, "channel=C123") || !strings.Contains(body, "timestamp=1710000005.000600") || !strings.Contains(body, "name=thumbsup") {
		t.Fatalf("body = %q", body)
	}
}

func TestRemoveReactionPostsFormBody(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reactions.remove" {
			t.Fatalf("path = %q, want /reactions.remove", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "xoxb-test", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if err := client.RemoveReaction(context.Background(), RemoveReactionOptions{
		Channel:   "C123",
		Timestamp: "1710000005.000600",
		Name:      "thumbsup",
	}); err != nil {
		t.Fatalf("RemoveReaction() error = %v", err)
	}
	if !strings.Contains(body, "channel=C123") || !strings.Contains(body, "timestamp=1710000005.000600") || !strings.Contains(body, "name=thumbsup") {
		t.Fatalf("body = %q", body)
	}
}

func TestSlackErrorBecomesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "bad-token", server.Client())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.AuthTest(context.Background())
	if err == nil {
		t.Fatal("AuthTest() error = nil, want APIError")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.Code != "invalid_auth" {
		t.Fatalf("APIError.Code = %q, want invalid_auth", apiErr.Code)
	}
}
