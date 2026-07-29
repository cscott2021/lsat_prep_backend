package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/lsat-prep/backend/internal/auth"
	"github.com/lsat-prep/backend/internal/billing"
	"github.com/lsat-prep/backend/internal/database"
	"github.com/lsat-prep/backend/internal/gamification"
	"github.com/lsat-prep/backend/internal/generator"
	"github.com/lsat-prep/backend/internal/learn"
	"github.com/lsat-prep/backend/internal/middleware"
	"github.com/lsat-prep/backend/internal/notify"
	"github.com/lsat-prep/backend/internal/questions"
	"github.com/rs/cors"
)

// maxRequestBody caps the size of any request body the server will read. It
// guards against memory-exhaustion from an oversized JSON payload while staying
// generous enough for the admin question-bank import.
const maxRequestBody = 10 << 20 // 10 MiB

// limitRequestBody wraps every request body in a MaxBytesReader so handlers that
// decode JSON fail with an error (surfaced as 400) instead of buffering
// unbounded input.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The admin import endpoint applies its own, larger limit; wrapping it
		// here too would cap it at the smaller global limit (the inner reader
		// reads through this outer one).
		if r.Body != nil && !strings.HasSuffix(r.URL.Path, "/admin/import") {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	// Initialize database
	db, err := database.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	if err := database.RunMigrations(db); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Load the JWT signing secret from the environment; fail fast if missing.
	if err := auth.InitJWTSecret(); err != nil {
		log.Fatalf("Failed to initialize auth: %v", err)
	}

	// Initialize handlers
	authHandler := auth.NewHandler(db)

	gen := generator.NewGenerator()
	val := generator.NewValidator()
	questionStore := questions.NewStore(db)
	questionService := questions.NewService(questionStore, gen, val)
	questionHandler := questions.NewHandler(questionService)

	// Initialize gamification
	gamStore := gamification.NewStore(db)
	gamService := gamification.NewService(gamStore)
	gamHandler := gamification.NewHandler(gamService)
	questionService.SetGamificationService(gamService)

	// Initialize learn
	learnStore := learn.NewStore(db)
	learnService := learn.NewService(learnStore)
	learnHandler := learn.NewHandler(learnService)

	// Initialize billing (Stripe). Config comes from the environment; if
	// STRIPE_SECRET_KEY is unset billing is DISABLED gracefully — endpoints
	// return 503, the paywall fails open, and the server still starts. This lets
	// nonprod run with no Stripe keys at all.
	billingCfg := billing.LoadConfig()
	if billingCfg.SecretKey != "" && billingCfg.WebhookSecret == "" {
		// Half-configured: a secret key with no webhook secret would charge cards
		// but never reconcile them (every payer stuck 'incomplete'). Enabled()
		// treats this as DISABLED; make the misconfig loud so ops notices.
		log.Println("[billing] WARNING: STRIPE_SECRET_KEY is set but STRIPE_WEBHOOK_SECRET is empty — billing stays DISABLED until both are provided.")
	}
	billingStore := billing.NewStore(db)
	billingService := billing.NewService(billingCfg, billingStore)
	billingHandler := billing.NewHandler(billingService)
	// Wire the metered-free-tier counter (rolling 24h answered count) so the
	// paywall can meter non-entitled users and /billing/subscription can report
	// usage. The questions store satisfies billing.FreeQuotaCounter.
	billingService.SetFreeQuotaCounter(questionStore)

	// Apple App Store IAP: offline JWS verification of StoreKit 2 signed
	// transactions (no secrets needed — see internal/billing/apple). Shares the
	// same subscriptions table + entitlement model as Stripe.
	appleService, err := billing.NewAppleService(billing.LoadAppleConfig(), billingStore)
	if err != nil {
		log.Fatalf("Failed to initialize Apple IAP: %v", err)
	}
	appleHandler := billing.NewAppleHandler(appleService)

	// Engagement push (APNs): device-token registration + the daily fan-out
	// worker. With no APNS_* credentials the worker logs one line and idles —
	// staging/dev never crash, and go-live is config-only.
	notifyStore := notify.NewStore(db)
	notifyHandler := notify.NewHandler(notifyStore)
	notifyWorker, err := notify.NewWorker(notify.LoadConfig(), notifyStore)
	if err != nil {
		log.Fatalf("Failed to initialize push notifications: %v", err)
	}

	// Wire best-effort billing teardown into account deletion (cancel Stripe
	// subscriptions + delete the customer before the users row cascades away).
	auth.AccountCleanup = billingService.CleanupUserBilling

	// Start background workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go questionService.StartGenerationWorker(ctx)
	go gamService.StartWeeklyResetWorker(ctx)
	go gamService.StartDailyStreakWorker(ctx)
	go billingService.StartEmailWorker(ctx)
	// Self-healing price seeder ($19.99 / $49.99 / $149.99) — retries until all
	// tiers exist so a transient failure can't leave the store unbuyable.
	go billingService.StartPriceSeeder(ctx)
	go notifyWorker.StartDailyWorker(ctx)

	// Setup router
	r := mux.NewRouter()
	api := r.PathPrefix("/api/v1").Subrouter()

	// Public routes
	api.HandleFunc("/auth/register", authHandler.Register).Methods("POST")
	api.HandleFunc("/auth/login", authHandler.Login).Methods("POST")
	api.HandleFunc("/auth/forgot-password", authHandler.ForgotPassword).Methods("POST")
	api.HandleFunc("/auth/reset-password", authHandler.ResetPassword).Methods("POST")

	// Stripe webhook — PUBLIC by necessity: Stripe cannot present a bearer token,
	// so it is mounted OUTSIDE AuthMiddleware. The request is authenticated by the
	// Stripe signature (verified with STRIPE_WEBHOOK_SECRET) inside the handler,
	// and processed idempotently via the billing_events table.
	billingHandler.RegisterWebhook(api)
	// App Store Server Notifications V2 — PUBLIC for the same reason as the
	// Stripe webhook; authenticity comes from the JWS signature, not a token.
	appleHandler.RegisterWebhook(api)

	// Protected routes
	protected := api.PathPrefix("").Subrouter()
	protected.Use(middleware.AuthMiddleware)
	// Defense-in-depth per-user rate limiter (in-memory, 60 req/min). Layered
	// AFTER AuthMiddleware so it keys on the authenticated user_id (not a shared
	// IP). The limit is far above normal interactive use — it exists only to cap
	// a runaway/scripted client (e.g. hammering the answer endpoint), not to
	// enforce the paywall. It is applied to the authenticated subrouter rather
	// than the outer /api subrouter because per-user-id keying requires
	// AuthMiddleware to have run; anonymous/public routes (login, register,
	// webhook) are intentionally not keyed here.
	protected.Use(middleware.RateLimit(60, time.Minute))
	protected.HandleFunc("/auth/me", authHandler.GetCurrentUser).Methods("GET")
	// Account deletion (Apple App Review guideline 5.1.1(v) requires in-app
	// deletion for apps with account creation). Best-effort billing teardown
	// runs first via the auth.AccountCleanup hook wired below.
	protected.HandleFunc("/auth/me", authHandler.DeleteAccount).Methods("DELETE")

	// User adaptive endpoints
	protected.HandleFunc("/users/ability", questionHandler.GetAbility).Methods("GET")
	protected.HandleFunc("/users/difficulty-slider", questionHandler.SetDifficultySlider).Methods("PUT")

	// Billing (protected): config, subscription status, subscribe, coupon,
	// cancel, update-payment, portal. These stay OPEN to any authenticated user
	// (a non-subscriber must be able to see plans and subscribe).
	billingHandler.RegisterRoutes(protected)
	// Apple IAP verify (iOS app calls this right after a StoreKit purchase).
	appleHandler.RegisterRoutes(protected)

	// Device-token registration for engagement push (APNs). Registered on
	// login/app start/token refresh; unregistered on logout.
	notifyHandler.RegisterRoutes(protected)

	// Entitlement-gated core practice endpoints. These consume paid resources (LLM
	// generation), so they sit behind a billing gate IN ADDITION to AuthMiddleware.
	//
	// Two tiers of gating:
	//   - /questions/generate stays PREMIUM-ONLY via RequireEntitlement (402
	//     subscription_required for non-entitled users) — it is the raw
	//     batch-generation endpoint.
	//   - The drill endpoints AND POST /questions/{id}/answer use
	//     RequireEntitlementOrFreeQuota: entitled users pass unlimited;
	//     non-entitled users get a metered free tier (freeTierLimit questions /
	//     rolling 24h, then 402 free_limit_reached). The middleware records the
	//     remaining allowance in the request context so the drill handlers clamp
	//     how many questions a free user is served. Gating the answer endpoint is
	//     what actually enforces the cap: each SubmitAnswer reveals the answer +
	//     explanation, so leaving it ungated let a free user consume unlimited
	//     questions and fully defeated the 3/24h limit.
	//
	// When billing is unconfigured both gates fail open (see the billing
	// middlewares). Both subrouters are created before the parameterized
	// /questions/{id} route so their fixed paths match first (gorilla/mux matches
	// in registration order).
	entitled := protected.PathPrefix("").Subrouter()
	entitled.Use(billing.RequireEntitlement(billingService))
	entitled.HandleFunc("/questions/generate", questionHandler.GenerateBatch).Methods("POST")

	metered := protected.PathPrefix("").Subrouter()
	metered.Use(billing.RequireEntitlementOrFreeQuota(billingService, questionStore))
	metered.HandleFunc("/questions/quick-drill", questionHandler.QuickDrill).Methods("POST")
	metered.HandleFunc("/questions/subtype-drill", questionHandler.SubtypeDrill).Methods("POST")
	metered.HandleFunc("/questions/rc-drill", questionHandler.RCDrill).Methods("POST")
	metered.HandleFunc("/questions/similar-drill", questionHandler.SimilarDrill).Methods("POST")
	// Answer submission is metered too (see the note above). It lives on the
	// metered subrouter, which is registered BEFORE the parameterized
	// /questions/{id} route below, so gorilla/mux (which matches in registration
	// order) still resolves the fixed drill paths and this parameterized answer
	// path correctly. An entitled/admin caller passes unlimited; a free caller is
	// allowed up to freeTierLimit answered questions per rolling 24h, then 402.
	metered.HandleFunc("/questions/{id}/answer", questionHandler.SubmitAnswer).Methods("POST")

	// Remaining question endpoints (fixed paths before parameterized). Left OPEN
	// so an in-progress drill and history review keep working regardless of
	// entitlement.
	protected.HandleFunc("/questions/batches", questionHandler.ListBatches).Methods("GET")
	protected.HandleFunc("/questions/batches/{id}", questionHandler.GetBatch).Methods("GET")
	// Offramp poll: how many unseen questions are ready + whether more are coming.
	protected.HandleFunc("/questions/generation-status", questionHandler.GenerationStatus).Methods("GET")
	protected.HandleFunc("/questions/{id}", questionHandler.GetQuestion).Methods("GET")

	// Passage endpoint
	protected.HandleFunc("/passages/{id}", questionHandler.GetPassage).Methods("GET")

	// Gamification endpoints
	protected.HandleFunc("/users/gamification", gamHandler.GetGamification).Methods("GET")
	protected.HandleFunc("/users/gamification/streak-freeze", gamHandler.BuyStreakFreeze).Methods("POST")
	protected.HandleFunc("/users/daily-goal", gamHandler.SetDailyGoal).Methods("PUT")
	protected.HandleFunc("/drills/complete", gamHandler.CompleteDrill).Methods("POST")

	// Leaderboard
	protected.HandleFunc("/leaderboard/global", gamHandler.GlobalLeaderboard).Methods("GET")
	protected.HandleFunc("/leaderboard/friends", gamHandler.FriendsLeaderboard).Methods("GET")

	// Friends (fixed paths before parameterized)
	protected.HandleFunc("/friends/request", gamHandler.SendFriendRequest).Methods("POST")
	protected.HandleFunc("/friends/respond", gamHandler.RespondFriendRequest).Methods("POST")
	protected.HandleFunc("/friends/search", gamHandler.SearchUsers).Methods("GET")
	protected.HandleFunc("/friends", gamHandler.ListFriends).Methods("GET")
	protected.HandleFunc("/friends/{id}", gamHandler.RemoveFriend).Methods("DELETE")

	// Nudges
	protected.HandleFunc("/nudges", gamHandler.ListNudges).Methods("GET")
	protected.HandleFunc("/nudges", gamHandler.SendNudge).Methods("POST")
	protected.HandleFunc("/nudges/{id}/read", gamHandler.MarkNudgeRead).Methods("POST")

	// Admin endpoints — gated by AdminMiddleware (is_admin) on top of auth.
	// These expose the full question bank (export/import) and calibration, so
	// they must never be reachable by an ordinary authenticated user.
	admin := protected.PathPrefix("/admin").Subrouter()
	admin.Use(middleware.AdminMiddleware(db))
	admin.HandleFunc("/quality-stats", questionHandler.GetQualityStats).Methods("GET")
	admin.HandleFunc("/generation-stats", questionHandler.GetGenerationStats).Methods("GET")
	admin.HandleFunc("/recalibrate", questionHandler.Recalibrate).Methods("POST")
	admin.HandleFunc("/flagged", questionHandler.GetFlaggedQuestions).Methods("GET")
	admin.HandleFunc("/export", questionHandler.ExportQuestions).Methods("GET")
	admin.HandleFunc("/import", questionHandler.ImportQuestions).Methods("POST")
	// Admin dashboards: question-bank browser, bank composition, user progress.
	admin.HandleFunc("/questions", questionHandler.GetAdminQuestions).Methods("GET")
	admin.HandleFunc("/question-spread", questionHandler.GetQuestionSpread).Methods("GET")
	admin.HandleFunc("/user-progress", questionHandler.GetUserProgress).Methods("GET")
	// Growth dashboard: signups over time, subscriber mix + estimated MRR,
	// DAU/WAU/MAU engagement, and early-cohort retention.
	admin.HandleFunc("/metrics", questionHandler.GetAdminMetrics).Methods("GET")
	// Admin billing: create/list coupons (Stripe coupon + promotion code) and
	// grant/revoke manual comps.
	billingHandler.RegisterAdminRoutes(admin)

	// History & bookmarks
	questionHandler.RegisterHistoryRoutes(protected)

	// Learn (fixed path before parameterized)
	protected.HandleFunc("/learn/guides/by-subtype", learnHandler.GetGuideBySubtype).Methods("GET")
	protected.HandleFunc("/learn/guides", learnHandler.ListGuides).Methods("GET")
	protected.HandleFunc("/learn/guides/{id}", learnHandler.GetGuide).Methods("GET")

	// Health check. Pings the database so the load balancer / ECS health check
	// treats an instance that has lost its DB connection as UNHEALTHY (503)
	// instead of reporting a hollow 200 while every real request fails.
	r.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		if err := db.PingContext(ctx); err != nil {
			log.Printf("[health] database ping failed: %v", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unavailable","db":"down"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}).Methods("GET")

	// CORS. Auth is via a Bearer token in the Authorization header (not cookies),
	// so credentialed CORS is unnecessary — and a wildcard origin combined with
	// AllowCredentials:true is an invalid combination that browsers reject.
	// PATCH is included so web clients can call the dismiss/undismiss routes.
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: false,
	})

	handler := c.Handler(limitRequestBody(r))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Graceful shutdown
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down server...")
		cancel()
		srv.Shutdown(context.Background())
	}()

	// Log all registered routes for debugging
	r.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		tpl, _ := route.GetPathTemplate()
		methods, _ := route.GetMethods()
		log.Printf("Route: %v %s", methods, tpl)
		return nil
	})

	log.Printf("Server starting on :%s", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}
