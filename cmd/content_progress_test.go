package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/progress"
)

func TestNormalizeDoneDate(t *testing.T) {
	s, err := normalizeDoneDate("2026-10-01T12:00:00Z")
	require.NoError(t, err)
	require.Equal(t, "2026-10-01T12:00:00.000Z", s)

	s, err = normalizeDoneDate("2026-10-01")
	require.NoError(t, err)
	require.Equal(t, "2026-10-01T00:00:00.000Z", s)

	s, err = normalizeDoneDate("")
	require.NoError(t, err)
	require.Equal(t, "", s)

	_, err = normalizeDoneDate("last tuesday")
	require.Error(t, err)
}

func TestValidateViewedFlags(t *testing.T) {
	require.NoError(t, validateViewedFlags(3, 0, 0, false))
	require.NoError(t, validateViewedFlags(-1, 2, 0, false))
	require.NoError(t, validateViewedFlags(-1, 0, time.Minute, true))

	require.Error(t, validateViewedFlags(-2, 0, 0, false))
	require.Error(t, validateViewedFlags(0, -1, 0, false))
	require.Error(t, validateViewedFlags(3, 2, 0, false))
	require.Error(t, validateViewedFlags(-1, 0, -time.Second, false))
	require.Error(t, validateViewedFlags(-1, 0, 0, true))
	require.Error(t, validateViewedFlags(-1, 0, 0, false))
}

func TestProgressPlanFromFlags(t *testing.T) {
	opts, on, err := progressPlanFromFlags("", 0, -1)
	require.NoError(t, err)
	require.False(t, on)
	require.Nil(t, opts.SetVisits)

	opts, on, err = progressPlanFromFlags("completed", 0, -1)
	require.NoError(t, err)
	require.True(t, on)
	require.Equal(t, progress.MarkCompleted, opts.MarkUnvisited)

	opts, on, err = progressPlanFromFlags(" visited ", 0, 5)
	require.NoError(t, err)
	require.True(t, on)
	require.NotNil(t, opts.SetVisits)
	require.Equal(t, 5, *opts.SetVisits)

	_, on, err = progressPlanFromFlags("", 4, -1)
	require.NoError(t, err)
	require.True(t, on)

	_, _, err = progressPlanFromFlags("bogus", 0, -1)
	require.Error(t, err)
	_, _, err = progressPlanFromFlags("visited", 2, 3)
	require.Error(t, err)
	_, _, err = progressPlanFromFlags("", -1, -1)
	require.Error(t, err)
	_, _, err = progressPlanFromFlags("", 0, -2)
	require.Error(t, err)
}

func TestHumanSeconds(t *testing.T) {
	require.Equal(t, "0s", humanSeconds(0))
	require.Equal(t, "45s", humanSeconds(45))
	require.Equal(t, "3m 20s", humanSeconds(200))
	require.Equal(t, "1h 5m 0s", humanSeconds(3900))
	require.Equal(t, "0s", humanSeconds(-1))
}
