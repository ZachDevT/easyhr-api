# EasyHR Domain Discovery: Frontend-to-Backend Specification

**Status:** discovery complete for the current prototype; implementation gate for core schemas  
**Purpose:** prevent the backend from being designed as generic CRUD. This document identifies the data, workflows, states, scopes and constraints evidenced by both EasyHR frontends.

## 1. Evidence reviewed

The customer application (`EasyHr/easyhr-next`) contains UI for:

- account creation, email verification, password recovery, invite acceptance and a six-step company-registration flow;
- people directory, employee profile/self-service profile, org chart, departments, job roles, promotions and offboarding;
- onboarding, documents, e-signatures, time-off, holidays, attendance, shifts, timesheets and payroll;
- employee relations, benefits, rewards, performance, engagement, hiring, workflows, reports, inbox and support.

The platform back-office (`EasyHrBO`) contains UI for:

- organizations, platform operators, subscriptions, billing, support and platform settings;
- feature configuration, security/compliance, audit logs, data-management requests, operations/job queue, integrations and webhooks; and
- platform-level views of every product module.

The current UI uses mock objects, React context and browser storage. Those objects are product evidence, **not** the production persistence model. Names such as `employeeName`, display strings such as `KES 180,000 / Year`, and browser-generated `Date.now()` IDs must be replaced with normalized relations, money fields and UUIDs.

## 2. Product-wide rules inferred from the UI

| Rule | Evidence / backend implication |
|---|---|
| EasyHR is multi-tenant | Customer workspaces and a platform view of many organizations require strict `organization_id` isolation on all tenant data. |
| One person can be both a user and an employee, but the concepts differ | Creator/operator may have a login before an employment record exists; candidate/pre-boarder may have an employee record before an active account. Model identity, membership and employment separately. |
| Role labels are not sufficient authorization | UI distinguishes Employee, Manager and HR Admin and further hides confidential fields; use permission + reporting-scope evaluation. |
| Historical HR facts cannot be overwritten | Promotion, compensation, employment status, leave and attendance all have legal/payroll consequences. Use effective-dated history and append-only corrections/ledgers. |
| Country and timezone are business inputs | Registration supports Uganda, Kenya, Rwanda, Tanzania, US and UK with local currencies/timezones. Store IANA timezone and country/currency policy version, not formatted display text. |
| Platform and customer administration are different security domains | `EasyHrBO` includes internal operator access, support, feature flags and all-tenant data. Its API must be separated from customer routes and identities. |
| Sensitive data is pervasive | Identity, birth date, compensation, payroll, disciplinary cases, bank/tax information and documents need data classification, strict access policy, auditing and retention. |

## 3. Identity, user, membership and organization

### 3.1 User / identity

The registration and auth flows require first name, last name, work email, password/SSO identity, email verification, password reset and invited-user acceptance. Employee self-service adds personal details and account preferences.

| Production entity | Confirmed fields | Required backend additions | Sensitivity / access |
|---|---|---|---|
| `users` | first name, last name, work email, phone | external identity-provider ID, normalized email, email verification state, locale, last login, deactivated timestamp | Account data; self and authorized admins |
| `user_identities` | Google sign-up is shown | provider, provider subject, linked time; supports future SSO | Auth administration only |
| `memberships` | creator becomes Company Owner / HR Admin; people can be employee/manager/admin | organization, user, status, invited by/at, accepted at, default role, entitlement context | Tenant administration |
| `roles`, `permissions`, `membership_roles` | Employee, Manager, HR Admin and back-office operator roles shown | custom roles, permission grants, immutable system-role rules, scope policy | Security critical |
| `invitations` | invite acceptance screen | email, intended role, organization, expiry, token hash, inviter, acceptance/revocation audit | Security critical |

Do not put personal employment fields on `users`. `users` represent account holders; `employees` represent people employed by a tenant. Link them with a nullable, one-to-one `employees.user_id` only after identity is verified/claimed.

### 3.2 Organization and workspace

The company registration form provides these confirmed fields:

