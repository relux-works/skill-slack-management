package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/relux-works/skill-slack-management/internal/slack"
)

type WebAPITransport interface {
	AuthTest(context.Context) (slack.AuthTestResult, error)
	GetConversation(context.Context, slack.GetConversationOptions) (slack.Conversation, error)
	GetConversationHistory(context.Context, slack.GetConversationHistoryOptions) (slack.ConversationHistoryPage, error)
	GetConversationReplies(context.Context, slack.GetConversationRepliesOptions) (slack.ConversationHistoryPage, error)
	ListConversations(context.Context, slack.ListConversationsOptions) (slack.ConversationPage, error)
	ListUsers(context.Context, slack.ListUsersOptions) (slack.UserPage, error)
	SearchContext(context.Context, slack.SearchContextOptions) (slack.SearchContextPage, error)
	SearchInfo(context.Context) (slack.SearchInfoResult, error)
	SearchMessages(context.Context, slack.SearchMessagesOptions) (slack.SearchMessagesPage, error)
}

type WebAPIProvider struct {
	transportKind TransportKind
	factory       func() (WebAPITransport, error)
	once          sync.Once
	transport     WebAPITransport
	err           error
}

func NewWebAPIProvider(kind TransportKind, factory func() (WebAPITransport, error)) (*WebAPIProvider, error) {
	if kind != TransportAPI && kind != TransportBrowser {
		return nil, fmt.Errorf("unsupported Web API transport %q", kind)
	}
	if factory == nil {
		return nil, fmt.Errorf("Web API transport factory is required")
	}
	return &WebAPIProvider{transportKind: kind, factory: factory}, nil
}

func (p *WebAPIProvider) Capabilities() Capabilities {
	operations := allReadOperations
	if p.transportKind == TransportBrowser {
		operations = []Operation{
			OperationAuthTest,
			OperationConversations,
			OperationConversation,
			OperationHistory,
			OperationReplies,
			OperationSearchMessages,
			OperationUsers,
		}
	}
	capabilities := NewCapabilities(KindWebAPI, operations...)
	capabilities.Transport = p.transportKind
	capabilities.ReadOnly = p.transportKind == TransportBrowser
	return capabilities
}

func (p *WebAPIProvider) Diagnose(context.Context) Diagnostic {
	return Diagnostic{
		Provider:     KindWebAPI,
		Ready:        true,
		Capabilities: p.Capabilities(),
		Message:      "transport initialization is deferred until a supported read is executed",
	}
}

func (p *WebAPIProvider) getTransport() (WebAPITransport, error) {
	p.once.Do(func() { p.transport, p.err = p.factory() })
	if p.err != nil {
		return nil, p.err
	}
	if p.transport == nil {
		return nil, fmt.Errorf("Web API transport factory returned nil")
	}
	return p.transport, nil
}

