package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// card.wb.ru returns prices in kopecks under sizes[0].price.{basic,product}.
// СПП = (1 - product/basic) * 100. Pins the kopeck->rub + СПП math.
func TestShowcaseByNmIDs_ComputesSppAndRubles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/cards/v4/detail", r.URL.Path)
		assert.Equal(t, "170516317;222", r.URL.Query().Get("nm"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"products":[
			{"id":170516317,"name":"Набор специй","sizes":[{"price":{"basic":225200,"product":91300}}]},
			{"id":222,"name":"Нет в наличии","sizes":[{"price":{"basic":0,"product":0}}]}
		]}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	res, err := client.ShowcaseByNmIDs(context.Background(), []int64{170516317, 222})
	require.NoError(t, err)

	got := res[170516317]
	assert.Equal(t, int64(2252), got.BasicRub) // 225200 kop -> 2252 rub
	assert.Equal(t, int64(913), got.BuyerRub)  // 91300 kop -> 913 rub
	assert.Equal(t, 59, got.SppPercent)        // round((1-913/2252)*100) = 59
	assert.Equal(t, "Набор специй", got.Name)

	// Out of stock: name kept, no price/СПП.
	assert.Equal(t, "Нет в наличии", res[222].Name)
	assert.Zero(t, res[222].BuyerRub)
}

func TestWBImageURL(t *testing.T) {
	// nmID 170516317 -> vol 1705 (basket-12), part 170516.
	assert.Equal(t,
		"https://basket-12.wbbasket.ru/vol1705/part170516/170516317/images/c246x328/1.webp",
		WBImageURL(170516317))
}
