DROP TABLE IF EXISTS attendance_corrections;
DROP TABLE IF EXISTS attendance_punches;
DROP TABLE IF EXISTS shift_assignments;
DROP TABLE IF EXISTS shift_templates;
DELETE FROM permissions WHERE key IN ('attendance:punch_self', 'attendance:read_team', 'attendance:correct', 'attendance:manage_shifts');
