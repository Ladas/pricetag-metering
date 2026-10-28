# Account and administration controls

Managed mode extends the existing **Administration** page:

- **Users:** create/update a non-email login, suspend/reactivate it, and issue a finite onboarding key. Use the existing user table to change roles and managers.
- **Safety Net:** use the existing global policy controls; the extension adds per-user USD overrides and return to the global default.
- **Pricing Discounts:** use existing discounts/refresh; the optional custom-rate extension sets future model prices. Historical repricing stays API-only.

**My keys** (`/management`) supports personal key creation, listing and revocation. It has no administrator editor. Each operation uses a service API with live authorization; hiding a control is not authorization. Transfer one-time keys privately. Provider configuration remains outside this demo UI.
