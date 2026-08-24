package mutate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/relux-works/skill-slack-management/internal/query"
	"github.com/relux-works/skill-slack-management/internal/slack"
)

type Runtime struct {
	SlackProvider func() (SlackClient, error)
}

type Engine struct {
	runtime Runtime
}

type SlackClient interface {
	ListConversations(ctx context.Context, opts slack.ListConversationsOptions) (slack.ConversationPage, error)
	PostMessage(ctx context.Context, opts slack.PostMessageOptions) (slack.PostMessageResult, error)
	UpdateMessage(ctx context.Context, opts slack.UpdateMessageOptions) (slack.UpdateMessageResult, error)
	DeleteMessage(ctx context.Context, opts slack.DeleteMessageOptions) (slack.DeleteMessageResult, error)
	AddReaction(ctx context.Context, opts slack.AddReactionOptions) error
	RemoveReaction(ctx context.Context, opts slack.RemoveReactionOptions) error
}

type mutationMetadata struct {
	Description          string           `json:"description"`
	Parameters           []map[string]any `json:"parameters,omitempty"`
	Examples             []string         `json:"examples,omitempty"`
	Destructive          bool             `json:"destructive"`
	Idempotent           bool             `json:"idempotent"`
	SupportsDryRun       bool             `json:"supports_dry_run"`
	ConfirmationRequired bool             `json:"confirmation_required"`
	RequiredScopes       []string         `json:"required_scopes"`
	TokenSemantics       string           `json:"token_semantics"`
	SideEffectClass      string           `json:"side_effect_class"`
}

