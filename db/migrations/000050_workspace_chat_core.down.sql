-- Rollback Migration 000050: Workspace Chat Core

DROP TABLE IF EXISTS chat_events;
DROP TABLE IF EXISTS chat_user_presence;
DROP TABLE IF EXISTS chat_mentions;
DROP TABLE IF EXISTS chat_poll_votes;
DROP TABLE IF EXISTS chat_poll_options;
DROP TABLE IF EXISTS chat_polls;
DROP TABLE IF EXISTS chat_attachments;
DROP TABLE IF EXISTS chat_message_saves;
DROP TABLE IF EXISTS chat_message_reactions;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS chat_channel_join_requests;
DROP TABLE IF EXISTS chat_channel_members;
DROP TABLE IF EXISTS chat_channels;

DELETE FROM role_permissions WHERE permission_key IN ('chat:read', 'chat:write', 'chat:manage_channels');
DELETE FROM permissions WHERE key IN ('chat:read', 'chat:write', 'chat:manage_channels');
