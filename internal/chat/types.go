package chat

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"
)

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// NewUUIDForTest exports newUUID for test fixtures across packages.
func NewUUIDForTest() string {
	return newUUID()
}

var (
	ErrNotFound          = errors.New("chat resource not found")
	ErrForbidden         = errors.New("access to chat resource is forbidden")
	ErrInvalidInput      = errors.New("invalid chat input")
	ErrAlreadyMember     = errors.New("user is already a member of this channel")
	ErrAlreadyRequested  = errors.New("join request already pending")
	ErrChannelArchived   = errors.New("channel is archived and read-only")
	ErrRateLimited       = errors.New("rate limit exceeded")
	ErrPayloadTooLarge   = errors.New("attachment exceeds maximum allowed size")
	ErrUnsupportedMedia  = errors.New("unsupported attachment file type")
)

const (
	EventVersionV1 = 1

	EventMessageCreated        = "message.created"
	EventMessageUpdated        = "message.updated"
	EventMessageDeleted        = "message.deleted"
	EventMessageReactionAdded  = "message.reaction_added"
	EventMessageReactionRemove = "message.reaction_removed"
	EventMessagePinned         = "message.pinned"
	EventMessageUnpinned       = "message.unpinned"
	EventMessagePollUpdated    = "message.poll_updated"
	EventThreadReplyCreated    = "thread.reply_created"
	EventChannelCreated        = "channel.created"
	EventChannelUpdated        = "channel.updated"
	EventChannelArchived       = "channel.archived"
	EventChannelDeleted        = "channel.deleted"
	EventChannelMemberJoined   = "channel.member_joined"
	EventChannelMemberLeft     = "channel.member_left"
	EventChannelJoinRequested  = "channel.join_requested"
	EventChannelJoinReviewed   = "channel.join_reviewed"
	EventTypingStarted         = "typing.started"
	EventTypingStopped         = "typing.stopped"
	EventPresenceUpdated       = "presence.updated"
	EventMessageRead           = "message.read"
	EventNotificationCreated   = "notification.created"
)

const (
	ChannelTypePublic     = "public"
	ChannelTypePrivate    = "private"
	ChannelTypeDepartment = "department"
	ChannelTypeDM         = "dm"
	ChannelTypeGroupDM    = "group_dm"
)

type Actor struct {
	OrganizationID string
	UserID         string
	EmployeeID     string
	MembershipID   string
	FullName       string
	RoleTitle      string
	Department     string
	AvatarURL      string
	IsAdmin        bool
}

type Channel struct {
	ID                    string   `json:"id"`
	OrganizationID        string   `json:"organizationId,omitempty"`
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Topic                 string   `json:"topic,omitempty"`
	IsPrivate             bool     `json:"isPrivate"`
	ChannelType           string   `json:"channelType"`
	DepartmentRestriction string   `json:"departmentRestriction,omitempty"`
	UnreadCount           int      `json:"unreadCount"`
	LastMessageAt         string   `json:"lastMessageAt,omitempty"`
	MemberIDs             []string `json:"memberIds"`
	PendingRequestUserIDs []string `json:"pendingRequestUserIds,omitempty"`
	CreatedBy             string   `json:"createdBy"`
	IsArchived            bool     `json:"isArchived,omitempty"`
	IsMuted               bool     `json:"isMuted,omitempty"`
	LastReadMessageID     string   `json:"lastReadMessageId,omitempty"`
	LastReadAt            string   `json:"lastReadAt,omitempty"`
	CreatedAt             string   `json:"createdAt,omitempty"`
	UpdatedAt             string   `json:"updatedAt,omitempty"`
}

type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Type     string `json:"type"` // "image" | "video" | "audio" | "document"
	MimeType string `json:"mimeType,omitempty"`
	Size     int64  `json:"size"`
}

type PollOption struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	VoterIDs []string `json:"voterIds"`
}

type Poll struct {
	Question      string       `json:"question"`
	Options       []PollOption `json:"options"`
	AllowMultiple bool         `json:"allowMultiple"`
	IsAnonymous   bool         `json:"isAnonymous"`
	Closed        bool         `json:"closed,omitempty"`
}

