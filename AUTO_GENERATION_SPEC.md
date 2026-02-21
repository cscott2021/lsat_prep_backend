# Backend Auto-Generation Spec

> Removes the manual `POST /questions/generate` endpoint. The drill endpoint automatically generates new questions when a user runs out of stored questions.

---

## 1. Overview

Currently, the frontend calls `GET /questions/drill` and if it returns empty, makes a separate `POST /questions/generate` call, then retries the drill. This creates unnecessary round-trips and exposes generation as a public endpoint.

**New behavior:** `GET /questions/drill` handles everything. If stored questions are insufficient, the backend generates new ones inline before responding—completely transparent to the frontend.

---

## 2. Changes Summary

| What | Action |
|------|--------|
| `POST /api/v1/questions/generate` | **Remove** from public routes |
| `GET /api/v1/questions/drill` | **Modify** — auto-generates when inventory is low |
| `questions/service.go` | **Modify** — add inventory check + inline generation |
| `questions/store.go` | **Add** `CountAvailable()` method |
| `questions/handler.go` | **Modify** — remove `GenerateBatch` handler from routes |
| `cmd/server/main.go` | **Modify** — remove `/questions/generate` route registration |
| Environment | **Add** `MIN_QUESTIONS_THRESHOLD` env var |

---

## 3. Environment Variables

Add to `docker-compose.yml` under the backend service:

```yaml
MIN_QUESTIONS_THRESHOLD: "12"    # Trigger generation when fewer than this many unseen questions remain
MAX_GENERATION_RETRIES: "2"      # Max generation attempts per drill request
GENERATION_TIMEOUT_SECONDS: "60" # Timeout for a single generation call
```

---

## 4. New Store Method: `CountAvailable`

Add to `store.go`:

```go
// CountAvailable returns the number of questions matching the given filters
// that have been served fewer than MAX_SERVES times (i.e., still "fresh").
// For the initial implementation, we count all matching questions regardless
// of times_served, since the drill query already orders by times_served ASC.
func (s *Store) CountAvailable(section models.Section, subtype *models.LRSubtype, difficulty models.Difficulty) (int, error) {
    var count int
    var err error

    if subtype != nil {
        err = s.db.QueryRow(
            `SELECT COUNT(*) FROM questions
             WHERE section = $1 AND lr_subtype = $2 AND difficulty = $3`,
            section, *subtype, difficulty,
        ).Scan(&count)
    } else {
        err = s.db.QueryRow(
            `SELECT COUNT(*) FROM questions
             WHERE section = $1 AND difficulty = $2`,
            section, difficulty,
        ).Scan(&count)
    }

    if err != nil {
        return 0, fmt.Errorf("count available: %w", err)
    }
    return count, nil
}
```

---

## 5. Modified Service: `GetDrillQuestions`

Replace the current pass-through in `service.go` with auto-generation logic:

```go
func (s *Service) GetDrillQuestions(
    ctx context.Context,
    section models.Section,
    subtype *models.LRSubtype,
    difficulty models.Difficulty,
    count int,
) ([]models.Question, error) {

    // 1. Check current inventory
    available, err := s.store.CountAvailable(section, subtype, difficulty)
    if err != nil {
        return nil, fmt.Errorf("check inventory: %w", err)
    }

    threshold := getEnvInt("MIN_QUESTIONS_THRESHOLD", 12)
    maxRetries := getEnvInt("MAX_GENERATION_RETRIES", 2)

    // 2. If inventory is below threshold, generate more
    if available < threshold {
        genCount := threshold  // Generate a full batch to replenish
        if genCount < count {
            genCount = count
        }

        req := models.GenerateBatchRequest{
            Section:    section,
            LRSubtype:  subtype,
            Difficulty: difficulty,
            Count:      genCount,
        }

        // Try generation with retries
        for attempt := 0; attempt < maxRetries; attempt++ {
            timeoutSeconds := getEnvInt("GENERATION_TIMEOUT_SECONDS", 60)
            genCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)

            _, genErr := s.GenerateBatch(genCtx, req)
            cancel()

            if genErr == nil {
                break // Success
            }

            log.Printf("Auto-generation attempt %d failed: %v", attempt+1, genErr)

            // If context was cancelled by the caller, stop retrying
            if ctx.Err() != nil {
                break
            }
        }
        // Generation failures are non-fatal — we still serve whatever we have
    }

    // 3. Fetch and return drill questions (may be empty if nothing exists)
    return s.store.GetDrillQuestions(section, subtype, difficulty, count)
}
```

**Key design decisions:**
- Generation failures are **non-fatal**. If generation fails, we still return whatever questions exist in the DB. The user gets a degraded experience (possibly fewer or repeated questions) rather than an error.
- The threshold check happens **every drill request**. This is a single COUNT query—negligible cost.
- Generation happens **synchronously** in the request. This means the first request after running out may be slow (~5-15s depending on LLM response time). This is acceptable for an LSAT prep app where users expect some loading time.
- `GenerateBatch` (the existing internal method) is reused as-is. We're just removing its public HTTP endpoint, not the service method.

---

## 6. Updated Function Signature

The current `GetDrillQuestions` in `service.go` does not accept a `context.Context`. Update the signature:

**Before:**
```go
func (s *Service) GetDrillQuestions(section models.Section, subtype *models.LRSubtype, difficulty models.Difficulty, count int) ([]models.Question, error) {
    return s.store.GetDrillQuestions(section, subtype, difficulty, count)
}
```

**After:**
```go
func (s *Service) GetDrillQuestions(ctx context.Context, section models.Section, subtype *models.LRSubtype, difficulty models.Difficulty, count int) ([]models.Question, error) {
    // ... auto-generation logic from Section 5 above
}
```

Update the handler's `GetDrill` method to pass `r.Context()`:

