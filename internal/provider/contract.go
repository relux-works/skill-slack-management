package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const ProtocolVersion = 1

type Kind string

const (
	KindWebAPI    Kind = "web-api"
	KindSlackdump Kind = "slackdump"
)

type TransportKind string

const (
	TransportAPI     TransportKind = "api"
	TransportBrowser TransportKind = "browser"
)

type Operation string

const (
	OperationAuthTest       Operation = "auth_test"
	OperationSearchInfo     Operation = "search_info"
	OperationConversations  Operation = "conversations"
	OperationConversation   Operation = "conversation"
	OperationHistory        Operation = "history"
	OperationReplies        Operation = "replies"
	OperationSearchMessages Operation = "search_messages"
	OperationSearchContext  Operation = "search_context"
	OperationUsers          Operation = "users"
)

var allReadOperations = []Operation{
	OperationAuthTest,
	OperationSearchInfo,
	OperationConversations,
	OperationConversation,
	OperationHistory,
	OperationReplies,
	OperationSearchMessages,
	OperationSearchContext,
	OperationUsers,
}

type Capabilities struct {
	ProtocolVersion int           `json:"protocol_version"`
	Provider        Kind          `json:"provider"`
	AdapterVersion  int           `json:"adapter_version"`
	ReadOnly        bool          `json:"read_only"`
	ExternalAuth    bool          `json:"external_authorization"`
	Transport       TransportKind `json:"transport,omitempty"`
	Operations      []Operation   `json:"operations"`
	CompatibleWith  string        `json:"compatible_with,omitempty"`
	Authorization   string        `json:"authorization_mode,omitempty"`
}

func NewCapabilities(kind Kind, operations ...Operation) Capabilities {
	operations = append([]Operation(nil), operations...)
	sort.Slice(operations, func(i, j int) bool { return operations[i] < operations[j] })
	return Capabilities{
		ProtocolVersion: ProtocolVersion,
		Provider:        kind,
		AdapterVersion:  1,
		ReadOnly:        true,
		Operations:      operations,
	}
}

func (c Capabilities) Supports(operation Operation) bool {
	for _, supported := range c.Operations {
		if supported == operation {
			return true
		}
	}
	return false
}

var ErrCapabilityUnsupported = errors.New("provider_capability_unsupported")

type CapabilityError struct {
	Provider  Kind
	Operation Operation
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("%s: provider %q does not support operation %q", ErrCapabilityUnsupported, e.Provider, e.Operation)
}

func (e *CapabilityError) Unwrap() error { return ErrCapabilityUnsupported }

func RequireOperation(provider ReadProvider, operation Operation) error {
	capabilities := provider.Capabilities()
	if !capabilities.Supports(operation) {
		return &CapabilityError{Provider: capabilities.Provider, Operation: operation}
	}
	return nil
}

type ProviderSpec struct {
	Kind      Kind
	Workspace string
	WebAPI    WebAPISpec
	Slackdump SlackdumpSpec
}

type WebAPISpec struct {
	Transport  TransportKind
	BrowserURL string
}

type SlackdumpSpec struct {
	Executable    string
	Workspace     string
	Authorization string
}

const SlackdumpExternalOptIn = "external_opt_in"

// Normalized provider models intentionally hide transport envelopes. The Web
// API adapter and external providers both populate these stable domain shapes.
type AuthTestResult struct {
	URL, Team, User, TeamID, UserID, BotID, EnterpriseID string
}

type Message struct {
	Type, Subtype, User, Username, Text, Ts, ThreadTs, ParentUserID, BotID string
	ReplyCount, ReplyUsersCount                                            int
	LatestReply                                                            string
	ReplyUsers                                                             []string
}

type ConversationField struct {
	Value string `json:"value,omitempty"`
}

type Conversation struct {
	ID, Name, NameNormalized                        string
	IsChannel, IsGroup, IsIM, IsPrivate, IsArchived bool
	IsGeneral, IsMember, IsMPIM                     bool
	NumMembers                                      int
	User, ContextTeamID, Creator, Locale            string
	Topic, Purpose                                  ConversationField
	Created                                         int64
	Latest                                          Message
}

type ListConversationsOptions struct {
	Cursor, Types, TeamID string
	Limit                 int
	ExcludeArchived       bool
}

type GetConversationOptions struct {
	Channel                          string
	IncludeLocale, IncludeNumMembers bool
}

type ConversationPage struct {
	Items      []Conversation
	NextCursor string
}

type UserProfile struct {
	DisplayName, RealName, Email string
}

type User struct {
	ID, TeamID, Name, RealName                  string
	Deleted, IsBot, IsAppUser, IsAdmin, IsOwner bool
	IsRestricted, IsUltraRestricted             bool
	Profile                                     UserProfile
}

