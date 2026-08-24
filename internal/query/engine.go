package query

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/relux-works/skill-slack-management/internal/attachments"
	"github.com/relux-works/skill-slack-management/internal/provider"
)

type Runtime struct {
	Getenv             func(string) string
	ManifestPath       string
	PublishAttachment  func(attachments.Attachment) (attachments.Attachment, error)
	PublishAttachments func([]attachments.Attachment) ([]attachments.Attachment, error)
	ReadProvider       func() (provider.ReadProvider, error)
}

type Engine struct {
	runtime Runtime
}

// ReadTransport is kept as a source-compatible name for transport factories;
// Engine itself depends only on provider.ReadProvider through Runtime.
type ReadTransport = provider.WebAPITransport

type ResultKind string

const (
	ResultKindObject ResultKind = "object"
	ResultKindList   ResultKind = "list"
)

type Result struct {
	Operation string
	Kind      ResultKind
	Columns   []string
	Object    map[string]any
	Items     []map[string]any
	Page      map[string]any
}

type operationMetadata struct {
	Description string           `json:"description"`
	Parameters  []map[string]any `json:"parameters,omitempty"`
	Examples    []string         `json:"examples,omitempty"`
}

type entitySchema struct {
	Fields  []string            `json:"fields"`
	Presets map[string][]string `json:"presets"`
	Default []string            `json:"defaultFields"`
}

var schemas = map[string]entitySchema{
	"attachment": {
		Fields: []string{"id", "name", "mime_type", "size_bytes", "sha256", "local_path", "source", "created_at", "path_exists", "metadata"},
		Presets: map[string][]string{
			"minimal":  {"id", "name", "local_path"},
			"default":  {"id", "name", "mime_type", "size_bytes", "local_path"},
			"overview": {"id", "name", "mime_type", "size_bytes", "local_path", "source"},
			"full":     {"id", "name", "mime_type", "size_bytes", "sha256", "local_path", "source", "created_at", "path_exists", "metadata"},
		},
		Default: []string{"default"},
	},
	"auth_test": {
		Fields: []string{"url", "team", "user", "team_id", "user_id", "bot_id", "enterprise_id"},
		Presets: map[string][]string{
			"minimal":  {"team", "team_id", "user", "user_id"},
			"default":  {"url", "team", "team_id", "user", "user_id", "bot_id"},
			"overview": {"team", "team_id", "user", "user_id", "bot_id", "enterprise_id"},
			"full":     {"url", "team", "user", "team_id", "user_id", "bot_id", "enterprise_id"},
		},
		Default: []string{"default"},
	},
	"search_info": {
		Fields: []string{"is_ai_search_enabled"},
		Presets: map[string][]string{
			"minimal":  {"is_ai_search_enabled"},
			"default":  {"is_ai_search_enabled"},
			"overview": {"is_ai_search_enabled"},
			"full":     {"is_ai_search_enabled"},
		},
		Default: []string{"default"},
	},
	"conversation": {
		Fields: []string{"id", "name", "name_normalized", "is_channel", "is_group", "is_im", "is_private", "is_archived", "is_general", "is_member", "is_mpim", "num_members", "user", "topic", "purpose", "creator", "context_team_id", "locale", "created", "latest_user", "latest_text", "latest_ts"},
		Presets: map[string][]string{
			"minimal":  {"id", "name"},
			"default":  {"id", "name", "is_private", "is_archived", "is_member"},
			"overview": {"id", "name", "is_private", "is_member", "is_archived", "num_members", "topic"},
			"full":     {"id", "name", "name_normalized", "is_channel", "is_group", "is_im", "is_private", "is_archived", "is_general", "is_member", "is_mpim", "num_members", "user", "topic", "purpose", "creator", "context_team_id", "locale", "created", "latest_user", "latest_text", "latest_ts"},
		},
		Default: []string{"default"},
	},
	"message": {
		Fields: []string{"type", "subtype", "user", "username", "text", "ts", "thread_ts", "parent_user_id", "bot_id", "reply_count", "reply_users_count", "latest_reply", "reply_users"},
		Presets: map[string][]string{
			"minimal":  {"ts", "text"},
			"default":  {"ts", "user", "text", "thread_ts"},
			"overview": {"ts", "user", "text", "subtype", "reply_count", "thread_ts"},
			"full":     {"type", "subtype", "user", "username", "text", "ts", "thread_ts", "parent_user_id", "bot_id", "reply_count", "reply_users_count", "latest_reply", "reply_users"},
		},
		Default: []string{"default"},
	},
	"search_match": {
		Fields: []string{"type", "user", "username", "text", "ts", "team", "iid", "permalink", "channel_id", "channel_name", "channel_is_private", "channel_is_mpim", "channel_is_shared"},
		Presets: map[string][]string{
			"minimal":  {"ts", "text", "channel_name"},
			"default":  {"ts", "text", "channel_name", "permalink"},
			"overview": {"ts", "user", "username", "text", "channel_name", "permalink"},
			"full":     {"type", "user", "username", "text", "ts", "team", "iid", "permalink", "channel_id", "channel_name", "channel_is_private", "channel_is_mpim", "channel_is_shared"},
		},
		Default: []string{"default"},
	},
	"search_context_result": {
		Fields: []string{"content_type", "team_id", "author_name", "author_user_id", "uploader_user_id", "creator_user_id", "creator_name", "user_id", "name", "real_name", "title", "email", "channel_id", "channel_name", "message_ts", "file_id", "file_type", "topic", "purpose", "content", "permalink", "is_author_bot", "is_bot", "is_deleted", "date_created", "date_updated", "context_before_count", "context_after_count", "blocks", "context_messages"},
		Presets: map[string][]string{
			"minimal":  {"content_type", "content", "name", "title", "permalink"},
			"default":  {"content_type", "channel_name", "name", "title", "content", "permalink"},
			"overview": {"content_type", "channel_name", "author_name", "name", "title", "content", "permalink"},
			"full":     {"content_type", "team_id", "author_name", "author_user_id", "uploader_user_id", "creator_user_id", "creator_name", "user_id", "name", "real_name", "title", "email", "channel_id", "channel_name", "message_ts", "file_id", "file_type", "topic", "purpose", "content", "permalink", "is_author_bot", "is_bot", "is_deleted", "date_created", "date_updated", "context_before_count", "context_after_count", "blocks", "context_messages"},
		},
		Default: []string{"default"},
	},
	"user": {
		Fields: []string{"id", "team_id", "name", "real_name", "display_name", "email", "deleted", "is_bot", "is_app_user", "is_admin", "is_owner", "is_restricted", "is_ultra_restricted"},
		Presets: map[string][]string{
			"minimal":  {"id", "name"},
			"default":  {"id", "name", "real_name", "display_name", "deleted", "is_bot"},
			"overview": {"id", "name", "real_name", "display_name", "deleted", "is_bot", "is_app_user"},
			"full":     {"id", "team_id", "name", "real_name", "display_name", "email", "deleted", "is_bot", "is_app_user", "is_admin", "is_owner", "is_restricted", "is_ultra_restricted"},
		},
		Default: []string{"default"},
	},
}