| Domain group | Fields from UI | Production model |
|---|---|---|
| Legal organization | legal name, display name, registration/CIN, TIN, country of registration, industry, company size | `organizations` + effective `organization_legal_profiles`; identifiers encrypted or restricted where appropriate |
| Contact | contact email, contact phone, website, headquarters address | `organization_contacts` / legal profile; address should be structured, not one textarea only |
| Operating settings | IANA timezone, primary currency, country, departments | `organization_settings`, `departments`, and policy-version links |
| Workspace brand | logo, brand color, workspace URL/domain, LinkedIn, X, Facebook, TikTok, Glassdoor | `workspaces`, `organization_branding`, `organization_social_links`; unique normalized subdomain and a verified custom-domain flow later |
| Initial plan/billing | Starter plan, mobile money/card/bank transfer | billing customer/subscription/payment-method preference; never treat selected UI option as proof of paid status |
| Location control | geofence enabled, latitude, longitude, radius | `locations` and `geofence_rules`; coordinates need encrypted/limited access and a defined accuracy policy |

**Migration correction:** the initial `organizations` table is intentionally only a bootstrap. It must be expanded/split according to this section before local or shared databases are migrated. We will add a new migration rather than silently changing an already-applied migration.

### 3.3 Organization configuration entities

| Entity | Essential fields and relationships |
|---|---|
| `locations` | organization, name, address fields, timezone override, country, active status, coordinates only when required |
| `geofence_rules` | location, enabled, geometry type, latitude/longitude/radius or approved polygon, accuracy tolerance, effective dates |
| `departments` | organization, name, unique code, parent department, department head employee, active/effective dates |
| `divisions` | organization, name, parent; may be optional but frontend displays division separately from department |
| `job_roles` | organization, title, job level, department, reports-to job role, pay type, currency, min/mid/max salary band and active dates |
| `holiday_calendars` / `holidays` | organization/country/location scope, date, name, paid/working-day behavior, observed date |
| `company_policies` | policy type, version, content/document, effective date, required acknowledgement, publication/retirement state |

## 4. Person and employment domain

### 4.1 Employee profile

The directory and employee-detail screens show the fields below. The employee self-profile tabs add Personal, Job, Documents, Time Off, Performance, Benefits, Timesheet, Pay Info and Emergency data.

| Category | Confirmed UI fields | Correct production representation |
|---|---|---|
| Core identity | first name, last name, preferred name, avatar, gender, birth date | `employees` holds display/lifecycle data; protected `employee_personal_profiles` holds personal data. Gender must be optional/configurable and never inferred. |
| Contact | work email, phone, location | work email belongs on employment/employee, personal contact in protected profile; link location by ID, not city text. |
| Organization | employee number, title, department, division, location, manager, direct reports | `employment_assignments` with foreign keys and effective dates; reporting line is `manager_employee_id`, never manager name. |
| Employment | hire date, Full-Time/In Office display, active/inactive/on leave, pay type | `employment_terms` with worker type, FTE/schedule, employment status, start/end dates, work arrangement. |
| Sensitive HR | compensation, pay type, birth date, gender | dated compensation records and protected personal fields; no combined display string. |
| Emergency | Self profile has an Emergency tab | `emergency_contacts` needs name, relation, phones, consent/last verified date. UI form is not yet shown, so fields remain a product decision. |

### 4.2 Required employee entities

| Entity | Must contain | Important constraints |
|---|---|---|
| `employees` | organization, employee number, user link nullable, display names, status, created/updated/terminated timestamps | employee number unique within organization; status transitions audited |
| `employee_personal_profiles` | employee, legal/preferred name components, birth date, gender if collected, personal email/phone, legal address fields | encrypted/protected data; limited readers; never broadly returned by `GET /employees` |
| `employment_assignments` | employee, job role/title, department/division/location, manager employee, start/end dates, primary flag | prevent reporting cycles; one primary active assignment |
| `employment_terms` | employee, worker type, employment type, pay type, FTE, work arrangement, start/end, probation if used | effective-dated; status derives from controlled lifecycle |
| `compensation_records` | employee, effective date, currency, pay frequency, base amount minor, pay type, reason/change reference | restricted permission; immutable after payroll lock |
| `employee_status_events` | employee, old/new status, effective date, reason, actor | auditable; no direct overwrite of status history |
| `emergency_contacts` | employee, full name, relationship, phone/email, priority | restricted to HR and approved emergency flows |
| `employee_custom_field_definitions/values` | tenant-defined field schema, classification, required/visible roles, typed value | do not store all custom data in an uncontrolled JSON blob |

