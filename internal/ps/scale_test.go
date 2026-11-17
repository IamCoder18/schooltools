package ps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupProficiency(t *testing.T) {
	p, ok := LookupProficiency("ex1")
	require.True(t, ok)
	assert.Equal(t, "EX1", p.Code)
	assert.Equal(t, "Exemplary 1", p.Label)
	require.NotNil(t, p.Percent)
	assert.Equal(t, 95, *p.Percent)

	p, ok = LookupProficiency(" PR2 ")
	require.True(t, ok)
	require.NotNil(t, p.Percent)
	assert.Equal(t, 85, *p.Percent)
}

func TestLookupProficiencyUnknown(t *testing.T) {
	for _, code := range []string{"", "   ", "ZZ9", "EX"} {
		_, ok := LookupProficiency(code)
		assert.False(t, ok, code)
	}
}

func TestScaleIsCopy(t *testing.T) {
	first := Scale()
	require.Len(t, first, 8)
	require.NotNil(t, first[0].Percent)
	*first[0].Percent = 1

	second := Scale()
	require.NotNil(t, second[0].Percent)
	assert.Equal(t, 100, *second[0].Percent)
}

func TestScalePercentKnownOnlyForPublishedLevels(t *testing.T) {
	for _, p := range Scale() {
		switch p.Code {
		case "EX2", "EX1", "PR2", "PR1":
			assert.NotNil(t, p.Percent, p.Code)
		default:
			assert.Nil(t, p.Percent, p.Code)
		}
	}
}
