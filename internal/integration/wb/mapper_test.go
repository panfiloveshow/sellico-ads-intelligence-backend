package wb

import (
	"testing"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mapWBStatus
// ---------------------------------------------------------------------------

func TestMapWBStatus(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{4, "ready"},
		{7, "completed"},
		{8, "declined"},
		{9, "active"},
		{11, "paused"},
		{0, "unknown"},
		{-1, "unknown"},
		{999, "unknown"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, mapWBStatus(tt.code), "status code %d", tt.code)
	}
}

// ---------------------------------------------------------------------------
// mapBidType
// ---------------------------------------------------------------------------

func TestMapBidType(t *testing.T) {
	assert.Equal(t, domain.BidTypeManual, mapBidType(0))
	assert.Equal(t, domain.BidTypeUnified, mapBidType(1))
	assert.Equal(t, domain.BidTypeManual, mapBidType(42), "unknown bid type defaults to manual")
	assert.Equal(t, domain.BidTypeManual, mapBidType(-1))
}

// ---------------------------------------------------------------------------
// roundRubles
// ---------------------------------------------------------------------------

func TestRoundRubles(t *testing.T) {
	assert.Equal(t, int64(0), roundRubles(0))
	assert.Equal(t, int64(1), roundRubles(1.0))
	assert.Equal(t, int64(11), roundRubles(10.50))
	assert.Equal(t, int64(10), roundRubles(9.99))
	assert.Equal(t, int64(0), roundRubles(0.009))
	assert.Equal(t, int64(-5), roundRubles(-5.0))
}

// ---------------------------------------------------------------------------
// MapCampaignDTO
// ---------------------------------------------------------------------------

func TestMapCampaignDTO_FullFields(t *testing.T) {
	wsID := uuid.New()
	scID := uuid.New()
	budget := int64(50000)

	dto := WBCampaignDTO{
		AdvertID:    12345,
		Name:        "Test Campaign",
		Status:      9,
		Type:        9,
		DailyBudget: &budget,
		BidType:     1,
		PaymentType: "cpm",
	}

	c := MapCampaignDTO(dto, wsID, scID)

	assert.NotEqual(t, uuid.Nil, c.ID)
	assert.Equal(t, wsID, c.WorkspaceID)
	assert.Equal(t, scID, c.SellerCabinetID)
	assert.Equal(t, int64(12345), c.WBCampaignID)
	assert.Equal(t, "Test Campaign", c.Name)
	assert.Equal(t, "active", c.Status)
	assert.Equal(t, 9, c.CampaignType)
	assert.Equal(t, domain.BidTypeUnified, c.BidType)
	assert.Equal(t, "cpm", c.PaymentType)
	require.NotNil(t, c.DailyBudget)
	assert.Equal(t, int64(50000), *c.DailyBudget)
	assert.False(t, c.CreatedAt.IsZero())
	assert.False(t, c.UpdatedAt.IsZero())
}

func TestMapCampaignDTO_NilBudget(t *testing.T) {
	dto := WBCampaignDTO{
		AdvertID:    1,
		Name:        "No Budget",
		Status:      4,
		Type:        9,
		DailyBudget: nil,
		BidType:     0,
		PaymentType: "cpc",
	}

	c := MapCampaignDTO(dto, uuid.New(), uuid.New())

	assert.Nil(t, c.DailyBudget)
	assert.Equal(t, "ready", c.Status)
	assert.Equal(t, domain.BidTypeManual, c.BidType)
	assert.Equal(t, "cpc", c.PaymentType)
}

func TestMapCampaignDTO_EmptyName(t *testing.T) {
	dto := WBCampaignDTO{Name: "", Status: 0}
	c := MapCampaignDTO(dto, uuid.New(), uuid.New())
	assert.Equal(t, "", c.Name)
	assert.Equal(t, "unknown", c.Status)
}

func TestMapCampaignDTO_ZeroAdvertID(t *testing.T) {
	dto := WBCampaignDTO{AdvertID: 0}
	c := MapCampaignDTO(dto, uuid.New(), uuid.New())
	assert.Equal(t, int64(0), c.WBCampaignID)
}

// ---------------------------------------------------------------------------
// MapCampaignStatDTO
// ---------------------------------------------------------------------------

func TestMapCampaignStatDTO_Success(t *testing.T) {
	campID := uuid.New()
	dto := WBCampaignStatDTO{
		AdvertID:     1,
		Date:         "2026-03-20",
		Views:        1500,
		Clicks:       120,
		Sum:          345.67,
		Orders:       int64Ptr(12),
		OrderedItems: int64Ptr(18),
		Revenue:      float64Ptr(4567.89),
	}

	stat, err := MapCampaignStatDTO(dto, campID)
	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, stat.ID)
	assert.Equal(t, campID, stat.CampaignID)
	assert.Equal(t, "2026-03-20", stat.Date.Format("2006-01-02"))
	assert.Equal(t, int64(1500), stat.Impressions)
	assert.Equal(t, int64(120), stat.Clicks)
	assert.Equal(t, int64(346), stat.Spend)
	require.NotNil(t, stat.Orders)
	assert.Equal(t, int64(18), *stat.Orders)
	require.NotNil(t, stat.Revenue)
	assert.Equal(t, int64(4568), *stat.Revenue)
}