### 4.3 Employee lifecycle actions discovered

- Hire/create employee and invite account holder.
- Change department, role, manager, location, pay type or compensation.
- Promotion: employee, from role, to role, effective date, salary increase percentage and rationale.
- Start offboarding: employee, owner, last day, exit reason, progress/risk and tasks.
- Disciplinary warning, paid/unpaid suspension or termination with dates, reason, severity and automatically generated documents/signatures.

These must become separate commands, not an unrestricted `PATCH /employees/{id}`. Examples: `POST /employees/{id}/promotions`, `POST /employees/{id}/offboarding`, `POST /employee-relations/cases`. Each command writes history/audit/outbox records.

## 5. Time, attendance, scheduling and leave

### 5.1 Attendance and scheduling

The attendance UI needs live status (clocked in/out, late, missing), expected start time, actual in/out, total worked, last location/source, weekly schedule, manual override reason, assigned shifts, shift profiles, schedule publishing and geofence/IP constraints.

| Entity | Required production fields |
|---|---|
| `shift_templates` | organization, name, start/end local time, cross-midnight behavior, unpaid/paid break rules, location, active dates |
| `shift_assignments` | employee, date or recurrence, shift template, assignment source, published at/by, status |
| `attendance_punches` | employee, kind (in/out/break start/end), occurred-at UTC, captured-at, source (web/mobile/kiosk/override), device/kiosk, location evidence, policy evaluation, correction reference |
| `attendance_corrections` | original punch, replacement/effective time, reason, actor, approval state; original punch stays immutable |
| `timesheets` / `timesheet_entries` | employee, pay period, day, regular/overtime/holiday/PTO minutes, approval status, derived-from source and manual adjustments |
| `attendance_anomalies` | employee/date, type (late/missing/geofence/missed break), severity, resolution and actor |
| `kiosks` | organization/location, device identity, credential rotation, active status; never accept an anonymous client-selected employee ID |

The UI’s `Morning`, `Evening`, `Night` and `Off` labels are presentation options. The backend needs actual local start/end times and policy rules. `Night` must handle cross-midnight shifts and DST correctly for future countries.

### 5.2 Leave

The customer UI supports Vacation, Sick Leave and Floating Holiday; status moves through Pending Manager, Pending HR, Approved, Denied/Rejected and Cancelled. It checks balance, dates, hours, reason and approval notes.

| Entity | Required production fields |
|---|---|
| `leave_types` | organization, code/name, paid status, unit (minutes/days), eligibility and active dates |
| `leave_policies` | leave type, accrual/carry-over/negative balance rules, effective dates, worker/location eligibility, policy version |
| `leave_policy_assignments` | employee/policy, effective dates, eligibility result |
| `leave_ledger_entries` | employee, leave type, minutes signed amount, source type/id, effective date, balance snapshot optional, audit reference |
| `leave_requests` | employee, leave type, requested range, calculated requested minutes, reason, status, submitted/cancelled/decided timestamps |
| `leave_request_decisions` | request, stage, decision, actor, note, decided time; supports manager then HR chain |

Do not store only `available` and `used` hours as the frontend mocks do. Display balances are derived from the ledger and approved/reserved requests. Public holidays, employee schedule, half days and timezone determine requested minutes server-side.

## 6. Onboarding, offboarding, documents and signatures

### 6.1 Onboarding and offboarding

Onboarding uses templates and per-person task instances. The UI demonstrates task title, description, category (IT, HR, Facilities, Training, Documents), assignee, due date, completion state, progress and at-risk status. It also supports converting hired candidates to an onboarding instance.

