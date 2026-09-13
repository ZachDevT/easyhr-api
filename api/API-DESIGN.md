# EasyHR API Design Rules

This document is the engineering agreement for the API contract in `openapi.yaml`. It deliberately designs the foundation before implementation so that both existing Next.js applications connect to a consistent, secure API.

## What is in the first API slice

The first contract covers the product’s MVP backbone:

- authenticated user, organization and permissions context;
- employee records;
- leave balances and requests;
- attendance punches, live attendance and audited corrections;
- onboarding templates, instances and tasks; and
- private document upload/download lifecycle.

The OpenAPI file is the source of truth for client types. Backend handlers, database migrations and generated TypeScript clients must conform to it; a page’s mock data shape does not override it.

## Non-negotiable invariants

### Tenant isolation

The API gets the tenant only from the authenticated local membership. Client request bodies and query parameters must never choose `organizationId` for customer endpoints. Every tenant query includes the server-resolved organization predicate.

Platform back-office endpoints will live under `/platform/v1`, use a separate operator authorization middleware, and are intentionally not included in this customer contract.

### Authorization

Authentication answers “who is calling?”; the local EasyHR permission system answers “may this person perform this action on this exact record?” A valid identity-provider token never grants access on its own.

Authorization occurs before reading or mutating data. Manager actions also evaluate reporting scope. Sensitive field access gets a separate policy from ordinary employee-profile access.

### Audit and historical correctness

Every mutation writes an audit event in the same database transaction as its business change. Important side effects also write an outbox event in that transaction.

- Leave approvals reserve/deduct the ledger atomically; they do not merely change a request status.
- Attendance punches are append-only. A manual edit creates a correction with actor and reason; it never overwrites the original punch.
- Employment, compensation and policy values are effective-dated history, not destructive updates.

### Idempotency

All mutation endpoints require an `Idempotency-Key` header. The backend stores the key, request fingerprint, response status and response body for a limited retention period (recommended: 24 hours for ordinary actions; longer where payment-provider requirements demand it).

Reusing a key with the same authenticated actor, route and request fingerprint returns the original response. Reusing it with a different payload returns `409 idempotency.key_reused`. This prevents duplicate clock-ins, duplicated leave submissions and duplicate onboarding instances on retries.

### Time, money and identifiers

- API timestamps: RFC 3339 UTC (`2026-09-08T12:30:00Z`).
- Business dates: ISO 8601 dates (`2026-09-08`), interpreted in the organization’s IANA timezone.
- IDs: UUIDs. Do not use the mock sequential IDs or browser `Date.now()` values.
- Money: later billing/payroll APIs use integer minor units and ISO currency codes; never floats.
- Durations/balances: API uses minutes for exact leave and attendance calculations, not decimal hours.

## HTTP conventions

| Need | Contract rule |
|---|---|
| Authentication | `Authorization: Bearer <JWT>`; API verifies it and resolves the local user/membership |
| Correlation | API generates/propagates `X-Request-Id` and returns it in every response/problem |
| Lists | Cursor pagination with `limit`, `nextCursor` and a stable default sort |
| Successful create | `201 Created` and the created resource |
| Async operation | `202 Accepted` with a job/export resource when the result is not ready |
| Validation failure | `422` with an `application/problem+json` error and field-level violations |
| Unauthenticated | `401`; malformed/missing token never becomes a permission decision |
| Unauthorized scope | `403`; inaccessible resource may deliberately return `404` where enumeration risk matters |
| State conflict | `409`; e.g. duplicate punch, stale state, idempotency-key conflict |
| Rate limited | `429` plus `Retry-After` |

## API error example

```json
{
  "type": "https://docs.easyhr.example/errors/leave-insufficient-balance",
  "title": "Leave balance is insufficient",
  "status": 409,
  "code": "leave.insufficient_balance",
  "detail": "Requested 960 minutes; 480 minutes are available.",
  "requestId": "req_01J..."
}
```

The API never returns database errors, stack traces, provider credentials or a hidden authorization reason in this envelope.

## Backend implementation map

| OpenAPI tag | Go module | Required transaction boundary |
|---|---|---|
| Context | `identity`, `tenancy` | Token verification + local membership resolution |
| Employees | `people` | Employee write + audit/outbox |
| Leave | `timeoff` | Request/ledger/approval/audit/outbox |
| Attendance | `attendance` | Punch or correction/audit/outbox |
| Onboarding | `onboarding` | Instance/task creation/audit/outbox |
| Documents | `documents` | Metadata state transition/audit; blob never enters PostgreSQL |

HTTP handlers only parse a request, resolve context and call an application service. Domain services enforce rules and own transactions. Repositories execute module-owned SQL only. Asynq job handlers call the same application services, never duplicate business rules.

## Document lifecycle

1. Authorized user requests `/documents/upload-intents` with file metadata.
2. API validates policy, writes a `pending_upload` document row and returns a short-lived upload URL for a private object key.
3. Browser uploads directly to private object storage; bytes do not transit the Go API.
4. Browser calls `/documents/{id}/complete` with SHA-256 checksum.
5. Worker verifies/quarantines/scans the object and moves the document to `available` only when safe.
6. Authorized user requests `/download-url`; API returns a short-lived URL only for an available document.

No permanent public document URLs. Deleted documents become unavailable immediately and are erased through a controlled retention job.

## Design decisions intentionally deferred

These need their own contracts rather than being improvised into this file:

- payroll, tax, statutory reporting and payment disbursement;
- billing checkout/provider event schema;
- signature provider callbacks and completed-document evidence;
- hiring, performance, engagement and full custom workflow engine;
- imports, exports, reporting query builder and external integrations;
- platform `/platform/v1` operator API.

They will share the same tenancy, audit, error and idempotency rules when designed.

## Contract change process

1. Propose the OpenAPI change with its permission, data classification and migration impact.
2. Review it with frontend and backend before implementation.
3. Generate/check TypeScript client types in CI.
4. Add handler integration tests for allowed, forbidden, wrong-tenant and idempotent-retry paths.
5. Release additive changes normally. Version or deprecate breaking changes with a migration window.
