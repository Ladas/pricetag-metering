# Atlas/AIR/AIBH Partner APIs

This is the integration contract for Atlas/AIR and AI Budget Helper (AIBH)
backends calling PriceTag Metering. All endpoints below are server-to-server
APIs. The summary is intentionally first so client teams can see the complete
surface before reading the detailed examples.
Authentication is provided by the EnMaaS OpenShift Route/AuthPolicy, not by
the Metering application listener.

## API summary

| Capability | Method and path | What it does | Main response |
|---|---|---|---|
| Create user | `POST /api/v1/users` | Strict create of a UUID-keyed user and tags | Created user (`201`) |
| Upsert user | `PUT /api/v1/users/{user_id}` | Create or replace the complete tag set | User (`201` or `200`) |
| Read/search users | `GET /api/v1/users/{user_id}`<br>`GET /api/v1/users?tag.<name>=<value>` | Read one user or exact tag-filtered pages | User or `{users, has_more}` |
| Deactivate user | `DELETE /api/v1/users/{user_id}` | Soft-disable and revoke only keys minted by this API | `{user_id, active, keys_revoked}` |
| Reactivate user | `POST /api/v1/users/{user_id}/reactivate` | Re-enable after key revocation completes | User |
| List keys | `GET /api/v1/users/{user_id}/keys` | List MaaS key metadata through Metering | `{user_id, keys}`; no plaintext keys |
| Mint key | `POST /api/v1/users/{user_id}/keys` | Validate user, then mint through Metering → MaaS | MaaS key response; plaintext once |
| Revoke key | `DELETE /api/v1/users/{user_id}/keys/{key_id}` | Revoke only a key minted by this API | `{user_id, key_id, status}` |
| Batch usage | `POST /api/v1/usage/reports` | Usage for up to 1,000 UUIDs over `[from,to)` | Users with tags, totals, model rows |
| Model allowlist | `GET/PUT/DELETE /api/v1/model-policies/users/{user_id}/allowlist` | Read, replace, or clear exact per-user allowed models | `{user_id, enabled, models}` |
| Model discovery (gateway) | `GET https://api.enmaas.devshift.net/v1/models` | List models accessible to the presented MaaS API key | OpenAI-compatible `{object, data}` model list |

### Model discovery

Model discovery already exists on the EnMaaS gateway; no duplicate Metering
proxy is needed:

```http
GET https://api.enmaas.devshift.net/v1/models
Authorization: Bearer <MAAS_API_KEY>
```

The API key is the caller's MaaS inference key and must be supplied by the
trusted backend, never browser code or this document. MaaS/gateway applies the
key's tenant, subscription, and model-access rules before returning the list.
The response is OpenAI-compatible:

```json
{
  "object": "list",
  "data": [
    {
      "id": "claude-sonnet-4-5",
      "object": "model",
      "created": 0,
      "owned_by": "vertex"
    }
  ]
}
```

Observed in the EnMaaS test environment on 30 September 2026:

- No `Authorization` header returned `401 Unauthorized`.
- A valid MaaS API key returned `200` and 10 accessible models, including
  `claude-sonnet-4-5`, `claude-sonnet-5`, `claude-haiku-4-5`,
  `claude-opus-4-5`, and `claude-opus-4-6`.

The existing `/api/v1/pricing` endpoint is a PriceTag dashboard/session API,
and `/api/v1/admin/models` is a super-admin API; they are not the discovery
contract for Atlas/AIR/AIBH.

## Authentication

Do not send a Metering bearer token. The OpenShift route must authenticate the
Atlas/AIR/AIBH caller before forwarding to the Metering service and must prevent
direct Service access or port-forward bypass. The route owner is responsible
for the AuthPolicy, workload identity/mTLS, and any IP restrictions.

Key endpoints additionally require `PARTNER_USER_KEY_GROUP`, the MaaS group
presented on every key operation. There is no default: the group must exist in
MaaS with an accessible subscription, and until it is configured the key
endpoints (list, mint, revoke, deactivate) answer `503`. Its value is an
operator decision tracked in PriceTag #31 (SSO is the canonical group source).

The authenticated route may forward `X-Partner-Client: <name>`
(`[a-z0-9._-]`, ≤32 chars) so audit rows distinguish, for example, `atlas`
from `aibh-refresh`. This header is audit metadata, not authentication.

