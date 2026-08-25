package mutate

import (
	"context"
	"testing"

	"github.com/relux-works/skill-slack-management/internal/query"
	"github.com/relux-works/skill-slack-management/internal/slack"
)

type fakeSlackClient struct {
	listResult   slack.ConversationPage
	postResult   slack.PostMessageResult
	updateResult slack.UpdateMessageResult
	deleteResult slack.DeleteMessageResult
	addReaction  slack.AddReactionOptions
	rmReaction   slack.RemoveReactionOptions
	listCalls    int
	postCalls    int
	updateCalls  int
	deleteCalls  int
	addCalls     int
	removeCalls  int
	lastPost     slack.PostMessageOptions
	lastUpdate   slack.UpdateMessageOptions
	lastDelete   slack.DeleteMessageOptions
}

func (f *fakeSlackClient) ListConversations(context.Context, slack.ListConversationsOptions) (slack.ConversationPage, error) {
	f.listCalls++
	return f.listResult, nil
}

func (f *fakeSlackClient) PostMessage(_ context.Context, opts slack.PostMessageOptions) (slack.PostMessageResult, error) {
	f.postCalls++
	f.lastPost = opts
	return f.postResult, nil
}

func (f *fakeSlackClient) UpdateMessage(_ context.Context, opts slack.UpdateMessageOptions) (slack.UpdateMessageResult, error) {
	f.updateCalls++
	f.lastUpdate = opts
	return f.updateResult, nil
}

func (f *fakeSlackClient) DeleteMessage(_ context.Context, opts slack.DeleteMessageOptions) (slack.DeleteMessageResult, error) {
	f.deleteCalls++
	f.lastDelete = opts
	return f.deleteResult, nil
}

func (f *fakeSlackClient) AddReaction(_ context.Context, opts slack.AddReactionOptions) error {
	f.addCalls++
	f.addReaction = opts
	return nil
}

func (f *fakeSlackClient) RemoveReaction(_ context.Context, opts slack.RemoveReactionOptions) error {
	f.removeCalls++
	f.rmReaction = opts
	return nil
}

func TestEngineSchema(t *testing.T) {
	engine := NewEngine(Runtime{})

	results, err := engine.Execute(context.Background(), "schema()")
	if err != nil {
		t.Fatalf("Execute(schema) error = %v", err)
	}
	if len(results) != 1 || results[0].Kind != query.ResultKindObject {
		t.Fatalf("results = %#v", results)
	}
	metadata := results[0].Object["mutationMetadata"].(map[string]mutationMetadata)
	if !metadata["post_message"].SupportsDryRun || metadata["post_message"].ConfirmationRequired || metadata["post_message"].Destructive {
		t.Fatalf("post_message metadata = %#v", metadata["post_message"])
	}
	if len(metadata["post_message"].RequiredScopes) != 1 || metadata["post_message"].RequiredScopes[0] != "chat:write" || metadata["post_message"].SideEffectClass != "write" {
		t.Fatalf("post_message scope metadata = %#v", metadata["post_message"])
	}
	for _, operation := range []string{"delete_message", "remove_reaction"} {
		if !metadata[operation].SupportsDryRun || !metadata[operation].ConfirmationRequired || !metadata[operation].Destructive {
			t.Fatalf("%s metadata = %#v", operation, metadata[operation])
		}
	}
	for operation, item := range metadata {
		if item.ConfirmationRequired != item.Destructive {
			t.Fatalf("%s confirmation metadata diverges from destructive=%t", operation, item.Destructive)
		}
	}
}

