package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
)

func TestClampInt32(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want int32
	}{
		{name: "zero", in: 0, want: 0},
		{name: "regular", in: 12345, want: 12345},
		{name: "negative", in: -7, want: -7},
		{name: "max int32", in: 2147483647, want: 2147483647},
		{name: "overflow clamps to max", in: 2147483648, want: 2147483647},
		{name: "huge counter clamps", in: 1 << 40, want: 2147483647},
		{name: "min int32", in: -2147483648, want: -2147483648},
		{name: "underflow clamps to min", in: -2147483649, want: -2147483648},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clampInt32(tc.in))
		})
	}
}

func TestFloatToPgNumericRoundTrip(t *testing.T) {
	for _, v := range []float64{0, 1, 0.01, 123.45, 99999.99, -50.5} {
		numeric := floatToPgNumeric(v)
		assert.True(t, numeric.Valid, "value %v must produce a valid numeric", v)
		assert.InDelta(t, v, pgNumericToFloat(numeric), 1e-9, "round trip for %v", v)
	}
}

func TestPgNumericToFloat_NullIsZero(t *testing.T) {
	assert.Zero(t, pgNumericToFloat(pgtype.Numeric{}))
	assert.Nil(t, pgNumericToFloatPtr(pgtype.Numeric{}))
}

func TestPgNumericToFloatPtr_Valid(t *testing.T) {
	ptr := pgNumericToFloatPtr(floatToPgNumeric(42.5))
	if assert.NotNil(t, ptr) {
		assert.InDelta(t, 42.5, *ptr, 1e-9)
	}
}
