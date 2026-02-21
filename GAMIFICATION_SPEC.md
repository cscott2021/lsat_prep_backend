# Backend Gamification Spec — XP, Streaks, Gems, Leaderboard, Friends & Nudges

> Persists all gamification data that currently lives only in frontend mock state. Adds a full social layer with friends, leaderboards, and nudges.

---

## 1. Overview

### What Exists (Frontend-Only, No Persistence)
- XP: Calculated per drill (8/10/15 by difficulty + 20 perfect bonus), accumulated in-memory
- Streaks: Day counter, resets on app close
- Gems: Hardcoded at 100
- Daily goal: In-memory counter (default 5 questions)
- Leaderboard: 10 fake entries in mock_leaderboard.dart
- Star ratings: 0–3 based on accuracy

### What This Spec Adds (Backend-Persisted)
- **XP engine** with streak multipliers, combo bonuses, time bonuses, and difficulty scaling
- **Streak system** with freeze protection (purchased with gems)
- **Gems economy** earned from milestones, spent on streak freezes and cosmetics
- **Daily goals** persisted per user with configurable targets
- **Real leaderboard** ranked by weekly XP with league tiers
- **Friends system** with add/accept/remove
- **Nudge system** to poke inactive friends

---

## 2. Database Schema

### 2a. New table: `user_gamification`

One row per user. The single source of truth for XP, streaks, gems.