func TestPreviewValidatesEveryMutationWithoutCreatingSlackClient(t *testing.T) {
	providerCalls := 0
	engine := NewEngine(Runtime{SlackProvider: func() (SlackClient, error) {
		providerCalls++
		return &fakeSlackClient{}, nil
	}})
	tests := []struct {
		operation string
		raw       string
	}{
		{"post_message", `post_message(C123, text="hello", thread_ts="1710000000.000100", reply_broadcast=true)`},
		{"update_message", `update_message(C123, ts="1710000005.000600", text="patched")`},
		{"delete_message", `delete_message(C123, ts="1710000005.000600")`},
		{"add_reaction", `add_reaction(C123, ts="1710000005.000600", name=":wave:")`},
		{"remove_reaction", `remove_reaction(C123, ts="1710000005.000600", name=":wave:")`},
	}
	for _, tt := range tests {
		t.Run(tt.operation, func(t *testing.T) {
			results, err := engine.Preview(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Operation != tt.operation || results[0].Object["dry_run"] != true || results[0].Object["would_execute"] != false {
				t.Fatalf("preview = %#v", results)
			}
			wantConfirmation := tt.operation == "delete_message" || tt.operation == "remove_reaction"
			if results[0].Object["confirmation_required"] != wantConfirmation {
				t.Fatalf("confirmation_required = %#v, want %t", results[0].Object["confirmation_required"], wantConfirmation)
			}
		})
	}
	if providerCalls != 0 {
		t.Fatalf("dry-run created Slack client %d times", providerCalls)
	}
}

func TestMutationValidationAndConfirmationFailBeforeProviderCreation(t *testing.T) {
	providerCalls := 0
	engine := NewEngine(Runtime{SlackProvider: func() (SlackClient, error) {
		providerCalls++
		return &fakeSlackClient{}, nil
	}})
	invalid := []string{
		`post_message(C123, text="hello"); add_reaction(C123, ts="1710000005.000600", name="wave")`,
		`post_message(C123, channel=C456, text="hello")`,
		`post_message(C123, text="first", text="second")`,
		`post_message(C123, text="hello") { full }`,
		`post_message(C123, text="hello", reply_broadcast=maybe)`,
		`post_message(C123, text="hello", reply_broadcast=true)`,
		`delete_message(C123, ts="1710000005.000600", unknown=true)`,
		`remove_reaction(C123, ts="1710000005.000600", timestamp="1710000006.000700", name="wave")`,
		`unknown_mutation(C123)`,
	}
	for _, raw := range invalid {
		if _, err := engine.Preview(raw); err == nil {
			t.Errorf("Preview(%q) error = nil", raw)
		}
		if _, err := engine.Execute(context.Background(), raw); err == nil {
			t.Errorf("Execute(%q) error = nil", raw)
		}
	}
	for _, tt := range []struct {
		raw  string
		want []string
	}{
		{`post_message(C123, text="hello")`, nil},
		{`delete_message(C123, ts="1710000005.000600")`, []string{"delete_message"}},
		{`remove_reaction(C123, ts="1710000005.000600", name="wave")`, []string{"remove_reaction"}},
	} {
		got, err := engine.ConfirmationRequired(tt.raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(tt.want) || len(got) == 1 && got[0] != tt.want[0] {
			t.Fatalf("ConfirmationRequired(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
	if providerCalls != 0 {
		t.Fatalf("local validation created Slack client %d times", providerCalls)
	}
}

func TestDestructiveMutationCannotBypassEngineConfirmation(t *testing.T) {
	providerCalls := 0
	engine := NewEngine(Runtime{SlackProvider: func() (SlackClient, error) {
		providerCalls++
		return &fakeSlackClient{}, nil
	}})
	for _, raw := range []string{
		`delete_message(C123, ts="1710000005.000600")`,
		`remove_reaction(C123, ts="1710000005.000600", name="wave")`,
	} {
		if _, err := engine.Execute(context.Background(), raw); err == nil {
			t.Fatalf("Execute(%q) bypassed confirmation", raw)
		}
	}
	if providerCalls != 0 {
		t.Fatalf("confirmation refusal created Slack client %d times", providerCalls)
	}
}

func TestPostMessageResolvesConversationName(t *testing.T) {
	fake := &fakeSlackClient{
		listResult: slack.ConversationPage{
			Items: []slack.Conversation{
				{ID: "C123", Name: "general", NameNormalized: "general"},
			},
		},
		postResult: slack.PostMessageResult{
			Channel: "C123",
			TS:      "1710000005.000600",
			Message: slack.Message{
				Text:     "hello world",
				Ts:       "1710000005.000600",
				ThreadTs: "1710000000.000100",
			},
		},
	}

	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	results, err := engine.Execute(context.Background(), `post_message(general, text="hello world", thread_ts="1710000000.000100", reply_broadcast=true)`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if fake.listCalls != 1 || fake.postCalls != 1 {
		t.Fatalf("calls = list:%d post:%d", fake.listCalls, fake.postCalls)
	}
	if fake.lastPost.Channel != "C123" || fake.lastPost.Text != "hello world" || fake.lastPost.ThreadTS != "1710000000.000100" || !fake.lastPost.ReplyBroadcast {
		t.Fatalf("lastPost = %#v", fake.lastPost)
	}
	if got := results[0].Object["resolved_target"]; got != "C123" {
		t.Fatalf("resolved_target = %#v", got)
	}

	compact, err := query.RenderCompact(results)
	if err != nil {
		t.Fatalf("RenderCompact() error = %v", err)
	}
	if compact == "" {
		t.Fatal("compact output should not be empty")
	}
}

func TestPostMessageDirectUserTargetSkipsResolution(t *testing.T) {
	fake := &fakeSlackClient{
		postResult: slack.PostMessageResult{
			Channel: "U123",
			TS:      "1710000006.000700",
			Message: slack.Message{
				Text: "hello app home",
				Ts:   "1710000006.000700",
			},
		},
	}

	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	results, err := engine.Execute(context.Background(), `post_message(U123, text="hello app home")`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fake.listCalls != 0 || fake.postCalls != 1 {
		t.Fatalf("calls = list:%d post:%d", fake.listCalls, fake.postCalls)
	}
	if got := results[0].Object["resolved_target"]; got != "U123" {
		t.Fatalf("resolved_target = %#v", got)
	}
}

func TestPostMessageRequiresText(t *testing.T) {
	fake := &fakeSlackClient{}
	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	_, err := engine.Execute(context.Background(), `post_message(general)`)
	if err == nil {
		t.Fatal("Execute(post_message without text) error = nil, want error")
	}
	if got := err.Error(); got != "post_message() requires text=..." {
		t.Fatalf("error = %q", got)
	}
}

func TestUpdateMessageResolvesConversationName(t *testing.T) {
	fake := &fakeSlackClient{
		listResult: slack.ConversationPage{
			Items: []slack.Conversation{
				{ID: "C123", Name: "general", NameNormalized: "general"},
			},
		},
		updateResult: slack.UpdateMessageResult{
			Channel: "C123",
			TS:      "1710000005.000600",
			Text:    "patched text",
			Message: slack.Message{
				Text: "patched text",
				Ts:   "1710000005.000600",
			},
		},
	}

	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	results, err := engine.Execute(context.Background(), `update_message(general, ts="1710000005.000600", text="patched text")`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if fake.listCalls != 1 || fake.updateCalls != 1 {
		t.Fatalf("calls = list:%d update:%d", fake.listCalls, fake.updateCalls)
	}
	if fake.lastUpdate.Channel != "C123" || fake.lastUpdate.TS != "1710000005.000600" || fake.lastUpdate.Text != "patched text" {
		t.Fatalf("lastUpdate = %#v", fake.lastUpdate)
	}
	if got := results[0].Object["resolved_target"]; got != "C123" {
		t.Fatalf("resolved_target = %#v", got)
	}
	if got := results[0].Object["text"]; got != "patched text" {
		t.Fatalf("text = %#v", got)
	}
}

func TestDeleteMessageDirectConversationTargetSkipsResolution(t *testing.T) {
	fake := &fakeSlackClient{
		deleteResult: slack.DeleteMessageResult{
			Channel: "C123",
			TS:      "1710000005.000600",
		},
	}

	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	results, err := engine.ExecuteConfirmed(context.Background(), `delete_message(C123, ts="1710000005.000600")`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fake.listCalls != 0 || fake.deleteCalls != 1 {
		t.Fatalf("calls = list:%d delete:%d", fake.listCalls, fake.deleteCalls)
	}
	if fake.lastDelete.Channel != "C123" || fake.lastDelete.TS != "1710000005.000600" {
		t.Fatalf("lastDelete = %#v", fake.lastDelete)
	}
	if got := results[0].Object["deleted"]; got != true {
		t.Fatalf("deleted = %#v", got)
	}
}

func TestUpdateMessageRequiresTS(t *testing.T) {
	fake := &fakeSlackClient{}
	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	_, err := engine.Execute(context.Background(), `update_message(general, text="patched text")`)
	if err == nil {
		t.Fatal("Execute(update_message without ts) error = nil, want error")
	}
	if got := err.Error(); got != "update_message() requires ts=..." {
		t.Fatalf("error = %q", got)
	}
}

func TestDeleteMessageRequiresTS(t *testing.T) {
	fake := &fakeSlackClient{}
	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	_, err := engine.Execute(context.Background(), `delete_message(general)`)
	if err == nil {
		t.Fatal("Execute(delete_message without ts) error = nil, want error")
	}
	if got := err.Error(); got != "delete_message() requires ts=..." {
		t.Fatalf("error = %q", got)
	}
}

func TestAddReactionResolvesConversationNameAndNormalizesEmoji(t *testing.T) {
	fake := &fakeSlackClient{
		listResult: slack.ConversationPage{
			Items: []slack.Conversation{
				{ID: "C123", Name: "general", NameNormalized: "general"},
			},
		},
	}

	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	results, err := engine.Execute(context.Background(), `add_reaction(general, ts="1710000005.000600", name=":thumbsup:")`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fake.listCalls != 1 || fake.addCalls != 1 {
		t.Fatalf("calls = list:%d add:%d", fake.listCalls, fake.addCalls)
	}
	if fake.addReaction.Channel != "C123" || fake.addReaction.Timestamp != "1710000005.000600" || fake.addReaction.Name != "thumbsup" {
		t.Fatalf("addReaction = %#v", fake.addReaction)
	}
	if got := results[0].Object["added"]; got != true {
		t.Fatalf("added = %#v", got)
	}
	if got := results[0].Object["name"]; got != "thumbsup" {
		t.Fatalf("name = %#v", got)
	}
}

func TestRemoveReactionDirectConversationTargetSkipsResolution(t *testing.T) {
	fake := &fakeSlackClient{}

	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	results, err := engine.ExecuteConfirmed(context.Background(), `remove_reaction(C123, timestamp="1710000005.000600", name="wave")`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if fake.listCalls != 0 || fake.removeCalls != 1 {
		t.Fatalf("calls = list:%d remove:%d", fake.listCalls, fake.removeCalls)
	}
	if fake.rmReaction.Channel != "C123" || fake.rmReaction.Timestamp != "1710000005.000600" || fake.rmReaction.Name != "wave" {
		t.Fatalf("rmReaction = %#v", fake.rmReaction)
	}
	if got := results[0].Object["removed"]; got != true {
		t.Fatalf("removed = %#v", got)
	}
}

func TestAddReactionRequiresName(t *testing.T) {
	fake := &fakeSlackClient{}
	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	_, err := engine.Execute(context.Background(), `add_reaction(general, ts="1710000005.000600")`)
	if err == nil {
		t.Fatal("Execute(add_reaction without name) error = nil, want error")
	}
	if got := err.Error(); got != "add_reaction() requires name=..." {
		t.Fatalf("error = %q", got)
	}
}

func TestRemoveReactionRequiresTS(t *testing.T) {
	fake := &fakeSlackClient{}
	engine := NewEngine(Runtime{
		SlackProvider: func() (SlackClient, error) {
			return fake, nil
		},
	})

	_, err := engine.Execute(context.Background(), `remove_reaction(general, name="thumbsup")`)
	if err == nil {
		t.Fatal("Execute(remove_reaction without ts) error = nil, want error")
	}
	if got := err.Error(); got != "remove_reaction() requires ts=... or timestamp=..." {
		t.Fatalf("error = %q", got)
	}
}
