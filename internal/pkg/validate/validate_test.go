package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequired_Empty(t *testing.T) {
	v := New()
	v.Required("name", "")
	assert.False(t, v.Valid())
	assert.Equal(t, "is required", v.FieldErrors()["name"])
}

func TestRequired_Whitespace(t *testing.T) {
	v := New()
	v.Required("name", "   ")
	assert.False(t, v.Valid())
}

func TestRequired_Valid(t *testing.T) {
	v := New()
	v.Required("name", "Alice")
	assert.True(t, v.Valid())
}

func TestEmail_Valid(t *testing.T) {
	v := New()
	v.Email("email", "user@example.com")
	assert.True(t, v.Valid())
}

func TestEmail_Invalid(t *testing.T) {
	cases := []string{"noatsign", "@missing.local", "a@", "a@b", "a@.com", "a@b."}
	for _, c := range cases {
		v := New()
		v.Email("email", c)
		assert.False(t, v.Valid(), "expected invalid for: %s", c)
	}
}

func TestEmail_Empty_Skipped(t *testing.T) {
	v := New()
	v.Email("email", "")
	assert.True(t, v.Valid())
}

func TestMinLength(t *testing.T) {
	v := New()
	v.MinLength("password", "ab", 8)
	assert.False(t, v.Valid())
	assert.Contains(t, v.FieldErrors()["password"], "at least 8")
}

func TestMinLength_Exact(t *testing.T) {
	v := New()
	v.MinLength("password", "12345678", 8)
	assert.True(t, v.Valid())
}

func TestMaxLength(t *testing.T) {
	v := New()
	v.MaxLength("name", "a very long name that exceeds limit", 10)
	assert.False(t, v.Valid())
	assert.Contains(t, v.FieldErrors()["name"], "at most 10")
}

func TestMaxLength_Exact(t *testing.T) {
	v := New()
	v.MaxLength("name", "1234567890", 10)
	assert.True(t, v.Valid())
}

func TestPositive_Zero(t *testing.T) {
	v := New()
	v.Positive("count", 0)
	assert.False(t, v.Valid())
	assert.Equal(t, "must be positive", v.FieldErrors()["count"])
}

func TestPositive_Negative(t *testing.T) {
	v := New()
	v.Positive("count", -5)
	assert.False(t, v.Valid())
}

func TestPositive_Valid(t *testing.T) {
	v := New()
	v.Positive("count", 1)
	assert.True(t, v.Valid())
}

func TestOneOf_Valid(t *testing.T) {
	v := New()
	v.OneOf("role", "admin", []string{"admin", "user", "viewer"})
	assert.True(t, v.Valid())
}

func TestOneOf_Invalid(t *testing.T) {
	v := New()
	v.OneOf("role", "superadmin", []string{"admin", "user", "viewer"})
	assert.False(t, v.Valid())
	assert.Contains(t, v.FieldErrors()["role"], "must be one of")
}

func TestMultipleErrors(t *testing.T) {
	v := New()
	v.Required("email", "")
	v.Required("name", "")
	v.Positive("age", -1)
	assert.False(t, v.Valid())
	assert.Len(t, v.FieldErrors(), 3)
}

func TestFirstErrorWins(t *testing.T) {
	v := New()
	v.Required("email", "")
	v.Email("email", "")
	// Required error should be kept, Email skips empty.
	errs := v.FieldErrors()
	assert.Equal(t, "is required", errs["email"])
}
