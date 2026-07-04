package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WB content API returns product photos under "photos" ([{big,c246x328,...}]),
// not "mediaFiles". This pins that ImageURL is populated from photos.
func TestListProducts_ParsesPhotosIntoImageURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/content/v2/get/cards/list", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		// One page, then an empty page to stop the cursor loop.
		if r.Header.Get("X-Page") == "" {
			w.Write([]byte(`{"cards":[
				{"nmID":111,"vendorCode":"A","title":"Сумка кожаная","brand":"МЛСКИН","object":"Сумки","photos":[{"big":"https://x/big.jpg","c246x328":"https://x/tn.jpg","square":"https://x/sq.jpg"}]},
				{"nmID":222,"vendorCode":"B","title":"Без фото","photos":[]}
			],"cursor":{"total":0,"nmID":0,"updatedAt":""}}`))
			return
		}
		w.Write([]byte(`{"cards":[],"cursor":{"total":0}}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	products, err := client.ListProducts(context.Background(), "tok")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(products), 2)

	byNm := map[int64]WBProductDTO{}
	for _, p := range products {
		byNm[p.NmID] = p
	}
	assert.Equal(t, "https://x/tn.jpg", byNm[111].ImageURL) // prefers c246x328 thumbnail
	assert.Equal(t, "Сумка кожаная", byNm[111].Title)
	assert.Equal(t, "", byNm[222].ImageURL) // no photos -> empty
}