var operationMetadataMap = map[string]operationMetadata{
	"schema": {
		Description: "Describe the available query operations, entities, fields, and presets.",
		Examples:    []string{"schema()"},
	},
	"provider_capabilities": {
		Description: "Describe the selected read provider and its machine-readable capability contract without performing a Slack read.",
		Examples:    []string{"provider_capabilities()"},
	},
	"attachments": {
		Description: "List currently materialized local attachments from the runtime manifest.",
		Examples:    []string{"attachments() { overview }", "attachments() { minimal }"},
	},
	"attachment": {
		Description: "Resolve one materialized attachment by id, name, or basename(local_path).",
		Parameters:  []map[string]any{{"name": "ref", "type": "string", "optional": false}},
		Examples:    []string{"attachment(att-1) { full }", "attachment(\"screenshot.png\") { default }"},
	},
	"auth_test": {
		Description: "Call Slack auth.test with the resolved access token.",
		Examples:    []string{"auth_test() { default }"},
	},
	"search_info": {
		Description: "Inspect whether Real-time Search / AI search is enabled for the current team.",
		Examples:    []string{"search_info() { default }"},
	},
	"conversations": {
		Description: "List Slack conversations using conversations.list.",
		Parameters: []map[string]any{
			{"name": "cursor", "type": "string", "optional": true},
			{"name": "limit", "type": "int", "optional": true},
			{"name": "types", "type": "string", "optional": true},
			{"name": "exclude_archived", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples: []string{"conversations(limit=20, types=\"public_channel,private_channel\") { overview }"},
	},
	"conversation": {
		Description: "Resolve and fetch one Slack conversation by id or human-readable name using conversations.info plus conversations.list fallback.",
		Parameters: []map[string]any{
			{"name": "conversation", "type": "string", "optional": true},
			{"name": "ref", "type": "string", "optional": true},
			{"name": "channel", "type": "string", "optional": true},
			{"name": "types", "type": "string", "optional": true},
			{"name": "exclude_archived", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
			{"name": "include_locale", "type": "bool", "optional": true},
			{"name": "include_num_members", "type": "bool", "optional": true},
		},
		Examples: []string{"conversation(general) { overview }", "conversation(ref=\"C123\", include_num_members=true) { full }"},
	},
	"history": {
		Description: "Read Slack conversation history using conversations.history with id-or-name resolution.",
		Parameters: []map[string]any{
			{"name": "conversation", "type": "string", "optional": true},
			{"name": "ref", "type": "string", "optional": true},
			{"name": "channel", "type": "string", "optional": true},
			{"name": "cursor", "type": "string", "optional": true},
			{"name": "limit", "type": "int", "optional": true},
			{"name": "oldest", "type": "string", "optional": true},
			{"name": "latest", "type": "string", "optional": true},
			{"name": "inclusive", "type": "bool", "optional": true},
			{"name": "include_all_metadata", "type": "bool", "optional": true},
			{"name": "types", "type": "string", "optional": true},
			{"name": "exclude_archived", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples: []string{"history(general, limit=20) { overview }", "history(channel=\"C123\", oldest=\"1710000000.000100\") { full }"},
	},
	"replies": {
		Description: "Read Slack thread replies using conversations.replies with id-or-name conversation resolution.",
		Parameters: []map[string]any{
			{"name": "conversation", "type": "string", "optional": true},
			{"name": "ref", "type": "string", "optional": true},
			{"name": "channel", "type": "string", "optional": true},
			{"name": "ts", "type": "string", "optional": false},
			{"name": "cursor", "type": "string", "optional": true},
			{"name": "limit", "type": "int", "optional": true},
			{"name": "oldest", "type": "string", "optional": true},
			{"name": "latest", "type": "string", "optional": true},
			{"name": "inclusive", "type": "bool", "optional": true},
			{"name": "include_all_metadata", "type": "bool", "optional": true},
			{"name": "types", "type": "string", "optional": true},
			{"name": "exclude_archived", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples: []string{"replies(general, ts=\"1710000000.000100\") { overview }"},
	},
	"search_messages": {
		Description: "Search Slack messages using the legacy search.messages endpoint.",
		Parameters: []map[string]any{
			{"name": "query", "type": "string", "optional": false},
			{"name": "count", "type": "int", "optional": true},
			{"name": "page", "type": "int", "optional": true},
			{"name": "cursor", "type": "string", "optional": true},
			{"name": "sort", "type": "string", "optional": true},
			{"name": "sort_dir", "type": "string", "optional": true},
			{"name": "highlight", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples: []string{"search_messages(\"error in:#alerts\", count=20, page=1) { overview }"},
	},
	"search_context": {
		Description: "Search Slack content using assistant.search.context, the newer Real-time Search API.",
		Parameters: []map[string]any{
			{"name": "query", "type": "string", "optional": false},
			{"name": "action_token", "type": "string", "optional": true},
			{"name": "channel_types", "type": "string", "optional": true},
			{"name": "content_types", "type": "string", "optional": true},
			{"name": "include_bots", "type": "bool", "optional": true},
			{"name": "include_deleted_users", "type": "bool", "optional": true},
			{"name": "before", "type": "int", "optional": true},
			{"name": "after", "type": "int", "optional": true},
			{"name": "include_context_messages", "type": "bool", "optional": true},
			{"name": "context_channel_id", "type": "string", "optional": true},
			{"name": "cursor", "type": "string", "optional": true},
			{"name": "limit", "type": "int", "optional": true},
			{"name": "sort", "type": "string", "optional": true},
			{"name": "sort_dir", "type": "string", "optional": true},
			{"name": "include_message_blocks", "type": "bool", "optional": true},
			{"name": "highlight", "type": "bool", "optional": true},
			{"name": "term_clauses", "type": "string", "optional": true},
			{"name": "modifiers", "type": "string", "optional": true},
			{"name": "include_archived_channels", "type": "bool", "optional": true},
			{"name": "disable_semantic_search", "type": "bool", "optional": true},
		},
		Examples: []string{"search_context(\"What is project gizmo?\", content_types=\"messages,files\", include_context_messages=true) { overview }"},
	},
	"users": {
		Description: "List Slack users using users.list.",
		Parameters: []map[string]any{
			{"name": "cursor", "type": "string", "optional": true},
			{"name": "limit", "type": "int", "optional": true},
			{"name": "include_locale", "type": "bool", "optional": true},
			{"name": "team_id", "type": "string", "optional": true},
		},
		Examples: []string{"users(limit=50) { overview }"},
	},
}

func NewEngine(rt Runtime) *Engine {
	if rt.Getenv == nil {
		rt.Getenv = os.Getenv
	}
	return &Engine{runtime: rt}
}

func DefaultAttachmentPreset() []string {
	return append([]string(nil), schemas["attachment"].Presets["default"]...)
}

func AttachmentToObject(item attachments.Attachment) map[string]any {
	object := map[string]any{
		"id":         item.ID,
		"name":       item.Name,
		"mime_type":  item.MIMEType,
		"size_bytes": item.SizeBytes,
		"sha256":     item.SHA256,
		"local_path": item.LocalPath,
		"source":     item.Source,
		"created_at": item.CreatedAt,
		"metadata":   item.Metadata,
	}
	if _, err := os.Stat(item.LocalPath); err == nil {
		object["path_exists"] = true
	} else {
		object["path_exists"] = false
	}
	return object
}

func (e *Engine) Execute(ctx context.Context, raw string) ([]Result, error) {
	requests, err := ParseBatch(raw)
	if err != nil {
		return nil, err
	}
	for _, request := range requests {
		if _, err := resolveRequestColumns(request); err != nil {
			return nil, err
		}
	}

	results := make([]Result, 0, len(requests))
	for _, request := range requests {
		result, err := e.executeOne(ctx, request)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}

	return results, nil
}

func (e *Engine) executeOne(ctx context.Context, request Request) (Result, error) {
	columns, err := resolveRequestColumns(request)
	if err != nil {
		return Result{}, err
	}
	switch strings.ToLower(request.Operation) {
	case "schema":
		object := e.schemaObject()
		return Result{
			Operation: "schema",
			Kind:      ResultKindObject,
			Object:    object,
			Columns:   sortedObjectKeys(object),
		}, nil
	case "provider_capabilities":
		readProvider, err := e.provider()
		if err != nil {
			return Result{}, err
		}
		object, err := structToObject(readProvider.Capabilities())
		if err != nil {
			return Result{}, err
		}
		return Result{Operation: "provider_capabilities", Kind: ResultKindObject, Object: object, Columns: sortedObjectKeys(object)}, nil
	case "attachments":
		list, err := e.loadAttachments()
		if err != nil {
			if err == attachments.ErrManifestNotFound {
				return Result{
					Operation: "attachments",
					Kind:      ResultKindList,
					Items:     []map[string]any{},
					Columns:   columns,
				}, nil
			}
			return Result{}, err
		}

		list, err = e.publishAttachments(list)
		if err != nil {
			return Result{}, err
		}
		items := make([]map[string]any, 0, len(list))
		for _, item := range list {
			object := AttachmentToObject(item)
			object["path_exists"] = true
			items = append(items, selectFields(object, columns))
		}
		return Result{
			Operation: "attachments",
			Kind:      ResultKindList,
			Items:     items,
			Columns:   columns,
		}, nil
	case "attachment":
		list, err := e.loadAttachments()
		if err != nil {
			if err == attachments.ErrManifestNotFound {
				return Result{}, fmt.Errorf("attachment manifest not found; run `slack-mgmt attachment materialize` first")
			}
			return Result{}, err
		}

		ref := strings.TrimSpace(request.Positional)
		if ref == "" {
			ref = strings.TrimSpace(request.Params["ref"])
		}
		if ref == "" {
			return Result{}, fmt.Errorf("attachment() requires a positional reference or ref=...")
		}

		item, err := attachments.FindAttachment(list, ref)
		if err != nil {
			return Result{}, err
		}
		item, err = e.publishAttachment(item)
		if err != nil {
			return Result{}, err
		}

		attachmentObject := AttachmentToObject(item)
		attachmentObject["path_exists"] = true
		object := selectFields(attachmentObject, columns)
		return Result{
			Operation: "attachment",
			Kind:      ResultKindObject,
			Object:    object,
			Columns:   orderedColumns(object, columns),
		}, nil
	case "auth_test":
		client, err := e.readProvider(provider.OperationAuthTest)
		if err != nil {
			return Result{}, err
		}

		authResult, err := client.AuthTest(ctx)
		if err != nil {
			return Result{}, err
		}

		object := selectFields(authTestToObject(authResult), columns)
		return Result{
			Operation: "auth_test",
			Kind:      ResultKindObject,
			Object:    object,
			Columns:   orderedColumns(object, columns),
		}, nil
	case "search_info":
		client, err := e.readProvider(provider.OperationSearchInfo)
		if err != nil {
			return Result{}, err
		}

		info, err := client.SearchInfo(ctx)
		if err != nil {
			return Result{}, err
		}

		object := selectFields(searchInfoToObject(info), columns)
		return Result{
			Operation: "search_info",
			Kind:      ResultKindObject,
			Object:    object,
			Columns:   orderedColumns(object, columns),
		}, nil
	case "conversations":
		client, err := e.readProvider(provider.OperationConversations)
		if err != nil {
			return Result{}, err
		}

		page, err := client.ListConversations(ctx, provider.ListConversationsOptions{
			Cursor:          strings.TrimSpace(request.Params["cursor"]),
			Limit:           mustParseInt(request.Params["limit"]),
			Types:           strings.TrimSpace(request.Params["types"]),
			ExcludeArchived: mustParseBool(request.Params["exclude_archived"]),
			TeamID:          strings.TrimSpace(request.Params["team_id"]),
		})
		if err != nil {
			return Result{}, err
		}

		items := make([]map[string]any, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, selectFields(conversationToObject(item), columns))
		}
		return Result{
			Operation: "conversations",
			Kind:      ResultKindList,
			Items:     items,
			Columns:   columns,
			Page:      pageMap(page.NextCursor),
		}, nil
	case "conversation":
		client, err := e.readProvider(provider.OperationConversation)
		if err != nil {
			return Result{}, err
		}

		item, err := e.resolveConversation(ctx, client, request)
		if err != nil {
			return Result{}, err
		}

		object := selectFields(conversationToObject(item), columns)
		return Result{
			Operation: "conversation",
			Kind:      ResultKindObject,
			Object:    object,
			Columns:   orderedColumns(object, columns),
		}, nil
	case "history":
		client, err := e.readProvider(provider.OperationHistory)
		if err != nil {
			return Result{}, err
		}

		resolved, err := e.resolveConversationRef(ctx, client, request)
		if err != nil {
			return Result{}, err
		}

		page, err := client.GetConversationHistory(ctx, provider.GetConversationHistoryOptions{
			Channel:            resolved.ID,
			Cursor:             strings.TrimSpace(request.Params["cursor"]),
			Limit:              mustParseInt(request.Params["limit"]),
			Oldest:             strings.TrimSpace(request.Params["oldest"]),
			Latest:             strings.TrimSpace(request.Params["latest"]),
			Inclusive:          mustParseBool(request.Params["inclusive"]),
			IncludeAllMetadata: mustParseBool(request.Params["include_all_metadata"]),
		})
		if err != nil {
			return Result{}, err
		}

		items := make([]map[string]any, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, selectFields(messageToObject(item), columns))
		}
		return Result{
			Operation: "history",
			Kind:      ResultKindList,
			Items:     items,
			Columns:   columns,
			Page:      historyPageMap(resolved, page),
		}, nil
	case "replies":
		client, err := e.readProvider(provider.OperationReplies)
		if err != nil {
			return Result{}, err
		}

		threadTs := strings.TrimSpace(request.Params["ts"])
		if threadTs == "" {
			return Result{}, fmt.Errorf("replies() requires ts=...")
		}

		resolved, err := e.resolveConversationRef(ctx, client, request)
		if err != nil {
			return Result{}, err
		}

		page, err := client.GetConversationReplies(ctx, provider.GetConversationRepliesOptions{
			Channel:            resolved.ID,
			Ts:                 threadTs,
			Cursor:             strings.TrimSpace(request.Params["cursor"]),
			Limit:              mustParseInt(request.Params["limit"]),
			Oldest:             strings.TrimSpace(request.Params["oldest"]),
			Latest:             strings.TrimSpace(request.Params["latest"]),
			Inclusive:          mustParseBool(request.Params["inclusive"]),
			IncludeAllMetadata: mustParseBool(request.Params["include_all_metadata"]),
		})
		if err != nil {
			return Result{}, err
		}

		items := make([]map[string]any, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, selectFields(messageToObject(item), columns))
		}
		return Result{
			Operation: "replies",
			Kind:      ResultKindList,
			Items:     items,
			Columns:   columns,
			Page:      threadPageMap(resolved, threadTs, page),
		}, nil
	case "search_messages":
		client, err := e.readProvider(provider.OperationSearchMessages)
		if err != nil {
			return Result{}, err
		}

		queryText := strings.TrimSpace(request.Positional)
		if queryText == "" {
			queryText = strings.TrimSpace(request.Params["query"])
		}
		if queryText == "" {
			return Result{}, fmt.Errorf("search_messages() requires a positional query string or query=...")
		}

		page, err := client.SearchMessages(ctx, provider.SearchMessagesOptions{
			Query:     queryText,
			Count:     mustParseInt(request.Params["count"]),
			Page:      mustParseInt(request.Params["page"]),
			Cursor:    strings.TrimSpace(request.Params["cursor"]),
			Sort:      strings.TrimSpace(request.Params["sort"]),
			SortDir:   strings.TrimSpace(request.Params["sort_dir"]),
			Highlight: mustParseBool(request.Params["highlight"]),
			TeamID:    strings.TrimSpace(request.Params["team_id"]),
		})
		if err != nil {
			return Result{}, err
		}

		items := make([]map[string]any, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, selectFields(searchMessageToObject(item), columns))
		}
		return Result{
			Operation: "search_messages",
			Kind:      ResultKindList,
			Items:     items,
			Columns:   columns,
			Page:      searchPageMap(page),
		}, nil
	case "search_context":
		client, err := e.readProvider(provider.OperationSearchContext)
		if err != nil {
			return Result{}, err
		}

		queryText := strings.TrimSpace(request.Positional)
		if queryText == "" {
			queryText = strings.TrimSpace(request.Params["query"])
		}
		if queryText == "" {
			return Result{}, fmt.Errorf("search_context() requires a positional query string or query=...")
		}

		page, err := client.SearchContext(ctx, provider.SearchContextOptions{
			Query:                   queryText,
			ActionToken:             strings.TrimSpace(request.Params["action_token"]),
			ChannelTypes:            strings.TrimSpace(request.Params["channel_types"]),
			ContentTypes:            strings.TrimSpace(request.Params["content_types"]),
			IncludeBots:             mustParseBool(request.Params["include_bots"]),
			IncludeDeletedUsers:     mustParseBool(request.Params["include_deleted_users"]),
			Before:                  mustParseInt64(request.Params["before"]),
			After:                   mustParseInt64(request.Params["after"]),
			IncludeContextMessages:  mustParseBool(request.Params["include_context_messages"]),
			ContextChannelID:        strings.TrimSpace(request.Params["context_channel_id"]),
			Cursor:                  strings.TrimSpace(request.Params["cursor"]),
			Limit:                   mustParseInt(request.Params["limit"]),
			Sort:                    strings.TrimSpace(request.Params["sort"]),
			SortDir:                 strings.TrimSpace(request.Params["sort_dir"]),
			IncludeMessageBlocks:    mustParseBool(request.Params["include_message_blocks"]),
			Highlight:               mustParseBool(request.Params["highlight"]),
			TermClauses:             strings.TrimSpace(request.Params["term_clauses"]),
			Modifiers:               strings.TrimSpace(request.Params["modifiers"]),
			IncludeArchivedChannels: mustParseBool(request.Params["include_archived_channels"]),
			DisableSemanticSearch:   mustParseBool(request.Params["disable_semantic_search"]),
		})
		if err != nil {
			return Result{}, err
		}

		items := make([]map[string]any, 0, len(page.Messages)+len(page.Files)+len(page.Channels)+len(page.Users))
		for _, item := range page.Messages {
			items = append(items, selectFields(searchContextMessageToObject(item), columns))
		}
		for _, item := range page.Files {
			items = append(items, selectFields(searchContextFileToObject(item), columns))
		}
		for _, item := range page.Channels {
			items = append(items, selectFields(searchContextChannelToObject(item), columns))
		}
		for _, item := range page.Users {
			items = append(items, selectFields(searchContextUserToObject(item), columns))
		}
		return Result{
			Operation: "search_context",
			Kind:      ResultKindList,
			Items:     items,
			Columns:   columns,
			Page:      searchContextPageMap(page),
		}, nil
	case "users":
		client, err := e.readProvider(provider.OperationUsers)
		if err != nil {
			return Result{}, err
		}

		page, err := client.ListUsers(ctx, provider.ListUsersOptions{
			Cursor:        strings.TrimSpace(request.Params["cursor"]),
			Limit:         mustParseInt(request.Params["limit"]),
			IncludeLocale: mustParseBool(request.Params["include_locale"]),
			TeamID:        strings.TrimSpace(request.Params["team_id"]),
		})
		if err != nil {
			return Result{}, err
		}

		items := make([]map[string]any, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, selectFields(userToObject(item), columns))
		}
		return Result{
			Operation: "users",
			Kind:      ResultKindList,
			Items:     items,
			Columns:   columns,
			Page:      pageMap(page.NextCursor),
		}, nil
	default:
		return Result{}, fmt.Errorf("unsupported query operation %q", request.Operation)
	}
}

func (e *Engine) loadAttachments() ([]attachments.Attachment, error) {
	items, _, err := attachments.LoadManifest(e.runtime.ManifestPath, e.runtime.Getenv)
	return items, err
}

func (e *Engine) publishAttachment(item attachments.Attachment) (attachments.Attachment, error) {
	if e.runtime.PublishAttachment == nil {
		return attachments.Attachment{}, fmt.Errorf("%w: publisher is not configured", attachments.ErrAttachmentAlias)
	}
	return e.runtime.PublishAttachment(item)
}

func (e *Engine) publishAttachments(items []attachments.Attachment) ([]attachments.Attachment, error) {
	if e.runtime.PublishAttachments == nil {
		return nil, fmt.Errorf("%w: batch publisher is not configured", attachments.ErrAttachmentAlias)
	}
	return e.runtime.PublishAttachments(items)
}

func (e *Engine) provider() (provider.ReadProvider, error) {
	if e.runtime.ReadProvider == nil {
		return nil, fmt.Errorf("Slack read provider is not configured")
	}
	return e.runtime.ReadProvider()
}

func (e *Engine) readProvider(operation provider.Operation) (provider.ReadProvider, error) {
	readProvider, err := e.provider()
	if err != nil {
		return nil, err
	}
	if err := provider.RequireOperation(readProvider, operation); err != nil {
		return nil, err
	}
	return readProvider, nil
}

func (e *Engine) schemaObject() map[string]any {
	operations := make([]string, 0, len(operationMetadataMap))
	for name := range operationMetadataMap {
		operations = append(operations, name)
	}
	sort.Strings(operations)

	entities := map[string]entitySchema{}
	for key, value := range schemas {
		entities[key] = value
	}

	return map[string]any{
		"operations":        operations,
		"entities":          entities,
		"operationMetadata": operationMetadataMap,
	}
}

func structToObject(value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode provider contract: %w", err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, fmt.Errorf("decode provider contract: %w", err)
	}
	return object, nil
}

func authTestToObject(result provider.AuthTestResult) map[string]any {
	return map[string]any{
		"url":           result.URL,
		"team":          result.Team,
		"user":          result.User,
		"team_id":       result.TeamID,
		"user_id":       result.UserID,
		"bot_id":        result.BotID,
		"enterprise_id": result.EnterpriseID,
	}
}

func searchInfoToObject(result provider.SearchInfoResult) map[string]any {
	return map[string]any{
		"is_ai_search_enabled": result.IsAISearchEnabled,
	}
}

func conversationToObject(item provider.Conversation) map[string]any {
	return map[string]any{
		"id":              item.ID,
		"name":            item.Name,
		"name_normalized": item.NameNormalized,
		"is_channel":      item.IsChannel,
		"is_group":        item.IsGroup,
		"is_im":           item.IsIM,
		"is_private":      item.IsPrivate,
		"is_archived":     item.IsArchived,
		"is_general":      item.IsGeneral,
		"is_member":       item.IsMember,
		"is_mpim":         item.IsMPIM,
		"num_members":     item.NumMembers,
		"user":            item.User,
		"topic":           item.Topic.Value,
		"purpose":         item.Purpose.Value,
		"creator":         item.Creator,
		"context_team_id": item.ContextTeamID,
		"locale":          item.Locale,
		"created":         item.Created,
		"latest_user":     item.Latest.User,
		"latest_text":     item.Latest.Text,
		"latest_ts":       item.Latest.Ts,
	}
}

func messageToObject(item provider.Message) map[string]any {
	return map[string]any{
		"type":              item.Type,
		"subtype":           item.Subtype,
		"user":              item.User,
		"username":          item.Username,
		"text":              item.Text,
		"ts":                item.Ts,
		"thread_ts":         item.ThreadTs,
		"parent_user_id":    item.ParentUserID,
		"bot_id":            item.BotID,
		"reply_count":       item.ReplyCount,
		"reply_users_count": item.ReplyUsersCount,
		"latest_reply":      item.LatestReply,
		"reply_users":       item.ReplyUsers,
	}
}

func searchMessageToObject(item provider.SearchMessage) map[string]any {
	return map[string]any{
		"type":               item.Type,
		"user":               item.User,
		"username":           item.Username,
		"text":               item.Text,
		"ts":                 item.Ts,
		"team":               item.Team,
		"iid":                item.IID,
		"permalink":          item.Permalink,
		"channel_id":         item.Channel.ID,
		"channel_name":       item.Channel.Name,
		"channel_is_private": item.Channel.IsPrivate,
		"channel_is_mpim":    item.Channel.IsMPIM,
		"channel_is_shared":  item.Channel.IsShared,
	}
}

func searchContextMessageToObject(item provider.SearchContextMessage) map[string]any {
	return map[string]any{
		"content_type":         "message",
		"team_id":              item.TeamID,
		"author_name":          item.AuthorName,
		"author_user_id":       item.AuthorUserID,
		"channel_id":           item.ChannelID,
		"channel_name":         item.ChannelName,
		"message_ts":           item.MessageTS,
		"content":              item.Content,
		"permalink":            item.Permalink,
		"is_author_bot":        item.IsAuthorBot,
		"context_before_count": len(item.ContextMessages.Before),
		"context_after_count":  len(item.ContextMessages.After),
		"blocks":               item.Blocks,
		"context_messages":     item.ContextMessages,
	}
}

func searchContextFileToObject(item provider.SearchContextFile) map[string]any {
	return map[string]any{
		"content_type":     "file",
		"team_id":          item.TeamID,
		"author_name":      item.AuthorName,
		"author_user_id":   item.AuthorUserID,
		"uploader_user_id": item.UploaderUserID,
		"file_id":          item.FileID,
		"title":            item.Title,
		"file_type":        item.FileType,
		"content":          item.Content,
		"permalink":        item.Permalink,
		"date_created":     item.DateCreated,
		"date_updated":     item.DateUpdated,
	}
}

func searchContextChannelToObject(item provider.SearchContextChannel) map[string]any {
	return map[string]any{
		"content_type":    "channel",
		"team_id":         item.TeamID,
		"creator_user_id": item.CreatorUserID,
		"creator_name":    item.CreatorName,
		"name":            item.Name,
		"topic":           item.Topic,
		"purpose":         item.Purpose,
		"permalink":       item.Permalink,
		"date_created":    item.DateCreated,
		"date_updated":    item.DateUpdated,
	}
}

func searchContextUserToObject(item provider.SearchContextUser) map[string]any {
	return map[string]any{
		"content_type": "user",
		"team_id":      item.TeamID,
		"user_id":      item.UserID,
		"name":         item.Name,
		"real_name":    item.RealName,
		"title":        item.Title,
		"email":        item.Email,
		"permalink":    item.Permalink,
		"is_bot":       item.IsBot,
		"is_deleted":   item.IsDeleted,
	}
}

func userToObject(item provider.User) map[string]any {
	displayName := item.Profile.DisplayName
	if displayName == "" {
		displayName = item.Name
	}
	realName := item.RealName
	if realName == "" {
		realName = item.Profile.RealName
	}

	return map[string]any{
		"id":                  item.ID,
		"team_id":             item.TeamID,
		"name":                item.Name,
		"real_name":           realName,
		"display_name":        displayName,
		"email":               item.Profile.Email,
		"deleted":             item.Deleted,
		"is_bot":              item.IsBot,
		"is_app_user":         item.IsAppUser,
		"is_admin":            item.IsAdmin,
		"is_owner":            item.IsOwner,
		"is_restricted":       item.IsRestricted,
		"is_ultra_restricted": item.IsUltraRestricted,
	}
}

func pageMap(nextCursor string) map[string]any {
	if strings.TrimSpace(nextCursor) == "" {
		return nil
	}
	return map[string]any{"next_cursor": strings.TrimSpace(nextCursor)}
}

type conversationRef struct {
	ID   string
	Name string
}

func (e *Engine) resolveConversation(ctx context.Context, client provider.ReadProvider, request Request) (provider.Conversation, error) {
	resolved, err := e.resolveConversationRef(ctx, client, request)
	if err != nil {
		return provider.Conversation{}, err
	}

	return client.GetConversation(ctx, provider.GetConversationOptions{
		Channel:           resolved.ID,
		IncludeLocale:     mustParseBool(request.Params["include_locale"]),
		IncludeNumMembers: mustParseBool(request.Params["include_num_members"]),
	})
}

func (e *Engine) resolveConversationRef(ctx context.Context, client provider.ReadProvider, request Request) (conversationRef, error) {
	ref := conversationRequestRef(request)
	if ref == "" {
		return conversationRef{}, fmt.Errorf("%s() requires a conversation id or name", request.Operation)
	}

	if looksLikeConversationID(ref) {
		return conversationRef{ID: strings.ToUpper(ref)}, nil
	}

	cursor := ""
	for {
		page, err := client.ListConversations(ctx, provider.ListConversationsOptions{
			Cursor:          cursor,
			Limit:           200,
			Types:           strings.TrimSpace(request.Params["types"]),
			ExcludeArchived: mustParseBool(request.Params["exclude_archived"]),
			TeamID:          strings.TrimSpace(request.Params["team_id"]),
		})
		if err != nil {
			return conversationRef{}, err
		}

		for _, item := range page.Items {
			if matchesConversationRef(item, ref) {
				return conversationRef{ID: item.ID, Name: item.Name}, nil
			}
		}

		if strings.TrimSpace(page.NextCursor) == "" {
			break
		}
		cursor = page.NextCursor
	}

	return conversationRef{}, fmt.Errorf("conversation %q not found", ref)
}

func conversationRequestRef(request Request) string {
	for _, candidate := range []string{
		strings.TrimSpace(request.Positional),
		strings.TrimSpace(request.Params["conversation"]),
		strings.TrimSpace(request.Params["ref"]),
		strings.TrimSpace(request.Params["channel"]),
	} {
		if candidate != "" {
			return normalizeConversationRef(candidate)
		}
	}
	return ""
}

func normalizeConversationRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "#")
	return strings.TrimSpace(ref)
}

func looksLikeConversationID(ref string) bool {
	ref = strings.ToUpper(strings.TrimSpace(ref))
	if len(ref) < 2 {
		return false
	}
	switch ref[0] {
	case 'C', 'D', 'G':
	default:
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

func matchesConversationRef(item provider.Conversation, ref string) bool {
	ref = normalizeConversationRef(ref)
	return strings.EqualFold(item.ID, ref) ||
		strings.EqualFold(item.Name, ref) ||
		strings.EqualFold(item.NameNormalized, ref)
}

func historyPageMap(resolved conversationRef, page provider.ConversationHistoryPage) map[string]any {
	result := map[string]any{
		"conversation_id": resolved.ID,
	}
	if resolved.Name != "" {
		result["conversation_name"] = resolved.Name
	}
	if strings.TrimSpace(page.NextCursor) != "" {
		result["next_cursor"] = strings.TrimSpace(page.NextCursor)
	}
	if page.HasMore {
		result["has_more"] = true
	}
	if strings.TrimSpace(page.Latest) != "" {
		result["latest"] = strings.TrimSpace(page.Latest)
	}
	return result
}

func threadPageMap(resolved conversationRef, threadTs string, page provider.ConversationHistoryPage) map[string]any {
	result := historyPageMap(resolved, page)
	if result == nil {
		result = map[string]any{}
	}
	result["thread_ts"] = strings.TrimSpace(threadTs)
	return result
}

func searchPageMap(page provider.SearchMessagesPage) map[string]any {
	result := map[string]any{
		"query":  page.Query,
		"legacy": true,
	}
	if page.Total > 0 {
		result["total"] = page.Total
	}
	if page.Pagination.Page > 0 {
		result["page"] = page.Pagination.Page
	}
	if page.Pagination.PageCount > 0 {
		result["page_count"] = page.Pagination.PageCount
	}
	if page.Pagination.PerPage > 0 {
		result["per_page"] = page.Pagination.PerPage
	}
	if page.Pagination.TotalCount > 0 {
		result["total_count"] = page.Pagination.TotalCount
	}
	if strings.TrimSpace(page.NextCursor) != "" {
		result["next_cursor"] = strings.TrimSpace(page.NextCursor)
	}
	return result
}

func searchContextPageMap(page provider.SearchContextPage) map[string]any {
	result := map[string]any{
		"query":            page.Query,
		"messages_count":   len(page.Messages),
		"files_count":      len(page.Files),
		"channels_count":   len(page.Channels),
		"users_count":      len(page.Users),
		"real_time_search": true,
	}
	if strings.TrimSpace(page.NextCursor) != "" {
		result["next_cursor"] = strings.TrimSpace(page.NextCursor)
	}
	return result
}

var operationEntity = map[string]string{
	"attachments":     "attachment",
	"attachment":      "attachment",
	"auth_test":       "auth_test",
	"search_info":     "search_info",
	"conversations":   "conversation",
	"conversation":    "conversation",
	"history":         "message",
	"replies":         "message",
	"search_messages": "search_match",
	"search_context":  "search_context_result",
	"users":           "user",
}

func resolveRequestColumns(request Request) ([]string, error) {
	operation := strings.ToLower(request.Operation)
	if (operation == "schema" || operation == "provider_capabilities") && len(request.Fields) != 0 {
		return nil, fmt.Errorf("%s() does not support field projections", operation)
	}
	entity, ok := operationEntity[operation]
	if !ok {
		return nil, nil
	}
	return resolveColumns(entity, request.Fields)
}

func resolveColumns(entity string, requested []string) ([]string, error) {
	schema, ok := schemas[entity]
	if !ok {
		return nil, fmt.Errorf("unknown entity schema %q", entity)
	}
	if len(requested) == 0 {
		return append([]string(nil), schema.Presets["default"]...), nil
	}

	fields := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, field := range requested {
		if preset, ok := schema.Presets[field]; ok {
			for _, expanded := range preset {
				if !seen[expanded] {
					fields = append(fields, expanded)
					seen[expanded] = true
				}
			}
			continue
		}
		if !contains(schema.Fields, field) {
			return nil, fmt.Errorf("unsupported field %q for %s", field, entity)
		}
		if !seen[field] {
			fields = append(fields, field)
			seen[field] = true
		}
	}
	return fields, nil
}

func selectFields(object map[string]any, columns []string) map[string]any {
	selected := make(map[string]any, len(columns))
	for _, column := range columns {
		selected[column] = object[column]
	}
	return selected
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func orderedColumns(object map[string]any, preferred []string) []string {
	result := make([]string, 0, len(object))
	seen := map[string]bool{}
	for _, candidate := range preferred {
		if _, ok := object[candidate]; ok {
			result = append(result, candidate)
			seen[candidate] = true
		}
	}
	for _, candidate := range sortedObjectKeys(object) {
		if !seen[candidate] {
			result = append(result, candidate)
		}
	}
	return result
}

func sortedObjectKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mustParseInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func mustParseInt64(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
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
