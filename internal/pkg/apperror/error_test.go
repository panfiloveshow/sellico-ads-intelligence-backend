package apperror

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppError_Error(t *testing.T) {
	assert.Equal(t, "resource not found", ErrNotFound.Error())
	assert.Equal(t, "authentication required", ErrUnauthorized.Error())
	assert.Equal(t, "internal server error", ErrInternal.Error())
}

func TestAppError_ImplementsErrorInterface(t *testing.T) {
	var err error = ErrNotFound
	assert.NotNil(t, err)
	assert.Equal(t, "resource not found", err.Error())
}

func TestNew(t *testing.T) {
	custom := New(ErrNotFound, "user not found")
	assert.Equal(t, "NOT_FOUND", custom.Code)
	assert.Equal(t, "user not found", custom.Message)
	assert.Equal(t, 404, custom.Status)

	// Original should be unchanged.
	assert.Equal(t, "resource not found", ErrNotFound.Message)
}

func TestNew_PreservesCodeAndStatus(t *testing.T) {
	custom := New(ErrWBAPIError, "timeout calling WB")
	assert.Equal(t, "WB_API_ERROR", custom.Code)
	assert.Equal(t, 502, custom.Status)
	assert.Equal(t, "timeout calling WB", custom.Message)
}

func TestIs_MatchesByCode(t *testing.T) {
	err := New(ErrNotFound, "workspace not found")
	assert.True(t, Is(err, ErrNotFound))
	assert.False(t, Is(err, ErrUnauthorized))
}

func TestIs_WrappedError(t *testing.T) {
	inner := New(ErrValidation, "bad email")
	wrapped := fmt.Errorf("handler: %w", inner)
	assert.True(t, Is(wrapped, ErrValidation))
	assert.False(t, Is(wrapped, ErrNotFound))
}

func TestIs_NonAppError(t *testing.T) {
	err := errors.New("some random error")
	assert.False(t, Is(err, ErrInternal))
}

func TestIs_NilError(t *testing.T) {
	assert.False(t, Is(nil, ErrNotFound))
}

func TestPredefinedErrors(t *testing.T) {
	tests := []struct {
		err    *AppError
		code   string
		status int
	}{
		{ErrNotFound, "NOT_FOUND", 404},
		{ErrUnauthorized, "UNAUTHORIZED", 401},
		{ErrForbidden, "FORBIDDEN", 403},
		{ErrValidation, "VALIDATION_ERROR", 400},
		{ErrConflict, "CONFLICT", 409},
		{ErrInternal, "INTERNAL_ERROR", 500},
		{ErrWBAPIError, "WB_API_ERROR", 502},
		{ErrDecryptionFail, "DECRYPTION_ERROR", 500},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			assert.Equal(t, tt.code, tt.err.Code)
			assert.Equal(t, tt.status, tt.err.Status)
			assert.NotEmpty(t, tt.err.Message)
		})
	}
}