```go
func (h *Handler) GetDrill(w http.ResponseWriter, r *http.Request) {
    // ... parse query params ...
    questions, err := h.service.GetDrillQuestions(r.Context(), section, subtype, difficulty, count)
    // ... rest unchanged ...
}
```

---

## 7. Remove Public Generate Endpoint

### 7a. `cmd/server/main.go`

Remove this line:
```go
protected.HandleFunc("/questions/generate", questionHandler.GenerateBatch).Methods("POST")
```

Keep `GenerateBatch` on the handler/service—it's still used internally by `GetDrillQuestions`. Just don't expose it as a route.

### 7b. `questions/handler.go`

The `GenerateBatch` handler method can remain in the file (it may be useful for admin tooling later), but it is no longer registered on any route.

Optionally, if you want to keep it for admin use only, register it under an admin-only subrouter:

```go
// Optional: admin-only route for manual generation
admin := api.PathPrefix("/admin").Subrouter()
admin.Use(middleware.AuthMiddleware)
admin.Use(middleware.AdminMiddleware)  // You'd need to create this
admin.HandleFunc("/questions/generate", questionHandler.GenerateBatch).Methods("POST")
```

---

## 8. Helper: `getEnvInt`

Add a utility (in `service.go` or a shared `config` package):

```go
func getEnvInt(key string, fallback int) int {
    val := os.Getenv(key)
    if val == "" {
        return fallback
    }
    n, err := strconv.Atoi(val)
    if err != nil {
        return fallback
    }
    return n
}
```

---

## 9. Request Flow (Before vs After)

### Before (3 round-trips on empty):
```
Frontend                          Backend
   |                                |
   |--- GET /questions/drill ------>|  (returns empty)
   |<--- 200 { questions: [] } ----|
   |                                |
   |--- POST /questions/generate -->|  (calls Anthropic API)
   |<--- 201 { batch } ------------|
   |                                |
   |--- GET /questions/drill ------>|  (returns questions)
   |<--- 200 { questions: [...] } -|
```

### After (1 round-trip always):
```
Frontend                          Backend
   |                                |
   |--- GET /questions/drill ------>|  1. COUNT available
   |                                |  2. If < threshold: generate inline
   |                                |  3. SELECT drill questions
   |<--- 200 { questions: [...] } -|
```

---

## 10. Edge Cases

| Scenario | Behavior |
|----------|----------|
| No questions exist, generation succeeds | User gets fresh questions (~5-15s wait) |
| No questions exist, generation fails | Returns empty array. Frontend falls back to mock data (existing behavior) |
| Some questions exist but below threshold | Generation happens in background of request; returns available questions (may include newly generated ones) |
| Generation times out | Timeout triggers, returns whatever exists. Generation batch is marked as failed in DB |
| Concurrent drill requests trigger multiple generations | Acceptable—each creates its own batch. The threshold check is not locked. Worst case: we generate 2 batches instead of 1. The `times_served ASC` ordering ensures fair distribution across all generated questions |
| `MOCK_GENERATOR=true` | Mock generator returns instantly, no Anthropic API call. Auto-generation still triggers but completes in milliseconds |

---

## 11. Logging

Add structured logging for observability:

```go
log.Printf("[auto-gen] section=%s subtype=%v difficulty=%s available=%d threshold=%d",
    section, subtype, difficulty, available, threshold)

// On generation trigger:
log.Printf("[auto-gen] triggering generation: count=%d", genCount)

// On generation complete:
log.Printf("[auto-gen] generation complete: batch_id=%d questions=%d elapsed=%dms",
    batch.ID, batch.QuestionCount, elapsed)

// On generation failure:
log.Printf("[auto-gen] generation failed (attempt %d/%d): %v", attempt+1, maxRetries, genErr)
```

---

## 12. Testing

### Unit Tests

```go
func TestGetDrillQuestions_AutoGenerates(t *testing.T) {
    // Setup: empty DB, mock generator
    // Call: GetDrillQuestions(ctx, SectionLR, &SubtypeMBT, DifficultyMedium, 6)
    // Assert: generator.GenerateLRBatch was called
    // Assert: returned questions are non-empty
}

func TestGetDrillQuestions_SkipsGenerationWhenSufficient(t *testing.T) {
    // Setup: DB has 20 questions matching filters
    // Call: GetDrillQuestions(...)
    // Assert: generator was NOT called
    // Assert: returned 6 questions
}

func TestGetDrillQuestions_GenerationFailureReturnsExisting(t *testing.T) {
    // Setup: DB has 3 questions, generator returns error
    // Call: GetDrillQuestions(...)
    // Assert: returned the 3 existing questions (no error propagated)
}

func TestGetDrillQuestions_GenerationFailureEmptyDB(t *testing.T) {
    // Setup: empty DB, generator returns error
    // Call: GetDrillQuestions(...)
    // Assert: returned empty slice, no error
}
```

### Integration Test

```go
func TestDrillEndpoint_AutoGenerates(t *testing.T) {
    // Setup: running server with MOCK_GENERATOR=true, empty DB
    // Call: GET /api/v1/questions/drill?section=logical_reasoning&lr_subtype=must_be_true&difficulty=medium&count=6
    // Assert: 200 with non-empty questions array
    // Assert: DB now contains generated questions
}
```

---

## 13. Implementation Order

1. Add `CountAvailable` to `store.go`
2. Add `getEnvInt` helper
3. Modify `GetDrillQuestions` in `service.go` (add context param + auto-gen logic)
4. Update `GetDrill` handler to pass `r.Context()`
5. Remove `/questions/generate` route from `main.go`
6. Add env vars to `docker-compose.yml`
7. Write tests
8. Test manually with `MOCK_GENERATOR=true` first, then with real API
