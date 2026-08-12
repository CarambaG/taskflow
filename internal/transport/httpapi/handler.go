package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/CarambaG/taskflow/api"
	"github.com/CarambaG/taskflow/internal/domain"
	"github.com/CarambaG/taskflow/internal/service"
	"github.com/go-chi/chi/v5"
)

type Pinger interface{ Ping(context.Context) error }

type Handler struct {
	service *service.Service
	logger  *slog.Logger
	mysql   Pinger
	redis   Pinger
}

func NewHandler(service *service.Service, logger *slog.Logger, mysql, redis Pinger) *Handler {
	return &Handler{service: service, logger: logger, mysql: mysql, redis: redis}
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(h.logger, w, r, invalidBody(err))
		return
	}
	user, err := h.service.Register(r.Context(), service.RegisterInput{Email: input.Email, Password: input.Password, Name: input.Name})
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(h.logger, w, r, invalidBody(err))
		return
	}
	result, err := h.service.Login(r.Context(), input.Email, input.Password)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(h.logger, w, r, invalidBody(err))
		return
	}
	team, err := h.service.CreateTeam(r.Context(), actorID, input.Name)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, team)
}

func (h *Handler) Teams(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	teams, err := h.service.Teams(r.Context(), actorID)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": teams})
}

func (h *Handler) Invite(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	teamID, err := pathID(r, "id")
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	var input struct {
		UserID int64       `json:"user_id"`
		Role   domain.Role `json:"role"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(h.logger, w, r, invalidBody(err))
		return
	}
	if input.UserID <= 0 {
		writeError(h.logger, w, r, invalid("user_id must be positive"))
		return
	}
	membership, err := h.service.Invite(r.Context(), actorID, teamID, input.UserID, input.Role)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, membership)
}

func (h *Handler) ChangeMemberRole(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	teamID, err := pathID(r, "id")
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	memberID, err := pathID(r, "user_id")
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	var input struct {
		Role domain.Role `json:"role"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(h.logger, w, r, invalidBody(err))
		return
	}
	membership, err := h.service.ChangeMemberRole(r.Context(), actorID, teamID, memberID, input.Role)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, membership)
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	var input struct {
		TeamID      int64             `json:"team_id"`
		Title       string            `json:"title"`
		Description string            `json:"description"`
		Status      domain.TaskStatus `json:"status"`
		AssigneeID  *int64            `json:"assignee_id"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(h.logger, w, r, invalidBody(err))
		return
	}
	if input.TeamID <= 0 {
		writeError(h.logger, w, r, invalid("team_id must be positive"))
		return
	}
	task, err := h.service.CreateTask(r.Context(), actorID, service.CreateTaskInput{
		TeamID: input.TeamID, Title: input.Title, Description: input.Description, Status: input.Status, AssigneeID: input.AssigneeID,
	})
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (h *Handler) Tasks(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	teamID, err := queryInt64(r, "team_id", true)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	limit, err := queryInt(r, "limit", 20)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	filter := domain.TaskFilter{TeamID: teamID, Limit: limit, Offset: offset}
	if raw := r.URL.Query().Get("status"); raw != "" {
		status := domain.TaskStatus(raw)
		filter.Status = &status
	}
	if raw := r.URL.Query().Get("assignee_id"); raw != "" {
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || id <= 0 {
			writeError(h.logger, w, r, invalid("assignee_id must be positive"))
			return
		}
		filter.AssigneeID = &id
	}
	list, err := h.service.Tasks(r.Context(), actorID, filter)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	taskID, err := pathID(r, "id")
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	var input updateTaskRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(h.logger, w, r, invalidBody(err))
		return
	}
	result, err := h.service.UpdateTask(r.Context(), actorID, taskID, service.UpdateTaskInput{
		Version: input.Version, Title: input.Title, Description: input.Description,
		Status: input.Status, AssigneeID: service.OptionalInt64{Set: input.AssigneeID.Set, Value: input.AssigneeID.Value},
	})
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	taskID, err := pathID(r, "id")
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	items, err := h.service.History(r.Context(), actorID, taskID)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) AddComment(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	taskID, err := pathID(r, "id")
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(h.logger, w, r, invalidBody(err))
		return
	}
	comment, err := h.service.AddComment(r.Context(), actorID, taskID, input.Content)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

func (h *Handler) Comments(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	taskID, err := pathID(r, "id")
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	items, err := h.service.Comments(r.Context(), actorID, taskID)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	actorID, _ := userID(r.Context())
	teamID, err := pathID(r, "team_id")
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	stats, err := h.service.Stats(r.Context(), actorID, teamID)
	if err != nil {
		writeError(h.logger, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	if err := h.mysql.Ping(r.Context()); err != nil {
		h.notReady(w, r, "mysql", err)
		return
	}
	if err := h.redis.Ping(r.Context()); err != nil {
		h.notReady(w, r, "redis", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) notReady(w http.ResponseWriter, r *http.Request, dependency string, err error) {
	h.logger.ErrorContext(r.Context(), "readiness check failed", "dependency", dependency, "error", err)
	writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: errorBody{
		Code: "not_ready", Message: "service dependency is unavailable", RequestID: requestID(r.Context()),
	}})
}

func OpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(api.OpenAPI)
}

type updateTaskRequest struct {
	Version     int64              `json:"version"`
	Title       *string            `json:"title"`
	Description *string            `json:"description"`
	Status      *domain.TaskStatus `json:"status"`
	AssigneeID  nullableInt64      `json:"assignee_id"`
}

type nullableInt64 struct {
	Set   bool
	Value *int64
}

func (n *nullableInt64) UnmarshalJSON(data []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		n.Value = nil
		return nil
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value <= 0 {
		return errors.New("assignee_id must be positive or null")
	}
	n.Value = &value
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalid(name + " must be positive")
	}
	return id, nil
}

func queryInt64(r *http.Request, name string, required bool) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" && required {
		return 0, invalid(name + " is required")
	}
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, invalid(name + " must be positive")
	}
	return value, nil
}

func queryInt(r *http.Request, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, invalid(name + " must be an integer")
	}
	return value, nil
}

func invalidBody(err error) error  { return invalid("invalid JSON body: " + err.Error()) }
func invalid(message string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, message) }
