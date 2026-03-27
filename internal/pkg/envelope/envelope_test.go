package envelope

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOK_WithData(t *testing.T) {
	resp := OK("hello", nil)
	assert.Equal(t, "hello", resp.Data)
	assert.Nil(t, resp.Meta)
	assert.Empty(t, resp.Errors)
}

func TestOK_WithMeta(t *testing.T) {
	meta := &Meta{Page: 2, PerPage: 10, Total: 55}
	resp := OK([]string{"a", "b"}, meta)
	assert.Equal(t, []string{"a", "b"}, resp.Data)
	assert.Equal(t, 2, resp.Meta.Page)
	assert.Equal(t, 10, resp.Meta.PerPage)
	assert.Equal(t, int64(55), resp.Meta.Total)
}

func TestOK_NilData(t *testing.T) {
	resp := OK(nil, nil)
	assert.Nil(t, resp.Data)
}

func TestErr_SingleError(t *testing.T) {
	resp := Err(Error{Code: "NOT_FOUND", Message: "resource not found"})
	assert.Nil(t, resp.Data)
	assert.Nil(t, resp.Meta)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "NOT_FOUND", resp.Errors[0].Code)
	assert.Equal(t, "resource not found", resp.Errors[0].Message)
	assert.Empty(t, resp.Errors[0].Field)
}

func TestErr_MultipleErrors(t *testing.T) {
	resp := Err(
		Error{Code: "ERR1", Message: "first"},
		Error{Code: "ERR2", Message: "second"},
	)
	assert.Len(t, resp.Errors, 2)
}

func TestValidationErr(t *testing.T) {
	fieldErrors := map[string]string{
		"email": "is required",
	}
	resp := ValidationErr(fieldErrors)
	assert.Nil(t, resp.Data)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "VALIDATION_ERROR", resp.Errors[0].Code)
	assert.Equal(t, "is required", resp.Errors[0].Message)
	assert.Equal(t, "email", resp.Errors[0].Field)
}

func TestValidationErr_MultipleFields(t *testing.T) {
	fieldErrors := map[string]string{
		"email":    "is required",
		"password": "too short",
	}
	resp := ValidationErr(fieldErrors)
	assert.Len(t, resp.Errors, 2)
	// All errors should have VALIDATION_ERROR code.
	for _, e := range resp.Errors {
		assert.Equal(t, "VALIDATION_ERROR", e.Code)
		assert.NotEmpty(t, e.Field)
	}
}

func TestValidationErr_EmptyMap(t *testing.T) {
	resp := ValidationErr(map[string]string{})
	assert.Empty(t, resp.Errors)
}

func TestResponse_JSONSerialization(t *testing.T) {
	resp := OK(map[string]int{"count": 5}, &Meta{Page: 1, PerPage: 20, Total: 5})
	b, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded Response
	err = json.Unmarshal(b, &decoded)
	require.NoError(t, err)
	assert.NotNil(t, decoded.Meta)
	assert.Equal(t, 1, decoded.Meta.Page)
	assert.Empty(t, decoded.Errors)
}

func TestResponse_JSONOmitsEmptyMeta(t *testing.T) {
	resp := OK("data", nil)
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "meta")
}

func TestResponse_JSONOmitsEmptyErrors(t *testing.T) {
	resp := OK("data", nil)
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "errors")
}
