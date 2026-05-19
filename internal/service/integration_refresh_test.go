package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/integration/sellico"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/crypto"
	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

type integrationRefreshDB struct {
	workspaceID uuid.UUID
	upserts     []sqlcgen.UpsertSellicoSellerCabinetParams
}

func (db *integrationRefreshDB) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag(""), nil
}

func (db *integrationRefreshDB) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (db *integrationRefreshDB) Query(_ context.Context, sql string, _ ...interface{}) (pgx.Rows, error) {
	if containsSQL(sql, "FROM workspaces") && containsSQL(sql, "external_workspace_id") {
		return &integrationRefreshWorkspaceRows{items: []sqlcgen.WorkspaceExternalID{{
			WorkspaceID:         uuidToPgtype(db.workspaceID),
			ExternalWorkspaceID: pgtype.Text{String: "42", Valid: true},
		}}}, nil
	}
	return &fakeRows{}, nil
}

func (db *integrationRefreshDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	if containsSQL(sql, "INSERT INTO seller_cabinets") {
		arg := sqlcgen.UpsertSellicoSellerCabinetParams{
			WorkspaceID:           args[0].(pgtype.UUID),
			Name:                  args[1].(string),
			EncryptedToken:        args[2].(string),
			Status:                args[3].(string),
			ExternalIntegrationID: args[4].(pgtype.Text),
			IntegrationType:       args[5].(pgtype.Text),
		}
		db.upserts = append(db.upserts, arg)
		return sellerCabinetToRow(sqlcgen.SellerCabinet{
			ID:                    uuidToPgtype(uuid.New()),
			WorkspaceID:           arg.WorkspaceID,
			Name:                  arg.Name,
			EncryptedToken:        arg.EncryptedToken,
			Status:                arg.Status,
			CreatedAt:             pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt:             pgtype.Timestamptz{Time: time.Now(), Valid: true},
			ExternalIntegrationID: arg.ExternalIntegrationID,
			Source:                "sellico",
			IntegrationType:       arg.IntegrationType,
			LastSellicoSyncAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
	}
	return &fakeRow{scanFunc: func(_ ...any) error { return pgx.ErrNoRows }}
}

type integrationRefreshWorkspaceRows struct {
	items []sqlcgen.WorkspaceExternalID
	idx   int
}

func (r *integrationRefreshWorkspaceRows) Close()     {}
func (r *integrationRefreshWorkspaceRows) Err() error { return nil }
func (r *integrationRefreshWorkspaceRows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag("")
}
func (r *integrationRefreshWorkspaceRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *integrationRefreshWorkspaceRows) Values() ([]any, error)                       { return nil, nil }
func (r *integrationRefreshWorkspaceRows) RawValues() [][]byte                          { return nil }
func (r *integrationRefreshWorkspaceRows) Conn() *pgx.Conn                              { return nil }

func (r *integrationRefreshWorkspaceRows) Next() bool {
	if r.idx >= len(r.items) {
		return false
	}
	r.idx++
	return true
}

func (r *integrationRefreshWorkspaceRows) Scan(dest ...any) error {
	item := r.items[r.idx-1]
	*dest[0].(*pgtype.UUID) = item.WorkspaceID
	*dest[1].(*pgtype.Text) = item.ExternalWorkspaceID
	return nil
}

func TestRefreshViaServiceAccountFetchesFullWBIntegrationBeforeUpsert(t *testing.T) {
	var detailCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer svc-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/collector/integrations":
			_, _ = w.Write([]byte(`[
				{"id":25,"name":"WB Store","marketplace":"WildBerries"},
				{"id":13,"name":"Ozon Store","marketplace":"OZON"}
			]`))
		case "/get-integration/25":
			detailCalls++
			_, _ = w.Write([]byte(`{"id":25,"work_space_id":42,"name":"WB Store","type":"WildBerries","api_key":"wb-secret-key","status":"active"}`))
		default:
			http.Error(w, fmt.Sprintf("unexpected path %s", r.URL.Path), http.StatusNotFound)
		}
	}))
	defer server.Close()

	db := &integrationRefreshDB{workspaceID: uuid.New()}
	encryptionKey := []byte("12345678901234567890123456789012")
	client := sellico.NewClient(server.URL, time.Second)
	tokenManager := sellico.NewServiceTokenManager(client, sellico.ServiceTokenConfig{StaticToken: "svc-token"})
	svc := NewIntegrationRefreshService(sqlcgen.New(db), client, nil, encryptionKey, zerolog.Nop()).WithServiceAccount(tokenManager)

	require.NoError(t, svc.RefreshViaServiceAccount(context.Background()))
	require.Equal(t, 1, detailCalls)
	require.Len(t, db.upserts, 1)
	require.Equal(t, db.workspaceID, uuidFromPgtype(db.upserts[0].WorkspaceID))
	require.Equal(t, "25", db.upserts[0].ExternalIntegrationID.String)
	require.Equal(t, "active", db.upserts[0].Status)
	require.NotEqual(t, "wb-secret-key", db.upserts[0].EncryptedToken)

	decrypted, err := crypto.Decrypt(db.upserts[0].EncryptedToken, encryptionKey)
	require.NoError(t, err)
	require.Equal(t, "wb-secret-key", decrypted)
}
