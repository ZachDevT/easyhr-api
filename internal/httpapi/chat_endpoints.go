package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/easyhr/easyhr-be/internal/chat"
	"github.com/easyhr/easyhr-be/internal/identity"
)

type userRateLimiter struct {
	mu      sync.Mutex
	history map[string][]time.Time
}

var chatRateLimiter = &userRateLimiter{
	history: make(map[string][]time.Time),
}

func (rl *userRateLimiter) check(key string, limit int, window time.Duration) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-window)

	timestamps := rl.history[key]
	valid := timestamps[:0]
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}

	if len(valid) >= limit {
		rl.history[key] = valid
		return false
	}

	valid = append(valid, now)
	rl.history[key] = valid
	return true
}

func (s *Server) getChatActor(c echo.Context) (chat.Actor, error) {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return chat.Actor{}, errors.New("missing identity principal")
	}
	return s.chat.ResolveActor(c.Request().Context(), principal)
}

// GET /api/v1/chat/channels
func (s *Server) listChatChannels(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channels, err := s.chat.ListChannels(c.Request().Context(), actor)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.list_failed", "Failed to list channels", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{
		"channels": channels,
	})
}

// POST /api/v1/chat/channels
func (s *Server) createChatChannel(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	var in chat.CreateChannelInput
	if err := c.Bind(&in); err != nil {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid channel input", err.Error())
	}

	ch, err := s.chat.CreateChannel(c.Request().Context(), actor, in)
	if err != nil {
		if errors.Is(err, chat.ErrInvalidInput) {
			return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid channel input", err.Error())
		}
		return problem(c, http.StatusInternalServerError, "chat.create_failed", "Failed to create channel", err.Error())
	}

	return c.JSON(http.StatusCreated, ch)
}

// POST /api/v1/chat/channels/dm
func (s *Server) getOrCreateDM(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	var in chat.CreateDMInput
	if err := c.Bind(&in); err != nil {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid DM input", err.Error())
	}

	ch, err := s.chat.GetOrCreateDM(c.Request().Context(), actor, in)
	if err != nil {
		if errors.Is(err, chat.ErrInvalidInput) {
			return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid DM input", err.Error())
		}
		return problem(c, http.StatusInternalServerError, "chat.dm_failed", "Failed to start direct message", err.Error())
	}

	return c.JSON(http.StatusOK, ch)
}

// PATCH /api/v1/chat/channels/:channelId
func (s *Server) updateChatChannel(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	var in chat.UpdateChannelInput
	if err := c.Bind(&in); err != nil {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid channel input", err.Error())
	}

	ch, err := s.chat.UpdateChannel(c.Request().Context(), actor, channelID, in)
	if err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "You do not have permission to edit this channel")
		}
		return problem(c, http.StatusInternalServerError, "chat.update_failed", "Failed to update channel", err.Error())
	}

	return c.JSON(http.StatusOK, ch)
}

