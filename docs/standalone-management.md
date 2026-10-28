# Opt-in standalone management candidate

This candidate adds service-owned management APIs and a browser editor for
installations without Kubernetes. It is not a production-readiness claim.
Existing administrator-only dashboard access remains the default.

## Enable the capability

Configure these values through protected runtime configuration; never commit
credentials or expose the metering service directly to untrusted clients.

| Setting | Purpose |
| --- | --- |
| `USER_DASHBOARDS_ENABLED=true` | Enable existing personal and manager views with server-side identity/reporting scope. Does not grant administrator access. |
| `MANAGEMENT_API_SECRET` | High-entropy operator credential; enables management APIs and active-profile checks. Keep private. |
| `MANAGEMENT_PUBLIC_ORIGIN` | Exact browser origin, such as `https://gateway.example:8443`; required for browser mutations/login. Never derive it from forwarded headers. |
| `SESSION_SECRET` | Persistent secret signing browser sessions. Protect and rotate as an operator credential. |
| `MAAS_VALIDATE_URL` | Private Models as a Service validation endpoint for browser login keys. |
| `KEY_SERVICE_URL` | Private Models as a Service base URL for personal key lifecycle. Configure identity header names to match that service. |
| `PARTNER_USER_KEY_GROUP` | MaaS group used for administrator-issued onboarding keys; requires the matching MaaS authorization configuration. |
| `GATEWAY_MANAGEMENT_SOCKET` | Optional absolute Unix-socket path of a separately installed gateway adapter. |
| `GATEWAY_MANAGEMENT_SECRET` | Separate capability authenticating adapter requests; never sent to the browser. |

Provision the initial administrator profile through the private operator API
before signing in. The deployment owns TLS termination, ingress restrictions,
header stripping, service configuration and the adapter installation. It must
strip caller identity headers and prevent public access to internal accounting
and MaaS administration endpoints. Internal HTTP requires trusted peers; it is
not mTLS.

## Browser and API operations

The `/management` page provides user creation/edit/suspension, reporting
relationships, USD overrides, onboarding keys, personal keys and custom prices.
Super-administrator authorization protects administration; ordinary users can
manage only their own keys. Manager status follows reporting relationships.
Imported-catalog discounts and the global post-budget exception remain in the
administrator dashboard. Historical repricing remains a separate API operation.

| Resource | Operations |
| --- | --- |
| `/api/v1/management/users` | GET profiles/login links; PUT a stable login profile, role or active state |
| `/api/v1/management/budgets` | GET overrides; PUT a per-login monthly USD allowance; DELETE an override |
| `/api/v1/management/prices` | GET rates; PUT four explicit rates or copy a source rate |
| `/api/v1/management/policy` | GET/PUT the global safety-net policy |
| `/api/v1/management/keys` | POST a finite onboarding key as the selected active user |
| `/me/keys` and `/me/keys/{id}` | Personal list/create/revoke; MaaS enforces ownership and lifetime limits |
| `/api/v1/admin/partner-users/{id}` | Existing administrator API for reporting relationships and roles |
| `/api/v1/management/gateway/config` | GET redacted configuration/revision; PUT asynchronous activation |
| `/api/v1/management/gateway/status` | GET the current activation result |
| `/api/v1/management/gateway/discover` | POST discovery using an explicitly provided credential |

Management endpoints accept a dedicated operator Bearer credential or an
administrator browser session. Existing administrator/team endpoints retain
session authorization. A browser mutation must supply the configured Origin;
keys and secrets must never be placed in URLs. API consumers must handle errors
and must not blindly retry key issuance after an ambiguous transport failure.

## Identity, sessions and quotas

- Profiles preserve a stable UUID and accounting history when edited or suspended.
  Non-email logins are linked explicitly; the existing partner/SSO API contract
  is unchanged.
- In management mode, unknown/inactive/revocation-pending profiles are denied
  inference entitlement and authenticated browser access. Directory failure is
  an error; the gateway must fail closed.
- Suspension preserves keys. Reactivation can restore access for still-valid
  keys and sessions. Permanent bulk key revocation is a separate existing workflow.
- Browser cookies are signed sessions, not JWTs. Key revocation/expiry does not
  automatically terminate an established session. Profile suspension blocks it;
  requests already running are not cancelled.
- View-as/logout navigation shows a confirmation; same-origin POST changes the
  session. Impersonated sessions cannot create personal keys.
- The post-budget model exception is global and names the recorded upstream model
  ID. It bypasses USD denial, not the separate token safety limit. It neither
  rewrites client models nor promises zero budget overshoot.

## Pricing and gateway responsibilities

Custom rates and imported refresh freeze legacy unpriced rows before updating
prices. New ledger entries store their charge; changing future rates does not
silently reprice existing entries. Explicit historical repricing can change them.
Large legacy backfills/concurrent production upgrades need separate qualification.
Source-rate copies are snapshots; manual rates are not automatically discounted
by imported-catalog refresh. A pricing row does not publish an inference route.

The gateway adapter owns routing files and Podman secrets, not business tables.
It validates host-approved origins and model metadata. The UI never submits
commands, filters, file paths or secret names. Activation is asynchronous;
operation status is in memory, so reload actual configuration after a metering
restart. Durable gateway rollback belongs to the adapter. A privileged host
adapter is not a sandbox merely because it uses a Unix socket.

## Validation

The candidate has isolated PostgreSQL integration tests, handler authorization
regressions, key-service client tests and JavaScript UI tests. The companion
single-server candidate exercises browser operations, native mock inference,
quota exceptions, recovery and concurrent accounting on RHEL. Mock tests do not
establish real-provider compatibility, billing accuracy or production support.
