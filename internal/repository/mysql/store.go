package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CarambaG/taskflow/internal/domain"
	"github.com/CarambaG/taskflow/internal/repository"
	mysqldriver "github.com/go-sql-driver/mysql"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store { return &Store{db: db} }

func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return db, nil
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) CreateUser(ctx context.Context, email, passwordHash, name string) (domain.User, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, password_hash, name) VALUES (?, ?, ?)`, email, passwordHash, name)
	if err != nil {
		return domain.User{}, mapError(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.User{}, fmt.Errorf("user id: %w", err)
	}
	return s.UserByID(ctx, id)
}

func (s *Store) UserByEmail(ctx context.Context, email string) (domain.UserWithPassword, error) {
	var user domain.UserWithPassword
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, name, created_at FROM users WHERE email = ?`, email,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.CreatedAt)
	if err != nil {
		return domain.UserWithPassword{}, mapError(err)
	}
	return user, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (domain.User, error) {
	var user domain.User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, name, created_at FROM users WHERE id = ?`, id,
	).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt)
	if err != nil {
		return domain.User{}, mapError(err)
	}
	return user, nil
}

func (s *Store) CreateTeam(ctx context.Context, name string, creatorID int64) (team domain.Team, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return team, fmt.Errorf("begin create team: %w", err)
	}
	defer rollback(tx)

	result, err := tx.ExecContext(ctx, `INSERT INTO teams (name, created_by) VALUES (?, ?)`, name, creatorID)
	if err != nil {
		return team, mapError(err)
	}
	team.ID, err = result.LastInsertId()
	if err != nil {
		return team, fmt.Errorf("team id: %w", err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, 'owner')`, team.ID, creatorID,
	); err != nil {
		return team, mapError(err)
	}
	if err = tx.QueryRowContext(ctx,
		`SELECT id, name, created_by, created_at FROM teams WHERE id = ?`, team.ID,
	).Scan(&team.ID, &team.Name, &team.CreatedBy, &team.CreatedAt); err != nil {
		return team, mapError(err)
	}
	team.Role = domain.RoleOwner
	if err = tx.Commit(); err != nil {
		return domain.Team{}, fmt.Errorf("commit create team: %w", err)
	}
	return team, nil
}

func (s *Store) UserTeams(ctx context.Context, userID int64) ([]domain.Team, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.created_by, tm.role, t.created_at
		FROM teams t
		JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.user_id = ?
		ORDER BY t.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()
	teams := make([]domain.Team, 0)
	for rows.Next() {
		var team domain.Team
		if err := rows.Scan(&team.ID, &team.Name, &team.CreatedBy, &team.Role, &team.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		teams = append(teams, team)
	}
	return teams, rows.Err()
}

func (s *Store) Membership(ctx context.Context, teamID, userID int64) (domain.Membership, error) {
	var member domain.Membership
	err := s.db.QueryRowContext(ctx,
		`SELECT team_id, user_id, role FROM team_members WHERE team_id = ? AND user_id = ?`, teamID, userID,
	).Scan(&member.TeamID, &member.UserID, &member.Role)
	if err != nil {
		return domain.Membership{}, mapError(err)
	}
	return member, nil
}

func (s *Store) AddMember(ctx context.Context, teamID, userID int64, role domain.Role) (domain.Membership, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, ?)`, teamID, userID, role)
	if err != nil {
		return domain.Membership{}, mapError(err)
	}
	return domain.Membership{TeamID: teamID, UserID: userID, Role: role}, nil
}

func (s *Store) UpdateMemberRole(ctx context.Context, teamID, userID int64, role domain.Role) (domain.Membership, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE team_members SET role = ?
		WHERE team_id = ? AND user_id = ? AND role <> 'owner'`, role, teamID, userID)
	if err != nil {
		return domain.Membership{}, mapError(err)
	}
	membership, err := s.Membership(ctx, teamID, userID)
	if err != nil {
		return domain.Membership{}, err
	}
	if membership.Role == domain.RoleOwner {
		return domain.Membership{}, domain.ErrForbidden
	}
	return membership, nil
}

func (s *Store) CreateTask(ctx context.Context, task domain.Task) (created domain.Task, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return created, fmt.Errorf("begin create task: %w", err)
	}
	defer rollback(tx)

	result, err := tx.ExecContext(ctx, `
		INSERT INTO tasks (team_id, title, description, status, created_by, assignee_id, closed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		task.TeamID, task.Title, task.Description, task.Status, task.CreatedBy, task.AssigneeID, task.ClosedAt)
	if err != nil {
		return created, mapError(err)
	}
	created.ID, err = result.LastInsertId()
	if err != nil {
		return created, fmt.Errorf("task id: %w", err)
	}
	created, err = taskByID(ctx, tx, created.ID, "")
	if err != nil {
		return created, err
	}
	changes, _ := json.Marshal(map[string]any{"created": map[string]any{"new": created}})
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO task_history (task_id, changed_by, changes) VALUES (?, ?, ?)`, created.ID, task.CreatedBy, changes,
	); err != nil {
		return created, mapError(err)
	}
	if err = tx.Commit(); err != nil {
		return domain.Task{}, fmt.Errorf("commit create task: %w", err)
	}
	return created, nil
}

