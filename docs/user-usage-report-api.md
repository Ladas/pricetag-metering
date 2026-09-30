# User Usage Report API

The canonical partner API contract, including the UUID-based batch usage
report, is documented in [Partner User Directory, Key, Usage, and Model-Policy
APIs](partner-user-directory-api.md#batch-usage-report).

The older `GET /api/v1/usage/users/{username}` endpoint remains available for
existing integrations. New integrations should use
`POST /api/v1/usage/reports`, which accepts stable user UUIDs and an explicit
time range and returns tags alongside each user's usage.