func (p *WebAPIProvider) AuthTest(ctx context.Context) (AuthTestResult, error) {
	t, err := p.getTransport()
	if err != nil {
		return AuthTestResult{}, err
	}
	result, err := t.AuthTest(ctx)
	return normalizeAuthTest(result), err
}
func (p *WebAPIProvider) GetConversation(ctx context.Context, opts GetConversationOptions) (Conversation, error) {
	t, err := p.getTransport()
	if err != nil {
		return Conversation{}, err
	}
	result, err := t.GetConversation(ctx, slack.GetConversationOptions{
		Channel: opts.Channel, IncludeLocale: opts.IncludeLocale, IncludeNumMembers: opts.IncludeNumMembers,
	})
	return normalizeConversation(result), err
}
func (p *WebAPIProvider) GetConversationHistory(ctx context.Context, opts GetConversationHistoryOptions) (ConversationHistoryPage, error) {
	t, err := p.getTransport()
	if err != nil {
		return ConversationHistoryPage{}, err
	}
	result, err := t.GetConversationHistory(ctx, slack.GetConversationHistoryOptions{
		Channel: opts.Channel, Cursor: opts.Cursor, Limit: opts.Limit, Oldest: opts.Oldest, Latest: opts.Latest,
		Inclusive: opts.Inclusive, IncludeAllMetadata: opts.IncludeAllMetadata,
	})
	return normalizeHistoryPage(result), err
}
func (p *WebAPIProvider) GetConversationReplies(ctx context.Context, opts GetConversationRepliesOptions) (ConversationHistoryPage, error) {
	t, err := p.getTransport()
	if err != nil {
		return ConversationHistoryPage{}, err
	}
	result, err := t.GetConversationReplies(ctx, slack.GetConversationRepliesOptions{
		Channel: opts.Channel, Ts: opts.Ts, Cursor: opts.Cursor, Limit: opts.Limit, Oldest: opts.Oldest, Latest: opts.Latest,
		Inclusive: opts.Inclusive, IncludeAllMetadata: opts.IncludeAllMetadata,
	})
	return normalizeHistoryPage(result), err
}
func (p *WebAPIProvider) ListConversations(ctx context.Context, opts ListConversationsOptions) (ConversationPage, error) {
	t, err := p.getTransport()
	if err != nil {
		return ConversationPage{}, err
	}
	result, err := t.ListConversations(ctx, slack.ListConversationsOptions{
		Cursor: opts.Cursor, Limit: opts.Limit, Types: opts.Types, ExcludeArchived: opts.ExcludeArchived, TeamID: opts.TeamID,
	})
	return ConversationPage{Items: normalizeConversations(result.Items), NextCursor: result.NextCursor}, err
}
func (p *WebAPIProvider) ListUsers(ctx context.Context, opts ListUsersOptions) (UserPage, error) {
	t, err := p.getTransport()
	if err != nil {
		return UserPage{}, err
	}
	result, err := t.ListUsers(ctx, slack.ListUsersOptions{
		Cursor: opts.Cursor, Limit: opts.Limit, IncludeLocale: opts.IncludeLocale, TeamID: opts.TeamID,
	})
	return UserPage{Items: normalizeUsers(result.Items), NextCursor: result.NextCursor}, err
}
func (p *WebAPIProvider) SearchContext(ctx context.Context, opts SearchContextOptions) (SearchContextPage, error) {
	t, err := p.getTransport()
	if err != nil {
		return SearchContextPage{}, err
	}
	result, err := t.SearchContext(ctx, slack.SearchContextOptions{
		Query: opts.Query, ActionToken: opts.ActionToken, ChannelTypes: opts.ChannelTypes, ContentTypes: opts.ContentTypes,
		IncludeBots: opts.IncludeBots, IncludeDeletedUsers: opts.IncludeDeletedUsers, Before: opts.Before, After: opts.After,
		IncludeContextMessages: opts.IncludeContextMessages, ContextChannelID: opts.ContextChannelID, Cursor: opts.Cursor,
		Limit: opts.Limit, Sort: opts.Sort, SortDir: opts.SortDir, IncludeMessageBlocks: opts.IncludeMessageBlocks,
		Highlight: opts.Highlight, TermClauses: opts.TermClauses, Modifiers: opts.Modifiers,
		IncludeArchivedChannels: opts.IncludeArchivedChannels, DisableSemanticSearch: opts.DisableSemanticSearch,
	})
	return normalizeSearchContextPage(result), err
}
func (p *WebAPIProvider) SearchInfo(ctx context.Context) (SearchInfoResult, error) {
	t, err := p.getTransport()
	if err != nil {
		return SearchInfoResult{}, err
	}
	result, err := t.SearchInfo(ctx)
	return SearchInfoResult{IsAISearchEnabled: result.IsAISearchEnabled}, err
}
func (p *WebAPIProvider) SearchMessages(ctx context.Context, opts SearchMessagesOptions) (SearchMessagesPage, error) {
	t, err := p.getTransport()
	if err != nil {
		return SearchMessagesPage{}, err
	}
	result, err := t.SearchMessages(ctx, slack.SearchMessagesOptions{
		Query: opts.Query, Count: opts.Count, Page: opts.Page, Cursor: opts.Cursor, Sort: opts.Sort,
		SortDir: opts.SortDir, Highlight: opts.Highlight, TeamID: opts.TeamID,
	})
	return normalizeSearchMessagesPage(result), err
}

func normalizeAuthTest(value slack.AuthTestResult) AuthTestResult {
	return AuthTestResult{URL: value.URL, Team: value.Team, User: value.User, TeamID: value.TeamID, UserID: value.UserID, BotID: value.BotID, EnterpriseID: value.EnterpriseID}
}

func normalizeMessage(value slack.Message) Message {
	return Message{
		Type: value.Type, Subtype: value.Subtype, User: value.User, Username: value.Username, Text: value.Text,
		Ts: value.Ts, ThreadTs: value.ThreadTs, ParentUserID: value.ParentUserID, BotID: value.BotID,
		ReplyCount: value.ReplyCount, ReplyUsersCount: value.ReplyUsersCount, LatestReply: value.LatestReply,
		ReplyUsers: append([]string(nil), value.ReplyUsers...),
	}
}

func normalizeMessages(values []slack.Message) []Message {
	result := make([]Message, len(values))
	for idx, value := range values {
		result[idx] = normalizeMessage(value)
	}
	return result
}

func normalizeConversation(value slack.Conversation) Conversation {
	return Conversation{
		ID: value.ID, Name: value.Name, NameNormalized: value.NameNormalized, IsChannel: value.IsChannel,
		IsGroup: value.IsGroup, IsIM: value.IsIM, IsPrivate: value.IsPrivate, IsArchived: value.IsArchived,
		IsGeneral: value.IsGeneral, IsMember: value.IsMember, IsMPIM: value.IsMPIM, NumMembers: value.NumMembers,
		User: value.User, Topic: ConversationField{Value: value.Topic.Value}, Purpose: ConversationField{Value: value.Purpose.Value},
		ContextTeamID: value.ContextTeamID, Creator: value.Creator, Locale: value.Locale, Created: value.Created,
		Latest: normalizeMessage(value.Latest),
	}
}

