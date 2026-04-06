package pagination

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newRequest(query string) *http.Request {
	u, _ := url.Parse("http://localhost/items?" + query)
	return &http.Request{URL: u}
}

func TestParse_Defaults(t *testing.T) {
	p := Parse(newRequest(""))
	assert.Equal(t, DefaultPage, p.Page)
	assert.Equal(t, DefaultPerPage, p.PerPage)
}

func TestParse_CustomValues(t *testing.T) {
	p := Parse(newRequest("page=3&per_page=50"))
	assert.Equal(t, 3, p.Page)
	assert.Equal(t, 50, p.PerPage)
}

func TestParse_MaxPerPageClamping(t *testing.T) {
	p := Parse(newRequest("per_page=9999"))
	assert.Equal(t, MaxPerPage, p.PerPage)
}

func TestParse_InvalidPage(t *testing.T) {
	p := Parse(newRequest("page=abc"))
	assert.Equal(t, DefaultPage, p.Page)
}

func TestParse_InvalidPerPage(t *testing.T) {
	p := Parse(newRequest("per_page=xyz"))
	assert.Equal(t, DefaultPerPage, p.PerPage)
}

func TestParse_NegativePage(t *testing.T) {
	p := Parse(newRequest("page=-1"))
	assert.Equal(t, DefaultPage, p.Page)
}

func TestParse_NegativePerPage(t *testing.T) {
	p := Parse(newRequest("per_page=-10"))
	assert.Equal(t, DefaultPerPage, p.PerPage)
}

func TestParse_ZeroPage(t *testing.T) {
	p := Parse(newRequest("page=0"))
	assert.Equal(t, DefaultPage, p.Page)
}

func TestParse_ZeroPerPage(t *testing.T) {
	p := Parse(newRequest("per_page=0"))
	assert.Equal(t, DefaultPerPage, p.PerPage)
}

func TestOffset(t *testing.T) {
	tests := []struct {
		page    int
		perPage int
		offset  int
	}{
		{1, 20, 0},
		{2, 20, 20},
		{3, 10, 20},
		{1, 100, 0},
		{5, 25, 100},
	}
	for _, tt := range tests {
		p := Params{Page: tt.page, PerPage: tt.perPage}
		assert.Equal(t, tt.offset, p.Offset(), "page=%d, per_page=%d", tt.page, tt.perPage)
	}
}

func TestParse_ExactMaxPerPage(t *testing.T) {
	p := Parse(newRequest("per_page=100"))
	assert.Equal(t, 100, p.PerPage)
}