var mutationMetadataMap = map[string]mutationMetadata{
	"post_message": {
		Description: "Send a Slack message or thread reply using chat.postMessage.",
		Parameters: []map[string]any{
			{"name": "channel", "type": "string", "optional": true},
			{"name": "conversation", "type": "string", "optional": true},
			{"name": "ref", "type": "string", "optional": true},
			{"name": "text", "type": "string", "optional": false},
			{"name": "thread_ts", "type": "string", "optional": true},
			{"name": "reply_broadcast", "type": "bool", "optional": true},
			{"name": "types", "type": "string", "optional": true},
			{"name": "exclude_archived", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples:        []string{"post_message(general, text=\"hello world\")", "post_message(C123, text=\"reply\", thread_ts=\"1710000000.000100\")"},
		Idempotent:      false,
		SupportsDryRun:  true,
		RequiredScopes:  []string{"chat:write"},
		TokenSemantics:  "bot_or_user_access_token",
		SideEffectClass: "write",
	},
	"update_message": {
		Description: "Update a Slack message using chat.update.",
		Parameters: []map[string]any{
			{"name": "channel", "type": "string", "optional": true},
			{"name": "conversation", "type": "string", "optional": true},
			{"name": "ref", "type": "string", "optional": true},
			{"name": "ts", "type": "string", "optional": false},
			{"name": "text", "type": "string", "optional": false},
			{"name": "types", "type": "string", "optional": true},
			{"name": "exclude_archived", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples:        []string{"update_message(general, ts=\"1710000005.000600\", text=\"edited\")", "update_message(C123, ts=\"1710000005.000600\", text=\"patched\")"},
		Idempotent:      false,
		SupportsDryRun:  true,
		RequiredScopes:  []string{"chat:write"},
		TokenSemantics:  "bot_or_user_access_token",
		SideEffectClass: "write",
	},
	"delete_message": {
		Description: "Delete a Slack message using chat.delete.",
		Parameters: []map[string]any{
			{"name": "channel", "type": "string", "optional": true},
			{"name": "conversation", "type": "string", "optional": true},
			{"name": "ref", "type": "string", "optional": true},
			{"name": "ts", "type": "string", "optional": false},
			{"name": "types", "type": "string", "optional": true},
			{"name": "exclude_archived", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples:        []string{"delete_message(general, ts=\"1710000005.000600\")", "delete_message(C123, ts=\"1710000005.000600\")"},
		Destructive:     true,
		Idempotent:      false,
		SupportsDryRun:  true,
		RequiredScopes:  []string{"chat:write"},
		TokenSemantics:  "bot_or_user_access_token",
		SideEffectClass: "destructive_write",
	},
	"add_reaction": {
		Description: "Add an emoji reaction to a Slack message using reactions.add; exactly one of ts or timestamp is required.",
		Parameters: []map[string]any{
			{"name": "channel", "type": "string", "optional": true},
			{"name": "conversation", "type": "string", "optional": true},
			{"name": "ref", "type": "string", "optional": true},
			{"name": "ts", "type": "string", "optional": true},
			{"name": "timestamp", "type": "string", "optional": true},
			{"name": "name", "type": "string", "optional": false},
			{"name": "types", "type": "string", "optional": true},
			{"name": "exclude_archived", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples:        []string{"add_reaction(general, ts=\"1710000005.000600\", name=\"thumbsup\")", "add_reaction(C123, ts=\"1710000005.000600\", name=\":wave:\")"},
		Idempotent:      false,
		SupportsDryRun:  true,
		RequiredScopes:  []string{"reactions:write"},
		TokenSemantics:  "bot_or_user_access_token",
		SideEffectClass: "write",
	},
	"remove_reaction": {
		Description: "Remove an emoji reaction from a Slack message using reactions.remove; exactly one of ts or timestamp is required.",
		Parameters: []map[string]any{
			{"name": "channel", "type": "string", "optional": true},
			{"name": "conversation", "type": "string", "optional": true},
			{"name": "ref", "type": "string", "optional": true},
			{"name": "ts", "type": "string", "optional": true},
			{"name": "timestamp", "type": "string", "optional": true},
			{"name": "name", "type": "string", "optional": false},
			{"name": "types", "type": "string", "optional": true},
			{"name": "exclude_archived", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples:        []string{"remove_reaction(general, ts=\"1710000005.000600\", name=\"thumbsup\")", "remove_reaction(C123, ts=\"1710000005.000600\", name=\":wave:\")"},
		Destructive:     true,
		Idempotent:      false,
		SupportsDryRun:  true,
		RequiredScopes:  []string{"reactions:write"},
		TokenSemantics:  "bot_or_user_access_token",
		SideEffectClass: "destructive_write",
	},
}

func init() {
	for operation, metadata := range mutationMetadataMap {
		metadata.ConfirmationRequired = metadata.Destructive
		mutationMetadataMap[operation] = metadata
	}
}

func NewEngine(rt Runtime) *Engine {
	return &Engine{runtime: rt}
}

func (e *Engine) Execute(ctx context.Context, raw string) ([]query.Result, error) {
	return e.execute(ctx, raw, false)
}

func (e *Engine) ExecuteConfirmed(ctx context.Context, raw string) ([]query.Result, error) {
	return e.execute(ctx, raw, true)
}

func (e *Engine) execute(ctx context.Context, raw string, confirmed bool) ([]query.Result, error) {
	requests, err := parseValidatedMutationRequests(raw)
	if err != nil {
		return nil, err
	}
	for _, request := range requests {
		operation := strings.ToLower(request.Operation)
		if metadata, ok := mutationMetadataMap[operation]; ok && metadata.Destructive && !confirmed {
			return nil, fmt.Errorf("mutation confirmation required for %s; inspect with --dry-run or rerun with --confirm", operation)
		}
	}

	results := make([]query.Result, 0, len(requests))
	for _, request := range requests {
		result, err := e.executeOne(ctx, request)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}

	return results, nil
}

func (e *Engine) Preview(raw string) ([]query.Result, error) {
	requests, err := parseValidatedMutationRequests(raw)
	if err != nil {
		return nil, err
	}
	if len(requests) != 1 {
		return nil, fmt.Errorf("mutation batches are not supported")
	}
	request := requests[0]
	operation := strings.ToLower(request.Operation)
	if operation == "schema" {
		result, err := e.executeOne(context.Background(), request)
		return []query.Result{result}, err
	}
	metadata := mutationMetadataMap[operation]
	object := map[string]any{
		"operation":             operation,
		"dry_run":               true,
		"would_execute":         false,
		"destructive":           metadata.Destructive,
		"idempotent":            metadata.Idempotent,
		"confirmation_required": metadata.Destructive,
		"required_scopes":       metadata.RequiredScopes,
		"token_semantics":       metadata.TokenSemantics,
		"side_effect_class":     metadata.SideEffectClass,
	}
	if target := mutationTargetRef(request); target != "" {
		object["requested_target"] = target
	}
	for _, name := range []string{"text", "ts", "timestamp", "thread_ts", "name", "types", "team_id"} {
		if value := strings.TrimSpace(request.Params[name]); value != "" {
			object[name] = value
		}
	}
	for _, name := range []string{"reply_broadcast", "exclude_archived"} {
		if value, ok := request.Params[name]; ok {
			parsed, _ := parseMutationBool(value)
			object[name] = parsed
		}
	}
	return []query.Result{{
		Operation: operation,
		Kind:      query.ResultKindObject,
		Object:    object,
		Columns:   sortedObjectKeys(object),
	}}, nil
}

func (e *Engine) ConfirmationRequired(raw string) ([]string, error) {
	requests, err := parseValidatedMutationRequests(raw)
	if err != nil {
		return nil, err
	}
	if len(requests) != 1 {
		return nil, fmt.Errorf("mutation batches are not supported")
	}
	var operations []string
	for _, request := range requests {
		operation := strings.ToLower(request.Operation)
		if metadata, ok := mutationMetadataMap[operation]; ok && metadata.Destructive {
			operations = append(operations, operation)
		}
	}
	return operations, nil
}

func (e *Engine) executeOne(ctx context.Context, request query.Request) (query.Result, error) {
	switch strings.ToLower(request.Operation) {
	case "schema":
		object := e.schemaObject()
		return query.Result{
			Operation: "schema",
			Kind:      query.ResultKindObject,
			Object:    object,
			Columns:   sortedObjectKeys(object),
		}, nil
	case "post_message":
		return e.executePostMessage(ctx, request)
	case "update_message":
		return e.executeUpdateMessage(ctx, request)
	case "delete_message":
		return e.executeDeleteMessage(ctx, request)
	case "add_reaction":
		return e.executeAddReaction(ctx, request)
	case "remove_reaction":
		return e.executeRemoveReaction(ctx, request)
	default:
		return query.Result{}, fmt.Errorf("unsupported mutation %q", request.Operation)
	}
}

func parseValidatedMutationRequests(raw string) ([]query.Request, error) {
	requests, err := query.ParseBatch(raw)
	if err != nil {
		return nil, err
	}
	if len(requests) != 1 {
		return nil, fmt.Errorf("mutation batches are not supported")
	}
	for _, request := range requests {
		if err := validateMutationRequest(request); err != nil {
			return nil, err
		}
	}
	return requests, nil
}

func validateMutationRequest(request query.Request) error {
	operation := strings.ToLower(strings.TrimSpace(request.Operation))
	if len(request.Fields) != 0 {
		return fmt.Errorf("%s() does not accept a field projection", operation)
	}
	if operation == "schema" {
		if request.Positional != "" || len(request.Params) != 0 {
			return fmt.Errorf("schema() accepts no arguments")
		}
		return nil
	}
	metadata, ok := mutationMetadataMap[operation]
	if !ok {
		return fmt.Errorf("unsupported mutation %q", request.Operation)
	}
	allowed := make(map[string]string, len(metadata.Parameters))
	for _, parameter := range metadata.Parameters {
		name, _ := parameter["name"].(string)
		typeName, _ := parameter["type"].(string)
		allowed[name] = typeName
	}
	for name, value := range request.Params {
		typeName, ok := allowed[name]
		if !ok {
			return fmt.Errorf("%s() received unknown parameter %q", operation, name)
		}
		if typeName == "bool" {
			if _, err := parseMutationBool(value); err != nil {
				return fmt.Errorf("%s() parameter %s must be a boolean", operation, name)
			}
		}
	}
	targetAliases := 0
	if strings.TrimSpace(request.Positional) != "" {
		targetAliases++
	}
	for _, name := range []string{"channel", "conversation", "ref"} {
		if strings.TrimSpace(request.Params[name]) != "" {
			targetAliases++
		}
	}
	if targetAliases != 1 {
		return fmt.Errorf("%s() requires exactly one positional target or channel/ref/conversation=...", operation)
	}
	switch operation {
	case "post_message":
		if strings.TrimSpace(request.Params["text"]) == "" {
			return fmt.Errorf("post_message() requires text=...")
		}
		if broadcast, _ := parseMutationBool(request.Params["reply_broadcast"]); broadcast && strings.TrimSpace(request.Params["thread_ts"]) == "" {
			return fmt.Errorf("post_message() reply_broadcast=true requires thread_ts=...")
		}
	case "update_message":
		if strings.TrimSpace(request.Params["ts"]) == "" {
			return fmt.Errorf("update_message() requires ts=...")
		}
		if strings.TrimSpace(request.Params["text"]) == "" {
			return fmt.Errorf("update_message() requires text=...")
		}
	case "delete_message":
		if strings.TrimSpace(request.Params["ts"]) == "" {
			return fmt.Errorf("delete_message() requires ts=...")
		}
	case "add_reaction", "remove_reaction":
		if strings.TrimSpace(request.Params["ts"]) != "" && strings.TrimSpace(request.Params["timestamp"]) != "" {
			return fmt.Errorf("%s() accepts exactly one of ts=... or timestamp=...", operation)
		}
		if mutationTS(request) == "" {
			return fmt.Errorf("%s() requires ts=... or timestamp=...", operation)
		}
		if normalizeReactionName(request.Params["name"]) == "" {
			return fmt.Errorf("%s() requires name=...", operation)
		}
	}
	return nil
}

func parseMutationBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no":
		return false, nil
	case "1", "true", "yes":
		return true, nil
	default:
		return false, fmt.Errorf("invalid boolean")
	}
}

func (e *Engine) executePostMessage(ctx context.Context, request query.Request) (query.Result, error) {
	client, err := e.slackClient()
	if err != nil {
		return query.Result{}, err
	}

	targetRef := mutationTargetRef(request)
	if targetRef == "" {
		return query.Result{}, fmt.Errorf("post_message() requires a positional target or channel/ref/conversation=...")
	}

	text := strings.TrimSpace(request.Params["text"])
	if text == "" {
		return query.Result{}, fmt.Errorf("post_message() requires text=...")
	}

	resolvedTarget, err := resolveTarget(ctx, client, targetRef, request, looksLikePostTargetID, "post target")
	if err != nil {
		return query.Result{}, err
	}

	threadTS := strings.TrimSpace(request.Params["thread_ts"])
	posted, err := client.PostMessage(ctx, slack.PostMessageOptions{
		Channel:        resolvedTarget,
		Text:           text,
		ThreadTS:       threadTS,
		ReplyBroadcast: mustParseBool(request.Params["reply_broadcast"]),
	})
	if err != nil {
		return query.Result{}, err
	}

	object := map[string]any{
		"channel":          posted.Channel,
		"requested_target": targetRef,
		"resolved_target":  resolvedTarget,
		"ts":               posted.TS,
		"text":             posted.Message.Text,
	}
	if posted.Message.ThreadTs != "" {
		object["thread_ts"] = posted.Message.ThreadTs
	} else if threadTS != "" {
		object["thread_ts"] = threadTS
	}
	if mustParseBool(request.Params["reply_broadcast"]) {
		object["reply_broadcast"] = true
	}

	return query.Result{
		Operation: "post_message",
		Kind:      query.ResultKindObject,
		Object:    object,
		Columns:   []string{"channel", "requested_target", "resolved_target", "ts", "thread_ts", "reply_broadcast", "text"},
	}, nil
}

func (e *Engine) executeUpdateMessage(ctx context.Context, request query.Request) (query.Result, error) {
	client, err := e.slackClient()
	if err != nil {
		return query.Result{}, err
	}

	targetRef := mutationTargetRef(request)
	if targetRef == "" {
		return query.Result{}, fmt.Errorf("update_message() requires a positional target or channel/ref/conversation=...")
	}

	ts := strings.TrimSpace(request.Params["ts"])
	if ts == "" {
		return query.Result{}, fmt.Errorf("update_message() requires ts=...")
	}

	text := strings.TrimSpace(request.Params["text"])
	if text == "" {
		return query.Result{}, fmt.Errorf("update_message() requires text=...")
	}

	resolvedTarget, err := resolveTarget(ctx, client, targetRef, request, looksLikeConversationTargetID, "message target")
	if err != nil {
		return query.Result{}, err
	}

	updated, err := client.UpdateMessage(ctx, slack.UpdateMessageOptions{
		Channel: resolvedTarget,
		TS:      ts,
		Text:    text,
	})
	if err != nil {
		return query.Result{}, err
	}

	updatedText := strings.TrimSpace(updated.Text)
	if updatedText == "" {
		updatedText = strings.TrimSpace(updated.Message.Text)
	}
	if updatedText == "" {
		updatedText = text
	}

	object := map[string]any{
		"channel":          updated.Channel,
		"requested_target": targetRef,
		"resolved_target":  resolvedTarget,
		"ts":               updated.TS,
		"text":             updatedText,
	}
	if updated.Message.ThreadTs != "" {
		object["thread_ts"] = updated.Message.ThreadTs
	}

	return query.Result{
		Operation: "update_message",
		Kind:      query.ResultKindObject,
		Object:    object,
		Columns:   []string{"channel", "requested_target", "resolved_target", "ts", "thread_ts", "text"},
	}, nil
}

func (e *Engine) executeDeleteMessage(ctx context.Context, request query.Request) (query.Result, error) {
	client, err := e.slackClient()
	if err != nil {
		return query.Result{}, err
	}

	targetRef := mutationTargetRef(request)
	if targetRef == "" {
		return query.Result{}, fmt.Errorf("delete_message() requires a positional target or channel/ref/conversation=...")
	}

	ts := strings.TrimSpace(request.Params["ts"])
	if ts == "" {
		return query.Result{}, fmt.Errorf("delete_message() requires ts=...")
	}

	resolvedTarget, err := resolveTarget(ctx, client, targetRef, request, looksLikeConversationTargetID, "message target")
	if err != nil {
		return query.Result{}, err
	}

	deleted, err := client.DeleteMessage(ctx, slack.DeleteMessageOptions{
		Channel: resolvedTarget,
		TS:      ts,
	})
	if err != nil {
		return query.Result{}, err
	}

	return query.Result{
		Operation: "delete_message",
		Kind:      query.ResultKindObject,
		Object: map[string]any{
			"channel":          deleted.Channel,
			"requested_target": targetRef,
			"resolved_target":  resolvedTarget,
			"ts":               deleted.TS,
			"deleted":          true,
		},
		Columns: []string{"channel", "requested_target", "resolved_target", "ts", "deleted"},
	}, nil
}

func (e *Engine) executeAddReaction(ctx context.Context, request query.Request) (query.Result, error) {
	return e.executeReactionMutation(ctx, request, "add_reaction", func(ctx context.Context, client SlackClient, opts slack.AddReactionOptions) error {
		return client.AddReaction(ctx, opts)
	}, nil)
}

func (e *Engine) executeRemoveReaction(ctx context.Context, request query.Request) (query.Result, error) {
	return e.executeReactionMutation(ctx, request, "remove_reaction", nil, func(ctx context.Context, client SlackClient, opts slack.RemoveReactionOptions) error {
		return client.RemoveReaction(ctx, opts)
	})
}

func (e *Engine) executeReactionMutation(
	ctx context.Context,
	request query.Request,
	operation string,
	addFn func(context.Context, SlackClient, slack.AddReactionOptions) error,
	removeFn func(context.Context, SlackClient, slack.RemoveReactionOptions) error,
) (query.Result, error) {
	client, err := e.slackClient()
	if err != nil {
		return query.Result{}, err
	}

	targetRef := mutationTargetRef(request)
	if targetRef == "" {
		return query.Result{}, fmt.Errorf("%s() requires a positional target or channel/ref/conversation=...", operation)
	}

	ts := mutationTS(request)
	if ts == "" {
		return query.Result{}, fmt.Errorf("%s() requires ts=... or timestamp=...", operation)
	}

	name := normalizeReactionName(request.Params["name"])
	if name == "" {
		return query.Result{}, fmt.Errorf("%s() requires name=...", operation)
	}

	resolvedTarget, err := resolveTarget(ctx, client, targetRef, request, looksLikeConversationTargetID, "reaction target")
	if err != nil {
		return query.Result{}, err
	}

	if addFn != nil {
		if err := addFn(ctx, client, slack.AddReactionOptions{
			Channel:   resolvedTarget,
			Timestamp: ts,
			Name:      name,
		}); err != nil {
			return query.Result{}, err
		}
		return query.Result{
			Operation: operation,
			Kind:      query.ResultKindObject,
			Object: map[string]any{
				"channel":          resolvedTarget,
				"requested_target": targetRef,
				"resolved_target":  resolvedTarget,
				"ts":               ts,
				"name":             name,
				"added":            true,
			},
			Columns: []string{"channel", "requested_target", "resolved_target", "ts", "name", "added"},
		}, nil
	}

	if err := removeFn(ctx, client, slack.RemoveReactionOptions{
		Channel:   resolvedTarget,
		Timestamp: ts,
		Name:      name,
	}); err != nil {
		return query.Result{}, err
	}
	return query.Result{
		Operation: operation,
		Kind:      query.ResultKindObject,
		Object: map[string]any{
			"channel":          resolvedTarget,
			"requested_target": targetRef,
			"resolved_target":  resolvedTarget,
			"ts":               ts,
			"name":             name,
			"removed":          true,
		},
		Columns: []string{"channel", "requested_target", "resolved_target", "ts", "name", "removed"},
	}, nil
}

func (e *Engine) schemaObject() map[string]any {
	mutations := make([]string, 0, len(mutationMetadataMap))
	for name := range mutationMetadataMap {
		mutations = append(mutations, name)
	}
	sort.Strings(mutations)

	return map[string]any{
		"mutations":        mutations,
		"mutationMetadata": mutationMetadataMap,
	}
}

func (e *Engine) slackClient() (SlackClient, error) {
	if e.runtime.SlackProvider == nil {
		return nil, fmt.Errorf("slack client is not configured")
	}
	return e.runtime.SlackProvider()
}

func mutationTargetRef(request query.Request) string {
	for _, candidate := range []string{
		strings.TrimSpace(request.Positional),
		strings.TrimSpace(request.Params["channel"]),
		strings.TrimSpace(request.Params["conversation"]),
		strings.TrimSpace(request.Params["ref"]),
	} {
		if candidate != "" {
			return normalizeTargetRef(candidate)
		}
	}
	return ""
}

func normalizeTargetRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "#")
	return strings.TrimSpace(ref)
}

func mutationTS(request query.Request) string {
	for _, candidate := range []string{
		request.Params["ts"],
		request.Params["timestamp"],
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func normalizeReactionName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, ":") && strings.HasSuffix(name, ":") && len(name) >= 2 {
		name = strings.TrimPrefix(strings.TrimSuffix(name, ":"), ":")
	}
	return strings.TrimSpace(name)
}

func resolveTarget(ctx context.Context, client SlackClient, ref string, request query.Request, directTargetMatcher func(string) bool, targetLabel string) (string, error) {
	if directTargetMatcher(ref) {
		return strings.ToUpper(ref), nil
	}

	cursor := ""
	for {
		page, err := client.ListConversations(ctx, slack.ListConversationsOptions{
			Cursor:          cursor,
			Limit:           200,
			Types:           strings.TrimSpace(request.Params["types"]),
			ExcludeArchived: mustParseBool(request.Params["exclude_archived"]),
			TeamID:          strings.TrimSpace(request.Params["team_id"]),
		})
		if err != nil {
			return "", err
		}

		for _, item := range page.Items {
			if matchesConversationRef(item, ref) {
				return item.ID, nil
			}
		}

		if strings.TrimSpace(page.NextCursor) == "" {
			break
		}
		cursor = page.NextCursor
	}

	return "", fmt.Errorf("%s %q not found", targetLabel, ref)
}

func looksLikePostTargetID(ref string) bool {
	return looksLikeTargetID(ref, "CDGUW")
}

func looksLikeConversationTargetID(ref string) bool {
	return looksLikeTargetID(ref, "CDG")
}

func looksLikeTargetID(ref string, allowedPrefixes string) bool {
	ref = strings.ToUpper(strings.TrimSpace(ref))
	if len(ref) < 2 {
		return false
	}
	if !strings.ContainsRune(allowedPrefixes, rune(ref[0])) {
		return false
	}
	hasDigit := false
	for _, r := range ref[1:] {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
		if r >= '0' && r <= '9' {
			hasDigit = true
		}
	}
	return hasDigit
}

func matchesConversationRef(item slack.Conversation, ref string) bool {
	ref = normalizeTargetRef(ref)
	return strings.EqualFold(item.ID, ref) ||
		strings.EqualFold(item.Name, ref) ||
		strings.EqualFold(item.NameNormalized, ref)
}

func mustParseBool(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func sortedObjectKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
