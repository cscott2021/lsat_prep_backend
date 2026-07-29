package notify

import "time"

// Push timing policy (documented in APPLE-style one place):
//   - Sends only during the user's LOCAL evening window [eveningStartHour,
//     eveningEndHour) — a daily 19:00–21:00 slot that matches the app's
//     default local reminder time.
//   - Quiet hours 21:00–09:00 local are never sent in (the evening window
//     already avoids them; the explicit check is belt-and-braces).
//   - Hard cap: at most ONE push per device per dailyCapHours (20h, so the
//     daily window never drifts later day over day).
const (
	eveningStartHour = 19
	eveningEndHour   = 21
	quietStartHour   = 21 // 21:00 → 09:00 next day
	quietEndHour     = 9
	dailyCapHours    = 20
	reengageDayEarly = 3
	reengageDayLate  = 7
)

// pushDecision is the outcome of evaluating one device: send nothing, or send
// exactly this.
type pushDecision struct {
	send     bool
	title    string
	body     string
	threadID string
	reason   string // log line explaining the decision
}

// inQuietHours reports whether localHour falls inside 21:00–09:00.
func inQuietHours(localHour int) bool {
	return localHour >= quietStartHour || localHour < quietEndHour
}

// daysInactive converts lastActive (a UTC calendar date, per the server's
// existing streak semantics) into whole days before today's UTC date. A user
// active TODAY is 0; yesterday is 1. nil (never active) returns -1.
func daysInactive(lastActive *time.Time, now time.Time) int {
	if lastActive == nil {
		return -1
	}
	today := now.UTC().Truncate(24 * time.Hour)
	last := lastActive.UTC().Truncate(24 * time.Hour)
	return int(today.Sub(last).Hours() / 24)
}

// goalMetToday reports whether the stored daily-goal progress belongs to
// today (UTC day, matching gamification semantics) and meets the target.
func goalMetToday(c PushCandidate, now time.Time) bool {
	if c.DailyGoalDate == nil {
		return false
	}
	today := now.UTC().Format("2006-01-02")
	return c.DailyGoalDate.UTC().Format("2006-01-02") == today &&
		c.DailyGoalProgress >= c.DailyGoalTarget
}

// decidePush evaluates ONE device and returns what (if anything) to push.
// Pure: all state comes from the candidate and `now`. Priority order:
//
//  1. re-engagement at exactly 3 / 7 days inactive
//  2. streak at risk (active recently, goal unmet) — freeze-aware
//  3. goal met → gentle digest nudge
//  4. anything else (inactive 4–6 or >7 days) → silence; we already nudged
func decidePush(c PushCandidate, now time.Time) pushDecision {
	// Hard daily cap first — cheaper than any other reasoning.
	if c.LastNotifiedAt != nil && now.Sub(*c.LastNotifiedAt) < dailyCapHours*time.Hour {
		return pushDecision{reason: "daily cap: already pushed within 20h"}
	}

	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		loc = time.UTC
	}
	localNow := now.In(loc)

	if inQuietHours(localNow.Hour()) {
		return pushDecision{reason: "quiet hours (21:00-09:00 local)"}
	}
	if localNow.Hour() < eveningStartHour || localNow.Hour() >= eveningEndHour {
		return pushDecision{reason: "outside local evening window (19:00-21:00)"}
	}

	inactive := daysInactive(c.LastActiveDate, now)

	// 1. Re-engagement — fires on the exact day thresholds; the daily cap
	// keeps it to one send per day.
	switch inactive {
	case reengageDayEarly:
		return pushDecision{send: true, title: TitleReengage3, body: BodyReengage3, threadID: ThreadReengage, reason: "3 days inactive"}
	case reengageDayLate:
		return pushDecision{send: true, title: TitleReengage7, body: BodyReengage7, threadID: ThreadReengage, reason: "7 days inactive"}
	}

	// Beyond the late threshold we stop nagging entirely; between them the
	// day-3 push already fired once (cap prevents repeats would be wrong —
	// so only exact days fire).
	if inactive < 0 || inactive > reengageDayEarly {
		return pushDecision{reason: "inactive but not on a re-engagement day"}
	}

	if goalMetToday(c, now) {
		// 3. Gentle digest — goal met, active today or recently.
		if inactive <= 1 {
			return pushDecision{send: true, title: TitleGoalMet, body: BodyGoalMet, threadID: ThreadGoalMet, reason: "goal met digest"}
		}
		return pushDecision{reason: "goal row is stale and user inactive"}
	}

	// 2. Goal unmet. Freeze owners get reassurance; a streak >= 2 gets
	// pressure; everyone else a gentle on-track nudge (reuse the goal-met
	// thread so these collapse together on the lock screen).
	if c.CurrentStreak >= 2 {
		if c.StreakFreezeActive || c.StreakFreezesOwned > 0 {
			return pushDecision{send: true, title: TitleFreezeSaved, body: BodyFreezeSavedf(c.CurrentStreak), threadID: ThreadStreak, reason: "freeze-protected streak"}
		}
		return pushDecision{send: true, title: TitleStreakRisk, body: BodyStreakRiskf(c.CurrentStreak), threadID: ThreadStreak, reason: "streak at risk"}
	}
	return pushDecision{reason: "no streak to protect and goal unmet — leave local notifications to it"}
}
