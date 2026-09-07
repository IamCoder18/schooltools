package httpclient_test

import (
	"testing"

	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/stretchr/testify/assert"
)

func TestRedirectStatusesContainsOnlyCanonical(t *testing.T) {
	assert.Equal(t, 5, len(httpclient.RedirectStatuses))
	for _, s := range []int{301, 302, 303, 307, 308} {
		assert.True(t, httpclient.IsRedirect(s), s)
	}
	for _, s := range []int{200, 204, 304, 400, 404, 500} {
		assert.False(t, httpclient.IsRedirect(s), s)
	}
}

func TestIsRedirectAgrees(t *testing.T) {
	assert.True(t, httpclient.IsRedirect(302))
	assert.False(t, httpclient.IsRedirect(200))
	assert.True(t, httpclient.IsRedirect(307))
	assert.False(t, httpclient.IsRedirect(404))
}