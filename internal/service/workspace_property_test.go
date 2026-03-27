package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
	"pgregory.net/rapid"
)

// Feature: sellico-ads-intelligence-backend, Property 4: Workspace — создатель получает роль owner
// Проверяет: Требования 2.1, 2.3

// --- In-memory workspace DB mock ---

type wsInMemDB struct {
	mu         sync.Mutex
	workspaces map[string]sqlcgen.Workspace // keyed by slug
	members    []sqlcgen.WorkspaceMember    // all members
	auditLogs  []sqlcgen.AuditLog           // audit log entries
}

func newWSInMemDB() *wsInMemDB {
	return &wsInMemDB{
		workspaces: make(map[string]sqlcgen.Workspace),
	}
}

func (db *wsInMemDB) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (db *wsInMemDB) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (db *wsInMemDB) Query(_ context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case containsSQL(sql, "FROM workspaces w") && containsSQL(sql, "JOIN workspace_members"):
		// ListWorkspacesByUserID: args = user_id, limit, offset
		userID := args[0].(pgtype.UUID)
		var matched []sqlcgen.Workspace
		for _, m := range db.members {
			if m.UserID == userID {
				for _, ws := range db.workspaces {
					if ws.ID == m.WorkspaceID && !ws.DeletedAt.Valid {
						matched = append(matched, ws)
					}
				}
			}
		}
		return &wsRows{items: matched}, nil

	case containsSQL(sql, "FROM workspace_members") && containsSQL(sql, "ORDER BY"):
		// ListWorkspaceMembers: args = workspace_id, limit, offset
		wsID := args[0].(pgtype.UUID)
		var matched []sqlcgen.WorkspaceMember
		for _, m := range db.members {
			if m.WorkspaceID == wsID {
				matched = append(matched, m)
			}
		}
		return &wsMemberRows{items: matched}, nil
	}

	return &fakeRows{}, nil
}

func (db *wsInMemDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case containsSQL(sql, "FROM workspaces") && containsSQL(sql, "slug"):
		// GetWorkspaceBySlug
		slug := args[0].(string)
		ws, ok := db.workspaces[slug]
		if !ok || ws.DeletedAt.Valid {
			return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
		}
		return wsToRow(ws)

	case containsSQL(sql, "INSERT INTO workspaces"):
		// CreateWorkspace: args = name, slug
		name := args[0].(string)
		slug := args[1].(string)
		id := uuid.New()
		now := time.Now()
		ws := sqlcgen.Workspace{
			ID:        pgtype.UUID{Bytes: id, Valid: true},
			Name:      name,
			Slug:      slug,
			CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}
		db.workspaces[slug] = ws
		return wsToRow(ws)

	case containsSQL(sql, "INSERT INTO workspace_members"):
		// CreateWorkspaceMember: args = workspace_id, user_id, role
		wsID := args[0].(pgtype.UUID)
		userID := args[1].(pgtype.UUID)
		role := args[2].(string)
		id := uuid.New()
		now := time.Now()
		m := sqlcgen.WorkspaceMember{
			ID:          pgtype.UUID{Bytes: id, Valid: true},
			WorkspaceID: wsID,
			UserID:      userID,
			Role:        role,
			CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
			UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		}
		db.members = append(db.members, m)
		return wsMemberToRow(m)

	case containsSQL(sql, "FROM workspace_members") && containsSQL(sql, "workspace_id") && containsSQL(sql, "user_id"):
		// GetWorkspaceMember: args = workspace_id, user_id
		wsID := args[0].(pgtype.UUID)
		userID := args[1].(pgtype.UUID)
		for _, m := range db.members {
			if m.WorkspaceID == wsID && m.UserID == userID {
				return wsMemberToRow(m)
			}
		}
		return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}

	case containsSQL(sql, "FROM workspaces") && containsSQL(sql, "WHERE id"):
		// GetWorkspaceByID
		wsID := args[0].(pgtype.UUID)
		for _, ws := range db.workspaces {
			if ws.ID == wsID && !ws.DeletedAt.Valid {
				return wsToRow(ws)
			}
		}
		return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}

	case containsSQL(sql, "INSERT INTO audit_logs"):
		// CreateAuditLog: args = workspace_id, user_id, action, entity_type, entity_id, metadata
		id := uuid.New()
		now := time.Now()
		al := sqlcgen.AuditLog{
			ID:        pgtype.UUID{Bytes: id, Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}
		db.auditLogs = append(db.auditLogs, al)
		return &fakeRow{scanFunc: func(dest ...any) error {
			*dest[0].(*pgtype.UUID) = al.ID
			*dest[1].(*pgtype.UUID) = args[0].(pgtype.UUID)
			*dest[2].(*pgtype.UUID) = args[1].(pgtype.UUID)
			*dest[3].(*string) = args[2].(string)
			*dest[4].(*string) = args[3].(string)
			*dest[5].(*pgtype.UUID) = args[4].(pgtype.UUID)
			*dest[6].(*[]byte) = args[5].([]byte)
			*dest[7].(*pgtype.Timestamptz) = al.CreatedAt
			return nil
		}}
	}

	return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
}