// POST /api/v1/chat/channels/:channelId/archive
func (s *Server) archiveChatChannel(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	if err := s.chat.ArchiveChannel(c.Request().Context(), actor, channelID); err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "Only channel creators or admins can archive this channel")
		}
		return problem(c, http.StatusInternalServerError, "chat.archive_failed", "Failed to archive channel", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

// DELETE /api/v1/chat/channels/:channelId
func (s *Server) deleteChatChannel(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	if err := s.chat.DeleteChannel(c.Request().Context(), actor, channelID); err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "Only channel creators or admins can delete this channel")
		}
		return problem(c, http.StatusInternalServerError, "chat.delete_failed", "Failed to delete channel", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

// POST /api/v1/chat/channels/:channelId/join
func (s *Server) joinChatChannel(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	if err := s.chat.JoinChannel(c.Request().Context(), actor, channelID); err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "Cannot join a private channel directly; request access instead")
		}
		return problem(c, http.StatusInternalServerError, "chat.join_failed", "Failed to join channel", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

// POST /api/v1/chat/channels/:channelId/leave
func (s *Server) leaveChatChannel(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	if err := s.chat.LeaveChannel(c.Request().Context(), actor, channelID); err != nil {
		return problem(c, http.StatusInternalServerError, "chat.leave_failed", "Failed to leave channel", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

// POST /api/v1/chat/channels/:channelId/request-join
func (s *Server) requestJoinChatChannel(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	if err := s.chat.RequestToJoinChannel(c.Request().Context(), actor, channelID); err != nil {
		return problem(c, http.StatusInternalServerError, "chat.request_failed", "Failed to request to join channel", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

type reviewRequestInput struct {
	RequesterID string `json:"requesterId"`
	Approved    bool   `json:"approved"`
}

// POST /api/v1/chat/channels/:channelId/review-request
func (s *Server) reviewJoinRequest(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	var in reviewRequestInput
	if err := c.Bind(&in); err != nil || in.RequesterID == "" {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid review input", "Requester ID is required")
	}

	if err := s.chat.ReviewJoinRequest(c.Request().Context(), actor, channelID, in.RequesterID, in.Approved); err != nil {
		return problem(c, http.StatusInternalServerError, "chat.review_failed", "Failed to review join request", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

type memberActionInput struct {
	EmployeeID string `json:"employeeId"`
}

// POST /api/v1/chat/channels/:channelId/members
func (s *Server) addChatMember(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	var in memberActionInput
	if err := c.Bind(&in); err != nil || in.EmployeeID == "" {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid member input", "Employee ID is required")
	}

	if err := s.chat.AddMember(c.Request().Context(), actor, channelID, in.EmployeeID); err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "You do not have permission to add members to this channel")
		}
		return problem(c, http.StatusInternalServerError, "chat.add_member_failed", "Failed to add channel member", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

// DELETE /api/v1/chat/channels/:channelId/members/:memberId
func (s *Server) removeChatMember(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	memberID := c.Param("memberId")

	if err := s.chat.RemoveMember(c.Request().Context(), actor, channelID, memberID); err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "You do not have permission to remove members from this channel")
		}
		return problem(c, http.StatusInternalServerError, "chat.remove_member_failed", "Failed to remove channel member", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

type chatMarkReadInput struct {
	LastMessageID string `json:"lastMessageId"`
}

// POST /api/v1/chat/channels/:channelId/read
func (s *Server) markChatChannelRead(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	var in chatMarkReadInput
	_ = c.Bind(&in)

	if err := s.chat.MarkChannelRead(c.Request().Context(), actor, channelID, in.LastMessageID); err != nil {
		return problem(c, http.StatusInternalServerError, "chat.mark_read_failed", "Failed to mark channel as read", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

type channelPreferencesInput struct {
	IsMuted bool `json:"isMuted"`
}

// PATCH /api/v1/chat/channels/:channelId/preferences
func (s *Server) updateChatChannelPreferences(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.Param("channelId")
	var in channelPreferencesInput
	if err := c.Bind(&in); err != nil {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid preferences input", err.Error())
	}

	if err := s.chat.UpdateChannelPreferences(c.Request().Context(), actor, channelID, in.IsMuted); err != nil {
		return problem(c, http.StatusInternalServerError, "chat.preferences_failed", "Failed to update channel preferences", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

// GET /api/v1/chat/messages
func (s *Server) listChatMessages(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.QueryParam("channelId")
	if channelID == "" {
		return problem(c, http.StatusBadRequest, "chat.channel_required", "Channel required", "channelId query param is required")
	}

	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	filter := chat.ListMessagesFilter{
		ChannelID:      channelID,
		BeforeCursor:   c.QueryParam("beforeCursor"),
		AfterCursor:    c.QueryParam("afterCursor"),
		Limit:          limit,
		IncludeThreads: c.QueryParam("includeThreads") == "true",
		OnlyPinned:     c.QueryParam("pinned") == "true",
		OnlySaved:      c.QueryParam("saved") == "true",
	}

	page, err := s.chat.ListMessages(c.Request().Context(), actor, filter)
	if err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "You do not have access to view messages in this channel")
		}
		return problem(c, http.StatusInternalServerError, "chat.list_messages_failed", "Failed to list messages", err.Error())
	}

	return c.JSON(http.StatusOK, page)
}

// GET /api/v1/chat/messages/search
func (s *Server) searchChatMessages(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	in := chat.SearchMessagesInput{
		Query:      c.QueryParam("q"),
		ChannelID:  c.QueryParam("channelId"),
		SenderID:   c.QueryParam("senderId"),
		OnlyPinned: c.QueryParam("pinned") == "true",
		OnlySaved:  c.QueryParam("saved") == "true",
		Cursor:     c.QueryParam("cursor"),
		Limit:      limit,
	}

	if from := c.QueryParam("from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			in.FromDate = &t
		}
	}
	if to := c.QueryParam("to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			in.ToDate = &t
		}
	}

	page, err := s.chat.SearchMessages(c.Request().Context(), actor, in)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.search_failed", "Failed to search messages", err.Error())
	}

	return c.JSON(http.StatusOK, page)
}

// GET /api/v1/chat/messages/:messageId/replies
func (s *Server) getThreadReplies(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	channelID := c.QueryParam("channelId")
	parentID := c.Param("messageId")
	if channelID == "" {
		return problem(c, http.StatusBadRequest, "chat.channel_required", "Channel required", "channelId query param is required")
	}

	replies, err := s.chat.GetThreadReplies(c.Request().Context(), actor, channelID, parentID)
	if err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "You do not have access to view this thread")
		}
		return problem(c, http.StatusInternalServerError, "chat.replies_failed", "Failed to get thread replies", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"replies": replies})
}

// POST /api/v1/chat/messages
func (s *Server) sendChatMessage(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	// Rate limit: 60 messages per minute per employee
	rateKey := "chat:send:" + actor.EmployeeID
	if !chatRateLimiter.check(rateKey, 60, time.Minute) {
		return problem(c, http.StatusTooManyRequests, "chat.rate_limited", "Rate limit exceeded", "You are sending messages too quickly. Please wait a moment.")
	}

	var in chat.SendMessageInput
	if err := c.Bind(&in); err != nil {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid message input", err.Error())
	}

	if strings.TrimSpace(in.Content) == "" && len(in.AttachmentIDs) == 0 && len(in.Attachments) == 0 && in.Poll == nil {
		return problem(c, http.StatusBadRequest, "chat.empty_message", "Empty message", "Message cannot be empty")
	}

	// Check Idempotency-Key header if clientMsgId not in body
	if in.ClientMsgID == "" {
		in.ClientMsgID = c.Request().Header.Get("Idempotency-Key")
	}

	msg, err := s.chat.SendMessage(c.Request().Context(), actor, in)
	if err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "You do not have permission to post in this channel")
		}
		if errors.Is(err, chat.ErrChannelArchived) {
			return problem(c, http.StatusForbidden, "chat.archived", "Channel archived", "This channel is archived and cannot receive new messages")
		}
		return problem(c, http.StatusInternalServerError, "chat.send_failed", "Failed to send message", err.Error())
	}

	return c.JSON(http.StatusCreated, msg)
}

// PATCH /api/v1/chat/messages/:messageId
func (s *Server) editChatMessage(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	messageID := c.Param("messageId")
	var in struct {
		ChannelID string `json:"channelId"`
		Content   string `json:"content"`
	}
	if err := c.Bind(&in); err != nil || strings.TrimSpace(in.Content) == "" {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid content", "Updated content is required")
	}

	msg, err := s.chat.EditMessage(c.Request().Context(), actor, in.ChannelID, messageID, in.Content)
	if err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "You can only edit your own messages")
		}
		return problem(c, http.StatusInternalServerError, "chat.edit_failed", "Failed to edit message", err.Error())
	}

	return c.JSON(http.StatusOK, msg)
}

// DELETE /api/v1/chat/messages/:messageId
func (s *Server) deleteChatMessage(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	messageID := c.Param("messageId")
	channelID := c.QueryParam("channelId")
	if channelID == "" {
		return problem(c, http.StatusBadRequest, "chat.channel_required", "Channel required", "channelId query param is required")
	}

	if err := s.chat.DeleteMessage(c.Request().Context(), actor, channelID, messageID); err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "You can only delete your own messages")
		}
		return problem(c, http.StatusInternalServerError, "chat.delete_failed", "Failed to delete message", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

// POST /api/v1/chat/messages/:messageId/pin
func (s *Server) togglePinMessage(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	messageID := c.Param("messageId")
	channelID := c.QueryParam("channelId")
	if channelID == "" {
		var body struct {
			ChannelID string `json:"channelId"`
		}
		_ = c.Bind(&body)
		channelID = body.ChannelID
	}
	if channelID == "" {
		return problem(c, http.StatusBadRequest, "chat.channel_required", "Channel required", "channelId is required")
	}

	pinned, err := s.chat.TogglePinMessage(c.Request().Context(), actor, channelID, messageID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.pin_failed", "Failed to toggle pin", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"pinned": pinned})
}

// POST /api/v1/chat/messages/:messageId/save
func (s *Server) toggleSaveMessage(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	messageID := c.Param("messageId")
	saved, err := s.chat.ToggleSaveMessage(c.Request().Context(), actor, messageID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.save_failed", "Failed to toggle save", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"saved": saved})
}

type reactionInput struct {
	ChannelID string `json:"channelId"`
	Emoji     string `json:"emoji"`
}

// POST /api/v1/chat/messages/:messageId/reactions
func (s *Server) toggleChatReaction(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	messageID := c.Param("messageId")
	var in reactionInput
	if err := c.Bind(&in); err != nil || in.Emoji == "" || in.ChannelID == "" {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid reaction input", "channelId and emoji are required")
	}

	reactions, err := s.chat.ToggleReaction(c.Request().Context(), actor, in.ChannelID, messageID, in.Emoji)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.reaction_failed", "Failed to toggle reaction", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"reactions": reactions})
}

type pollVoteInput struct {
	ChannelID string `json:"channelId"`
	OptionID  string `json:"optionId"`
}

// POST /api/v1/chat/messages/:messageId/poll/vote
func (s *Server) voteChatPoll(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	messageID := c.Param("messageId")
	var in pollVoteInput
	if err := c.Bind(&in); err != nil || in.OptionID == "" || in.ChannelID == "" {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid vote input", "channelId and optionId are required")
	}

	poll, err := s.chat.VotePoll(c.Request().Context(), actor, in.ChannelID, messageID, in.OptionID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.vote_failed", "Failed to vote in poll", err.Error())
	}

	return c.JSON(http.StatusOK, poll)
}

type pollCloseInput struct {
	ChannelID string `json:"channelId"`
}

// POST /api/v1/chat/messages/:messageId/poll/close
func (s *Server) toggleCloseChatPoll(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	messageID := c.Param("messageId")
	var in pollCloseInput
	if err := c.Bind(&in); err != nil || in.ChannelID == "" {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid input", "channelId is required")
	}

	poll, err := s.chat.ToggleClosePoll(c.Request().Context(), actor, in.ChannelID, messageID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.poll_close_failed", "Failed to toggle poll status", err.Error())
	}

	return c.JSON(http.StatusOK, poll)
}

// POST /api/v1/chat/attachments
func (s *Server) uploadChatAttachment(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	// Rate limit: 20 file uploads per minute
	rateKey := "chat:upload:" + actor.EmployeeID
	if !chatRateLimiter.check(rateKey, 20, time.Minute) {
		return problem(c, http.StatusTooManyRequests, "chat.rate_limited", "Rate limit exceeded", "Upload limit exceeded. Please wait a moment.")
	}

	channelID := c.FormValue("channelId")
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return problem(c, http.StatusBadRequest, "chat.file_required", "File required", "A file must be attached in form field 'file'")
	}

	if fileHeader.Size > chat.MaxAttachmentSize {
		return problem(c, http.StatusRequestEntityTooLarge, "chat.file_too_large", "File too large", "File exceeds maximum allowed size of 50MB")
	}

	src, err := fileHeader.Open()
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.file_open_failed", "Failed to open file", err.Error())
	}
	defer src.Close()

	data, err := io.ReadAll(src)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.file_read_failed", "Failed to read file", err.Error())
	}

	att, err := s.chat.UploadAttachment(c.Request().Context(), actor, chat.UploadAttachmentInput{
		ChannelID:   channelID,
		FileName:    fileHeader.Filename,
		ContentType: fileHeader.Header.Get("Content-Type"),
		SizeBytes:   fileHeader.Size,
		Data:        data,
	})
	if err != nil {
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "You do not have access to upload to this channel")
		}
		return problem(c, http.StatusInternalServerError, "chat.upload_failed", "Failed to upload attachment", err.Error())
	}

	return c.JSON(http.StatusCreated, att)
}

