package domain

import (
	"encoding/json"
	"time"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

func (r Role) ValidInviteRole() bool { return r == RoleAdmin || r == RoleMember }
func (r Role) CanManage() bool       { return r == RoleOwner || r == RoleAdmin }

type TaskStatus string

const (
	StatusTodo       TaskStatus = "todo"
	StatusInProgress TaskStatus = "in_progress"
	StatusDone       TaskStatus = "done"
)

func (s TaskStatus) Valid() bool {
	return s == StatusTodo || s == StatusInProgress || s == StatusDone
}

type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type UserWithPassword struct {
	User
	PasswordHash string
}

type Team struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedBy int64     `json:"created_by"`
	Role      Role      `json:"role,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Membership struct {
	TeamID int64 `json:"team_id"`
	UserID int64 `json:"user_id"`
	Role   Role  `json:"role"`
}

type Task struct {
	ID          int64      `json:"id"`
	TeamID      int64      `json:"team_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	CreatedBy   int64      `json:"created_by"`
	AssigneeID  *int64     `json:"assignee_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	Version     int64      `json:"version"`
}

type TaskHistory struct {
	ID        int64           `json:"id"`
	TaskID    int64           `json:"task_id"`
	ChangedBy int64           `json:"changed_by"`
	Changes   json.RawMessage `json:"changes"`
	CreatedAt time.Time       `json:"created_at"`
}

type Comment struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	UserID    int64     `json:"user_id"`
	UserName  string    `json:"user_name,omitempty"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type TaskFilter struct {
	TeamID     int64
	Status     *TaskStatus
	AssigneeID *int64
	Limit      int
	Offset     int
}

type TaskList struct {
	Items  []Task `json:"items"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Total  int64  `json:"total"`
}

type TopAssignee struct {
	UserID      int64  `json:"user_id"`
	Name        string `json:"name"`
	ClosedTasks int64  `json:"closed_tasks"`
}

type TeamStats struct {
	TasksByStatus       map[TaskStatus]int64 `json:"tasks_by_status"`
	TopAssignees        []TopAssignee        `json:"top_assignees"`
	AverageCloseSeconds *float64             `json:"average_close_seconds"`
	CommentsCount       int64                `json:"comments_count"`
}
