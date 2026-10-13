-- 000036_inbound_emails_and_message_threading.down.sql

DROP TABLE IF EXISTS inbound_emails;

ALTER TABLE candidate_email_messages
    DROP COLUMN IF EXISTS message_id,
    DROP COLUMN IF EXISTS in_reply_to,
    DROP COLUMN IF EXISTS raw_json;
