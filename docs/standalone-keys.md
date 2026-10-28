# Managed personal keys

The opt-in `MANAGEMENT_API_SECRET` enables a private operator credential and managed-account checks. Set `MANAGEMENT_PUBLIC_ORIGIN` to the exact HTTPS browser origin. Protect this credential and block public operator access at the gateway.

Provision accounts in the partner directory before enabling this mode. Existing deployments keep their default behavior when it is unset. The managed onboarding endpoint issues finite keys for existing active users. Personal key operations always use the caller identity; stale administrator groups do not grant cross-user access. MaaS enforces its maximum lifetime. Revoking a key does not terminate an already established browser session.
