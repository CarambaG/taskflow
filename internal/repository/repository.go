package repository

import (
	"context"

	"github.com/CarambaG/taskflow/internal/domain"
)

type UserRepository interface {
	CreateUser(ctx context.Context, email, passwordHash, name string) (domain.User, error)
	UserByEmail(ctx context.Context, email string) (domain.UserWithPassword, error)
	UserByID(ctx context.Context, id int64) (domain.User, error)
}

type TeamRepository interface {
	CreateTeam(ctx context.Context, name string, creatorID int64) (domain.Team, error)
	UserTeams(ctx context.Context, userID int64) ([]domain.Team, error)
	Membership(ctx context.Context, teamID, userID int64) (domain.Membership, error)
	AddMember(ctx context.Context, teamID, userID int64, role domain.Role) (domain.Membership, error)
	UpdateMemberRole(ctx context.Context, teamID, userID int64, role domain.Role) (domain.Membership, error)
}

type TaskMutator func(current domain.Task) (updated domain.Task, changes map[string]any, err error)

type TaskRepository interface {
	CreateTask(ctx context.Context, task domain.Task) (domain.Task, error)
	TaskByID(ctx context.Context, id int64) (domain.Task, error)
	ListTasks(ctx context.Context, filter domain.TaskFilter) (domain.TaskList, error)
	UpdateTask(ctx context.Context, taskID, expectedVersion, actorID int64, mutate TaskMutator) (domain.Task, error)
	TaskHistory(ctx context.Context, taskID int64) ([]domain.TaskHistory, error)
	AddComment(ctx context.Context, comment domain.Comment) (domain.Comment, error)
	TaskComments(ctx context.Context, taskID int64) ([]domain.Comment, error)
	TeamStats(ctx context.Context, teamID int64) (domain.TeamStats, error)
}