// --- Row helpers ---

func wsToRow(ws sqlcgen.Workspace) pgx.Row {
	return &fakeRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = ws.ID
		*dest[1].(*string) = ws.Name
		*dest[2].(*string) = ws.Slug
		*dest[3].(*pgtype.Timestamptz) = ws.CreatedAt
		*dest[4].(*pgtype.Timestamptz) = ws.UpdatedAt
		*dest[5].(*pgtype.Timestamptz) = ws.DeletedAt
		return nil
	}}
}

func wsMemberToRow(m sqlcgen.WorkspaceMember) pgx.Row {
	return &fakeRow{scanFunc: func(dest ...any) error {
		*dest[0].(*pgtype.UUID) = m.ID
		*dest[1].(*pgtype.UUID) = m.WorkspaceID
		*dest[2].(*pgtype.UUID) = m.UserID
		*dest[3].(*string) = m.Role
		*dest[4].(*pgtype.Timestamptz) = m.CreatedAt
		*dest[5].(*pgtype.Timestamptz) = m.UpdatedAt
		return nil
	}}
}

// wsRows implements pgx.Rows for workspace list queries.
type wsRows struct {
	items []sqlcgen.Workspace
	idx   int
}

func (r *wsRows) Close()                                       {}
func (r *wsRows) Err() error                                   { return nil }
func (r *wsRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *wsRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *wsRows) RawValues() [][]byte                          { return nil }
func (r *wsRows) Conn() *pgx.Conn                              { return nil }
func (r *wsRows) Values() ([]any, error)                       { return nil, nil }

func (r *wsRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}

func (r *wsRows) Scan(dest ...any) error {
	ws := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = ws.ID
	*dest[1].(*string) = ws.Name
	*dest[2].(*string) = ws.Slug
	*dest[3].(*pgtype.Timestamptz) = ws.CreatedAt
	*dest[4].(*pgtype.Timestamptz) = ws.UpdatedAt
	*dest[5].(*pgtype.Timestamptz) = ws.DeletedAt
	return nil
}

// wsMemberRows implements pgx.Rows for workspace member list queries.
type wsMemberRows struct {
	items []sqlcgen.WorkspaceMember
	idx   int
}

func (r *wsMemberRows) Close()                                       {}
func (r *wsMemberRows) Err() error                                   { return nil }
func (r *wsMemberRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *wsMemberRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *wsMemberRows) RawValues() [][]byte                          { return nil }
func (r *wsMemberRows) Conn() *pgx.Conn                              { return nil }
func (r *wsMemberRows) Values() ([]any, error)                       { return nil, nil }

func (r *wsMemberRows) Next() bool {
	if r.idx < len(r.items) {
		r.idx++
		return true
	}
	return false
}

func (r *wsMemberRows) Scan(dest ...any) error {
	m := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = m.ID
	*dest[1].(*pgtype.UUID) = m.WorkspaceID
	*dest[2].(*pgtype.UUID) = m.UserID
	*dest[3].(*string) = m.Role
	*dest[4].(*pgtype.Timestamptz) = m.CreatedAt
	*dest[5].(*pgtype.Timestamptz) = m.UpdatedAt
	return nil
}

// --- Generators ---

// genSlug generates a valid workspace slug (lowercase alphanumeric with hyphens).
func genSlug() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-z][a-z0-9\-]{2,20}[a-z0-9]`)
}

// genWorkspaceName generates a workspace display name.
func genWorkspaceName() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z][A-Za-z0-9 ]{2,30}`)
}

func newTestWorkspaceService(db *wsInMemDB) *WorkspaceService {
	queries := sqlcgen.New(db)
	return NewWorkspaceService(queries)
}

// --- Property Tests ---

