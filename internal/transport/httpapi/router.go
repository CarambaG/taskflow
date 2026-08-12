package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/CarambaG/taskflow/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/swaggest/swgui/v5emb"
)

func NewRouter(handler *Handler, logger *slog.Logger, tokens *auth.Manager, rps float64, burst int) http.Handler {
	router := chi.NewRouter()
	middleware := NewMiddleware(logger, tokens, rps, burst)
	router.Use(middleware.RequestID, middleware.Recover, middleware.Log, middleware.RateLimit)

	router.Get("/healthz", handler.Health)
	router.Get("/readyz", handler.Ready)
	router.Get("/openapi.yaml", OpenAPISpec)
	router.Mount("/swagger", v5emb.New("TaskFlow API", "/openapi.yaml", "/swagger"))

	router.Route("/api/v1", func(api chi.Router) {
		api.Post("/register", handler.Register)
		api.Post("/login", handler.Login)
		api.Group(func(protected chi.Router) {
			protected.Use(middleware.Authenticate)
			protected.Post("/teams", handler.CreateTeam)
			protected.Get("/teams", handler.Teams)
			protected.Post("/teams/{id}/invite", handler.Invite)
			protected.Patch("/teams/{id}/members/{user_id}/role", handler.ChangeMemberRole)
			protected.Get("/teams/{team_id}/stats", handler.Stats)
			protected.Post("/tasks", handler.CreateTask)
			protected.Get("/tasks", handler.Tasks)
			protected.Put("/tasks/{id}", handler.UpdateTask)
			protected.Get("/tasks/{id}/history", handler.History)
			protected.Post("/tasks/{id}/comments", handler.AddComment)
			protected.Get("/tasks/{id}/comments", handler.Comments)
		})
	})

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: errorBody{Code: "route_not_found", Message: "route was not found", RequestID: requestID(r.Context())}})
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: errorBody{Code: "method_not_allowed", Message: "method is not allowed", RequestID: requestID(r.Context())}})
	})
	return router
}