func TestMapCampaignStatDTO_RFC3339Date(t *testing.T) {
	campID := uuid.New()
	dto := WBCampaignStatDTO{
		AdvertID: 1,
		Date:     "2026-03-20T00:00:00Z",
		Views:    1500,
		Clicks:   120,
		Sum:      345.67,
	}

	stat, err := MapCampaignStatDTO(dto, campID)
	require.NoError(t, err)
	assert.Equal(t, "2026-03-20", stat.Date.Format("2006-01-02"))
}

func TestMapCampaignStatDTO_InvalidDate(t *testing.T) {
	_, err := MapCampaignStatDTO(WBCampaignStatDTO{Date: "not-a-date"}, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse campaign stat date")
}

func TestMapCampaignStatDTO_EmptyDate(t *testing.T) {
	_, err := MapCampaignStatDTO(WBCampaignStatDTO{Date: ""}, uuid.New())
	require.Error(t, err)
}

func TestMapCampaignStatDTO_ZeroValues(t *testing.T) {
	dto := WBCampaignStatDTO{Date: "2026-01-01", Views: 0, Clicks: 0, Sum: 0}
	stat, err := MapCampaignStatDTO(dto, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(0), stat.Impressions)
	assert.Equal(t, int64(0), stat.Clicks)
	assert.Equal(t, int64(0), stat.Spend)
}

// ---------------------------------------------------------------------------
// MapSearchClusterDTO
// ---------------------------------------------------------------------------

func TestMapSearchClusterDTO_FullFields(t *testing.T) {
	campID := uuid.New()
	wsID := uuid.New()
	dto := WBSearchClusterDTO{
		ClusterID: 777,
		Keywords:  []string{"кроссовки", "обувь спортивная"},
		Count:     42,
		Bid:       15000,
	}

	p := MapSearchClusterDTO(dto, campID, wsID)

	assert.NotEqual(t, uuid.Nil, p.ID)
	assert.Equal(t, campID, p.CampaignID)
	assert.Equal(t, wsID, p.WorkspaceID)
	assert.Equal(t, int64(777), p.WBClusterID)
	assert.Equal(t, "кроссовки", p.Keyword)
	require.NotNil(t, p.Count)
	assert.Equal(t, 42, *p.Count)
	require.NotNil(t, p.CurrentBid)
	assert.Equal(t, int64(15000), *p.CurrentBid)
}

func TestMapSearchClusterDTO_EmptyKeywords(t *testing.T) {
	dto := WBSearchClusterDTO{ClusterID: 1, Keywords: []string{}}
	p := MapSearchClusterDTO(dto, uuid.New(), uuid.New())
	assert.Equal(t, "", p.Keyword, "empty keywords slice should produce empty keyword")
}

func TestMapSearchClusterDTO_NilKeywords(t *testing.T) {
	dto := WBSearchClusterDTO{ClusterID: 1, Keywords: nil}
	p := MapSearchClusterDTO(dto, uuid.New(), uuid.New())
	assert.Equal(t, "", p.Keyword)
}

func TestMapSearchClusterDTO_ZeroBidAndCount(t *testing.T) {
	dto := WBSearchClusterDTO{ClusterID: 1, Keywords: []string{"test"}, Count: 0, Bid: 0}
	p := MapSearchClusterDTO(dto, uuid.New(), uuid.New())
	require.NotNil(t, p.Count)
	assert.Equal(t, 0, *p.Count)
	require.NotNil(t, p.CurrentBid)
	assert.Equal(t, int64(0), *p.CurrentBid)
}

// ---------------------------------------------------------------------------
// MapSearchClusterStatDTO
// ---------------------------------------------------------------------------

func TestMapSearchClusterStatDTO_Success(t *testing.T) {
	phraseID := uuid.New()
	dto := WBSearchClusterStatDTO{
		ClusterID: 10,
		Date:      "2026-03-15",
		Views:     500,
		Clicks:    30,
		Sum:       12.34,
	}

	stat, err := MapSearchClusterStatDTO(dto, phraseID)
	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, stat.ID)
	assert.Equal(t, phraseID, stat.PhraseID)
	assert.Equal(t, "2026-03-15", stat.Date.Format("2006-01-02"))
	assert.Equal(t, int64(500), stat.Impressions)
	assert.Equal(t, int64(30), stat.Clicks)
	assert.Equal(t, int64(12), stat.Spend)
}

