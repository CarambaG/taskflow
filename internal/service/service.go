package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/CarambaG/taskflow/internal/auth"
	"github.com/CarambaG/taskflow/internal/cache"
	"github.com/CarambaG/taskflow/internal/domain"
	"github.com/CarambaG/taskflow/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	users  repository.UserRepository
	teams  repository.TeamRepository
	tasks  repository.TaskRepository
	cache  cache.TaskCache
	tokens *auth.Manager
	logger *slog.Logger
}

func New(users repository.UserRepository, teams repository.TeamRepository, tasks repository.TaskRepository,
	cache cache.TaskCache, tokens *auth.Manager, logger *slog.Logger) *Service {
	return &Service{users: users, teams: teams, tasks: tasks, cache: cache, tokens: tokens, logger: logger}
}

type RegisterInput struct{ Email, Password, Name string }

type LoginResult struct {
	Token string      `json:"token"`
	User  domain.User `json:"user"`
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (domain.User, error) {
	email := strings.ToLower(strings.TrimSpace(input.Email))
	name := strings.TrimSpace(input.Name)
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email || len(email) > 255 {
		return domain.User{}, invalid("invalid email")
	}
	if len(input.Password) < 8 || len(input.Password) > 72 {
		return domain.User{}, invalid("password length must be between 8 and 72")
	}
	if len(name) < 2 || len(name) > 120 {
		return domain.User{}, invalid("name length must be between 2 and 120")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}
	return s.users.CreateUser(ctx, email, string(hash), name)
}

func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	user, err := s.users.UserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return LoginResult{}, domain.ErrUnauthorized
		}
		return LoginResult{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return LoginResult{}, domain.ErrUnauthorized
	}
	token, err := s.tokens.Issue(user.ID)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, User: user.User}, nil
}

func (s *Service) CreateTeam(ctx context.Context, actorID int64, name string) (domain.Team, error) {
	name = strings.TrimSpace(name)
	if len(name) < 2 || len(name) > 160 {
		return domain.Team{}, invalid("team name length must be between 2 and 160")
	}
	return s.teams.CreateTeam(ctx, name, actorID)
}

func (s *Service) Teams(ctx context.Context, actorID int64) ([]domain.Team, error) {
	return s.teams.UserTeams(ctx, actorID)
}

func (s *Service) Invite(ctx context.Context, actorID, teamID, userID int64, role domain.Role) (domain.Membership, error) {
	if !role.ValidInviteRole() {
		return domain.Membership{}, invalid("role must be admin or member")
	}
	actor, err := s.teams.Membership(ctx, teamID, actorID)
	if err != nil {
		return domain.Membership{}, concealMembership(err)
	}
	if !actor.Role.CanManage() {
		return domain.Membership{}, domain.ErrForbidden
	}
	if _, err := s.users.UserByID(ctx, userID); err != nil {
		return domain.Membership{}, err
	}
	return s.teams.AddMember(ctx, teamID, userID, role)
}

func (s *Service) ChangeMemberRole(ctx context.Context, actorID, teamID, userID int64, role domain.Role) (domain.Membership, error) {
	if !role.ValidInviteRole() {
		return domain.Membership{}, invalid("role must be admin or member")
	}
	actor, err := s.teams.Membership(ctx, teamID, actorID)
	if err != nil {
		return domain.Membership{}, concealMembership(err)
	}
	if actor.Role != domain.RoleOwner {
		return domain.Membership{}, domain.ErrForbidden
	}
	target, err := s.teams.Membership(ctx, teamID, userID)
	if err != nil {
		return domain.Membership{}, concealMembership(err)
	}
	if target.Role == domain.RoleOwner {
		return domain.Membership{}, domain.ErrForbidden
	}
	if target.Role == role {
		return target, nil
	}
	return s.teams.UpdateMemberRole(ctx, teamID, userID, role)
}

type CreateTaskInput struct {
	TeamID      int64
	Title       string
	Description string
	Status      domain.TaskStatus
	AssigneeID  *int64
}