// TestProperty_Workspace_CreatorGetsOwnerRole verifies Requirement 2.1:
// For any valid workspace name and slug, when a user creates a workspace,
// the creator MUST be assigned the "owner" role as a workspace member.
func TestProperty_Workspace_CreatorGetsOwnerRole(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := genWorkspaceName().Draw(t, "name")
		slug := genSlug().Draw(t, "slug")
		userID := uuid.New()

		db := newWSInMemDB()
		svc := newTestWorkspaceService(db)

		ws, err := svc.Create(context.Background(), userID, name, slug)
		if err != nil {
			t.Fatalf("Create(%q, %q) failed: %v", name, slug, err)
		}

		// Workspace must be returned with correct name and slug.
		if ws.Name != name {
			t.Fatalf("workspace name mismatch: got %q, want %q", ws.Name, name)
		}
		if ws.Slug != slug {
			t.Fatalf("workspace slug mismatch: got %q, want %q", ws.Slug, slug)
		}
		if ws.ID == uuid.Nil {
			t.Fatal("workspace ID must not be nil")
		}

		// Verify the creator was assigned the owner role.
		db.mu.Lock()
		defer db.mu.Unlock()

		var found bool
		for _, m := range db.members {
			mUserID := uuidFromPgtype(m.UserID)
			mWsID := uuidFromPgtype(m.WorkspaceID)
			if mUserID == userID && mWsID == ws.ID {
				found = true
				if m.Role != domain.RoleOwner {
					t.Fatalf("creator role must be %q, got %q", domain.RoleOwner, m.Role)
				}
				break
			}
		}
		if !found {
			t.Fatal("creator must be added as a workspace member")
		}
	})
}

// TestProperty_Workspace_CreatorOnlyMember verifies Requirement 2.1:
// After creating a workspace, there MUST be exactly one member (the creator)
// and that member MUST have the "owner" role.
func TestProperty_Workspace_CreatorOnlyMember(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := genWorkspaceName().Draw(t, "name")
		slug := genSlug().Draw(t, "slug")
		userID := uuid.New()

		db := newWSInMemDB()
		svc := newTestWorkspaceService(db)

		ws, err := svc.Create(context.Background(), userID, name, slug)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		db.mu.Lock()
		defer db.mu.Unlock()

		// Count members for this workspace.
		wsUUID := uuidToPgtype(ws.ID)
		var count int
		for _, m := range db.members {
			if m.WorkspaceID == wsUUID {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected exactly 1 member after creation, got %d", count)
		}
	})
}

// TestProperty_Workspace_ListReturnsOnlyMemberWorkspaces verifies Requirement 2.3:
// When a user lists workspaces, the result MUST contain only workspaces
// where the user is a member. Workspaces created by other users MUST NOT appear.
func TestProperty_Workspace_ListReturnsOnlyMemberWorkspaces(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		user1 := uuid.New()
		user2 := uuid.New()

		db := newWSInMemDB()
		svc := newTestWorkspaceService(db)
		ctx := context.Background()

		// User1 creates a workspace.
		slug1 := genSlug().Draw(t, "slug1")
		name1 := genWorkspaceName().Draw(t, "name1")
		ws1, err := svc.Create(ctx, user1, name1, slug1)
		if err != nil {
			t.Fatalf("Create ws1 failed: %v", err)
		}

		// User2 creates a different workspace.
		slug2 := slug1 + "x" // ensure unique slug
		name2 := genWorkspaceName().Draw(t, "name2")
		_, err = svc.Create(ctx, user2, name2, slug2)
		if err != nil {
			t.Fatalf("Create ws2 failed: %v", err)
		}

		// User1 should only see their own workspace.
		list, err := svc.List(ctx, user1, 100, 0)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}

		for _, ws := range list {
			if ws.ID != ws1.ID {
				t.Fatalf("user1 should only see ws1 (id=%s), but got ws with id=%s", ws1.ID, ws.ID)
			}
		}
		if len(list) != 1 {
			t.Fatalf("expected 1 workspace for user1, got %d", len(list))
		}
	})
}

// TestProperty_Workspace_DuplicateSlugRejected verifies Requirement 2.1:
// Creating two workspaces with the same slug MUST fail on the second attempt.
func TestProperty_Workspace_DuplicateSlugRejected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name1 := genWorkspaceName().Draw(t, "name1")
		name2 := genWorkspaceName().Draw(t, "name2")
		slug := genSlug().Draw(t, "slug")
		user1 := uuid.New()
		user2 := uuid.New()

		db := newWSInMemDB()
		svc := newTestWorkspaceService(db)
		ctx := context.Background()

		_, err := svc.Create(ctx, user1, name1, slug)
		if err != nil {
			t.Fatalf("first Create failed: %v", err)
		}

		_, err = svc.Create(ctx, user2, name2, slug)
		if err == nil {
			t.Fatal("second Create with same slug must fail")
		}
	})
}
