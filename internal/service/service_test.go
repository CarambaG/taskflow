package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/CarambaG/taskflow/internal/auth"
	"github.com/CarambaG/taskflow/internal/cache"
	"github.com/CarambaG/taskflow/internal/domain"
	"github.com/CarambaG/taskflow/internal/repository"
)

func TestUpdateTaskPermissions(t *testing.T) {
	tests := []struct {
		name      string
		actorID   int64
		role      domain.Role
		input     UpdateTaskInput
		wantError error
	}{
		{
			name: "assigned member can change status", actorID: 2, role: domain.RoleMember,
			input: UpdateTaskInput{Version: 1, Status: statusPtr(domain.StatusDone)},
		},
		{
			name: "assigned member cannot rename task", actorID: 2, role: domain.RoleMember,
			input: UpdateTaskInput{Version: 1, Title: stringPtr("renamed")}, wantError: domain.ErrForbidden,
		},
		{
			name: "unrelated member cannot change task", actorID: 3, role: domain.RoleMember,
			input: UpdateTaskInput{Version: 1, Status: statusPtr(domain.StatusDone)}, wantError: domain.ErrForbidden,
		},
		{
			name: "admin can rename any task", actorID: 4, role: domain.RoleAdmin,
			input: UpdateTaskInput{Version: 1, Title: stringPtr("renamed")},
		},
		{
			name: "task creator can reassign task", actorID: 1, role: domain.RoleMember,
			input: UpdateTaskInput{Version: 1, AssigneeID: OptionalInt64{Set: true, Value: int64Ptr(3)}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newFakeRepository()
			repo.members[test.actorID] = domain.Membership{TeamID: 10, UserID: test.actorID, Role: test.role}
			service := New(repo, repo, repo, fakeCache{}, auth.NewManager("01234567890123456789012345678901", time.Hour),
				slog.New(slog.NewTextHandler(io.Discard, nil)))
			updated, err := service.UpdateTask(context.Background(), test.actorID, 100, test.input)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("UpdateTask() error = %v, want %v", err, test.wantError)
			}
			if test.wantError == nil && updated.Version != 2 {
				t.Fatalf("UpdateTask() version = %d, want 2", updated.Version)
			}
		})
	}
}

func TestUpdateTaskRejectsStaleVersion(t *testing.T) {
	repo := newFakeRepository()
	service := New(repo, repo, repo, fakeCache{}, auth.NewManager("01234567890123456789012345678901", time.Hour),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := service.UpdateTask(context.Background(), 1, 100, UpdateTaskInput{Version: 9, Status: statusPtr(domain.StatusDone)})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("UpdateTask() error = %v, want conflict", err)
	}
}

func TestChangeMemberRole(t *testing.T) {
	repo := newFakeRepository()
	repo.members[1] = domain.Membership{TeamID: 10, UserID: 1, Role: domain.RoleOwner}
	service := New(repo, repo, repo, fakeCache{}, auth.NewManager("01234567890123456789012345678901", time.Hour),
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	updated, err := service.ChangeMemberRole(context.Background(), 1, 10, 2, domain.RoleAdmin)
	if err != nil {
		t.Fatalf("owner changes member role: %v", err)
	}
	if updated.Role != domain.RoleAdmin {
		t.Fatalf("updated role = %q, want %q", updated.Role, domain.RoleAdmin)
	}

	if _, err := service.ChangeMemberRole(context.Background(), 2, 10, 3, domain.RoleAdmin); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin changes member role: error = %v, want forbidden", err)
	}
	if _, err := service.ChangeMemberRole(context.Background(), 1, 10, 1, domain.RoleMember); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("owner changes owner role: error = %v, want forbidden", err)
	}
}

