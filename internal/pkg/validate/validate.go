package validate

import (
	"strings"
)

// Validator collects validation errors for request fields.
type Validator struct {
	Errors map[string]string
}

// New creates a new Validator.
func New() *Validator {
	return &Validator{
		Errors: make(map[string]string),
	}
}

// Required checks that a string field is not empty.
func (v *Validator) Required(field, value string) {
	if strings.TrimSpace(value) == "" {
		v.addError(field, "is required")
	}
}

// Email checks that a string is a valid email format (simple check).
func (v *Validator) Email(field, value string) {
	if value == "" {
		return
	}
	at := strings.Index(value, "@")
	dot := strings.LastIndex(value, ".")
	if at < 1 || dot < at+2 || dot >= len(value)-1 {
		v.addError(field, "must be a valid email address")
	}
}

// MinLength checks minimum string length.
func (v *Validator) MinLength(field, value string, min int) {
	if len(value) < min {
		v.addError(field, "must be at least "+itoa(min)+" characters")
	}
}

// MaxLength checks maximum string length.
func (v *Validator) MaxLength(field, value string, max int) {
	if len(value) > max {
		v.addError(field, "must be at most "+itoa(max)+" characters")
	}
}

// Positive checks that an int is positive.
func (v *Validator) Positive(field string, value int) {
	if value <= 0 {
		v.addError(field, "must be positive")
	}
}

// OneOf checks that a string is one of the allowed values.
func (v *Validator) OneOf(field, value string, allowed []string) {
	for _, a := range allowed {
		if value == a {
			return
		}
	}
	v.addError(field, "must be one of: "+strings.Join(allowed, ", "))
}

// Valid returns true if there are no validation errors.
func (v *Validator) Valid() bool {
	return len(v.Errors) == 0
}

// FieldErrors returns the collected errors.
func (v *Validator) FieldErrors() map[string]string {
	return v.Errors
}

func (v *Validator) addError(field, message string) {
	if _, exists := v.Errors[field]; !exists {
		v.Errors[field] = message
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
