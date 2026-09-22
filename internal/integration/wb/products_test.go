package wb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Токен только с «Продвижением»: реклама отвечает, content-api — 401/403.
// Подключение отклоняется понятной ошибкой, синк карточек даёт ErrNoContentAccess.
func TestContentAccess_PromotionOnlyToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/adv/v1/promotion/count":
			w.Write([]byte(`{"adverts":[],"all":0}`))
		case "/ping":
			assert.Equal(t, http.MethodGet, r.Method)
			w.WriteHeader(http.StatusUnauthorized)
		case "/content/v2/get/cards/list":
			w.WriteHeader(http.StatusForbidden)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(server.URL)

	err := client.ValidateToken(context.Background(), "tok")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoContentAccess))
	assert.Contains(t, err.Error(), "«Контент»")

	_, err = client.ListProducts(context.Background(), "tok")
	assert.True(t, errors.Is(err, ErrNoContentAccess))
}

func TestValidateToken_ChecksContentPing(t *testing.T) {
	var pinged bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			pinged = true
			w.Write([]byte(`{"TS":"2026-09-22T10:00:00+03:00","Status":"OK"}`))
			return
		}
		assert.Equal(t, "/adv/v1/promotion/count", r.URL.Path)
		w.Write([]byte(`{"adverts":[],"all":0}`))
	}))
	defer server.Close()

	require.NoError(t, newTestClient(server.URL).ValidateToken(context.Background(), "tok"))
	assert.True(t, pinged)
}

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
