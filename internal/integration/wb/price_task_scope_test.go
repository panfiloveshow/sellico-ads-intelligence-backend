package wb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPriceTaskHistoryRejectsUnprovenTaskIdentityAndStatus(t *testing.T) {
	for _, body := range []string{
		`{"data":{"uploadID":999,"status":3}}`,
		`{"data":{"status":3}}`,
		`{"data":{"uploadID":77}}`,
		`{"data":{"uploadID":77,"status":null}}`,
		`{"data":{"uploadID":77,"status":99}}`,
		`{"data":{}}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			status, err := newPricesTestClient(server.URL).GetPriceTaskHistory(context.Background(), "test-token", 77)
			require.Error(t, err)
			require.Nil(t, status)
		})
	}
}

func TestPriceTaskHistoryNullDataAllowsBufferLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":null}`)) }))
	defer server.Close()
	status, err := newPricesTestClient(server.URL).GetPriceTaskHistory(context.Background(), "test-token", 77)
	require.NoError(t, err)
	require.Nil(t, status)
}

func TestPriceTaskGoodsRejectWrongUploadAndIncompleteRows(t *testing.T) {
	for _, body := range []string{
		`{"data":{"uploadID":999,"historyGoods":[{"nmID":101,"price":901,"discount":17,"status":2}]}}`,
		`{"data":{"historyGoods":[{"nmID":101,"price":901,"discount":17,"status":2}]}}`,
		`{"data":{"uploadID":77,"historyGoods":null}}`,
		`{"data":{"uploadID":77,"historyGoods":[{"nmID":101,"price":901,"discount":17}]}}`,
		`{"data":{"uploadID":77,"historyGoods":[{"nmID":101,"price":901,"status":2}]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			goods, err := newPricesTestClient(server.URL).ListPriceTaskHistoryGoods(context.Background(), "test-token", 77, 1000, 0)
			require.Error(t, err)
			require.Nil(t, goods)
		})
	}
}
