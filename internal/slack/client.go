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
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL       = "https://slack.com/api"
	maxRetryAfterSeconds = int64(60)
)

type APIError struct {
	StatusCode int
	Code       string
	Needed     string
	Provided   string
}

type RateLimitError struct {
	Method            string
	Workspace         string
	StatusCode        int
	RetryAfterSeconds int64
	Attempt           int
	Exhausted         bool
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("slack rate limited: method=%s status=%d retry_after_seconds=%d attempt=%d exhausted=%t", e.Method, e.StatusCode, e.RetryAfterSeconds, e.Attempt, e.Exhausted)
}

type RateLimitDecodeError struct {
	Method    string
	Workspace string
	Attempt   int
}

type RateLimitWaitError struct {
	Method               string
	Workspace            string
	Attempt              int
	RetryAfterSeconds    int64
	MaxRetryAfterSeconds int64
}

func (e *RateLimitWaitError) Error() string {
	return fmt.Sprintf("slack Retry-After exceeds wait bound: method=%s retry_after_seconds=%d max_retry_after_seconds=%d attempt=%d", e.Method, e.RetryAfterSeconds, e.MaxRetryAfterSeconds, e.Attempt)
}

func (e *RateLimitDecodeError) Error() string {
	return fmt.Sprintf("decode Slack Retry-After metadata: method=%s attempt=%d", e.Method, e.Attempt)
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("slack api request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("slack api error: %s", e.Code)
}

type Client struct {
	readTransport  JSONTransport
	writeTransport JSONTransport
}

type JSONRequest struct {
	HTTPMethod string
	Method     string
	Query      url.Values
	Body       url.Values
	Write      bool
}

type JSONTransport interface {
	DoJSON(ctx context.Context, request JSONRequest, out any) error
}

type apiJSONTransport struct {
	httpClient *http.Client
	baseURL    string
	token      string
	workspace  string
	sleep      func(context.Context, time.Duration) error
}

var ErrWriteTransportUnavailable = errors.New("write_transport_unavailable")

type AuthTestResult struct {
	URL          string `json:"url,omitempty"`
	Team         string `json:"team,omitempty"`
	User         string `json:"user,omitempty"`
	TeamID       string `json:"team_id,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	BotID        string `json:"bot_id,omitempty"`
	EnterpriseID string `json:"enterprise_id,omitempty"`
}

type Message struct {
	Type            string   `json:"type,omitempty"`
	Subtype         string   `json:"subtype,omitempty"`
	User            string   `json:"user,omitempty"`
	Username        string   `json:"username,omitempty"`
	Text            string   `json:"text,omitempty"`
	Ts              string   `json:"ts,omitempty"`
	ThreadTs        string   `json:"thread_ts,omitempty"`
	ParentUserID    string   `json:"parent_user_id,omitempty"`
	BotID           string   `json:"bot_id,omitempty"`
	ReplyCount      int      `json:"reply_count,omitempty"`
	ReplyUsersCount int      `json:"reply_users_count,omitempty"`
	LatestReply     string   `json:"latest_reply,omitempty"`
	ReplyUsers      []string `json:"reply_users,omitempty"`
}

type Conversation struct {
	ID             string            `json:"id"`
	Name           string            `json:"name,omitempty"`
	NameNormalized string            `json:"name_normalized,omitempty"`
	IsChannel      bool              `json:"is_channel,omitempty"`
	IsGroup        bool              `json:"is_group,omitempty"`
	IsIM           bool              `json:"is_im,omitempty"`
	IsPrivate      bool              `json:"is_private,omitempty"`
	IsArchived     bool              `json:"is_archived,omitempty"`
	IsGeneral      bool              `json:"is_general,omitempty"`
	IsMember       bool              `json:"is_member,omitempty"`
	IsMPIM         bool              `json:"is_mpim,omitempty"`
	NumMembers     int               `json:"num_members,omitempty"`
	User           string            `json:"user,omitempty"`
	Topic          ConversationField `json:"topic,omitempty"`
	Purpose        ConversationField `json:"purpose,omitempty"`
	ContextTeamID  string            `json:"context_team_id,omitempty"`
	Creator        string            `json:"creator,omitempty"`
	Locale         string            `json:"locale,omitempty"`
	Created        int64             `json:"created,omitempty"`
	Latest         Message           `json:"latest,omitempty"`
}

type ConversationField struct {
	Value string `json:"value,omitempty"`
}

type ListConversationsOptions struct {
	Cursor          string
	Limit           int
	Types           string
	ExcludeArchived bool
	TeamID          string
}

type GetConversationOptions struct {
	Channel           string
	IncludeLocale     bool
	IncludeNumMembers bool
}

type ConversationPage struct {
	Items      []Conversation `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type User struct {
	ID                string      `json:"id"`
	TeamID            string      `json:"team_id,omitempty"`
	Name              string      `json:"name,omitempty"`
	RealName          string      `json:"real_name,omitempty"`
	Deleted           bool        `json:"deleted,omitempty"`
	IsBot             bool        `json:"is_bot,omitempty"`
	IsAppUser         bool        `json:"is_app_user,omitempty"`
	IsAdmin           bool        `json:"is_admin,omitempty"`
	IsOwner           bool        `json:"is_owner,omitempty"`
	IsRestricted      bool        `json:"is_restricted,omitempty"`
	IsUltraRestricted bool        `json:"is_ultra_restricted,omitempty"`
	Profile           UserProfile `json:"profile"`
}

type UserProfile struct {
	DisplayName string `json:"display_name,omitempty"`
	RealName    string `json:"real_name,omitempty"`
	Email       string `json:"email,omitempty"`
}

type ListUsersOptions struct {
	Cursor        string
	Limit         int
	IncludeLocale bool
	TeamID        string
}

type GetConversationHistoryOptions struct {
	Channel            string
	Cursor             string
	Limit              int
	Oldest             string
	Latest             string
	Inclusive          bool
	IncludeAllMetadata bool
}

type GetConversationRepliesOptions struct {
	Channel            string
	Ts                 string
	Cursor             string
	Limit              int
	Oldest             string
	Latest             string
	Inclusive          bool
	IncludeAllMetadata bool
}

type UserPage struct {
	Items      []User `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type ConversationHistoryPage struct {
	Items      []Message `json:"items"`
	HasMore    bool      `json:"has_more,omitempty"`
	Latest     string    `json:"latest,omitempty"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type SearchChannel struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	IsPrivate bool   `json:"is_private,omitempty"`
	IsMPIM    bool   `json:"is_mpim,omitempty"`
	IsShared  bool   `json:"is_shared,omitempty"`
}

type SearchMessage struct {
	Type      string        `json:"type,omitempty"`
	User      string        `json:"user,omitempty"`
	Username  string        `json:"username,omitempty"`
	Text      string        `json:"text,omitempty"`
	Ts        string        `json:"ts,omitempty"`
	Team      string        `json:"team,omitempty"`
	IID       string        `json:"iid,omitempty"`
	Permalink string        `json:"permalink,omitempty"`
	Channel   SearchChannel `json:"channel"`
}

type SearchPagination struct {
	Page       int `json:"page,omitempty"`
	PageCount  int `json:"page_count,omitempty"`
	PerPage    int `json:"per_page,omitempty"`
	TotalCount int `json:"total_count,omitempty"`
}

type SearchMessagesOptions struct {
	Query     string
	Count     int
	Page      int
	Cursor    string
	Sort      string
	SortDir   string
	Highlight bool
	TeamID    string
}

type SearchMessagesPage struct {
	Query      string           `json:"query,omitempty"`
	Items      []SearchMessage  `json:"items"`
	Total      int              `json:"total,omitempty"`
	NextCursor string           `json:"next_cursor,omitempty"`
	Pagination SearchPagination `json:"pagination"`
}

type SearchInfoResult struct {
	IsAISearchEnabled bool `json:"is_ai_search_enabled"`
}

type SearchContextMessageContext struct {
	Text   string `json:"text,omitempty"`
	UserID string `json:"user_id,omitempty"`
	Ts     string `json:"ts,omitempty"`
	Blocks any    `json:"blocks,omitempty"`
}

type SearchContextMessageContexts struct {
	Before []SearchContextMessageContext `json:"before,omitempty"`
	After  []SearchContextMessageContext `json:"after,omitempty"`
}

type SearchContextMessage struct {
	AuthorName      string                       `json:"author_name,omitempty"`
	AuthorUserID    string                       `json:"author_user_id,omitempty"`
	TeamID          string                       `json:"team_id,omitempty"`
	ChannelID       string                       `json:"channel_id,omitempty"`
	ChannelName     string                       `json:"channel_name,omitempty"`
	MessageTS       string                       `json:"message_ts,omitempty"`
	Content         string                       `json:"content,omitempty"`
	IsAuthorBot     bool                         `json:"is_author_bot,omitempty"`
	Permalink       string                       `json:"permalink,omitempty"`
	Blocks          any                          `json:"blocks,omitempty"`
	ContextMessages SearchContextMessageContexts `json:"context_messages,omitempty"`
}

type SearchContextFile struct {
	UploaderUserID string `json:"uploader_user_id,omitempty"`
	AuthorUserID   string `json:"author_user_id,omitempty"`
	AuthorName     string `json:"author_name,omitempty"`
	TeamID         string `json:"team_id,omitempty"`
	FileID         string `json:"file_id,omitempty"`
	DateCreated    int64  `json:"date_created,omitempty"`
	DateUpdated    int64  `json:"date_updated,omitempty"`
	Title          string `json:"title,omitempty"`
	FileType       string `json:"file_type,omitempty"`
	Permalink      string `json:"permalink,omitempty"`
	Content        string `json:"content,omitempty"`
}

type SearchContextChannel struct {
	TeamID        string `json:"team_id,omitempty"`
	CreatorUserID string `json:"creator_user_id,omitempty"`
	CreatorName   string `json:"creator_name,omitempty"`
	DateCreated   int64  `json:"date_created,omitempty"`
	DateUpdated   int64  `json:"date_updated,omitempty"`
	Name          string `json:"name,omitempty"`
	Topic         string `json:"topic,omitempty"`
	Purpose       string `json:"purpose,omitempty"`
	Permalink     string `json:"permalink,omitempty"`
}

type SearchContextUser struct {
	TeamID    string `json:"team_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	Name      string `json:"name,omitempty"`
	RealName  string `json:"real_name,omitempty"`
	Title     string `json:"title,omitempty"`
	Email     string `json:"email,omitempty"`
	Permalink string `json:"permalink,omitempty"`
	IsBot     bool   `json:"is_bot,omitempty"`
	IsDeleted bool   `json:"is_deleted,omitempty"`
}

type SearchContextOptions struct {
	Query                   string
	ActionToken             string
	ChannelTypes            string
	ContentTypes            string
	IncludeBots             bool
	IncludeDeletedUsers     bool
	Before                  int64
	After                   int64
	IncludeContextMessages  bool
	ContextChannelID        string
	Cursor                  string
	Limit                   int
	Sort                    string
	SortDir                 string
	IncludeMessageBlocks    bool
	Highlight               bool
	TermClauses             string
	Modifiers               string
	IncludeArchivedChannels bool
	DisableSemanticSearch   bool
}

type SearchContextPage struct {
	Query      string                 `json:"query,omitempty"`
	Messages   []SearchContextMessage `json:"messages"`
	Files      []SearchContextFile    `json:"files"`
	Channels   []SearchContextChannel `json:"channels"`
	Users      []SearchContextUser    `json:"users"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

type PostMessageOptions struct {
	Channel        string
	Text           string
	ThreadTS       string
	ReplyBroadcast bool
}

type PostMessageResult struct {
	Channel string  `json:"channel,omitempty"`
	TS      string  `json:"ts,omitempty"`
	Message Message `json:"message"`
}

type UpdateMessageOptions struct {
	Channel string
	TS      string
	Text    string
}

type UpdateMessageResult struct {
	Channel string  `json:"channel,omitempty"`
	TS      string  `json:"ts,omitempty"`
	Text    string  `json:"text,omitempty"`
	Message Message `json:"message"`
}

type DeleteMessageOptions struct {
	Channel string
	TS      string
}

type DeleteMessageResult struct {
	Channel string `json:"channel,omitempty"`
	TS      string `json:"ts,omitempty"`
}

type AddReactionOptions struct {
	Channel   string
	Timestamp string
	Name      string
}

type RemoveReactionOptions struct {
	Channel   string
	Timestamp string
	Name      string
}

type apiEnvelope struct {
	OK               bool `json:"ok"`
	Error            string
	Needed           string `json:"needed,omitempty"`
	Provided         string `json:"provided,omitempty"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor,omitempty"`
	} `json:"response_metadata,omitempty"`
}

func NewClient(baseURL, token string, httpClient *http.Client) (*Client, error) {
	return NewClientForWorkspace(baseURL, token, httpClient, "")
}

func NewClientForWorkspace(baseURL, token string, httpClient *http.Client, workspace string) (*Client, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("slack access token is required")
	}

	transport := &apiJSONTransport{
		httpClient: httpClient,
		baseURL:    baseURL,
		token:      strings.TrimSpace(token),
		workspace:  strings.TrimSpace(workspace),
		sleep:      sleepWithContext,
	}
	return &Client{readTransport: transport, writeTransport: transport}, nil
}

func NewReadClient(transport JSONTransport) (*Client, error) {
	if transport == nil {
		return nil, fmt.Errorf("read transport is required")
	}
	return &Client{readTransport: transport}, nil
}

func (c *Client) AuthTest(ctx context.Context) (AuthTestResult, error) {
	var response struct {
		apiEnvelope
		URL          string `json:"url"`
		Team         string `json:"team"`
		User         string `json:"user"`
		TeamID       string `json:"team_id"`
		UserID       string `json:"user_id"`
		BotID        string `json:"bot_id"`
		EnterpriseID string `json:"enterprise_id"`
	}

	if err := c.doReadJSON(ctx, http.MethodPost, "auth.test", nil, nil, &response); err != nil {
		return AuthTestResult{}, err
	}

	return AuthTestResult{
		URL:          response.URL,
		Team:         response.Team,
		User:         response.User,
		TeamID:       response.TeamID,
		UserID:       response.UserID,
		BotID:        response.BotID,
		EnterpriseID: response.EnterpriseID,
	}, nil
}

func (c *Client) ListConversations(ctx context.Context, opts ListConversationsOptions) (ConversationPage, error) {
	query := url.Values{}
	if strings.TrimSpace(opts.Cursor) != "" {
		query.Set("cursor", strings.TrimSpace(opts.Cursor))
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if strings.TrimSpace(opts.Types) != "" {
		query.Set("types", strings.TrimSpace(opts.Types))
	}
	if opts.ExcludeArchived {
		query.Set("exclude_archived", "true")
	}
	if strings.TrimSpace(opts.TeamID) != "" {
		query.Set("team_id", strings.TrimSpace(opts.TeamID))
	}

	var response struct {
		apiEnvelope
		Channels []Conversation `json:"channels"`
	}
	if err := c.doReadJSON(ctx, http.MethodGet, "conversations.list", query, nil, &response); err != nil {
		return ConversationPage{}, err
	}

	return ConversationPage{
		Items:      response.Channels,
		NextCursor: response.ResponseMetadata.NextCursor,
	}, nil
}

func (c *Client) GetConversation(ctx context.Context, opts GetConversationOptions) (Conversation, error) {
	query := url.Values{}
	if strings.TrimSpace(opts.Channel) != "" {
		query.Set("channel", strings.TrimSpace(opts.Channel))
	}
	if opts.IncludeLocale {
		query.Set("include_locale", "true")
	}
	if opts.IncludeNumMembers {
		query.Set("include_num_members", "true")
	}

	var response struct {
		apiEnvelope
		Channel Conversation `json:"channel"`
	}
	if err := c.doReadJSON(ctx, http.MethodGet, "conversations.info", query, nil, &response); err != nil {
		return Conversation{}, err
	}

	return response.Channel, nil
}

func (c *Client) GetConversationHistory(ctx context.Context, opts GetConversationHistoryOptions) (ConversationHistoryPage, error) {
	query := url.Values{}
	if strings.TrimSpace(opts.Channel) != "" {
		query.Set("channel", strings.TrimSpace(opts.Channel))
	}
	if strings.TrimSpace(opts.Cursor) != "" {
		query.Set("cursor", strings.TrimSpace(opts.Cursor))
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if strings.TrimSpace(opts.Oldest) != "" {
		query.Set("oldest", strings.TrimSpace(opts.Oldest))
	}
	if strings.TrimSpace(opts.Latest) != "" {
		query.Set("latest", strings.TrimSpace(opts.Latest))
	}
	if opts.Inclusive {
		query.Set("inclusive", "true")
	}
	if opts.IncludeAllMetadata {
		query.Set("include_all_metadata", "true")
	}

	var response struct {
		apiEnvelope
		Messages []Message `json:"messages"`
		HasMore  bool      `json:"has_more"`
		Latest   string    `json:"latest"`
	}
	if err := c.doReadJSON(ctx, http.MethodGet, "conversations.history", query, nil, &response); err != nil {
		return ConversationHistoryPage{}, err
	}

	return ConversationHistoryPage{
		Items:      response.Messages,
		HasMore:    response.HasMore,
		Latest:     response.Latest,
		NextCursor: response.ResponseMetadata.NextCursor,
	}, nil
}

func (c *Client) GetConversationReplies(ctx context.Context, opts GetConversationRepliesOptions) (ConversationHistoryPage, error) {
	query := url.Values{}
	if strings.TrimSpace(opts.Channel) != "" {
		query.Set("channel", strings.TrimSpace(opts.Channel))
	}
	if strings.TrimSpace(opts.Ts) != "" {
		query.Set("ts", strings.TrimSpace(opts.Ts))
	}
	if strings.TrimSpace(opts.Cursor) != "" {
		query.Set("cursor", strings.TrimSpace(opts.Cursor))
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if strings.TrimSpace(opts.Oldest) != "" {
		query.Set("oldest", strings.TrimSpace(opts.Oldest))
	}
	if strings.TrimSpace(opts.Latest) != "" {
		query.Set("latest", strings.TrimSpace(opts.Latest))
	}
	if opts.Inclusive {
		query.Set("inclusive", "true")
	}
	if opts.IncludeAllMetadata {
		query.Set("include_all_metadata", "true")
	}

	var response struct {
		apiEnvelope
		Messages []Message `json:"messages"`
		HasMore  bool      `json:"has_more"`
		Latest   string    `json:"latest"`
	}
	if err := c.doReadJSON(ctx, http.MethodGet, "conversations.replies", query, nil, &response); err != nil {
		return ConversationHistoryPage{}, err
	}

	return ConversationHistoryPage{
		Items:      response.Messages,
		HasMore:    response.HasMore,
		Latest:     response.Latest,
		NextCursor: response.ResponseMetadata.NextCursor,
	}, nil
}

func (c *Client) ListUsers(ctx context.Context, opts ListUsersOptions) (UserPage, error) {
	query := url.Values{}
	if strings.TrimSpace(opts.Cursor) != "" {
		query.Set("cursor", strings.TrimSpace(opts.Cursor))
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.IncludeLocale {
		query.Set("include_locale", "true")
	}
	if strings.TrimSpace(opts.TeamID) != "" {
		query.Set("team_id", strings.TrimSpace(opts.TeamID))
	}

	var response struct {
		apiEnvelope
		Members []User `json:"members"`
	}
	if err := c.doReadJSON(ctx, http.MethodGet, "users.list", query, nil, &response); err != nil {
		return UserPage{}, err
	}

	return UserPage{
		Items:      response.Members,
		NextCursor: response.ResponseMetadata.NextCursor,
	}, nil
}

func (c *Client) SearchMessages(ctx context.Context, opts SearchMessagesOptions) (SearchMessagesPage, error) {
	query := url.Values{}
	if strings.TrimSpace(opts.Query) != "" {
		query.Set("query", strings.TrimSpace(opts.Query))
	}
	if opts.Count > 0 {
		query.Set("count", strconv.Itoa(opts.Count))
	}
	if opts.Page > 0 {
		query.Set("page", strconv.Itoa(opts.Page))
	}
	if strings.TrimSpace(opts.Cursor) != "" {
		query.Set("cursor", strings.TrimSpace(opts.Cursor))
	}
	if strings.TrimSpace(opts.Sort) != "" {
		query.Set("sort", strings.TrimSpace(opts.Sort))
	}
	if strings.TrimSpace(opts.SortDir) != "" {
		query.Set("sort_dir", strings.TrimSpace(opts.SortDir))
	}
	if opts.Highlight {
		query.Set("highlight", "true")
	}
	if strings.TrimSpace(opts.TeamID) != "" {
		query.Set("team_id", strings.TrimSpace(opts.TeamID))
	}

	var response struct {
		apiEnvelope
		Query    string `json:"query"`
		Messages struct {
			Matches    []SearchMessage `json:"matches"`
			Total      int             `json:"total"`
			Pagination struct {
				Page       int `json:"page"`
				PageCount  int `json:"page_count"`
				PerPage    int `json:"per_page"`
				TotalCount int `json:"total_count"`
			} `json:"pagination"`
			Paging struct {
				Page  int `json:"page"`
				Pages int `json:"pages"`
				Count int `json:"count"`
				Total int `json:"total"`
			} `json:"paging"`
		} `json:"messages"`
	}
	if err := c.doReadJSON(ctx, http.MethodGet, "search.messages", query, nil, &response); err != nil {
		return SearchMessagesPage{}, err
	}

	page := SearchPagination{
		Page:       response.Messages.Pagination.Page,
		PageCount:  response.Messages.Pagination.PageCount,
		PerPage:    response.Messages.Pagination.PerPage,
		TotalCount: response.Messages.Pagination.TotalCount,
	}
	if page.Page == 0 {
		page.Page = response.Messages.Paging.Page
	}
	if page.PageCount == 0 {
		page.PageCount = response.Messages.Paging.Pages
	}
	if page.PerPage == 0 {
		page.PerPage = response.Messages.Paging.Count
	}
	if page.TotalCount == 0 {
		page.TotalCount = response.Messages.Paging.Total
	}

	return SearchMessagesPage{
		Query:      response.Query,
		Items:      response.Messages.Matches,
		Total:      response.Messages.Total,
		NextCursor: response.ResponseMetadata.NextCursor,
		Pagination: page,
	}, nil
}

func (c *Client) SearchInfo(ctx context.Context) (SearchInfoResult, error) {
	var response struct {
		apiEnvelope
		IsAISearchEnabled bool `json:"is_ai_search_enabled"`
	}
	if err := c.doReadJSON(ctx, http.MethodPost, "assistant.search.info", nil, url.Values{}, &response); err != nil {
		return SearchInfoResult{}, err
	}

	return SearchInfoResult{
		IsAISearchEnabled: response.IsAISearchEnabled,
	}, nil
}

func (c *Client) SearchContext(ctx context.Context, opts SearchContextOptions) (SearchContextPage, error) {
	body := url.Values{}
	if strings.TrimSpace(opts.Query) != "" {
		body.Set("query", strings.TrimSpace(opts.Query))
	}
	if strings.TrimSpace(opts.ActionToken) != "" {
		body.Set("action_token", strings.TrimSpace(opts.ActionToken))
	}
	if strings.TrimSpace(opts.ChannelTypes) != "" {
		body.Set("channel_types", strings.TrimSpace(opts.ChannelTypes))
	}
	if strings.TrimSpace(opts.ContentTypes) != "" {
		body.Set("content_types", strings.TrimSpace(opts.ContentTypes))
	}
	if opts.IncludeBots {
		body.Set("include_bots", "true")
	}
	if opts.IncludeDeletedUsers {
		body.Set("include_deleted_users", "true")
	}
	if opts.Before > 0 {
		body.Set("before", strconv.FormatInt(opts.Before, 10))
	}
	if opts.After > 0 {
		body.Set("after", strconv.FormatInt(opts.After, 10))
	}
	if opts.IncludeContextMessages {
		body.Set("include_context_messages", "true")
	}
	if strings.TrimSpace(opts.ContextChannelID) != "" {
		body.Set("context_channel_id", strings.TrimSpace(opts.ContextChannelID))
	}
	if strings.TrimSpace(opts.Cursor) != "" {
		body.Set("cursor", strings.TrimSpace(opts.Cursor))
	}
	if opts.Limit > 0 {
		body.Set("limit", strconv.Itoa(opts.Limit))
	}
	if strings.TrimSpace(opts.Sort) != "" {
		body.Set("sort", strings.TrimSpace(opts.Sort))
	}
	if strings.TrimSpace(opts.SortDir) != "" {
		body.Set("sort_dir", strings.TrimSpace(opts.SortDir))
	}
	if opts.IncludeMessageBlocks {
		body.Set("include_message_blocks", "true")
	}
	if opts.Highlight {
		body.Set("highlight", "true")
	}
	if strings.TrimSpace(opts.TermClauses) != "" {
		body.Set("term_clauses", strings.TrimSpace(opts.TermClauses))
	}
	if strings.TrimSpace(opts.Modifiers) != "" {
		body.Set("modifiers", strings.TrimSpace(opts.Modifiers))
	}
	if opts.IncludeArchivedChannels {
		body.Set("include_archived_channels", "true")
	}
	if opts.DisableSemanticSearch {
		body.Set("disable_semantic_search", "true")
	}

	var response struct {
		apiEnvelope
		Results struct {
			Messages []SearchContextMessage `json:"messages"`
			Files    []SearchContextFile    `json:"files"`
			Channels []SearchContextChannel `json:"channels"`
			Users    []SearchContextUser    `json:"users"`
		} `json:"results"`
	}
	if err := c.doReadJSON(ctx, http.MethodPost, "assistant.search.context", nil, body, &response); err != nil {
		return SearchContextPage{}, err
	}

	return SearchContextPage{
		Query:      opts.Query,
		Messages:   response.Results.Messages,
		Files:      response.Results.Files,
		Channels:   response.Results.Channels,
		Users:      response.Results.Users,
		NextCursor: response.ResponseMetadata.NextCursor,
	}, nil
}

func (c *Client) PostMessage(ctx context.Context, opts PostMessageOptions) (PostMessageResult, error) {
	body := url.Values{}
	if strings.TrimSpace(opts.Channel) != "" {
		body.Set("channel", strings.TrimSpace(opts.Channel))
	}
	if strings.TrimSpace(opts.Text) != "" {
		body.Set("text", strings.TrimSpace(opts.Text))
	}
	if strings.TrimSpace(opts.ThreadTS) != "" {
		body.Set("thread_ts", strings.TrimSpace(opts.ThreadTS))
	}
	if opts.ReplyBroadcast {
		body.Set("reply_broadcast", "true")
	}

	var response struct {
		apiEnvelope
		Channel string  `json:"channel"`
		TS      string  `json:"ts"`
		Message Message `json:"message"`
	}
	if err := c.doWriteJSON(ctx, http.MethodPost, "chat.postMessage", nil, body, &response); err != nil {
		return PostMessageResult{}, err
	}

	return PostMessageResult{
		Channel: response.Channel,
		TS:      response.TS,
		Message: response.Message,
	}, nil
}

func (c *Client) UpdateMessage(ctx context.Context, opts UpdateMessageOptions) (UpdateMessageResult, error) {
	body := url.Values{}
	if strings.TrimSpace(opts.Channel) != "" {
		body.Set("channel", strings.TrimSpace(opts.Channel))
	}
	if strings.TrimSpace(opts.TS) != "" {
		body.Set("ts", strings.TrimSpace(opts.TS))
	}
	if strings.TrimSpace(opts.Text) != "" {
		body.Set("text", strings.TrimSpace(opts.Text))
	}

	var response struct {
		apiEnvelope
		Channel string  `json:"channel"`
		TS      string  `json:"ts"`
		Text    string  `json:"text"`
		Message Message `json:"message"`
	}
	if err := c.doWriteJSON(ctx, http.MethodPost, "chat.update", nil, body, &response); err != nil {
		return UpdateMessageResult{}, err
	}

	return UpdateMessageResult{
		Channel: response.Channel,
		TS:      response.TS,
		Text:    response.Text,
		Message: response.Message,
	}, nil
}

func (c *Client) DeleteMessage(ctx context.Context, opts DeleteMessageOptions) (DeleteMessageResult, error) {
	body := url.Values{}
	if strings.TrimSpace(opts.Channel) != "" {
		body.Set("channel", strings.TrimSpace(opts.Channel))
	}
	if strings.TrimSpace(opts.TS) != "" {
		body.Set("ts", strings.TrimSpace(opts.TS))
	}

	var response struct {
		apiEnvelope
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	if err := c.doWriteJSON(ctx, http.MethodPost, "chat.delete", nil, body, &response); err != nil {
		return DeleteMessageResult{}, err
	}

	return DeleteMessageResult{
		Channel: response.Channel,
		TS:      response.TS,
	}, nil
}

func (c *Client) AddReaction(ctx context.Context, opts AddReactionOptions) error {
	body := url.Values{}
	if strings.TrimSpace(opts.Channel) != "" {
		body.Set("channel", strings.TrimSpace(opts.Channel))
	}
	if strings.TrimSpace(opts.Timestamp) != "" {
		body.Set("timestamp", strings.TrimSpace(opts.Timestamp))
	}
	if strings.TrimSpace(opts.Name) != "" {
		body.Set("name", strings.TrimSpace(opts.Name))
	}

	var response apiEnvelope
	if err := c.doWriteJSON(ctx, http.MethodPost, "reactions.add", nil, body, &response); err != nil {
		return err
	}
	return nil
}

func (c *Client) RemoveReaction(ctx context.Context, opts RemoveReactionOptions) error {
	body := url.Values{}
	if strings.TrimSpace(opts.Channel) != "" {
		body.Set("channel", strings.TrimSpace(opts.Channel))
	}
	if strings.TrimSpace(opts.Timestamp) != "" {
		body.Set("timestamp", strings.TrimSpace(opts.Timestamp))
	}
	if strings.TrimSpace(opts.Name) != "" {
		body.Set("name", strings.TrimSpace(opts.Name))
	}

	var response apiEnvelope
	if err := c.doWriteJSON(ctx, http.MethodPost, "reactions.remove", nil, body, &response); err != nil {
		return err
	}
	return nil
}

func (c *Client) doReadJSON(ctx context.Context, httpMethod, method string, query, body url.Values, out any) error {
	if c.readTransport == nil {
		return fmt.Errorf("read transport is unavailable")
	}
	return c.readTransport.DoJSON(ctx, JSONRequest{HTTPMethod: httpMethod, Method: method, Query: query, Body: body}, out)
}

func (c *Client) doWriteJSON(ctx context.Context, httpMethod, method string, query, body url.Values, out any) error {
	if c.writeTransport == nil {
		return ErrWriteTransportUnavailable
	}
	return c.writeTransport.DoJSON(ctx, JSONRequest{HTTPMethod: httpMethod, Method: method, Query: query, Body: body, Write: true}, out)
}

func (t *apiJSONTransport) DoJSON(ctx context.Context, request JSONRequest, out any) error {
	maxAttempts := 3
	if request.Write {
		maxAttempts = 2
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, retryAfter, data, err := t.doJSONAttempt(ctx, request)
		if err != nil {
			return err
		}
		if status == http.StatusTooManyRequests {
			seconds, parseErr := strconv.ParseInt(strings.TrimSpace(retryAfter), 10, 32)
			if parseErr != nil || seconds < 0 {
				return &RateLimitDecodeError{Method: request.Method, Workspace: t.workspace, Attempt: attempt}
			}
			if seconds > maxRetryAfterSeconds {
				return &RateLimitWaitError{
					Method:               request.Method,
					Workspace:            t.workspace,
					Attempt:              attempt,
					RetryAfterSeconds:    seconds,
					MaxRetryAfterSeconds: maxRetryAfterSeconds,
				}
			}
			if attempt == maxAttempts {
				return &RateLimitError{Method: request.Method, Workspace: t.workspace, StatusCode: status, RetryAfterSeconds: seconds, Attempt: attempt, Exhausted: true}
			}
			if err := t.sleep(ctx, time.Duration(seconds)*time.Second); err != nil {
				return err
			}
			continue
		}
		return decodeSlackJSONResponse(status, data, out)
	}
	return fmt.Errorf("slack request exhausted without a response")
}

func (t *apiJSONTransport) doJSONAttempt(ctx context.Context, request JSONRequest) (int, string, []byte, error) {
	endpoint := t.baseURL + "/" + strings.TrimLeft(request.Method, "/")
	if len(request.Query) > 0 {
		endpoint += "?" + request.Query.Encode()
	}

	var payload io.Reader
	if request.Body != nil {
		payload = bytes.NewBufferString(request.Body.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, request.HTTPMethod, endpoint, payload)
	if err != nil {
		return 0, "", nil, fmt.Errorf("build slack request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+t.token)
	if request.Body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return 0, "", nil, fmt.Errorf("slack request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", nil, fmt.Errorf("read slack response: %w", err)
	}
	return resp.StatusCode, resp.Header.Get("Retry-After"), data, nil
}

func decodeSlackJSONResponse(statusCode int, data []byte, out any) error {
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode slack response: %w", err)
	}

	envelope, ok := out.(interface{ envelope() *apiEnvelope })
	if ok {
		meta := envelope.envelope()
		if !meta.OK {
			return &APIError{
				StatusCode: statusCode,
				Code:       meta.Error,
				Needed:     meta.Needed,
				Provided:   meta.Provided,
			}
		}
		return nil
	}

	metaBytes, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	var generic apiEnvelope
	if err := json.Unmarshal(metaBytes, &generic); err == nil && !generic.OK {
		return &APIError{
			StatusCode: statusCode,
			Code:       generic.Error,
			Needed:     generic.Needed,
			Provided:   generic.Provided,
		}
	}
	return nil
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *apiEnvelope) envelope() *apiEnvelope {
	return a
}