func normalizeConversations(values []slack.Conversation) []Conversation {
	result := make([]Conversation, len(values))
	for idx, value := range values {
		result[idx] = normalizeConversation(value)
	}
	return result
}

func normalizeUser(value slack.User) User {
	return User{
		ID: value.ID, TeamID: value.TeamID, Name: value.Name, RealName: value.RealName, Deleted: value.Deleted,
		IsBot: value.IsBot, IsAppUser: value.IsAppUser, IsAdmin: value.IsAdmin, IsOwner: value.IsOwner,
		IsRestricted: value.IsRestricted, IsUltraRestricted: value.IsUltraRestricted,
		Profile: UserProfile{DisplayName: value.Profile.DisplayName, RealName: value.Profile.RealName, Email: value.Profile.Email},
	}
}

func normalizeUsers(values []slack.User) []User {
	result := make([]User, len(values))
	for idx, value := range values {
		result[idx] = normalizeUser(value)
	}
	return result
}

func normalizeHistoryPage(value slack.ConversationHistoryPage) ConversationHistoryPage {
	return ConversationHistoryPage{Items: normalizeMessages(value.Items), HasMore: value.HasMore, Latest: value.Latest, NextCursor: value.NextCursor}
}

func normalizeSearchMessagesPage(value slack.SearchMessagesPage) SearchMessagesPage {
	items := make([]SearchMessage, len(value.Items))
	for idx, item := range value.Items {
		items[idx] = SearchMessage{
			Type: item.Type, User: item.User, Username: item.Username, Text: item.Text, Ts: item.Ts,
			Team: item.Team, IID: item.IID, Permalink: item.Permalink,
			Channel: SearchChannel{ID: item.Channel.ID, Name: item.Channel.Name, IsPrivate: item.Channel.IsPrivate, IsMPIM: item.Channel.IsMPIM, IsShared: item.Channel.IsShared},
		}
	}
	return SearchMessagesPage{
		Query: value.Query, Items: items, Total: value.Total, NextCursor: value.NextCursor,
		Pagination: SearchPagination{Page: value.Pagination.Page, PageCount: value.Pagination.PageCount, PerPage: value.Pagination.PerPage, TotalCount: value.Pagination.TotalCount},
	}
}

func normalizeSearchContext(value slack.SearchContextMessageContext) SearchContextMessageContext {
	return SearchContextMessageContext{Text: value.Text, UserID: value.UserID, Ts: value.Ts, Blocks: value.Blocks}
}

func normalizeSearchContexts(values []slack.SearchContextMessageContext) []SearchContextMessageContext {
	result := make([]SearchContextMessageContext, len(values))
	for idx, value := range values {
		result[idx] = normalizeSearchContext(value)
	}
	return result
}

func normalizeSearchContextPage(value slack.SearchContextPage) SearchContextPage {
	result := SearchContextPage{Query: value.Query, NextCursor: value.NextCursor}
	for _, item := range value.Messages {
		result.Messages = append(result.Messages, SearchContextMessage{
			AuthorName: item.AuthorName, AuthorUserID: item.AuthorUserID, TeamID: item.TeamID, ChannelID: item.ChannelID,
			ChannelName: item.ChannelName, MessageTS: item.MessageTS, Content: item.Content, IsAuthorBot: item.IsAuthorBot,
			Permalink: item.Permalink, Blocks: item.Blocks,
			ContextMessages: SearchContextMessageContexts{Before: normalizeSearchContexts(item.ContextMessages.Before), After: normalizeSearchContexts(item.ContextMessages.After)},
		})
	}
	for _, item := range value.Files {
		result.Files = append(result.Files, SearchContextFile{
			UploaderUserID: item.UploaderUserID, AuthorUserID: item.AuthorUserID, AuthorName: item.AuthorName, TeamID: item.TeamID,
			FileID: item.FileID, DateCreated: item.DateCreated, DateUpdated: item.DateUpdated, Title: item.Title,
			FileType: item.FileType, Permalink: item.Permalink, Content: item.Content,
		})
	}
	for _, item := range value.Channels {
		result.Channels = append(result.Channels, SearchContextChannel{
			TeamID: item.TeamID, CreatorUserID: item.CreatorUserID, CreatorName: item.CreatorName,
			DateCreated: item.DateCreated, DateUpdated: item.DateUpdated, Name: item.Name, Topic: item.Topic,
			Purpose: item.Purpose, Permalink: item.Permalink,
		})
	}
	for _, item := range value.Users {
		result.Users = append(result.Users, SearchContextUser{
			TeamID: item.TeamID, UserID: item.UserID, Name: item.Name, RealName: item.RealName, Title: item.Title,
			Email: item.Email, Permalink: item.Permalink, IsBot: item.IsBot, IsDeleted: item.IsDeleted,
		})
	}
	return result
}