| Entity | Required production fields |
|---|---|
| `onboarding_templates` | organization, name, eligibility/role conditions, active/published version |
| `onboarding_template_tasks` | template/version, title, description, category, assignee rule, due-date rule, required flag, sort order |
| `onboarding_instances` | employee/candidate, template version, start date, status, progress derived, owner, created by |
| `onboarding_tasks` | instance, source template task, assignee user/team/employee, due date, status, completed by/at, note, dependency |
| `offboarding_instances` | employee, reason, last working day, owner, risk, status, progress derived |
| `offboarding_tasks` | instance, category, assignee, due date, access-revocation flag, status/evidence |

`progress` and `at risk` should be calculated/read models from task state and due dates, not unrestricted user inputs.

### 6.2 Documents and electronic signatures

The document areas require employee association, category/folder, status, dates, expiry monitoring, notes, source files, templates, multi-recipient signature requests and signed/expired states.

| Entity | Required production fields |
|---|---|
| `documents` | organization, employee nullable, title/file name, category, object key, MIME type, byte size, SHA-256, status, expires on, classification, retention schedule |
| `document_versions` | document, version number, object key/checksum, uploaded by/at, supersedes version; no in-place replacement |
| `document_access_grants` | document, principal (employee/user/role), access type, expiry; supplement policy checks where sharing is explicit |
| `document_templates` | organization, name, category, provider template reference, active version, merge fields |
| `signature_requests` | document/template version, provider, provider envelope ID, status, sent/expiry/completion timestamps, custom message |
| `signature_recipients` | signature request, recipient user/employee/external email, signing order, status, viewed/signed timestamps, provider evidence |
| `document_expiry_events` | document, schedule/reminder state, event timestamps |

The UI's “Signed/Pending/Draft/Expired” states are distinct from file scanning/upload states. Keep those state machines separate.

## 7. Payroll, compensation, rewards and benefits

The prototype contains a Uganda-focused payroll calculator and payroll run UI, with base salary, expected/actual/absent/overtime hours, allowances, bonuses, taxes/deductions, paystubs and settings. It also contains compensation bands, total reward statements and benefit plans.

| Entity | Required production fields |
|---|---|
| `payroll_configurations` | organization/country, currency, standard monthly minutes/hours, overtime/holiday multiplier, effective dates, statutory rule-set version |
| `payroll_periods` | organization, start/end, pay date, frequency, status (open/calculating/review/approved/locked/paid) |
| `payroll_runs` | payroll period, country rule version, initiated/approved/locked by, status, totals, idempotency/audit references |
| `payroll_run_items` | run, employee employment/compensation snapshot, base pay, earnings, deductions, taxes, employer cost, net pay — all minor units |
| `payroll_adjustments` | employee/period, type, minutes or amount, evidence/note, approval status, source attendance/leave reference |
| `paystubs` | payroll item, immutable rendered-document version, delivery status |
| `compensation_bands` | job role, currency, min/mid/max minor units, effective range |
| `benefit_plans` | organization, name, category, eligibility rules, employer/employee cost components, currency, description, active dates |
| `benefit_enrollments` | employee, plan, coverage tier, effective dates, status/cost snapshot |
| `reward_statements` | employee, effective date, compensation/benefits/equity snapshots, generated document |

**Critical:** the current Uganda calculator is a prototype and must not be treated as authoritative tax/payroll law. A production payroll engine needs versioned statutory rules, country-specific review, legal/accounting sign-off, test fixtures from qualified payroll experts and immutable payroll snapshots. No statutory calculation is implemented yet.

## 8. Hiring, performance, engagement, workflows and relations

These are not first-MVP persistence priorities, but their data model must not be blocked by the people core.

