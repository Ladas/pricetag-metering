# Partner User Directory, Key, Usage, and Model-Policy APIs

This is the integration contract for Atlas/AIR and AI Budget Helper (AIBH)
backends calling PriceTag Metering. All endpoints are server-to-server APIs.
Never put a bearer token in browser code, URLs, logs, telemetry, or source
control.

## Authentication

Send the endpoint-specific bearer credential in the `Authorization` header.
Metering compares credentials in constant time and returns `503` when the
corresponding secret is not configured.

| API | Environment variable | Deployment secret |
|---|---|---|
| User directory and key issuance | `USER_MANAGEMENT_API_SECRET` | `metering-user-management-api` (`token`) |
| Batch usage report | `USAGE_REPORT_API_SECRET` | `metering-partner-api` (`usage-report`) |
| Model allowlist | `MODEL_POLICY_API_SECRET` | `metering-partner-api` (`model-policy`) |

```http
Authorization: Bearer <secret-from-approved-secret-manager>
Content-Type: application/json
```

The bearer credentials are backend credentials, not Atlas/AIBH user sessions.
The Atlas backend must validate its own SSO session and derive the canonical
`user_id` before calling these APIs.

## User identity and tags

`user_id` is the stable SSO/Atlas UUID. It is distinct from the email address,
the MaaS username, and MaaS's UUID for an individual API key. Metering stores
tags as a JSON object; `tags.email` is the MaaS username used for key creation
and inference usage attribution.

Required when creating or replacing a user:

| Tag | Requirement |
|---|---|
| `email` | Required bare email address; unique case-insensitively. Used as MaaS username. |
| `first_name` | Required non-empty string. |
| `last_name` | Required non-empty string. |
| `manager_uuid` | Optional; when present, a UUID string or `null` (an empty string is normalized to `null`). |

Additional tags are supported as string values. Tag keys use lowercase
letters/digits plus `_`, `.`, and `-`; at most 100 tags are accepted, each key
is at most 64 characters, and each string value is at most 2048 bytes. The
`PUT` operation replaces the full tag object, so include all desired tags in
each update. JSON request bodies are limited to 64 KiB.

Example tags:

```json
{
  "email": "alice@example.com",
  "first_name": "Alice",
  "last_name": "Example",
  "manager_uuid": "123e4567-e89b-12d3-a456-426614174000",
  "department": "engineering",
  "employee_number": "004281"
}
```

## User directory CRUD and tag search

### Create

```http
POST /api/v1/users
```

```json
{
  "user_id": "123e4567-e89b-12d3-a456-426614174000",
  "tags": {
    "email": "alice@example.com",
    "first_name": "Alice",
    "last_name": "Example",
    "manager_uuid": null,
    "department": "engineering"
  }
}
```

Returns `201 Created`, `Location: /api/v1/users/{user_id}`, and the created
record. Creating the record also links `tags.email` to the stable UUID for
usage attribution and syncs the first/last name into the PriceTag dashboard
profile.

### Read or search

```http
GET /api/v1/users/{user_id}
GET /api/v1/users?tag.department=engineering&tag.manager_uuid=<uuid>&limit=50&offset=0
```

Tag filters are exact matches and combine with AND. The list defaults to 50
records and accepts a `limit` from 1 to 100 and a non-negative `offset`.
Inactive records are excluded by default; set `include_inactive=true` to
include deactivated users. Response:

```json
{
  "users": [
    {
      "user_id": "123e4567-e89b-12d3-a456-426614174000",
      "tags": {
        "email": "alice@example.com",
        "first_name": "Alice",
        "last_name": "Example",
        "manager_uuid": null,
        "department": "engineering"
      },
      "active": true,
      "created_at": "2026-09-30T12:00:00Z",
      "updated_at": "2026-09-30T12:00:00Z"
    }
  ],
  "has_more": false
}
```

### Upsert (replace tags, or create)

```http
PUT /api/v1/users/{user_id}
```