// GET /api/v1/chat/attachments/:attachmentId/content
func (s *Server) getChatAttachmentContent(c echo.Context) error {
	attachmentID := c.Param("attachmentId")
	token := c.QueryParam("token")

	var actorPtr *chat.Actor
	if actor, err := s.getChatActor(c); err == nil {
		actorPtr = &actor
	}

	rec, rc, err := s.chat.GetAttachmentContent(c.Request().Context(), attachmentID, token, actorPtr)
	if err != nil {
		if errors.Is(err, chat.ErrNotFound) {
			return problem(c, http.StatusNotFound, "chat.attachment_not_found", "Attachment not found", "The requested attachment does not exist")
		}
		if errors.Is(err, chat.ErrForbidden) {
			return problem(c, http.StatusForbidden, "chat.forbidden", "Forbidden", "Access to this attachment is forbidden")
		}
		return problem(c, http.StatusInternalServerError, "chat.download_failed", "Failed to retrieve attachment", err.Error())
	}
	defer rc.Close()

	c.Response().Header().Set("Content-Type", rec.MimeType)
	c.Response().Header().Set("Content-Length", strconv.FormatInt(rec.SizeBytes, 10))
	c.Response().Header().Set("Cache-Control", "private, max-age=86400")
	if !strings.HasPrefix(rec.MimeType, "image/") && !strings.HasPrefix(rec.MimeType, "video/") && !strings.HasPrefix(rec.MimeType, "audio/") {
		c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rec.FileName))
	} else {
		c.Response().Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", rec.FileName))
	}

	return c.Stream(http.StatusOK, rec.MimeType, rc)
}

