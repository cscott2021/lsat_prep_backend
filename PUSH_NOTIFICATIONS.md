# Engagement Notifications (Local + APNs Push) — ScoreRight

Duolingo-style engagement notifications in two layers:

- **Phase 1 — on-device (local) notifications.** No Apple config needed.
  Scheduled by the Flutter app with `flutter_local_notifications`,
  timezone-correct via `flutter_timezone` + `timezone`. Re-armed on every app
  open / drill completion / goal change so copy and timing always match the
  freshest streak state.
- **Phase 2 — server push (APNs).** Reaches a CLOSED app. Device tokens are
  registered by the app; a daily backend worker fans out in each user's LOCAL
  evening with a hard cap of 1 push/device/day and 21:00–09:00 local quiet
  hours.

## Backend endpoints

| Route | Auth | Purpose |
|---|---|---|
| `POST /api/v1/devices` | bearer | Register/refresh a device token `{platform, token, timezone}` (login, app start, token refresh). |
| `DELETE /api/v1/devices/{token}` | bearer | Unregister on logout (idempotent). |

Tokens live in `device_tokens` (migration 012), cascade on account deletion,
and are pruned when APNs returns 400/410 (`ErrTokenGone`).

## Daily worker logic (`internal/notify/decide.go`, pure + unit-tested)

Tick every 15 min. For each device, only inside the user's local 19:00–21:00
window (IANA tz from the device), not quiet hours, not pushed in the last 20h:

| Condition | Push |
|---|---|
| Exactly 3 days inactive | rusty-skills re-engagement |
| Exactly 7 days inactive | streak-froze-melting re-engagement |
| Goal unmet + streak ≥ 2 + freeze owned | freeze reassurance |
| Goal unmet + streak ≥ 2 | streak-at-risk |
| Goal met today (active ≤ 1 day ago) | gentle digest |
| 4–6 or >7 days inactive, never active, streak < 2 | silence |

## Configuration (env / SSM — mirrors STRIPE_*)

| Env var | Required | Default | Purpose |
|---|---|---|---|
| `APNS_KEY_ID` | for push | — | 10-char id of the .p8 APNs Auth Key (Apple portal → Keys). |
| `APNS_TEAM_ID` | for push | — | 10-char Apple team id. |
| `APNS_PRIVATE_KEY` | for push | — | .p8 PEM contents (literal `\n` escapes are unfolded); `APNS_PRIVATE_KEY_PATH` may point at a mounted file instead. |
| `APNS_BUNDLE_ID` | no | `com.scoreright.app` | APNs topic. |
| `APNS_USE_SANDBOX` | no | false | `true` targets the APNs sandbox (TestFlight/dev). |

Without all three credential vars the worker logs ONE line at startup and
idles; device registration keeps working, so go-live is config-only.

## User-side setup (account holder only)

1. Apple Developer portal → Certificates, Identifiers & Profiles → **Keys** →
   new key with **Apple Push Notifications service (APNs)**. Download the
   `.p8` (shown once) and note the **Key ID**; the Team ID is top-right in the
   portal. Set the five env vars above (SSM parameters in prod, same as
   `STRIPE_*`).
2. Xcode → Runner target → Signing & Capabilities: attach the paid team and
   confirm **Push Notifications** capability is present (the
   `ios/Runner/Runner.entitlements` file already declares
   `aps-environment=development`; Xcode swaps it to `production` for
   App Store archives automatically).
3. App Store Connect: push needs no extra review declaration. If asked during
   App Privacy review, the notification-permission usage is covered by the
   existing privacy manifest (no tracking; the permission prompt appears
   after the user's first drill, never at cold launch).
4. Sandbox testing: run a dev-signed build on a physical device (Simulator
   has no APNs) with `APNS_USE_SANDBOX=true`.

## Notification copy

All on-device copy: `lib/constants/notification_copy.dart` (Flutter).
All server-push copy: `internal/notify/copy.go` (mirrors the same triggers).