`PUT` is an upsert: an unknown `user_id` is created (`201 Created`), a known
one has its full tag object replaced (`200 OK`). An SSO front end can send the
user's current profile on every visit; `POST /api/v1/users` remains available
when a strict create-only (`409` on duplicate) is wanted.

```json
{
  "tags": {
    "email": "alice.new@example.com",
    "first_name": "Alice",
    "last_name": "Example",
    "manager_uuid": null,
    "department": "engineering",
    "employee_number": "004281"
  }
}
```

Email changes create a new current MaaS login mapping and retain the prior
login mapping for historical reporting and policy enforcement. Keys minted
before an email change remain valid until revoked; newly minted keys use the
new email. An email that is, or ever was, mapped to a different `user_id`
cannot be assigned (`409`), so historical usage is never re-attributed.

Tag and status changes take effect on inference within the Metering
entitlement cache window (up to 15 seconds per replica).

### Deactivate

```http
DELETE /api/v1/users/{user_id}
```

Deletion is a soft deactivation: tags and login history remain stored. Metering
first disables future key issuance and forces the model policy to deny all,
then asks MaaS to revoke keys for every current and historical login. The
model-policy denial is enforced on inference only when the Praxis model-policy
preflight is enabled; successful MaaS revocation is the completion criterion.
A successful response is:

```json
{"user_id":"123e4567-e89b-12d3-a456-426614174000","active":false,"keys_revoked":true}
```

If MaaS revocation fails, the API returns `502` and leaves the user inactive
with `key_revocation_pending=true`. Repeat the same `DELETE` to retry the
idempotent MaaS revocations. Do not reactivate a user until all keys are
revoked. Once revocation completes, a user can be reactivated with
`POST /api/v1/users/{user_id}/reactivate`; this preserves the tags and login
history and allows new key issuance.

## MaaS keys

Metering does not store keys; MaaS remains the system of record. These
endpoints act on MaaS **as the user's own login**, so they need no MaaS admin
privileges and can never touch another user's keys.

### List a user's keys

```http
GET /api/v1/users/{user_id}/keys
```

Returns `{"user_id": ..., "keys": [...]}` with MaaS key metadata (id, name,
status, subscription, creation/expiration/last-used dates) for every login
ever mapped to the user. Plaintext keys are never included.

### Revoke one key

```http
DELETE /api/v1/users/{user_id}/keys/{key_id}
```

Returns `{"user_id": ..., "key_id": ..., "status": "revoked"}`. A key that
does not belong to this user (or does not exist) returns `404`; MaaS does not
distinguish the two cases, by design.

### Mint a key

```http
POST /api/v1/users/{user_id}/keys
```

```json
{"name":"Atlas Alice primary"}
```

Metering confirms the user exists and is active, then calls MaaS with
`tags.email` and the fixed MaaS group `GE`; callers cannot override the group.
The MaaS environment must have a subscription accessible to that username and
group. A missing/inaccessible subscription makes MaaS reject key minting and
Metering returns `502`.

The response is MaaS's created API-key record and includes the raw key exactly
once. It is not stored or logged by Metering. The response has
`Cache-Control: no-store`; the client must persist the returned `key` securely
at once. The requested key `name` is required and limited to 128 characters.

## Batch usage report

```http
POST /api/v1/usage/reports
```

```json
{
  "user_ids": [
    "123e4567-e89b-12d3-a456-426614174000",
    "123e4567-e89b-12d3-a456-426614174001"
  ],
  "from": "2026-09-01T00:00:00Z",
  "to": "2026-10-01T00:00:00Z"
}
```

`from` and `to` are RFC3339 timestamps defining a half-open interval
`[from,to)`. The range must be ordered, no longer than 366 days, and not in the
future. Supply 1–1000 UUIDs. Duplicate IDs are collapsed. The response includes
each known requested user, its stored tags, active status, and usage; users
with no events receive zero totals and an empty `models` list. Unknown IDs are
listed in `missing_user_ids`.