func (s *Store) TaskByID(ctx context.Context, id int64) (domain.Task, error) {
	return taskByID(ctx, s.db, id, "")
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func taskByID(ctx context.Context, q queryer, id int64, suffix string) (domain.Task, error) {
	var task domain.Task
	var assignee sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT id, team_id, title, description, status, created_by, assignee_id,
		       created_at, updated_at, closed_at, version
		FROM tasks WHERE id = ? `+suffix, id).Scan(
		&task.ID, &task.TeamID, &task.Title, &task.Description, &task.Status, &task.CreatedBy, &assignee,
		&task.CreatedAt, &task.UpdatedAt, &task.ClosedAt, &task.Version,
	)
	if err != nil {
		return domain.Task{}, mapError(err)
	}
	if assignee.Valid {
		task.AssigneeID = &assignee.Int64
	}
	return task, nil
}

func (s *Store) ListTasks(ctx context.Context, filter domain.TaskFilter) (domain.TaskList, error) {
	where := []string{"team_id = ?"}
	args := []any{filter.TeamID}
	if filter.Status != nil {
		where = append(where, "status = ?")
		args = append(args, *filter.Status)
	}
	if filter.AssigneeID != nil {
		where = append(where, "assignee_id = ?")
		args = append(args, *filter.AssigneeID)
	}
	predicate := strings.Join(where, " AND ")

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE `+predicate, args...).Scan(&total); err != nil {
		return domain.TaskList{}, fmt.Errorf("count tasks: %w", err)
	}
	queryArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, team_id, title, description, status, created_by, assignee_id,
		       created_at, updated_at, closed_at, version
		FROM tasks WHERE `+predicate+` ORDER BY id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return domain.TaskList{}, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Task, 0)
	for rows.Next() {
		var task domain.Task
		var assignee sql.NullInt64
		if err := rows.Scan(&task.ID, &task.TeamID, &task.Title, &task.Description, &task.Status,
			&task.CreatedBy, &assignee, &task.CreatedAt, &task.UpdatedAt, &task.ClosedAt, &task.Version); err != nil {
			return domain.TaskList{}, fmt.Errorf("scan task: %w", err)
		}
		if assignee.Valid {
			task.AssigneeID = &assignee.Int64
		}
		items = append(items, task)
	}
	if err := rows.Err(); err != nil {
		return domain.TaskList{}, fmt.Errorf("iterate tasks: %w", err)
	}
	return domain.TaskList{Items: items, Limit: filter.Limit, Offset: filter.Offset, Total: total}, nil
}

