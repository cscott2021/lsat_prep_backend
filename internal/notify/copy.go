package notify

import "fmt"

// All server-push copy in ONE place, mirroring the on-device copy table in the
// Flutter app's lib/constants/notification_copy.dart (same triggers, same
// voice). Wordsmith here for APNs pushes; there for local notifications.
//
// Trigger → copy table:
//
//	streak at risk (goal unmet, streak >= 2) → TitleStreakRisk / BodyStreakRiskf
//	freeze-protected streak                  → TitleFreezeSaved / BodyFreezeSavedf
//	goal met (gentle, digest-style)          → TitleGoalMet / BodyGoalMet
//	3 days inactive                          → TitleReengage3 / BodyReengage3
//	7 days inactive                          → TitleReengage7 / BodyReengage7
//
// ThreadIDs collapse repeat sends on the lock screen (replace, not stack).
const (
	ThreadStreak   = "streak-reminder"
	ThreadReengage = "re-engagement"
	ThreadGoalMet  = "daily-digest"
)

// Streak at risk — evening, goal unmet, a real streak to lose.
const TitleStreakRisk = "Your streak is on the line"

func BodyStreakRiskf(streak int) string {
	return fmt.Sprintf("Don't lose your %d-day streak — one quick drill before bed?", streak)
}

// Freeze-protected — reassurance instead of pressure.
const TitleFreezeSaved = "Your streak freeze has you covered"

func BodyFreezeSavedf(streak int) string {
	return fmt.Sprintf("Your %d-day streak is safe tonight — but a quick drill keeps the momentum.", streak)
}

// Goal met — gentle, digest-flavored; still capped at 1 push/day.
const TitleGoalMet = "Today's rep is done — nice work"
const BodyGoalMet = "You hit your daily goal. Want a bonus round to stay sharp?"

// 3 days inactive.
const TitleReengage3 = "Your LSAT skills are getting rusty"
const BodyReengage3 = "A 5-minute drill today keeps your score gains from slipping."

// 7 days inactive — the streak-freeze-melting flavor.
const TitleReengage7 = "Your streak froze — come back before it melts"
const BodyReengage7 = "One easy drill revives the routine. We kept your spot warm."
