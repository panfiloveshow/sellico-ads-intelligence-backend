package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckedInt32(t *testing.T) {
	value, err := checkedInt32(math.MaxInt32)
	require.NoError(t, err)
	require.Equal(t, int32(math.MaxInt32), value)

	_, err = checkedInt32(int(uint64(math.MaxInt32) + 1))
	require.Error(t, err)
}

func TestBoundedInt32(t *testing.T) {
	require.Equal(t, int32(math.MaxInt32), boundedInt32(int(uint64(math.MaxInt32)+1)))
	require.Equal(t, int32(math.MinInt32), boundedInt32(int(math.MinInt32)-1))
}
