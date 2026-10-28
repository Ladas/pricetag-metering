# Managed identities and USD budgets

With managed-account mode enabled, `/api/v1/management/users` provides GET/PUT for stable subjects and active state; `/budgets` provides GET/PUT/DELETE for per-user USD overrides; `/policy` reuses global policy management. A dedicated private operator credential or current super-administrator session is required. Browser writes must match `MANAGEMENT_PUBLIC_ORIGIN`. Provision the initial administrator over the private API before browser login.

Suspension preserves identity and history but denies entitlement and browser access. Missing directory state fails closed. Existing organization APIs own manager relationships. This mode does not supply OIDC, approval workflows or cryptographic authentication of internal network peers.