func (s *Service) CreateTask(ctx context.Context, actorID int64, input CreateTaskInput) (domain.Task, error) {
	if _, err := s.teams.Membership(ctx, input.TeamID, actorID); err != nil {
		return domain.Task{}, concealMembership(err)
	}
	input.Title = strings.TrimSpace(input.Title)
	if len(input.Title) < 1 || len(input.Title) > 240 {
		return domain.Task{}, invalid("title length must be between 1 and 240")
	}
	if len(input.Description) > 10000 {
		return domain.Task{}, invalid("description is too long")
	}
	if input.Status == "" {
		input.Status = domain.StatusTodo
	}
	if !input.Status.Valid() {
		return domain.Task{}, invalid("invalid task status")
	}
	if input.AssigneeID != nil {
		if _, err := s.teams.Membership(ctx, input.TeamID, *input.AssigneeID); err != nil {
			return domain.Task{}, invalid("assignee must be a team member")
		}
	}
	task := domain.Task{TeamID: input.TeamID, Title: input.Title, Description: input.Description,
		Status: input.Status, CreatedBy: actorID, AssigneeID: input.AssigneeID}
	if task.Status == domain.StatusDone {
		now := time.Now().UTC()
		task.ClosedAt = &now
	}
	created, err := s.tasks.CreateTask(ctx, task)
	if err == nil {
		s.invalidate(ctx, input.TeamID)
	}
	return created, err
}

func (s *Service) Tasks(ctx context.Context, actorID int64, filter domain.TaskFilter) (domain.TaskList, error) {
	if filter.TeamID <= 0 {
		return domain.TaskList{}, invalid("team_id is required")
	}
	if _, err := s.teams.Membership(ctx, filter.TeamID, actorID); err != nil {
		return domain.TaskList{}, concealMembership(err)
	}
	if filter.Status != nil && !filter.Status.Valid() {
		return domain.TaskList{}, invalid("invalid task status")
	}
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return domain.TaskList{}, invalid("invalid pagination")
	}
	list, generation, cacheErr := s.cache.Get(ctx, filter)
	if cacheErr == nil {
		return list, nil
	}
	cacheMiss := errors.Is(cacheErr, cache.ErrMiss)
	if !cacheMiss {
		s.logger.WarnContext(ctx, "task cache read failed", "error", cacheErr, "team_id", filter.TeamID)
	}
	list, err := s.tasks.ListTasks(ctx, filter)
	if err != nil {
		return domain.TaskList{}, err
	}
	if cacheMiss {
		if err := s.cache.Set(ctx, filter, list, generation); err != nil {
			s.logger.WarnContext(ctx, "task cache write failed", "error", err, "team_id", filter.TeamID)
		}
	}
	return list, nil
}

type OptionalInt64 struct {
	Set   bool
	Value *int64
}

type UpdateTaskInput struct {
	Version     int64
	Title       *string
	Description *string
	Status      *domain.TaskStatus
	AssigneeID  OptionalInt64
}

