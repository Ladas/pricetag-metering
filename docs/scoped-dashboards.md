# Scoped personal and manager views

`USER_DASHBOARDS_ENABLED=true` enables the existing personal and manager views; it does not grant administrator permissions. Leave it unset to retain restricted defaults. Use the managed-account checks and key protections when deploying this with a non-Kubernetes directory. Directory browsing remains administrative, and manager scope follows reporting relationships. Personal spend can show the applicable monthly USD allowance; an exception is not automatic model fallback.