```sql
CREATE TABLE IF NOT EXISTS user_gamification (
    user_id              BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    total_xp             BIGINT NOT NULL DEFAULT 0,
    weekly_xp            BIGINT NOT NULL DEFAULT 0,
    weekly_xp_reset_at   TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    current_streak       INT NOT NULL DEFAULT 0,
    longest_streak       INT NOT NULL DEFAULT 0,
    last_active_date     DATE,                              -- date of last completed question
    streak_freeze_active BOOLEAN NOT NULL DEFAULT FALSE,    -- currently using a freeze?
    streak_freezes_owned INT NOT NULL DEFAULT 0,            -- purchased freezes in inventory
    gems                 INT NOT NULL DEFAULT 0,
    daily_goal_target    INT NOT NULL DEFAULT 6,            -- questions per day
    daily_goal_progress  INT NOT NULL DEFAULT 0,            -- questions answered today
    daily_goal_date      DATE DEFAULT CURRENT_DATE,         -- resets when date changes
    league_tier          VARCHAR(20) NOT NULL DEFAULT 'bronze', -- bronze/silver/gold/diamond/obsidian
    questions_answered_total INT NOT NULL DEFAULT 0,
    questions_correct_total  INT NOT NULL DEFAULT 0,
    drills_completed_total   INT NOT NULL DEFAULT 0,
    perfect_drills_total     INT NOT NULL DEFAULT 0,
    created_at           TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at           TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

### 2b. New table: `xp_events`

Audit log of every XP-earning event. Enables leaderboard queries and analytics.

```sql
CREATE TABLE IF NOT EXISTS xp_events (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type  VARCHAR(50) NOT NULL,   -- 'question_correct', 'combo_bonus', 'streak_bonus',
                                        -- 'perfect_drill', 'daily_goal', 'time_bonus',
                                        -- 'first_drill', 'milestone'
    xp_amount   INT NOT NULL,
    metadata    JSONB,                  -- { question_id, difficulty_score, combo_count, etc. }
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_xp_events_user ON xp_events(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_xp_events_weekly ON xp_events(user_id, created_at)
    WHERE created_at >= date_trunc('week', CURRENT_DATE);
```

### 2c. New table: `friendships`

```sql
CREATE TABLE IF NOT EXISTS friendships (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    friend_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status      VARCHAR(20) NOT NULL DEFAULT 'pending',  -- 'pending', 'accepted', 'blocked'
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    accepted_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(user_id, friend_id),
    CHECK(user_id != friend_id)
);

CREATE INDEX IF NOT EXISTS idx_friends_user ON friendships(user_id, status);
CREATE INDEX IF NOT EXISTS idx_friends_friend ON friendships(friend_id, status);
```

### 2d. New table: `nudges`

```sql
CREATE TABLE IF NOT EXISTS nudges (
    id          BIGSERIAL PRIMARY KEY,
    sender_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receiver_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message     VARCHAR(200),           -- optional custom message
    nudge_type  VARCHAR(30) NOT NULL DEFAULT 'comeback', -- 'comeback', 'challenge', 'cheer'
    read        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_nudges_receiver ON nudges(receiver_id, read, created_at DESC);

-- Rate limit: max 1 nudge per sender→receiver per day
CREATE UNIQUE INDEX IF NOT EXISTS idx_nudges_daily
    ON nudges(sender_id, receiver_id, (created_at::date));
```

### 2e. New table: `achievements`

```sql
CREATE TABLE IF NOT EXISTS achievements (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    achievement VARCHAR(100) NOT NULL,  -- 'first_drill', 'streak_7', 'streak_30', 'perfect_10', etc.
    earned_at   TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(user_id, achievement)
);

CREATE INDEX IF NOT EXISTS idx_achievements_user ON achievements(user_id);
```

---

## 3. XP Engine

### 3a. Per-Question XP

Every correct answer earns XP. The amount scales with question difficulty and the gap between user ability and question difficulty.

```go
// BaseXP returns XP for a correct answer based on difficulty score (0-100)
func BaseXP(difficultyScore int) int {
    // 0-20: 5 XP, 21-40: 8 XP, 41-60: 10 XP, 61-80: 13 XP, 81-100: 16 XP
    if difficultyScore <= 20 { return 5 }
    if difficultyScore <= 40 { return 8 }
    if difficultyScore <= 60 { return 10 }
    if difficultyScore <= 80 { return 13 }
    return 16
}

// ChallengeBonus adds XP when you answer a question above your ability level
func ChallengeBonus(userAbility, difficultyScore int) int {
    gap := difficultyScore - userAbility
    if gap <= 0 { return 0 }       // Question at or below your level
    if gap <= 10 { return 2 }      // Slightly above
    if gap <= 20 { return 5 }      // Challenging
    return 8                        // Well above your level
}
```

| Difficulty Score | Base XP | Challenge Bonus (if 15 above ability) | Total |
|:---:|:---:|:---:|:---:|
| 25 (easy) | 8 | 0–5 | 8–13 |
| 50 (medium) | 10 | 0–5 | 10–15 |
| 75 (hard) | 13 | 0–8 | 13–21 |
| 95 (expert) | 16 | 0–8 | 16–24 |

Wrong answers earn **0 XP**. No penalty — you just miss the reward.

### 3b. Combo Bonus (Consecutive Correct)

Answering multiple questions correctly in a row within a single drill earns escalating combo bonuses.

```go
func ComboXP(consecutiveCorrect int) int {
    switch {
    case consecutiveCorrect < 3:
        return 0                  // No combo yet
    case consecutiveCorrect == 3:
        return 3                  // "Nice streak!" — 3 in a row
    case consecutiveCorrect == 4:
        return 5                  // "On fire!"
    case consecutiveCorrect == 5:
        return 8                  // "Unstoppable!"
    default:
        return 10                 // 6+ in a row — "PERFECT" (if full drill)
    }
}
```

Wrong answer resets the combo counter to 0.

### 3c. Time Bonus

Rewarding fast (but correct) answers. Based on avg time per question in the drill.

```go
func TimeBonus(avgSecondsPerQuestion float64) int {
    if avgSecondsPerQuestion <= 45 { return 10 }  // Speed demon
    if avgSecondsPerQuestion <= 75 { return 5 }   // Efficient
    if avgSecondsPerQuestion <= 120 { return 2 }  // Steady
    return 0                                       // No bonus (but no penalty)
}
```

Applied once at drill completion, not per question.

### 3d. Streak Multiplier

Daily streak multiplies all XP earned in a drill.

```go
func StreakMultiplier(currentStreak int) float64 {
    if currentStreak < 3 { return 1.0 }   // No bonus yet
    if currentStreak < 7 { return 1.15 }  // 3-6 days: +15%
    if currentStreak < 14 { return 1.25 } // 7-13 days: +25%
    if currentStreak < 30 { return 1.5 }  // 14-29 days: +50%
    return 2.0                             // 30+ days: double XP
}
```

### 3e. Drill Completion Bonuses

```go
func DrillCompletionXP(correct, total int) int {
    xp := 0
    accuracy := float64(correct) / float64(total)

    if correct == total {
        xp += 25        // Perfect drill bonus
    } else if accuracy >= 0.8 {
        xp += 10        // Great drill bonus
    }

    return xp
}
```

### 3f. Full XP Calculation Flow

After a drill of 6 questions:

```
1. Per-question XP:    sum of BaseXP + ChallengeBonus for each correct answer
2. Combo bonuses:      sum of ComboXP triggered during the drill
3. Time bonus:         TimeBonus(avgTime) — applied once
4. Drill completion:   DrillCompletionXP(correct, total)
5. Subtotal:           (1 + 2 + 3 + 4)
6. Streak multiplier:  Subtotal × StreakMultiplier(streak)
7. Final XP:           round(6)
```

**Example — 5/6 correct, medium difficulty, 7-day streak, avg 60s:**

```
Per-question:    5 × 10 = 50 XP
Challenge:       2 × 5 = 10 XP (2 questions above ability)
Combo:           3 + 5 = 8 XP (got 3-in-a-row and 4-in-a-row before missing #5)
Time bonus:      5 XP (avg 60s)
Drill complete:  10 XP (80%+ accuracy)
Subtotal:        83 XP
Streak (1.25×):  104 XP
```

---

## 4. Streak System

### 4a. Logic

```go
func (s *Service) UpdateStreak(userID int64) error {
    gam, _ := s.store.GetUserGamification(userID)

    today := time.Now().Truncate(24 * time.Hour)
    lastActive := gam.LastActiveDate // date only, no time

    if lastActive == nil || today.After(*lastActive) {
        daysSinceLast := 0
        if lastActive != nil {
            daysSinceLast = int(today.Sub(*lastActive).Hours() / 24)
        }

        switch {
        case daysSinceLast == 0:
            // Already active today, no change
        case daysSinceLast == 1:
            // Consecutive day — increment streak
            gam.CurrentStreak++
        case daysSinceLast == 2 && gam.StreakFreezeActive:
            // Missed yesterday but had a freeze — streak preserved
            gam.CurrentStreak++           // Still counts as continuation
            gam.StreakFreezeActive = false // Freeze consumed
            gam.StreakFreezesOwned--
        default:
            // Streak broken
            gam.CurrentStreak = 1 // Today is day 1 of new streak
            gam.StreakFreezeActive = false
        }

        if gam.CurrentStreak > gam.LongestStreak {
            gam.LongestStreak = gam.CurrentStreak
        }

        gam.LastActiveDate = &today
        s.store.UpdateUserGamification(userID, gam)
    }
    return nil
}
```

### 4b. Streak Freeze

Users spend gems to buy streak freezes. A freeze protects one missed day.

```
Cost: 50 gems per freeze
Max inventory: 3 freezes
Auto-activate: If user misses a day and has freezes, auto-consume one
```

### 4c. Streak Milestones (award gems)

| Streak | Reward | Achievement |
|:---:|:---:|:---|
| 3 days | 10 gems | `streak_3` |
| 7 days | 25 gems | `streak_7` |
| 14 days | 50 gems | `streak_14` |
| 30 days | 100 gems | `streak_30` |
| 60 days | 200 gems | `streak_60` |
| 100 days | 500 gems | `streak_100` |
| 365 days | 1000 gems | `streak_365` |

---

## 5. Gems Economy

### 5a. Earning Gems

| Source | Amount | Frequency |
|--------|--------|-----------|
| Complete first drill ever | 50 | Once |
| Daily goal completed | 5 | Daily |
| Perfect drill (6/6) | 10 | Per drill |
| Streak milestones | 10–1000 | Per milestone |
| League promotion | 25 | Per promotion |
| Weekly leaderboard top 3 | 50/30/20 | Weekly |
| Achievement unlocked | 5–50 | Per achievement |

### 5b. Spending Gems

| Item | Cost | Description |
|------|------|-------------|
| Streak freeze | 50 | Protects one missed day |
| Nudge (with flair) | 5 | Send an animated nudge to a friend |
| Double XP (1 drill) | 100 | Next drill earns 2× XP |

---

## 6. Daily Goals

### 6a. Logic

```go
func (s *Service) UpdateDailyGoal(userID int64, questionsJustAnswered int) error {
    gam, _ := s.store.GetUserGamification(userID)

    today := time.Now().Format("2006-01-02")
    goalDate := gam.DailyGoalDate.Format("2006-01-02")

    // Reset if new day
    if today != goalDate {
        gam.DailyGoalProgress = 0
        gam.DailyGoalDate = time.Now()
    }

    wasCompleted := gam.DailyGoalProgress >= gam.DailyGoalTarget
    gam.DailyGoalProgress += questionsJustAnswered
    nowCompleted := gam.DailyGoalProgress >= gam.DailyGoalTarget

    // Award gems if just completed
    if !wasCompleted && nowCompleted {
        gam.Gems += 5
        s.store.LogXPEvent(userID, "daily_goal", 0, map[string]interface{}{
            "gems_awarded": 5,
            "target": gam.DailyGoalTarget,
        })
    }

    s.store.UpdateUserGamification(userID, gam)
    return nil
}
```

### 6b. Configurable Target

`PUT /api/v1/users/daily-goal` — lets user set 3, 6, 12, or 18 questions/day.

---

## 7. Leaderboard

### 7a. Weekly XP Leaderboard

Ranked by XP earned this week (Monday 00:00 UTC to Sunday 23:59 UTC).

```sql
-- Global leaderboard (top N)
SELECT u.id, u.name, g.weekly_xp, g.league_tier,
       ROW_NUMBER() OVER (ORDER BY g.weekly_xp DESC) as rank
FROM user_gamification g
JOIN users u ON u.id = g.user_id
WHERE g.weekly_xp > 0
ORDER BY g.weekly_xp DESC
LIMIT $1;

-- Friends leaderboard
SELECT u.id, u.name, g.weekly_xp, g.league_tier,
       ROW_NUMBER() OVER (ORDER BY g.weekly_xp DESC) as rank
FROM user_gamification g
JOIN users u ON u.id = g.user_id
WHERE g.user_id IN (
    SELECT friend_id FROM friendships WHERE user_id = $1 AND status = 'accepted'
    UNION
    SELECT user_id FROM friendships WHERE friend_id = $1 AND status = 'accepted'
    UNION
    SELECT $1  -- include self
)
ORDER BY g.weekly_xp DESC;
```

### 7b. Weekly Reset

A cron job (or background goroutine) runs every Monday at 00:00 UTC:

```go
func (s *Service) WeeklyLeaderboardReset(ctx context.Context) {
    // 1. Award gems to top 3 in global leaderboard
    top3, _ := s.store.GetGlobalLeaderboard(3)
    gemRewards := []int{50, 30, 20}
    for i, entry := range top3 {
        s.store.AwardGems(entry.UserID, gemRewards[i])
    }

    // 2. Process league promotions/demotions
    s.processLeagueChanges()

    // 3. Reset weekly_xp to 0 for all users
    s.store.ResetWeeklyXP()
}
```

### 7c. League Tiers

Users are placed in leagues based on their highest weekly XP ever, with promotion/demotion.

| Tier | Threshold (weekly XP to promote) | Promotion | Demotion (weekly XP below) |
|------|------|------|------|
| Bronze | Default | → Silver at 500 weekly XP | N/A |
| Silver | 500 | → Gold at 1000 | → Bronze below 200 |
| Gold | 1000 | → Diamond at 2000 | → Silver below 500 |
| Diamond | 2000 | → Obsidian at 4000 | → Gold below 1000 |
| Obsidian | 4000 | N/A | → Diamond below 2000 |

Evaluated at weekly reset. Only promote/demote one tier at a time.

---

## 8. Friends System

### 8a. Endpoints

#### `POST /api/v1/friends/request`

Send a friend request by email.

```
Request:
{
    "friend_email": "alex@example.com"
}

Response 201:
{
    "friendship_id": 42,
    "status": "pending",
    "friend": {
        "id": 7,
        "name": "Alex R."
    }
}

Errors:
- 404: "User not found"
- 409: "Friend request already exists"
- 400: "Cannot friend yourself"
```

#### `POST /api/v1/friends/respond`

Accept or reject a friend request.

```
Request:
{
    "friendship_id": 42,
    "action": "accept"   // or "reject"
}

Response 200:
{
    "status": "accepted"
}
```

#### `GET /api/v1/friends`

List all friends (accepted) with their gamification stats.

```
Response 200:
{
    "friends": [
        {
            "user_id": 7,
            "name": "Alex R.",
            "weekly_xp": 1240,
            "current_streak": 12,
            "league_tier": "gold",
            "last_active_date": "2026-02-20",
            "is_online_today": true
        },
        ...
    ],
    "pending_received": [
        {
            "friendship_id": 55,
            "user_id": 14,
            "name": "Jordan M.",
            "created_at": "2026-02-21T10:30:00Z"
        }
    ],
    "pending_sent": [...]
}
```

#### `DELETE /api/v1/friends/{friendship_id}`

Remove a friend.

```
Response 200:
{
    "status": "removed"
}
```

#### `GET /api/v1/friends/search?q=alex`

Search users by name or email to send friend requests.

```
Response 200:
{
    "results": [
        {
            "user_id": 7,
            "name": "Alex R.",
            "league_tier": "gold",
            "is_friend": false,
            "request_pending": false
        }
    ]
}
```

---

## 9. Nudge System

### 9a. What's a Nudge?

A lightweight notification sent to a friend to encourage them to study. Three types:

| Type | Purpose | Default Message |
|------|---------|----------------|
| `comeback` | Friend hasn't studied in 2+ days | "Hey! Your streak is at risk — come back!" |
| `challenge` | Friendly competition | "I just scored 5/6 on Strengthen — can you beat me?" |
| `cheer` | Encouragement | "You've got this! Keep grinding!" |

### 9b. Endpoints

#### `POST /api/v1/nudges`

```
Request:
{
    "receiver_id": 7,
    "nudge_type": "comeback",
    "message": "Miss you on the leaderboard!"   // optional custom message
}

Response 201:
{
    "nudge_id": 88,
    "sent": true
}

Errors:
- 429: "Already nudged this person today"
- 403: "You can only nudge friends"
- 402: "Not enough gems" (if flair nudge)
```

Rate limit: 1 nudge per sender→receiver per calendar day (enforced by unique index).

#### `GET /api/v1/nudges`

Get unread nudges for the current user.

```
Response 200:
{
    "nudges": [
        {
            "id": 88,
            "sender_name": "Sarah K.",
            "sender_id": 3,
            "nudge_type": "comeback",
            "message": "Miss you on the leaderboard!",
            "created_at": "2026-02-21T08:15:00Z"
        }
    ],
    "unread_count": 1
}
```

#### `POST /api/v1/nudges/{id}/read`

Mark a nudge as read.

```
Response 200:
{
    "status": "read"
}
```

---

## 10. Leaderboard Endpoints

#### `GET /api/v1/leaderboard/global?limit=20`

```
Response 200:
{
    "period": "2026-02-17 to 2026-02-23",
    "entries": [
        {
            "rank": 1,
            "user_id": 3,
            "name": "Sarah K.",
            "weekly_xp": 2450,
            "league_tier": "diamond",
            "current_streak": 23,
            "is_current_user": false
        },
        ...
    ],
    "current_user": {
        "rank": 42,
        "weekly_xp": 1120,
        "league_tier": "gold"
    }
}
```

#### `GET /api/v1/leaderboard/friends`

Same format but filtered to friends only. Always includes the current user.

---

## 11. Gamification Endpoints

#### `GET /api/v1/users/gamification`

Full gamification state for the current user.

```
Response 200:
{
    "total_xp": 14520,
    "weekly_xp": 1120,
    "current_streak": 12,
    "longest_streak": 23,
    "streak_freeze_active": false,
    "streak_freezes_owned": 2,
    "gems": 340,
    "daily_goal_target": 6,
    "daily_goal_progress": 4,
    "league_tier": "gold",
    "questions_answered_total": 487,
    "questions_correct_total": 389,
    "drills_completed_total": 78,
    "perfect_drills_total": 12,
    "achievements": ["first_drill", "streak_7", "streak_14", "perfect_10"],
    "unread_nudges": 1
}
```

#### `POST /api/v1/users/gamification/streak-freeze`

Purchase a streak freeze with gems.

```
Response 200:
{
    "gems_remaining": 290,
    "streak_freezes_owned": 3
}

Errors:
- 400: "Already have maximum freezes (3)"
- 402: "Not enough gems (need 50, have 20)"
```

#### `PUT /api/v1/users/daily-goal`

```
Request:
{
    "target": 12    // must be 3, 6, 12, or 18
}

Response 200:
{
    "daily_goal_target": 12
}
```

---

## 12. Modified Answer Submission

Update `POST /api/v1/questions/{id}/answer` to trigger gamification:

```go
func (s *Service) SubmitAnswer(ctx context.Context, userID, questionID int64, choiceID string) (*SubmitAnswerResponse, error) {
    // 1. Existing logic: check answer, update times_served/times_correct
    question, _ := s.store.GetQuestionWithChoices(questionID)
    correct := question.CorrectAnswerID == choiceID

    s.store.IncrementServed(questionID)
    if correct { s.store.IncrementCorrect(questionID) }

    // 2. Record in user_question_history (existing)
    s.store.RecordAnswer(userID, questionID, correct)

    // 3. Update ability scores (existing)
    abilitySnapshot, _ := s.UpdateAbilityScores(ctx, userID, question, correct)

    // 4. NEW: Award XP if correct
    var xpAwarded int
    if correct {
        base := BaseXP(question.DifficultyScore)
        challenge := ChallengeBonus(abilitySnapshot.SubtypeAbility, question.DifficultyScore)
        xpAwarded = base + challenge

        s.store.LogXPEvent(userID, "question_correct", xpAwarded, map[string]interface{}{
            "question_id": questionID,
            "difficulty_score": question.DifficultyScore,
            "base_xp": base,
            "challenge_bonus": challenge,
        })
    }

    // 5. NEW: Update daily goal
    s.UpdateDailyGoal(userID, 1)

    // 6. NEW: Update streak (checks if first activity today)
    s.UpdateStreak(userID)

    // 7. NEW: Increment gamification counters
    s.store.IncrementGamificationCounters(userID, correct)

    return &SubmitAnswerResponse{
        Correct:         correct,
        CorrectAnswerID: question.CorrectAnswerID,
        Explanation:     question.Explanation,
        Choices:         question.Choices,
        AbilityUpdated:  abilitySnapshot,
        XPAwarded:       xpAwarded,  // NEW field
    }, nil
}
```

### Drill Completion Endpoint (New)

`POST /api/v1/drills/complete` — Called by frontend after all 6 questions answered.

```
Request:
{
    "question_ids": [42, 43, 44, 45, 46, 47],
    "correct_ids": [42, 43, 44, 46],      // which ones were correct
    "avg_time_seconds": 62.5,
    "combo_max": 4                          // longest consecutive correct streak
}

Response 200:
{
    "xp_breakdown": {
        "questions": 52,
        "combo_bonuses": 8,
        "time_bonus": 5,
        "drill_completion": 10,
        "subtotal": 75,
        "streak_multiplier": 1.25,
        "total_xp": 94
    },
    "gems_earned": 0,
    "streak": {
        "current": 12,
        "multiplier": 1.25
    },
    "daily_goal": {
        "progress": 10,
        "target": 12,
        "completed": false
    },
    "achievements_unlocked": [],
    "league_tier": "gold"
}
```

This endpoint handles: combo XP, time bonus, drill completion bonus, streak multiplier application, perfect drill detection, achievement checks, and gem awards.

---

## 13. Achievement Definitions

```go
var Achievements = map[string]AchievementDef{
    "first_drill":     {Name: "First Steps", Description: "Complete your first drill", Gems: 50},
    "streak_3":        {Name: "Getting Started", Description: "3-day streak", Gems: 10},
    "streak_7":        {Name: "Week Warrior", Description: "7-day streak", Gems: 25},
    "streak_14":       {Name: "Dedicated", Description: "14-day streak", Gems: 50},
    "streak_30":       {Name: "Monthly Master", Description: "30-day streak", Gems: 100},
    "streak_100":      {Name: "Centurion", Description: "100-day streak", Gems: 500},
    "perfect_1":       {Name: "Flawless", Description: "First perfect drill", Gems: 10},
    "perfect_10":      {Name: "Perfectionist", Description: "10 perfect drills", Gems: 50},
    "perfect_50":      {Name: "Machine", Description: "50 perfect drills", Gems: 200},
    "questions_100":   {Name: "Century", Description: "Answer 100 questions", Gems: 25},
    "questions_500":   {Name: "Scholar", Description: "Answer 500 questions", Gems: 50},
    "questions_1000":  {Name: "Expert", Description: "Answer 1000 questions", Gems: 100},
    "xp_1000":         {Name: "Rising Star", Description: "Earn 1,000 total XP", Gems: 10},
    "xp_10000":        {Name: "Powerhouse", Description: "Earn 10,000 total XP", Gems: 50},
    "xp_50000":        {Name: "Legend", Description: "Earn 50,000 total XP", Gems: 200},
    "all_lr_subtypes": {Name: "LR Complete", Description: "Practice all 14 LR subtypes", Gems: 50},
    "all_rc_subtypes": {Name: "RC Complete", Description: "Practice all 10 RC subtypes", Gems: 50},
    "friend_5":        {Name: "Social Butterfly", Description: "Add 5 friends", Gems: 25},
    "nudge_first":     {Name: "Motivator", Description: "Send your first nudge", Gems: 5},
    "league_silver":   {Name: "Silver League", Description: "Reach Silver league", Gems: 25},
    "league_gold":     {Name: "Gold League", Description: "Reach Gold league", Gems: 50},
    "league_diamond":  {Name: "Diamond League", Description: "Reach Diamond league", Gems: 100},
    "league_obsidian": {Name: "Obsidian League", Description: "Reach Obsidian league", Gems: 250},
}
```

---

## 14. Route Registration

Add to `cmd/server/main.go`:

```go
// Gamification
protected.HandleFunc("/users/gamification", gamHandler.GetGamification).Methods("GET")
protected.HandleFunc("/users/gamification/streak-freeze", gamHandler.BuyStreakFreeze).Methods("POST")
protected.HandleFunc("/users/daily-goal", gamHandler.SetDailyGoal).Methods("PUT")
protected.HandleFunc("/drills/complete", gamHandler.CompleteDrill).Methods("POST")

// Leaderboard
protected.HandleFunc("/leaderboard/global", gamHandler.GlobalLeaderboard).Methods("GET")
protected.HandleFunc("/leaderboard/friends", gamHandler.FriendsLeaderboard).Methods("GET")

// Friends
protected.HandleFunc("/friends", gamHandler.ListFriends).Methods("GET")
protected.HandleFunc("/friends/request", gamHandler.SendFriendRequest).Methods("POST")
protected.HandleFunc("/friends/respond", gamHandler.RespondFriendRequest).Methods("POST")
protected.HandleFunc("/friends/{id}", gamHandler.RemoveFriend).Methods("DELETE")
protected.HandleFunc("/friends/search", gamHandler.SearchUsers).Methods("GET")

// Nudges
protected.HandleFunc("/nudges", gamHandler.ListNudges).Methods("GET")
protected.HandleFunc("/nudges", gamHandler.SendNudge).Methods("POST")
protected.HandleFunc("/nudges/{id}/read", gamHandler.MarkNudgeRead).Methods("POST")
```

---

## 15. Background Jobs

Start in `main.go` as goroutines:

```go
// Weekly leaderboard reset — every Monday 00:00 UTC
go gamService.StartWeeklyResetWorker(ctx)

// Daily streak check — auto-activate streak freezes at midnight
go gamService.StartDailyStreakWorker(ctx)

// Daily goal reset — reset progress counters at midnight per user timezone
// (simplification: reset for everyone at UTC midnight)
go gamService.StartDailyGoalResetWorker(ctx)
```

---

## 16. Implementation Order

1. `user_gamification` table + model + basic CRUD
2. XP engine (BaseXP, ChallengeBonus, ComboXP, TimeBonus, StreakMultiplier)
3. Modify answer submission to award XP
4. `POST /drills/complete` endpoint with full XP breakdown
5. Streak logic + streak freeze purchase
6. Daily goal tracking
7. `xp_events` table + XP event logging
8. Leaderboard queries + endpoints
9. League tier system + weekly reset
10. `friendships` table + friend endpoints
11. `nudges` table + nudge endpoints
12. `achievements` table + achievement checks
13. Gem economy (earning + spending)
14. Background workers