type ListUsersOptions struct {
	Cursor, TeamID string
	Limit          int
	IncludeLocale  bool
}

type GetConversationHistoryOptions struct {
	Channel, Cursor, Oldest, Latest string
	Limit                           int
	Inclusive, IncludeAllMetadata   bool
}

type GetConversationRepliesOptions struct {
	Channel, Ts, Cursor, Oldest, Latest string
	Limit                               int
	Inclusive, IncludeAllMetadata       bool
}

type UserPage struct {
	Items      []User
	NextCursor string
}

type ConversationHistoryPage struct {
	Items      []Message
	HasMore    bool
	Latest     string
	NextCursor string
}

type SearchChannel struct {
	ID, Name                    string
	IsPrivate, IsMPIM, IsShared bool
}

type SearchMessage struct {
	Type, User, Username, Text, Ts, Team, IID, Permalink string
	Channel                                              SearchChannel
}

type SearchPagination struct {
	Page, PageCount, PerPage, TotalCount int
}

type SearchMessagesOptions struct {
	Query, Cursor, Sort, SortDir, TeamID string
	Count, Page                          int
	Highlight                            bool
}

type SearchMessagesPage struct {
	Query, NextCursor string
	Items             []SearchMessage
	Total             int
	Pagination        SearchPagination
}

type SearchInfoResult struct{ IsAISearchEnabled bool }

type SearchContextMessageContext struct {
	Text, UserID, Ts string
	Blocks           any
}

type SearchContextMessageContexts struct {
	Before, After []SearchContextMessageContext
}

type SearchContextMessage struct {
	AuthorName, AuthorUserID, TeamID, ChannelID, ChannelName string
	MessageTS, Content, Permalink                            string
	IsAuthorBot                                              bool
	Blocks                                                   any
	ContextMessages                                          SearchContextMessageContexts
}

type SearchContextFile struct {
	UploaderUserID, AuthorUserID, AuthorName, TeamID string
	FileID, Title, FileType, Permalink, Content      string
	DateCreated, DateUpdated                         int64
}

type SearchContextChannel struct {
	TeamID, CreatorUserID, CreatorName, Name string
	Topic, Purpose, Permalink                string
	DateCreated, DateUpdated                 int64
}

type SearchContextUser struct {
	TeamID, UserID, Name, RealName, Title, Email, Permalink string
	IsBot, IsDeleted                                        bool
}

type SearchContextOptions struct {
	Query, ActionToken, ChannelTypes, ContentTypes string
	ContextChannelID, Cursor, Sort, SortDir        string
	TermClauses, Modifiers                         string
	IncludeBots, IncludeDeletedUsers               bool
	IncludeContextMessages, IncludeMessageBlocks   bool
	Highlight, IncludeArchivedChannels             bool
	DisableSemanticSearch                          bool
	Before, After                                  int64
	Limit                                          int
}

type SearchContextPage struct {
	Query, NextCursor string
	Messages          []SearchContextMessage
	Files             []SearchContextFile
	Channels          []SearchContextChannel
	Users             []SearchContextUser
}

type ReadProvider interface {
	Capabilities() Capabilities
	AuthTest(context.Context) (AuthTestResult, error)
	GetConversation(context.Context, GetConversationOptions) (Conversation, error)
	GetConversationHistory(context.Context, GetConversationHistoryOptions) (ConversationHistoryPage, error)
	GetConversationReplies(context.Context, GetConversationRepliesOptions) (ConversationHistoryPage, error)
	ListConversations(context.Context, ListConversationsOptions) (ConversationPage, error)
	ListUsers(context.Context, ListUsersOptions) (UserPage, error)
	SearchContext(context.Context, SearchContextOptions) (SearchContextPage, error)
	SearchInfo(context.Context) (SearchInfoResult, error)
	SearchMessages(context.Context, SearchMessagesOptions) (SearchMessagesPage, error)
}

type Factory interface {
	New(ProviderSpec) (ReadProvider, error)
}

type FactoryFunc func(ProviderSpec) (ReadProvider, error)

func (f FactoryFunc) New(spec ProviderSpec) (ReadProvider, error) { return f(spec) }

type Diagnostic struct {
	Provider     Kind         `json:"provider"`
	Ready        bool         `json:"ready"`
	Code         string       `json:"code,omitempty"`
	Version      string       `json:"version,omitempty"`
	Executable   string       `json:"executable,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
	Message      string       `json:"message,omitempty"`
}

type Diagnosable interface {
	Diagnose(context.Context) Diagnostic
}

func Unsupported(provider ReadProvider, operation Operation) error {
	if err := RequireOperation(provider, operation); err != nil {
		return err
	}
	return fmt.Errorf("provider %q declared but did not implement operation %q", provider.Capabilities().Provider, operation)
}

func NormalizeWorkspace(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}