func (s *Service) UpdateTask(ctx context.Context, actorID, taskID int64, input UpdateTaskInput) (domain.Task, error) {
	if input.Version <= 0 {
		return domain.Task{}, invalid("version is required")
	}
	current, err := s.tasks.TaskByID(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	member, err := s.teams.Membership(ctx, current.TeamID, actorID)
	if err != nil {
		return domain.Task{}, concealMembership(err)
	}

	updated, err := s.tasks.UpdateTask(ctx, taskID, input.Version, actorID, func(task domain.Task) (domain.Task, map[string]any, error) {
		canEditAll := member.Role.CanManage() || task.CreatedBy == actorID
		isAssignee := task.AssigneeID != nil && *task.AssigneeID == actorID
		if !canEditAll && !isAssignee {
			return domain.Task{}, nil, domain.ErrForbidden
		}
		if isAssignee && !canEditAll && (input.Title != nil || input.Description != nil || input.AssigneeID.Set) {
			return domain.Task{}, nil, domain.ErrForbidden
		}
		changes := make(map[string]any)
		if input.Title != nil {
			value := strings.TrimSpace(*input.Title)
			if len(value) < 1 || len(value) > 240 {
				return domain.Task{}, nil, invalid("title length must be between 1 and 240")
			}
			if value != task.Title {
				changes["title"] = change(task.Title, value)
				task.Title = value
			}
		}
		if input.Description != nil {
			if len(*input.Description) > 10000 {
				return domain.Task{}, nil, invalid("description is too long")
			}
			if *input.Description != task.Description {
				changes["description"] = change(task.Description, *input.Description)
				task.Description = *input.Description
			}
		}
		if input.AssigneeID.Set {
			if input.AssigneeID.Value != nil {
				if _, err := s.teams.Membership(ctx, task.TeamID, *input.AssigneeID.Value); err != nil {
					return domain.Task{}, nil, invalid("assignee must be a team member")
				}
			}
			if !sameID(task.AssigneeID, input.AssigneeID.Value) {
				changes["assignee_id"] = change(task.AssigneeID, input.AssigneeID.Value)
				task.AssigneeID = input.AssigneeID.Value
			}
		}
		if input.Status != nil {
			if !input.Status.Valid() {
				return domain.Task{}, nil, invalid("invalid task status")
			}
			if *input.Status != task.Status {
				changes["status"] = change(task.Status, *input.Status)
				oldClosedAt := task.ClosedAt
				task.Status = *input.Status
				if task.Status == domain.StatusDone {
					now := time.Now().UTC()
					task.ClosedAt = &now
				} else {
					task.ClosedAt = nil
				}
				changes["closed_at"] = change(oldClosedAt, task.ClosedAt)
			}
		}
		return task, changes, nil
	})
	if err == nil {
		s.invalidate(ctx, current.TeamID)
	}
	return updated, err
}

func (s *Service) History(ctx context.Context, actorID, taskID int64) ([]domain.TaskHistory, error) {
	task, err := s.taskVisible(ctx, actorID, taskID)
	if err != nil {
		return nil, err
	}
	return s.tasks.TaskHistory(ctx, task.ID)
}

func (s *Service) AddComment(ctx context.Context, actorID, taskID int64, content string) (domain.Comment, error) {
	task, err := s.taskVisible(ctx, actorID, taskID)
	if err != nil {
		return domain.Comment{}, err
	}
	content = strings.TrimSpace(content)
	if len(content) < 1 || len(content) > 5000 {
		return domain.Comment{}, invalid("comment length must be between 1 and 5000")
	}
	return s.tasks.AddComment(ctx, domain.Comment{TaskID: task.ID, UserID: actorID, Content: content})
}

func (s *Service) Comments(ctx context.Context, actorID, taskID int64) ([]domain.Comment, error) {
	task, err := s.taskVisible(ctx, actorID, taskID)
	if err != nil {
		return nil, err
	}
	return s.tasks.TaskComments(ctx, task.ID)
}

func (s *Service) Stats(ctx context.Context, actorID, teamID int64) (domain.TeamStats, error) {
	member, err := s.teams.Membership(ctx, teamID, actorID)
	if err != nil {
		return domain.TeamStats{}, concealMembership(err)
	}
	if !member.Role.CanManage() {
		return domain.TeamStats{}, domain.ErrForbidden
	}
	return s.tasks.TeamStats(ctx, teamID)
}

func (s *Service) taskVisible(ctx context.Context, actorID, taskID int64) (domain.Task, error) {
	task, err := s.tasks.TaskByID(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	if _, err := s.teams.Membership(ctx, task.TeamID, actorID); err != nil {
		return domain.Task{}, concealMembership(err)
	}
	return task, nil
}

func (s *Service) invalidate(ctx context.Context, teamID int64) {
	if err := s.cache.InvalidateTeam(ctx, teamID); err != nil {
		s.logger.WarnContext(ctx, "task cache invalidation failed", "error", err, "team_id", teamID)
	}
}

func invalid(message string) error { return fmt.Errorf("%w: %s", domain.ErrInvalid, message) }
func concealMembership(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}
func change(oldValue, newValue any) map[string]any {
	return map[string]any{"old": oldValue, "new": newValue}
}
func sameID(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
