package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gorilla/mux"
	"github.com/lsat-prep/backend/internal/auth"
	"github.com/lsat-prep/backend/internal/database"
	"github.com/lsat-prep/backend/internal/gamification"
	"github.com/lsat-prep/backend/internal/generator"
	"github.com/lsat-prep/backend/internal/learn"
	"github.com/lsat-prep/backend/internal/middleware"
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

	// Start background workers
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go questionService.StartGenerationWorker(ctx)
	go gamService.StartWeeklyResetWorker(ctx)
	go gamService.StartDailyStreakWorker(ctx)

	// Setup router
	r := mux.NewRouter()
	api := r.PathPrefix("/api/v1").Subrouter()

	// Public routes
	api.HandleFunc("/auth/register", authHandler.Register).Methods("POST")
	api.HandleFunc("/auth/login", authHandler.Login).Methods("POST")

	// Protected routes
	protected := api.PathPrefix("").Subrouter()
	protected.Use(middleware.AuthMiddleware)
	protected.HandleFunc("/auth/me", authHandler.GetCurrentUser).Methods("GET")

	// User adaptive endpoints
	protected.HandleFunc("/users/ability", questionHandler.GetAbility).Methods("GET")
	protected.HandleFunc("/users/difficulty-slider", questionHandler.SetDifficultySlider).Methods("PUT")

	// Question endpoints (fixed paths before parameterized)
	protected.HandleFunc("/questions/generate", questionHandler.GenerateBatch).Methods("POST")
	protected.HandleFunc("/questions/batches", questionHandler.ListBatches).Methods("GET")
	protected.HandleFunc("/questions/batches/{id}", questionHandler.GetBatch).Methods("GET")
	protected.HandleFunc("/questions/quick-drill", questionHandler.QuickDrill).Methods("POST")
	protected.HandleFunc("/questions/subtype-drill", questionHandler.SubtypeDrill).Methods("POST")
	protected.HandleFunc("/questions/rc-drill", questionHandler.RCDrill).Methods("POST")
	protected.HandleFunc("/questions/similar-drill", questionHandler.SimilarDrill).Methods("POST")
	protected.HandleFunc("/questions/{id}", questionHandler.GetQuestion).Methods("GET")
	protected.HandleFunc("/questions/{id}/answer", questionHandler.SubmitAnswer).Methods("POST")

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

	// History & bookmarks
	questionHandler.RegisterHistoryRoutes(protected)

	// Learn (fixed path before parameterized)
	protected.HandleFunc("/learn/guides/by-subtype", learnHandler.GetGuideBySubtype).Methods("GET")
	protected.HandleFunc("/learn/guides", learnHandler.ListGuides).Methods("GET")
	protected.HandleFunc("/learn/guides/{id}", learnHandler.GetGuide).Methods("GET")

	// Health check
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
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