type Message struct {
	ID           string              `json:"id"`
	ChannelID    string              `json:"channelId"`
	SenderID     string              `json:"senderId"`
	SenderName   string              `json:"senderName"`
	SenderAvatar string              `json:"senderAvatar,omitempty"`
	SenderRole   string              `json:"senderRole,omitempty"`
	Content      string              `json:"content"`
	Timestamp    string              `json:"timestamp"`
	EditedAt     string              `json:"editedAt,omitempty"`
	IsDeleted    bool                `json:"isDeleted,omitempty"`
	ClientMsgID  string              `json:"clientMsgId,omitempty"`
	Attachments  []Attachment        `json:"attachments,omitempty"`
	Poll         *Poll               `json:"poll,omitempty"`
	Reactions    map[string][]string `json:"reactions"`
	ReplyToID    string              `json:"replyToId,omitempty"`
	ThreadCount  int                 `json:"threadCount,omitempty"`
	Pinned       bool                `json:"pinned,omitempty"`
	Saved        bool                `json:"saved,omitempty"`
	Mentions     []string            `json:"mentions,omitempty"`
}

type MessagePage struct {
	Messages       []Message `json:"messages"`
	ThreadPreviews []Message `json:"threadPreviews,omitempty"`
	NextCursor     string    `json:"nextCursor,omitempty"`
	HasMore        bool      `json:"hasMore"`
}

type Presence struct {
	EmployeeID    string `json:"employeeId"`
	UserID        string `json:"userId,omitempty"`
	Status        string `json:"status"` // "online", "away", "dnd", "offline"
	StatusText    string `json:"statusText,omitempty"`
	DoNotDisturb  bool   `json:"doNotDisturb"`
	DndUntil      string `json:"dndUntil,omitempty"`
	LastSeenAt    string `json:"lastSeenAt"`
}

type TypingState struct {
	ChannelID  string `json:"channelId"`
	EmployeeID string `json:"employeeId"`
	UserName   string `json:"userName"`
	IsTyping   bool   `json:"isTyping"`
	Timestamp  string `json:"timestamp"`
}

type RealtimeEvent struct {
	ID             int64       `json:"id,omitempty"`
	Version        int         `json:"version"`
	Type           string      `json:"type"`
	OrganizationID string      `json:"organizationId"`
	ChannelID      string      `json:"channelId,omitempty"`
	ActorID        string      `json:"actorId,omitempty"`
	TargetIDs      []string    `json:"-"` // Optional explicit target employee IDs
	Payload        interface{} `json:"payload"`
	Timestamp      string      `json:"timestamp"`
}

type CreateChannelInput struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Topic                 string   `json:"topic"`
	IsPrivate             bool     `json:"isPrivate"`
	ChannelType           string   `json:"channelType"`
	DepartmentRestriction string   `json:"departmentRestriction"`
	MemberIDs             []string `json:"memberIds"`
}

type UpdateChannelInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Topic       *string `json:"topic,omitempty"`
	IsPrivate   *bool   `json:"isPrivate,omitempty"`
}

type CreateDMInput struct {
	ParticipantIDs []string `json:"participantIds"`
	Name           string   `json:"name,omitempty"`
}

type SendMessageInput struct {
	ChannelID     string       `json:"channelId"`
	Content       string       `json:"content"`
	ReplyToID     string       `json:"replyToId,omitempty"`
	ClientMsgID   string       `json:"clientMsgId,omitempty"`
	AttachmentIDs []string     `json:"attachmentIds,omitempty"`
	Attachments   []Attachment `json:"attachments,omitempty"`
	Poll          *Poll        `json:"poll,omitempty"`
}

type EditMessageInput struct {
	Content string `json:"content"`
}

type ListMessagesFilter struct {
	ChannelID      string
	BeforeCursor   string
	AfterCursor    string
	Limit          int
	IncludeThreads bool
	OnlySaved      bool
	OnlyPinned     bool
}

type SearchMessagesInput struct {
	Query      string
	ChannelID  string
	SenderID   string
	FromDate   *time.Time
	ToDate     *time.Time
	OnlyPinned bool
	OnlySaved  bool
	Cursor     string
	Limit      int
}

type UploadAttachmentInput struct {
	ChannelID   string
	FileName    string
	ContentType string
	SizeBytes   int64
	Data        []byte
}

type AttachmentRecord struct {
	ID             string
	OrganizationID string
	ChannelID      string
	MessageID      string
	UploadedBy     string
	FileName       string
	FileType       string
	MimeType       string
	SizeBytes      int64
	StorageKey     string
	InlineData     []byte
	CreatedAt      time.Time
}
