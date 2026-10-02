# Standalone dashboard login

The metering service and its dashboards can run with PostgreSQL outside
OpenShift. To accept administrator-issued gateway JWTs, set all of:

```text
DASHBOARD_JWT_PUBLIC_KEY_FILE=/run/secrets/gateway-public.pem
DASHBOARD_JWT_ISSUER=https://secure-single-server.local
DASHBOARD_JWT_AUDIENCE=praxis-gateway
DASHBOARD_ORIGIN=https://gateway.example.com:8443
SESSION_SECRET=<persistent random secret of at least 32 bytes>
SUPERADMIN_USERS=admin
M2M_AUTH_REQUIRED=true
M2M_SHARED_SECRET=<separate random service credential>
MONTHLY_TOKEN_QUOTA=0
```

Mount only the RSA public verification key (at least 2048 bits). Keep the
signing key with the administrator. Accepted caller tokens use RS256 and
require `iss`, `aud`, `sub`, `iat` and `exp`; `nbf`, when present, is checked.
Subjects use the gateway issuer's 1–128 character alphanumeric, dot,
underscore, hyphen and `@` format. Groups are not taken from these tokens.
Admin usernames match exactly, including case and any email domain.

Users paste the same JWT they use in their harness into `/login`. The secure,
HttpOnly, SameSite=Lax session expires at the earlier of JWT expiry or eight
hours. Rotating the public key, issuer, audience or session secret invalidates
existing sessions. There is no per-token revocation list; renew with the same
subject to retain spending history. Cookie-authenticated mutations and login
require the exact configured HTTPS Origin. GET-based impersonation is disabled
in this mode. Authenticated APIs also accept the caller JWT as a Bearer token.

Expose dashboard routes through a TLS proxy. The app's HTTP port and database
stay private. Caller JWTs never authenticate `/api/v1/events` or
`/api/v1/customers/`; these require the separate M2M secret. The proxy must also
block those paths from public clients. Leave Kubernetes and MaaS key-management
routes private when those integrations are absent.

Explicit `MONTHLY_TOKEN_QUOTA=0` disables only the legacy token gate. Enable
the existing USD policy separately through the admin dashboard/API. Its
default is tracking only. Per-person monthly allowances, overrides and grants
are unchanged. Enforcement follows recorded spending and the existing decision
cache; it is not a reservation system or an exact upper bound on concurrent spend.

With the JWT variables unset, the existing MaaS credential-validation login
continues to work. Partial JWT configuration fails startup. The Dockerfile
builds for the builder's native architecture, including arm64 Podman machines.
