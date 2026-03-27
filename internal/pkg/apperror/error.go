package apperror

import "errors"

// AppError represents a typed application error.
type AppError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *AppError) Error() string {
	return e.Message
}

// Predefined errors.
var (
	ErrNotFound       = &AppError{Code: "NOT_FOUND", Message: "resource not found", Status: 404}
	ErrUnauthorized   = &AppError{Code: "UNAUTHORIZED", Message: "authentication required", Status: 401}
	ErrForbidden      = &AppError{Code: "FORBIDDEN", Message: "insufficient permissions", Status: 403}
	ErrValidation     = &AppError{Code: "VALIDATION_ERROR", Message: "invalid input", Status: 400}
	ErrConflict       = &AppError{Code: "CONFLICT", Message: "resource already exists", Status: 409}
	ErrInternal       = &AppError{Code: "INTERNAL_ERROR", Message: "internal server error", Status: 500}
	ErrWBAPIError     = &AppError{Code: "WB_API_ERROR", Message: "wildberries api error", Status: 502}
	ErrDecryptionFail = &AppError{Code: "DECRYPTION_ERROR", Message: "failed to process credentials", Status: 500}
)

// New creates a new AppError with a custom message, inheriting Code and Status from base.
func New(base *AppError, message string) *AppError {
	return &AppError{
		Code:    base.Code,
		Message: message,
		Status:  base.Status,
	}
}

// Is checks if an error is a specific AppError type (by Code).
func Is(err error, target *AppError) bool {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code == target.Code
	}
	return false
}