| Area | UI evidence | Required entities |
|---|---|---|
| Applicant tracking | jobs, candidate pipeline Sourced→Applied→Interviewing→Offer→Hired, source, rating, email/phone/location, custom application fields | job openings, application forms/fields, candidates, applications, pipeline stages, interviews/scorecards, offers, candidate-to-employee conversion record |
| Performance | review cycles, reviews, goals, check-ins, self/manager/360 feedback, completion | review cycles, review participants, questions/responses, goals, check-ins, feedback requests/responses |
| Engagement | pulse/eNPS/lifecycle surveys, department audience, response count, comments and trends | surveys, survey versions/questions, audience snapshots, anonymous response rules, responses/comments, aggregate read models |
| Workflows | trigger, category, owner, status, steps, approval inbox, activity | workflow definitions/versioned steps, conditions, runs, approval tasks, decisions, timers/escalations |
| Employee relations | warning/suspension/termination, category, incident description, severity, paid/unpaid suspension, dates, severance choice | relation cases, incidents, actions, suspension terms, termination terms, evidence/documents, restricted access grants |
| Promotions | employee, current/new role, effective date, salary increase %, rationale | promotion events linked to assignment and compensation records; route through approval workflow |

## 9. Billing, platform operations and integrations

The back office is evidence that this is a SaaS platform, not merely an HR database.

| Platform area | Core entities needed |
|---|---|
| Subscription/billing | plans, plan prices, organization subscriptions, entitlements, payment customers, invoices, payment attempts, refunds, provider webhook events |
| Support | tickets, comments, attachments, organization references, SLA policy, assignment, support-access session and reason |
| Feature configuration | feature flags, targeting rules, organization overrides, audit history; flags do not replace authorization |
| Data management | imports/import rows/errors, exports, data-subject requests, retention policies, legal holds |
| Operator identity | platform operators, operator roles/permissions, MFA state, support-session approvals |
| Audit/security | immutable audit events, security events, consent/change records, alert/findings and review tasks |
| Integrations/webhooks | installed integrations, encrypted credentials/reference, connection state, sync cursors, webhook subscriptions, deliveries and retry history |
| Operations | background job metadata, incidents, service health measurements; Asynq remains the execution mechanism, not product source of truth |

## 10. Data classification

| Classification | Examples | Controls |
|---|---|---|
| Internal | departments, role catalog, published policies | tenant role access and normal audit |
| Confidential | work contact, attendance, leave, performance | tenant/scoped authorization, audit mutations and sensitive reads |
| Highly restricted | birth date, identity documents, compensation, bank/tax details, disciplinary evidence, salary/payroll | separate permission, field-level filtering, strong audit, export controls, encrypted storage where appropriate |
| Platform restricted | payment credentials, provider secrets, platform operator activity | separate platform identity, secret manager, least privilege |

## 11. Gaps requiring product decisions before implementation

These are deliberately not guessed in code:

1. Legal entity model: can one customer organization have multiple legal entities, countries, payroll calendars and currencies?
2. Employment types: supported classifications (employee, contractor, intern, part-time, fixed term) and local rules.
3. Permission matrix: exact actions and field visibility for employee, manager, HR Admin, owner, payroll admin, support and platform operators.
4. Attendance evidence: consent, geofence accuracy, photo/kiosk policy, IP restrictions, offline clocking and correction approvals.
5. Leave rules: accrual, carryover, negative balance, public-holiday/weekend counting, half-day/hourly leave and approval escalation per country.
6. Payroll scope: calculation-only versus payments/filing; country rollout; statutory rule ownership and validation process.
7. Data retention/deletion: jurisdiction, employee/candidate retention windows, legal holds and audit-log retention.
8. Billing geography: supported merchants, settlement currencies, mobile-money provider contract and tax invoicing.
9. E-signature jurisdiction/consent: provider, evidence level, templates, retention and signature order.
10. Survey anonymity threshold and who may access comments/segment results.

## 12. Implementation order after discovery

1. Correct organization/workspace/location/department schema and company-registration command.
2. Implement identity, membership, role/permission and tenant middleware; establish the exact permission matrix.
3. Implement employees, employment assignments/terms and scoped profile reads.
4. Add documents/onboarding, then leave ledger/requests and attendance/shift foundations.
5. Only after those history/security foundations are tested, add payroll, signatures, relations, hiring and workflows.

No production API route will be connected to the frontend until its entity ownership, permissions, audit requirements, state transitions and migration are represented in this specification.
