# Per-user Model Allowlist API

The canonical partner API contract, including UUID-based model-policy
operations, is documented in [Partner User Directory, Key, Usage, and
Model-Policy APIs](partner-user-directory-api.md#per-user-model-allowlist).

Policies are keyed by stable user UUID. Gateway entitlements resolve each
current or historical MaaS login to that UUID, so email changes do not detach
the model policy from the SSO identity.