func (s *Store) UpdateTask(ctx context.Context, taskID, expectedVersion, actorID int64, mutate repository.TaskMutator) (updated domain.Task, err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return updated, fmt.Errorf("begin update task: %w", err)
	}
	defer rollback(tx)

	current, err := taskByID(ctx, tx, taskID, "FOR UPDATE")
	if err != nil {
		return updated, err
	}
	if current.Version != expectedVersion {
		return updated, domain.ErrConflict
	}
	updated, changes, err := mutate(current)
	if err != nil {
		return domain.Task{}, err
	}
	if len(changes) == 0 {
		return current, nil
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE tasks SET title = ?, description = ?, status = ?, assignee_id = ?, closed_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		updated.Title, updated.Description, updated.Status, updated.AssigneeID, updated.ClosedAt, taskID, expectedVersion)
	if err != nil {
		return domain.Task{}, mapError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.Task{}, fmt.Errorf("updated rows: %w", err)
	}
	if affected != 1 {
		return domain.Task{}, domain.ErrConflict
	}
	payload, err := json.Marshal(changes)
	if err != nil {
		return domain.Task{}, fmt.Errorf("marshal history: %w", err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO task_history (task_id, changed_by, changes) VALUES (?, ?, ?)`, taskID, actorID, payload,
	); err != nil {
		return domain.Task{}, mapError(err)
	}
	updated, err = taskByID(ctx, tx, taskID, "")
	if err != nil {
		return domain.Task{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Task{}, fmt.Errorf("commit update task: %w", err)
	}
	return updated, nil
}

func (s *Store) TaskHistory(ctx context.Context, taskID int64) ([]domain.TaskHistory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, changed_by, changes, created_at
		FROM task_history WHERE task_id = ? ORDER BY created_at, id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("task history: %w", err)
	}
	defer rows.Close()
	items := make([]domain.TaskHistory, 0)
	for rows.Next() {
		var item domain.TaskHistory
		if err := rows.Scan(&item.ID, &item.TaskID, &item.ChangedBy, &item.Changes, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AddComment(ctx context.Context, comment domain.Comment) (domain.Comment, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO task_comments (task_id, user_id, content) VALUES (?, ?, ?)`, comment.TaskID, comment.UserID, comment.Content)
	if err != nil {
		return domain.Comment{}, mapError(err)
	}
	comment.ID, err = result.LastInsertId()
	if err != nil {
		return domain.Comment{}, fmt.Errorf("comment id: %w", err)
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT c.id, c.task_id, c.user_id, u.name, c.content, c.created_at
		FROM task_comments c JOIN users u ON u.id = c.user_id WHERE c.id = ?`, comment.ID,
	).Scan(&comment.ID, &comment.TaskID, &comment.UserID, &comment.UserName, &comment.Content, &comment.CreatedAt)
	if err != nil {
		return domain.Comment{}, mapError(err)
	}
	return comment, nil
}

func (s *Store) TaskComments(ctx context.Context, taskID int64) ([]domain.Comment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.task_id, c.user_id, u.name, c.content, c.created_at
		FROM task_comments c JOIN users u ON u.id = c.user_id
		WHERE c.task_id = ? ORDER BY c.created_at, c.id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("task comments: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Comment, 0)
	for rows.Next() {
		var item domain.Comment
		if err := rows.Scan(&item.ID, &item.TaskID, &item.UserID, &item.UserName, &item.Content, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan comment: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) TeamStats(ctx context.Context, teamID int64) (domain.TeamStats, error) {
	const query = `
	WITH status_totals AS (
	    SELECT status, COUNT(*) AS total FROM tasks WHERE team_id = ? GROUP BY status
	), top_three AS (
	    SELECT t.assignee_id AS user_id, u.name, COUNT(*) AS closed_tasks
	    FROM tasks t JOIN users u ON u.id = t.assignee_id
	    WHERE t.team_id = ? AND t.status = 'done' AND t.closed_at >= UTC_TIMESTAMP() - INTERVAL 30 DAY
	    GROUP BY t.assignee_id, u.name ORDER BY closed_tasks DESC, t.assignee_id LIMIT 3
	)
	SELECT
	    COALESCE((SELECT JSON_OBJECTAGG(status, total) FROM status_totals), JSON_OBJECT()),
	    COALESCE((SELECT JSON_ARRAYAGG(JSON_OBJECT('user_id', user_id, 'name', name, 'closed_tasks', closed_tasks)) FROM top_three), JSON_ARRAY()),
	    (SELECT AVG(TIMESTAMPDIFF(SECOND, created_at, closed_at)) FROM tasks WHERE team_id = ? AND closed_at IS NOT NULL),
	    (SELECT COUNT(*) FROM task_comments c JOIN tasks t ON t.id = c.task_id WHERE t.team_id = ?)`
	var statusJSON, topJSON []byte
	var average sql.NullFloat64
	var stats domain.TeamStats
	if err := s.db.QueryRowContext(ctx, query, teamID, teamID, teamID, teamID).Scan(
		&statusJSON, &topJSON, &average, &stats.CommentsCount,
	); err != nil {
		return domain.TeamStats{}, fmt.Errorf("team stats: %w", err)
	}
	var rawStatuses map[string]int64
	if err := json.Unmarshal(statusJSON, &rawStatuses); err != nil {
		return domain.TeamStats{}, fmt.Errorf("decode statuses: %w", err)
	}
	stats.TasksByStatus = make(map[domain.TaskStatus]int64, len(rawStatuses))
	for key, value := range rawStatuses {
		stats.TasksByStatus[domain.TaskStatus(key)] = value
	}
	if err := json.Unmarshal(topJSON, &stats.TopAssignees); err != nil {
		return domain.TeamStats{}, fmt.Errorf("decode top assignees: %w", err)
	}
	if average.Valid {
		stats.AverageCloseSeconds = &average.Float64
	}
	return stats, nil
}

func mapError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	var mysqlErr *mysqldriver.MySQLError
	if errors.As(err, &mysqlErr) {
		switch mysqlErr.Number {
		case 1062:
			return domain.ErrConflict
		case 1451, 1452:
			return domain.ErrInvalid
		}
	}
	return err
}

func rollback(tx *sql.Tx) { _ = tx.Rollback() }
