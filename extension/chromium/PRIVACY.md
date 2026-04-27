# Sellico Ads Intelligence — Privacy Policy

**Effective date**: 2026-04-27
**Operator**: Sellico, sellico.ru

This extension exists to enrich your existing Wildberries Seller Cabinet with
Sellico's bid intelligence and to feed Sellico's analytics with bid /
position snapshots that you, as a Sellico customer, have explicitly opted
into capturing.

## 1. What we collect

When you are signed into Sellico AND visit a page on `seller.wildberries.ru`
or `cmp.wildberries.ru`, the extension may capture:

- **Bid snapshots** — campaign ID, NM ID, bid value, timestamp.
- **Position snapshots** — search query, position rank, page URL, timestamp.
- **UI signals** — clicks/views on bid widgets you've configured Sellico to
  track (used for "this CPC was set manually at 14:32" telemetry).
- **Page context** — the cabinet ID and active campaign ID currently in the
  URL bar, so the panel can show relevant Sellico data.

We do NOT collect:
- Your Wildberries password or any login credential.
- Order data, customer PII, financial transactions, balance information.
- Browsing history outside the two `*.wildberries.ru` host patterns above.
- Page contents from any non-WB site.

## 2. Where the data goes

Captured events are sent over HTTPS only to `https://api.sellico.ru`
(or, in development builds, the localhost API the user has explicitly
authorised via Settings → Optional Permissions). They are stored in your
Sellico workspace, accessible only to you and the workspace members you've
invited.

Data is encrypted at rest in our PostgreSQL database. WB API tokens you
configure for sync are encrypted with AES-256-GCM under a workspace key.

## 3. Data retention

- Bid and position snapshots: retained as long as your Sellico subscription
  is active.
- On account deletion: all captured data is deleted within 30 days.

## 4. Permissions explained

| Permission | Why we need it |
|------------|----------------|
| `storage` | Save your Sellico backend URL and access token between sessions. |
| `cookies` | Read the Sellico session cookie so the extension can share auth with the web app. |
| `host_permissions: seller.wildberries.ru, cmp.wildberries.ru` | Required to inject the panel and listen for bid/position changes on those exact pages. |
| `host_permissions: sellico.ru, api.sellico.ru` | Send captured events to your Sellico account. |
| `optional_host_permissions: localhost:8080, 127.0.0.1:8080` | OFF by default; only granted if you opt-in for self-hosted or local Sellico testing. |

The extension does NOT request `tabs`, `activeTab`, `webRequest`, or
`scripting` permissions, so it cannot inspect or modify pages outside the
explicit `host_permissions` above.

## 5. Third parties

We do not share captured data with third parties. Sellico's own product
analytics (Plausible) runs only on `sellico.ru`, NOT inside this extension.

## 6. Your rights

- View, export, or delete your captured data anytime in Sellico → Settings → Data.
- Revoke the extension at any time via Chrome → Manage Extensions; doing so
  immediately stops capture and does not affect data already in Sellico.
- Email `privacy@sellico.ru` for any request related to your data.

## 7. Changes

If we change what we collect or how we use it we'll publish a new version
of this policy and update the extension changelog. The "Effective date"
above always reflects the current policy.

## 8. Contact

privacy@sellico.ru — privacy / data requests
support@sellico.ru — general support
