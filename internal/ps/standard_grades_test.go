package ps

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/httpclient"
)

func TestGetStandardGradesParsesGradeAndPercent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/standard_grade_section", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"standardgradesectionid":1,"studentsdcid":90000001,"standardid":11,"sectionsdcid":96000001,"storecode":"T1","standardgrade":"EX2","standardpercent":100.0},
			{"standardgradesectionid":2,"studentsdcid":90000001,"standardid":12,"sectionsdcid":96000001,"storecode":"T1"}
		]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := NewClient(jar)
	c.Base = srv.URL

	rows, err := c.GetStandardGrades(90000001, []int64{96000001}, 36)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	assert.Equal(t, "EX2", rows[0].Grade)
	require.NotNil(t, rows[0].Percent)
	assert.Equal(t, 100.0, *rows[0].Percent)

	assert.Empty(t, rows[1].Grade)
	assert.Nil(t, rows[1].Percent)
}
