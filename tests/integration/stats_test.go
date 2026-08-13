//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CarambaG/taskflow/internal/domain"
	mysqlrepo "github.com/CarambaG/taskflow/internal/repository/mysql"
	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestTeamStatsSQLReport(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := applySchema(db); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	store := mysqlrepo.New(db)
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	owner, err := store.CreateUser(ctx, "owner-"+suffix+"@example.com", "hash", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	assignee, err := store.CreateUser(ctx, "worker-"+suffix+"@example.com", "hash", "Worker")
	if err != nil {
		t.Fatal(err)
	}
	team, err := store.CreateTeam(ctx, "Integration team "+suffix, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMember(ctx, team.ID, assignee.ID, domain.RoleMember); err != nil {
		t.Fatal(err)
	}
	updatedMembership, err := store.UpdateMemberRole(ctx, team.ID, assignee.ID, domain.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if updatedMembership.Role != domain.RoleAdmin {
		t.Fatalf("updated role = %q, want %q", updatedMembership.Role, domain.RoleAdmin)
	}
	if _, err := store.UpdateMemberRole(ctx, team.ID, owner.ID, domain.RoleMember); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("changing owner role: error = %v, want forbidden", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM teams WHERE id = ?`, team.ID)
		_, _ = db.Exec(`DELETE FROM users WHERE id IN (?, ?)`, owner.ID, assignee.ID)
	})

	now := time.Now().UTC()
	doneTask, err := store.CreateTask(ctx, domain.Task{
		TeamID: team.ID, Title: "done", Status: domain.StatusDone, CreatedBy: owner.ID,
		AssigneeID: &assignee.ID, ClosedAt: &now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTask(ctx, domain.Task{TeamID: team.ID, Title: "todo", Status: domain.StatusTodo, CreatedBy: owner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddComment(ctx, domain.Comment{TaskID: doneTask.ID, UserID: owner.ID, Content: "checked"}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.TeamStats(ctx, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TasksByStatus[domain.StatusDone] != 1 || stats.TasksByStatus[domain.StatusTodo] != 1 {
		t.Fatalf("unexpected status counts: %#v", stats.TasksByStatus)
	}
	if stats.CommentsCount != 1 {
		t.Fatalf("comments count = %d, want 1", stats.CommentsCount)
	}
	if len(stats.TopAssignees) != 1 || stats.TopAssignees[0].UserID != assignee.ID || stats.TopAssignees[0].ClosedTasks != 1 {
		t.Fatalf("unexpected top assignees: %#v", stats.TopAssignees)
	}
	if stats.AverageCloseSeconds == nil {
		t.Fatal("average close time must not be nil")
	}
}

func applySchema(db *sql.DB) error {
	path := filepath.Join("..", "..", "migrations", "000001_init.up.sql")
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, statement := range strings.Split(string(payload), ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil && !isTableExists(err) {
			return err
		}
	}
	return nil
}

func isTableExists(err error) bool {
	mysqlErr, ok := err.(*mysqldriver.MySQLError)
	return ok && mysqlErr.Number == 1050
}