type typingInput struct {
	ChannelID string `json:"channelId"`
	IsTyping  bool   `json:"isTyping"`
}

// POST /api/v1/chat/typing
func (s *Server) setChatTyping(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	var in typingInput
	if err := c.Bind(&in); err != nil || in.ChannelID == "" {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid typing input", "channelId is required")
	}

	s.chat.Hub().SetTyping(actor.OrganizationID, in.ChannelID, actor.EmployeeID, actor.FullName, in.IsTyping)
	return c.JSON(http.StatusOK, map[string]any{"success": true})
}

// GET /api/v1/chat/presence
func (s *Server) listChatPresence(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	list, err := s.chat.ListPresence(c.Request().Context(), actor.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.presence_failed", "Failed to list presence", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{"presence": list})
}

type updatePresenceInput struct {
	Status       string     `json:"status"`
	StatusText   string     `json:"statusText"`
	DoNotDisturb bool       `json:"doNotDisturb"`
	DndUntil     *time.Time `json:"dndUntil"`
}

// PATCH /api/v1/chat/presence
func (s *Server) updateChatPresence(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	var in updatePresenceInput
	if err := c.Bind(&in); err != nil {
		return problem(c, http.StatusBadRequest, "chat.invalid_input", "Invalid presence input", err.Error())
	}

	p, err := s.chat.UpdatePresence(c.Request().Context(), actor, in.Status, in.StatusText, in.DoNotDisturb, in.DndUntil)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "chat.presence_failed", "Failed to update presence", err.Error())
	}

	return c.JSON(http.StatusOK, p)
}

