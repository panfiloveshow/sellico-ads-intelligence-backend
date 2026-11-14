package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/stretchr/testify/require"
)

func TestManagerPriceRoutesRequireFreshPersonalWritePermissionsAndExactJSON(t *testing.T) {
	auth := managerRouterAuth{uuid.New(), uuid.New()}
	verifier := &managerRouterVerifier{permissions: []string{"marketing.view", "integrations.view"}}
	router := NewRouter(RouterDeps{Authenticator: auth, WorkspaceResolver: auth, ManagerAccessVerifier: verifier, ManagerPriceHandler: handler.NewManagerPriceHandler(nil)})
	call := func(method, path, body string, authenticate bool) int {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Sellico-Workspace", "3")
		if authenticate {
			r.Header.Set("Authorization", "Bearer personal-token")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	path := "/api/v1/manager/prices/operations"
	require.Equal(t, 401, call(http.MethodPost, path, "{}", false))
	require.Equal(t, 403, call(http.MethodPost, path, "{}", true))
	verifier.permissions = []string{"products.view", "marketing.view", "integrations.view", "unit_economics.view", "unit_economics.calculate", "marketing.manage"}
	require.Equal(t, 400, call(http.MethodPost, path, `{"unexpected":true}`, true))
	require.Equal(t, 400, call(http.MethodPost, path, `{} {}`, true))
	require.Equal(t, 400, call(http.MethodPost, path+"?force=true", `{}`, true))
	require.Equal(t, 503, call(http.MethodPost, path, `{}`, true))
	require.Equal(t, 400, call(http.MethodGet, path+"/invalid", "", true))
	verifier.permissions = []string{"marketing.view", "integrations.view"}
	require.Equal(t, 403, call(http.MethodGet, path+"/"+uuid.NewString(), "", true))
}
