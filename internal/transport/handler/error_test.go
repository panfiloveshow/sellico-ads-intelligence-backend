package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/pkg/apperror"
)

func TestWriteAppErrorUnwrapsApplicationError(t *testing.T) {
	recorder := httptest.NewRecorder()

	writeAppError(recorder, fmt.Errorf("cabinet upload: %w", apperror.New(apperror.ErrRateLimited, "WB price upload is cooling down")))

	assert.Equal(t, http.StatusTooManyRequests, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "WB price upload is cooling down")
}
