# EasyHR Backend

Standalone backend repository for EasyHR.

## Current contents

- [`api/openapi.yaml`](api/openapi.yaml) — the versioned customer API contract for the first MVP slice.
- [`api/API-DESIGN.md`](api/API-DESIGN.md) — the engineering rules behind the contract: tenant isolation, authorization, auditability, idempotency, error handling and data lifecycle.
- [`docs/FRONTEND-DOMAIN-DISCOVERY.md`](docs/FRONTEND-DOMAIN-DISCOVERY.md) — field-level production domain requirements discovered from both frontend prototypes.

## Intended repository structure

```text
EasyHrBE/
├── api/                 # OpenAPI contract and API design rules
├── cmd/api/             # Go HTTP API entrypoint
├── cmd/worker/          # Asynq job worker entrypoint
├── internal/            # Domain modules and HTTP adapters
├── db/migrations/       # Versioned PostgreSQL migrations
├── db/queries/          # sqlc query definitions
├── deployments/         # Docker and environment configuration
└── tests/               # Integration and contract tests
```

The repository is intentionally at the API-design stage. The next implementation step is to create the Go module and the identity/tenancy foundation before adding business endpoints.

## Local development

1. Copy `.env.example` to `.env` and fill the Clerk values.
2. Start PostgreSQL and Redis: `docker compose up -d`.
3. Apply exactly one reviewed migration at a time: `set -a; source .env; set +a; go run ./cmd/migrate -direction up`.
4. Check schema state: `set -a; source .env; set +a; go run ./cmd/migrate -direction version`.
5. Start the API: `set -a; source .env; set +a; go run ./cmd/api`.

Do not use `force` to repair a production migration without a reviewed recovery procedure. A dirty migration state is an incident, not something to bypass.

## Relationship to the other repositories

- `EasyHr/easyhr-next` is the customer-facing Next.js frontend.
- `EasyHrBO` is the EasyHR platform back-office frontend.
- `EasyHrBE` is this independent backend repository.

The frontends will consume generated TypeScript types/client code from the OpenAPI contract. Neither frontend is a source of authorization or tenant selection.