```json
{
  "from": "2026-09-01T00:00:00Z",
  "to": "2026-10-01T00:00:00Z",
  "as_of": "2026-09-30T12:00:00Z",
  "users": [
    {
      "user_id": "123e4567-e89b-12d3-a456-426614174000",
      "tags": {"email":"alice@example.com","first_name":"Alice","last_name":"Example"},
      "active": true,
      "totals": {
        "requests": 42,
        "promptTokens": 12000,
        "completionTokens": 3400,
        "totalTokens": 15400,
        "cachedInputTokens": 2300,
        "cacheCreationTokens": 800,
        "reasoningTokens": 500,
        "estimatedCostUsd": 0.4821
      },
      "models": [
        {
          "provider": "anthropic",
          "model": "claude-sonnet-4-5",
          "requests": 42,
          "promptTokens": 12000,
          "completionTokens": 3400,
          "totalTokens": 15400,
          "cachedInputTokens": 2300,
          "cacheCreationTokens": 800,
          "reasoningTokens": 500,
          "estimatedCostUsd": 0.4821
        }
      ]
    }
  ],
  "missing_user_ids": []
}
```

Usage is aggregated across all MaaS usernames retained for the UUID, including
prior email logins. `estimatedCostUsd` uses PriceTag's current model prices and
fallback rates and is an estimate, not a provider invoice. This report is
read-only and does not create quota-denial records.

## Per-user model allowlist

Policies now use the stable UUID rather than an email/MaaS username:

```http
GET    /api/v1/model-policies/users/{user_id}/allowlist
PUT    /api/v1/model-policies/users/{user_id}/allowlist
DELETE /api/v1/model-policies/users/{user_id}/allowlist
```

Replace the complete allowlist with an exact, case-sensitive list of up to 50
model IDs. An empty list denies all models; `DELETE` clears the additional
restriction and returns the user to baseline MaaS/Praxis policy.

```json
{"models":["claude-sonnet-4-5","gpt-5.6-luna"]}
```

MaaS usernames observed by the gateway resolve through the retained login map
to this UUID policy. Inactive users fail closed. Existing MaaS and Praxis model
permissions and Metering quotas continue to apply independently.

## Relationship to the architect design

This contract implements the user/key model in James Russell's "MaaS API /
User model" whiteboard with these deliberate differences:

| Whiteboard | This contract | Why |
|---|---|---|
| MaaS issues an internal `MaaS_UID`; the RH UUID is an `external_id` tag | The caller's immutable SSO UUID **is** `user_id` | One identifier end to end; no mapping table for Atlas/AIR/AIBH to keep in sync. If a MaaS-issued ID is later required, it can be added as a column without changing the API. |
| `POST /users/upsert` | `PUT /api/v1/users/{user_id}` (upsert) and `POST /api/v1/users` (strict create) | Same semantics, resource-style paths. |
| `GET /get_mtd_usage/<UID>` per user | `POST /api/v1/usage/reports` for up to 1000 users and any range ≤ 366 days | Month-to-date is `from` = first of month; batch avoids N calls per poll. |
| Metering holds a Keys table | MaaS is the only key store; Metering proxies list/mint/revoke self-scoped | Avoids a second copy of key state that can drift from MaaS. |
| "Principals" (humans and bots) with optional name/email | `email`, `first_name`, `last_name` required | The MaaS username that owns keys is the email today, so a principal without one cannot mint. Bot/service principals are a follow-up once MaaS ownership can be non-email. |

Search-by-tag, tag-carrying user records, active/disabled status, hourly full
listing (`GET /api/v1/users` paginated) and per-user usage match the
whiteboard directly.

## Common status codes

| Status | Meaning |
|---|---|
| `400` | Invalid UUID/tags/time range/body or unsupported query parameter |
| `401` | Missing or incorrect bearer credential |
| `404` | Unknown user UUID |
| `409` | Duplicate ID/email, inactive user, or operation conflict |
| `502` | MaaS key mint or key revocation failed |
| `503` | Endpoint credential is not configured |
| `500` | Metering database or internal operation failed |

All API responses use `Cache-Control: no-store`. Keep the routes behind HTTPS
and do not expose a catch-all route to the Metering listener.
