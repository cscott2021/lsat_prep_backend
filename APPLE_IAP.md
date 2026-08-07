# Apple In-App Purchase (IAP) — ScoreRight

How iOS subscriptions work, what maps to what, and exactly what must be
configured outside the code (App Store Connect / Apple Developer account).
Everything here is already implemented; the remaining steps are account-side.

## Architecture

- **One entitlement model.** iOS purchases land in the SAME `subscriptions`
  table Stripe uses, with `provider='apple'`. The paywall middleware
  (`RequireEntitlement`) only reads `status`, so an iOS buyer is entitled on
  the web and vice versa. Statuses reuse the existing vocabulary:
  `trialing` (intro offer), `active`, `past_due`, `canceled`.
- **Offline verification.** `POST /api/v1/billing/apple/verify` (authenticated)
  accepts the StoreKit 2 signed transaction (JWS) from the app and verifies the
  x5c certificate chain against Apple's PUBLIC root CAs vendored in
  `internal/billing/apple/certs/` (valid to 2035/2039). **No secrets are
  required** — no .p8 key, no shared secret. Tradeoffs (no revocation checks,
  no transaction-history API) are documented in `internal/billing/apple/jws.go`.
- **Renewals/cancellations.** `POST /api/v1/billing/apple/notifications`
  (PUBLIC — authenticity is the JWS signature) receives App Store Server
  Notifications V2: SUBSCRIBED / DID_RENEW → upsert active; DID_CHANGE_RENEWAL_STATUS
  → cancel-at-period-end flag; DID_FAIL_TO_RENEW → past_due (grace period keeps
  access); EXPIRED / REFUND / REVOKE → canceled. Idempotent via
  `apple_events(notification_uuid)`. Events are routed to the local user via
  `subscriptions.apple_original_transaction_id` (migration 011).
- **iOS app.** `lib/services/apple_iap_service.dart` (StoreKit 2 via
  `in_app_purchase`), paywall variant `lib/widgets/ios_iap_plans.dart`
  (App Store prices from StoreKit, Restore Purchases, auto-renewal
  disclosure, Terms/Privacy links). The Stripe payment surface is NOT
  offered on iOS (App Review 3.1.1).

## Product IDs and prices (create EXACTLY these)

| Tier      | Product ID                     | App Store price | Web price (Stripe) |
|-----------|--------------------------------|-----------------|--------------------|
| Monthly   | `app.scoreright.monthly.ios`   | $27.99          | $19.99             |
| Quarterly | `app.scoreright.quarterly.ios` | $69.99          | $49.99             |
| Annual    | `app.scoreright.annual.ios`    | $199.99         | $149.99            |

iOS prices are ~1.4x web to offset Apple's 15–30% commission. The maps live in
`internal/billing/apple_service.go` (`AppleProductIDs`) and
`lib/services/apple_iap_service.dart` (`productTiers`) — keep them in sync.

"Cheaper online" paywall copy is a single constant —
`kIosWebPricingNote` in `lib/widgets/ios_purchase_note.dart`. The opt-in link
version (`kEnableExternalPurchaseLink`) and its 3.1.1(a)/entitlement rules are
documented in that file.

**Which one ships:** `kEnableExternalPurchaseLink = false` as of 2026-08-06, so
the neutral factual note ships for the first submission. It was briefly `true`
(a 2026-07-29 decision resting on US-only 3.1.1(a) link-out rules) — but that
depends on App Store Connect availability being restricted to the United
States, which cannot be set until the app record exists. Re-enable in 1.0.1
after confirming that setting.

**Guideline 3.1.1 gating is enforced in code, not by convention.**
`lib/util/billing_surface.dart` resolves which subscription-management controls
Settings may render, and `test/billing_surface_test.dart` exhaustively asserts
that no input combination yields the Stripe surface on iOS. `payment_issue_screen.dart`
is gated on `isIosNative` (`lib/util/platform_target.dart`) for the same reason:
before 2026-08-06 it presented the Stripe PaymentSheet to iOS users whose
subscription went `past_due`.

## Configuration (environment / SSM)

Apple verification needs **no credentials**. Optional knobs, mirroring how
`STRIPE_*` vars are sourced from env (SSM-backed in ECS):

| Env var             | Required | Default             | Purpose |
|---------------------|----------|---------------------|---------|
| `APPLE_BUNDLE_ID`   | no       | `com.scoreright.app`| Signed transactions must match this bundle id. |
| `APPLE_ENVIRONMENT` | no       | (accept both)       | Set `Sandbox` or `Production` to restrict which signed transactions verify. Leave unset so TestFlight and prod share one deploy. |

If a future change needs the App Store Server API (refund lookup, transaction
history, extend-subscription), add `APPLE_ISSUER_ID`, `APPLE_KEY_ID`,
`APPLE_PRIVATE_KEY` (base64 .p8) the same way `STRIPE_*` secrets are wired —
see the tradeoff note in `internal/billing/apple/jws.go`.

## iOS release build

Release builds default to the STAGING API. Build the App Store binary against
production explicitly:

```sh
flutter build ipa --release \
  --dart-define=API_BASE_URL=https://api.scoreright.app/api/v1
```

## What only the account holder can do (App Store Connect)

1. Create the app record: bundle id `com.scoreright.app` (already set in
   Xcode), name **ScoreRight**.
2. Create 3 auto-renewable subscriptions in ONE subscription group (one group
   so upgrade/downgrade works): the product IDs and prices above.
3. Localization/review info for each IAP (display name e.g. "ScoreRight
   Premium Monthly", review screenshot).
4. Enable App Store Server Notifications V2 on the app record:
   production AND sandbox URL = `https://api.scoreright.app/api/v1/billing/apple/notifications`.
5. (Optional) Introductory offers (e.g. 7-day free trial) per product —
   the backend maps intro periods to `trialing` automatically.
6. Submit the IAPs WITH the first app version (they are reviewed together).

## Files

Backend (all additive):
- `internal/billing/apple/` — offline JWS verifier + StoreKit types + vendored
  Apple root CAs (+ tests).
- `internal/billing/apple_store.go` — `UpsertFromApple`, lookup/update by
  original transaction id, notification idempotency.
- `internal/billing/apple_service.go` — verify + notification lifecycle logic.
- `internal/billing/apple_handler.go` — the two HTTP routes.
- `internal/billing/apple_service_test.go` — mapping tests.
- `internal/database/migrations/011_apple_iap.{up,down}.sql`.

iOS/Flutter:
- `lib/services/apple_iap_service.dart`, `lib/widgets/ios_iap_plans.dart`,
  `lib/widgets/ios_purchase_note.dart`; paywall/settings/unlock-trial branches;
  `in_app_purchase` in `pubspec.yaml`.