func TestTasksWritesObservedCacheGeneration(t *testing.T) {
	repo := newFakeRepository()
	trackedCache := &generationCache{generation: 9}
	service := New(repo, repo, repo, trackedCache, auth.NewManager("01234567890123456789012345678901", time.Hour),
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := service.Tasks(context.Background(), 1, domain.TaskFilter{TeamID: 10, Limit: 20})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if trackedCache.setGeneration != trackedCache.generation {
		t.Fatalf("cache generation = %d, want observed generation %d", trackedCache.setGeneration, trackedCache.generation)
	}
}

type fakeRepository struct {
	task    domain.Task
	members map[int64]domain.Membership
}

func newFakeRepository() *fakeRepository {
	assigneeID := int64(2)
	return &fakeRepository{
		task: domain.Task{ID: 100, TeamID: 10, Title: "task", Status: domain.StatusTodo, CreatedBy: 1, AssigneeID: &assigneeID, Version: 1},
		members: map[int64]domain.Membership{
			1: {TeamID: 10, UserID: 1, Role: domain.RoleMember},
			2: {TeamID: 10, UserID: 2, Role: domain.RoleMember},
			3: {TeamID: 10, UserID: 3, Role: domain.RoleMember},
			4: {TeamID: 10, UserID: 4, Role: domain.RoleAdmin},
		},
	}
}

func (f *fakeRepository) CreateUser(context.Context, string, string, string) (domain.User, error) {
	return domain.User{}, nil
}
func (f *fakeRepository) UserByEmail(context.Context, string) (domain.UserWithPassword, error) {
	return domain.UserWithPassword{}, domain.ErrNotFound
}
func (f *fakeRepository) UserByID(_ context.Context, id int64) (domain.User, error) {
	return domain.User{ID: id}, nil
}
func (f *fakeRepository) CreateTeam(context.Context, string, int64) (domain.Team, error) {
	return domain.Team{}, nil
}
func (f *fakeRepository) UserTeams(context.Context, int64) ([]domain.Team, error) { return nil, nil }
func (f *fakeRepository) Membership(_ context.Context, teamID, userID int64) (domain.Membership, error) {
	member, ok := f.members[userID]
	if !ok || member.TeamID != teamID {
		return domain.Membership{}, domain.ErrNotFound
	}
	return member, nil
}
func (f *fakeRepository) AddMember(context.Context, int64, int64, domain.Role) (domain.Membership, error) {
	return domain.Membership{}, nil
}
func (f *fakeRepository) UpdateMemberRole(_ context.Context, teamID, userID int64, role domain.Role) (domain.Membership, error) {
	membership, ok := f.members[userID]
	if !ok || membership.TeamID != teamID {
		return domain.Membership{}, domain.ErrNotFound
	}
	if membership.Role == domain.RoleOwner {
		return domain.Membership{}, domain.ErrForbidden
	}
	membership.Role = role
	f.members[userID] = membership
	return membership, nil
}
func (f *fakeRepository) CreateTask(context.Context, domain.Task) (domain.Task, error) {
	return domain.Task{}, nil
}
func (f *fakeRepository) TaskByID(_ context.Context, id int64) (domain.Task, error) {
	if id != f.task.ID {
		return domain.Task{}, domain.ErrNotFound
	}
	return f.task, nil
}
func (f *fakeRepository) ListTasks(context.Context, domain.TaskFilter) (domain.TaskList, error) {
	return domain.TaskList{}, nil
}
func (f *fakeRepository) UpdateTask(_ context.Context, taskID, version, _ int64, mutate repository.TaskMutator) (domain.Task, error) {
	if taskID != f.task.ID {
		return domain.Task{}, domain.ErrNotFound
	}
	if version != f.task.Version {
		return domain.Task{}, domain.ErrConflict
	}
	updated, changes, err := mutate(f.task)
	if err != nil {
		return domain.Task{}, err
	}
	if len(changes) == 0 {
		return f.task, nil
	}
	updated.Version++
	f.task = updated
	return updated, nil
}
func (f *fakeRepository) TaskHistory(context.Context, int64) ([]domain.TaskHistory, error) {
	return nil, nil
}
func (f *fakeRepository) AddComment(context.Context, domain.Comment) (domain.Comment, error) {
	return domain.Comment{}, nil
}
func (f *fakeRepository) TaskComments(context.Context, int64) ([]domain.Comment, error) {
	return nil, nil
}
func (f *fakeRepository) TeamStats(context.Context, int64) (domain.TeamStats, error) {
	return domain.TeamStats{}, nil
}

type fakeCache struct{}

func (fakeCache) Get(context.Context, domain.TaskFilter) (domain.TaskList, int64, error) {
	return domain.TaskList{}, 0, cache.ErrMiss
}
func (fakeCache) Set(context.Context, domain.TaskFilter, domain.TaskList, int64) error { return nil }
func (fakeCache) InvalidateTeam(context.Context, int64) error                          { return nil }

type generationCache struct {
	generation    int64
	setGeneration int64
}

func (c *generationCache) Get(context.Context, domain.TaskFilter) (domain.TaskList, int64, error) {
	return domain.TaskList{}, c.generation, cache.ErrMiss
}
func (c *generationCache) Set(_ context.Context, _ domain.TaskFilter, _ domain.TaskList, generation int64) error {
	c.setGeneration = generation
	return nil
}
func (*generationCache) InvalidateTeam(context.Context, int64) error { return nil }

func stringPtr(value string) *string                       { return &value }
func int64Ptr(value int64) *int64                          { return &value }
func statusPtr(value domain.TaskStatus) *domain.TaskStatus { return &value }
