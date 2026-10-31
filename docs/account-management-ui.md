# Account and administration controls

Managed mode extends the existing **Administration** page:

- **Users:** create/update a login and suspend/reactivate it. Use the user table to change roles and managers. User details show searchable, paginated active keys and a confirmed revoke-all action.
- **User keys:** issue a finite onboarding key; copy or download it privately.
- **Safety Net:** configure the default monthly allowance, enforcement and models allowed after the limit.
- **User quota:** set or remove per-user USD overrides. Search and page through existing overrides.
- **Pricing Discounts:** manage discounts and refresh imported prices.
- **Model pricing:** set custom rates for future requests and search the current model rates. Historical repricing stays API-only.

**My keys** (`/management`) opens the caller’s key list automatically. Create a key in the first card; use the table below to search by name or key ID, filter **Active / Revoked / Expired / All**, and revoke active keys. Tables default to five rows with 20/50-row options, Previous/Next buttons and a page indicator. Revoked and expired records remain available for audit. Keys that expire while the page is open move to the appropriate filtered view.

The key service’s pinned search implementation does not apply text filters. My keys therefore loads owner-scoped metadata pages before filtering locally, with a maximum of 5,000 records. Failed or incomplete loads display an error instead of partial search results. Use **Refresh** for changes made elsewhere. The key list never contains the secret key value; newly issued keys remain available only through the private copy/download controls.

Every operation uses a service API with live authorization. Impersonation cannot manage keys. Provider configuration remains outside this UI.

## Tests

Run `node --test tests/ui/*.test.cjs` for unit tests. With Playwright and Chromium installed, run `node tests/ui/table-browser.cjs` for fixture-backed browser checks. These tests intercept all API requests and use synthetic metadata; the Secure Single Server deployment suite separately tests real service authorization and key lifecycle with mock inference.