func TestMapSearchClusterStatDTO_InvalidDate(t *testing.T) {
	_, err := MapSearchClusterStatDTO(WBSearchClusterStatDTO{Date: "bad"}, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse cluster stat date")
}

func TestMapSearchClusterStatDTO_ZeroValues(t *testing.T) {
	dto := WBSearchClusterStatDTO{Date: "2026-01-01", Views: 0, Clicks: 0, Sum: 0}
	stat, err := MapSearchClusterStatDTO(dto, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(0), stat.Impressions)
	assert.Equal(t, int64(0), stat.Clicks)
	assert.Equal(t, int64(0), stat.Spend)
}

// ---------------------------------------------------------------------------
// MapProductDTO
// ---------------------------------------------------------------------------

func TestMapProductDTO_FullFields(t *testing.T) {
	wsID := uuid.New()
	scID := uuid.New()
	dto := WBProductDTO{
		NmID:       987654,
		VendorCode: "ABC-123",
		Title:      "Кроссовки Nike",
		Brand:      "Nike",
		ChrtID:     111222,
	}

	p := MapProductDTO(dto, wsID, scID)

	assert.NotEqual(t, uuid.Nil, p.ID)
	assert.Equal(t, wsID, p.WorkspaceID)
	assert.Equal(t, scID, p.SellerCabinetID)
	assert.Equal(t, int64(987654), p.WBProductID)
	assert.Equal(t, "Кроссовки Nike", p.Title)
	require.NotNil(t, p.Brand)
	assert.Equal(t, "Nike", *p.Brand)
	assert.Nil(t, p.Category)
	assert.Nil(t, p.ImageURL)
	assert.Nil(t, p.Price)
}

func TestMapProductDTO_EmptyFields(t *testing.T) {
	dto := WBProductDTO{NmID: 0, Title: "", Brand: ""}
	p := MapProductDTO(dto, uuid.New(), uuid.New())

	assert.Equal(t, int64(0), p.WBProductID)
	assert.Equal(t, "", p.Title)
	require.NotNil(t, p.Brand)
	assert.Equal(t, "", *p.Brand)
}

// ---------------------------------------------------------------------------
// MapSalesFunnelDTO
// ---------------------------------------------------------------------------

func TestMapSalesFunnelDTO_Success(t *testing.T) {
	campID := uuid.New()
	dto := WBSalesFunnelDTO{
		NmID:      100,
		Date:      "2026-03-18",
		Views:     2000,
		AddToCart: 150,
		Orders:    25,
		OrdersSum: 75000.50,
	}

	stat, err := MapSalesFunnelDTO(dto, campID)
	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, stat.ID)
	assert.Equal(t, campID, stat.CampaignID)
	assert.Equal(t, "2026-03-18", stat.Date.Format("2006-01-02"))
	// Impressions/Clicks/Spend should be zero — they come from campaign stats import
	assert.Equal(t, int64(0), stat.Impressions)
	assert.Equal(t, int64(0), stat.Clicks)
	assert.Equal(t, int64(0), stat.Spend)
	require.NotNil(t, stat.Orders)
	assert.Equal(t, int64(25), *stat.Orders)
	require.NotNil(t, stat.Revenue)
	assert.Equal(t, int64(75001), *stat.Revenue)
}

func TestMapSalesFunnelDTO_InvalidDate(t *testing.T) {
	_, err := MapSalesFunnelDTO(WBSalesFunnelDTO{Date: "2026/03/18"}, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse sales funnel date")
}

func TestMapSalesFunnelDTO_ZeroOrders(t *testing.T) {
	dto := WBSalesFunnelDTO{Date: "2026-01-01", Orders: 0, OrdersSum: 0}
	stat, err := MapSalesFunnelDTO(dto, uuid.New())
	require.NoError(t, err)
	require.NotNil(t, stat.Orders)
	assert.Equal(t, int64(0), *stat.Orders)
	require.NotNil(t, stat.Revenue)
	assert.Equal(t, int64(0), *stat.Revenue)
}

func int64Ptr(value int64) *int64 {
	return &value
}

func float64Ptr(value float64) *float64 {
	return &value
}

// ---------------------------------------------------------------------------
// Unique IDs — each call should produce a distinct UUID
// ---------------------------------------------------------------------------

func TestMappers_GenerateUniqueIDs(t *testing.T) {
	wsID := uuid.New()
	scID := uuid.New()

	c1 := MapCampaignDTO(WBCampaignDTO{AdvertID: 1}, wsID, scID)
	c2 := MapCampaignDTO(WBCampaignDTO{AdvertID: 1}, wsID, scID)
	assert.NotEqual(t, c1.ID, c2.ID, "each mapped campaign should get a unique ID")

	p1 := MapProductDTO(WBProductDTO{NmID: 1}, wsID, scID)
	p2 := MapProductDTO(WBProductDTO{NmID: 1}, wsID, scID)
	assert.NotEqual(t, p1.ID, p2.ID, "each mapped product should get a unique ID")
}
