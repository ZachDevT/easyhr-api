DROP TABLE IF EXISTS document_versions;
DROP TABLE IF EXISTS documents;
DELETE FROM permissions WHERE key IN ('documents:upload', 'documents:read');
