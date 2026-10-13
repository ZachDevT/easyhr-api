DROP TABLE IF EXISTS user_preferences;
DROP TABLE IF EXISTS user_notifications;
DROP TABLE IF EXISTS celebration_wishes;
DROP TABLE IF EXISTS company_announcements;
ALTER TABLE attendance_punches DROP COLUMN IF EXISTS is_billable;
ALTER TABLE attendance_punches DROP COLUMN IF EXISTS activity;
DROP TABLE IF EXISTS attendance_activities;