```http
X-Partner-Client: atlas
Content-Type: application/json
```

The Atlas backend must validate its own SSO session and derive the canonical
`user_id` before calling these APIs. The OpenShift route must not trust
caller-supplied identity headers without validating the caller first.

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
usage attribution and fills the first/last name into the PriceTag dashboard
profile when none is set there (a name an admin already entered is never
overwritten). The MaaS username is stored exactly as the email was sent —
case is preserved — while email uniqueness across users is case-insensitive.
Partner users are not added to the dashboard's People & Org directory; group
membership there remains an admin action.

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
then revokes **every key minted through this API** for the user, one by one,
as the key's owner. Keys the user obtained elsewhere (PriceTag dashboard,
MaaS directly) are deliberately left untouched: this API only manages what it
issued. The model-policy denial is enforced on inference only when the Praxis
model-policy preflight is enabled; successful revocation of the partner-minted
keys is the completion criterion. A successful response is:

```json
{"user_id":"123e4567-e89b-12d3-a456-426614174000","active":false,"keys_revoked":1}
```

If MaaS revocation fails, the API returns `502` and leaves the user inactive
with `key_revocation_pending=true`. Repeat the same `DELETE` to retry the
idempotent MaaS revocations. Do not reactivate a user until all keys are
revoked. Once revocation completes, a user can be reactivated with
`POST /api/v1/users/{user_id}/reactivate`; this preserves the tags and login
history and allows new key issuance.

## MaaS keys

MaaS remains the system of record for keys; Metering records only the **ids**
of keys it minted (never the secret) so it can later revoke exactly those.
These endpoints act on MaaS **as the user's own login**, so they need no MaaS
admin privileges and can never touch another user's keys.

### List a user's keys

```http
GET /api/v1/users/{user_id}/keys
```

Returns `{"user_id": ..., "keys": [...]}` with MaaS key metadata (id, name,
status, subscription, creation/expiration/last-used dates) for every login
ever mapped to the user, each with `partner_managed: true|false` indicating
whether this API minted it. Plaintext keys are never included.

### Revoke one key

```http
DELETE /api/v1/users/{user_id}/keys/{key_id}
```

Only keys minted through this API can be revoked here; the call is
idempotent. Returns `{"user_id": ..., "key_id": ..., "status": "revoked"}`. A
key that this API did not mint for this user (including the user's dashboard
keys, and unknown ids) returns `404`.

### Mint a key

```http
POST /api/v1/users/{user_id}/keys
```

```json
{"name":"Atlas Alice primary"}
```

Metering confirms the user exists and is active, then calls MaaS with
`tags.email` and the configured `PARTNER_USER_KEY_GROUP`; callers cannot
override the group. There is no default group.
The MaaS environment must have a subscription accessible to that username and
group. A missing/inaccessible subscription makes MaaS reject key minting and
Metering returns `502`.

The response is MaaS's created API-key record and includes the raw key exactly
once. Metering stores the key id, owner login and name (for later revocation)
but never the secret, and never logs it. The response has
`Cache-Control: no-store`; the client must persist the returned `key` securely
at once. The requested key `name` is required and limited to 128 characters.

Mints are bounded to a few in flight per Metering replica; beyond that the
API answers `429` with `Retry-After`. Metering holds no database connection
while waiting on MaaS. If the user is deactivated while a mint is in flight,
or the mint cannot be recorded, the freshly minted key is revoked again and
the call fails (`409` / `500`) — a key is only ever returned once it is tracked.

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

> **Breaking change.** Before this revision the path segment was the MaaS
> username (email). It is now the stable UUID; an email in the path returns
> `400`. No username-keyed policies had been created in any environment when
> this changed, and legacy rows, if any, remain enforced read-only.

Policies use the stable UUID rather than an email/MaaS username:

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
| `404` | Unknown user UUID |
| `409` | Duplicate ID/email, inactive user, or operation conflict |
| `429` | Too many concurrent key mints on this replica; honour `Retry-After` |
| `502` | MaaS key mint or key revocation failed |
| `503` | `PARTNER_USER_KEY_GROUP` is not configured, or the route/AuthPolicy is unavailable |
| `500` | Metering database or internal operation failed |

All API responses use `Cache-Control: no-store`. Keep the routes behind HTTPS
and do not expose a catch-all route to the Metering listener.