// GET /api/v1/chat/events (Server-Sent Events)
func (s *Server) chatEventsSSE(c echo.Context) error {
	actor, err := s.getChatActor(c)
	if err != nil {
		return problem(c, http.StatusUnauthorized, "chat.unauthorized", "Unauthorized", err.Error())
	}

	w := c.Response().Writer
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache, no-transform")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().Header().Set("X-Accel-Buffering", "no")
	c.Response().WriteHeader(http.StatusOK)

	// Ensure long-lived connection is not killed by server write deadlines
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	flusher, ok := w.(http.Flusher)
	if !ok {
		return problem(c, http.StatusInternalServerError, "chat.streaming_unsupported", "Streaming unsupported", "HTTP streaming is not supported by connection")
	}
	flusher.Flush()

	// Initial handshake
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	// Reconnection catch-up: check Last-Event-ID header or afterEventId query param
	afterEventStr := c.Request().Header.Get("Last-Event-ID")
	if afterEventStr == "" {
		afterEventStr = c.QueryParam("afterEventId")
	}
	if afterEventStr != "" {
		if afterID, err := strconv.ParseInt(afterEventStr, 10, 64); err == nil && afterID > 0 {
			rows, err := s.pool.Query(c.Request().Context(), `
				SELECT id, event_type, organization_id::text, COALESCE(channel_id::text, ''), actor_id, payload, created_at
				FROM chat_events
				WHERE organization_id = $1::uuid AND id > $2
				ORDER BY id ASC
				LIMIT 100
			`, actor.OrganizationID, afterID)
			if err == nil {
				for rows.Next() {
					var ev chat.RealtimeEvent
					var rawPayload []byte
					var createdAt time.Time
					if err := rows.Scan(&ev.ID, &ev.Type, &ev.OrganizationID, &ev.ChannelID, &ev.ActorID, &rawPayload, &createdAt); err == nil {
						if ev.ChannelID == "" || s.chat.AuthorizeChannel(c.Request().Context(), ev.OrganizationID, ev.ChannelID, actor.EmployeeID, actor.IsAdmin) {
							fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, string(rawPayload))
						}
					}
				}
				rows.Close()
				flusher.Flush()
			}
		}
	}

	subscriber, unsubscribe := s.chat.Hub().Subscribe(actor.OrganizationID, actor.EmployeeID, actor.UserID, actor.IsAdmin)
	defer unsubscribe()

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	reqCtx := c.Request().Context()
	for {
		select {
		case <-reqCtx.Done():
			return nil
		case <-heartbeatTicker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case event, ok := <-subscriber.Events:
			if !ok {
				return nil
			}
			payloadRaw, err := json.Marshal(event.Payload)
			if err != nil {
				continue
			}
			if event.ID > 0 {
				fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, string(payloadRaw))
			} else {
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, string(payloadRaw))
			}
			flusher.Flush()
		}
	}
}
