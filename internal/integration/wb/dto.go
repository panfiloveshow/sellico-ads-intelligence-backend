// Package wb provides integration with the Wildberries Advertising API.
//
// WB API DTOs v1 — Data Transfer Objects for WB API responses.
// These structs represent the JSON structures returned by the WB Advertising API.
// Versioned separately from domain models to adapt to WB API changes.
package wb

// WBCampaignDTO represents a campaign from the WB Advertising API.
type WBCampaignDTO struct {
	AdvertID    int     `json:"advertId"`
	Name        string  `json:"name"`
	Status      int     `json:"status"`
	Type        int     `json:"type"`
	DailyBudget *int64  `json:"dailyBudget"`
	BidType     int     `json:"bidType"`
	PaymentType string  `json:"paymentType"`
	NMIDs       []int64 `json:"-"`
}

// WBCampaignStatDTO represents campaign statistics from the WB Advertising API.
type WBCampaignStatDTO struct {
	AdvertID     int      `json:"advertId"`
	Date         string   `json:"date"`
	Views        int64    `json:"views"`
	Clicks       int64    `json:"clicks"`
	Sum          float64  `json:"sum"`
	Orders       *int64   `json:"orders,omitempty"`
	OrderedItems *int64   `json:"ordered_items,omitempty"`
	Revenue      *float64 `json:"revenue,omitempty"`
	Atbs         *int64   `json:"atbs,omitempty"`     // Добавления в корзину
	Canceled     *int64   `json:"canceled,omitempty"`  // Технические отмены
	CR           *float64 `json:"cr,omitempty"`        // Конверсия
	CPC          *float64 `json:"cpc,omitempty"`       // Стоимость клика (коп)
	CTR          *float64 `json:"ctr,omitempty"`       // Кликабельность
}

// WBSearchClusterStatDTOExtended has additional fields from normquery v1.
type WBSearchClusterStatDTOExtended struct {
	WBSearchClusterStatDTO
	AvgPos *float64 `json:"avg_pos,omitempty"` // Средняя позиция
	Atbs   *int64   `json:"atbs,omitempty"`    // Добавления в корзину
	Orders *int64   `json:"orders,omitempty"`  // Заказы
}

// WBSearchClusterDTO represents a Search Cluster from the WB Advertising API.
type WBSearchClusterDTO struct {
	ClusterID int64    `json:"id"`
	Keywords  []string `json:"keywords"`
	Count     int      `json:"count"`
	Bid       int64    `json:"bid"`
}

// WBSearchClusterStatDTO represents Search Cluster statistics from the WB API.
type WBSearchClusterStatDTO struct {
	ClusterID int64   `json:"id"`
	Date      string  `json:"date"`
	Views     int64   `json:"views"`
	Clicks    int64   `json:"clicks"`
	Sum       float64 `json:"sum"`
}

// WBBidDTO represents recommended bids from the WB Advertising API.
type WBBidDTO struct {
	NmID           int64 `json:"nmId"`
	CompetitiveBid int64 `json:"competitiveBid"`
	LeadershipBid  int64 `json:"leadershipBid"`
}

// WBCategoryConfigDTO represents category configuration from the WB API.
type WBCategoryConfigDTO struct {
	CategoryID int    `json:"id"`
	Name       string `json:"name"`
	CPMMin     int64  `json:"cpmMin"`
}

// WBProductDTO represents a product from the WB Advertising API.
type WBProductDTO struct {
	NmID       int64  `json:"nmId"`
	VendorCode string `json:"vendorCode"`
	Title      string `json:"title"`
	Brand      string `json:"brand"`
	ChrtID     int64  `json:"chrtId"`
	Category   string `json:"category,omitempty"`
	ImageURL   string `json:"imageUrl,omitempty"`
	Price      *int64 `json:"price,omitempty"`
}

// WBSalesFunnelDTO represents Sales Funnel data from the WB Analytics API.
type WBSalesFunnelDTO struct {
	NmID      int64   `json:"nmId"`
	Date      string  `json:"date"`
	Views     int64   `json:"views"`
	AddToCart int64   `json:"addToCart"`
	Orders    int64   `json:"orders"`
	OrdersSum float64 `json:"ordersSum"`
}

// WBSellerAnalyticsDTO represents a row from the Seller Analytics CSV report.
type WBSellerAnalyticsDTO struct {
	Query          string  `json:"query"`
	MedianPosition float64 `json:"medianPosition"`
	Frequency      int64   `json:"frequency"`
	Date           string  `json:"date"`
}

// SalesFunnelParams holds parameters for a Sales Funnel API request.
type SalesFunnelParams struct {
	DateFrom string  `json:"dateFrom"`
	DateTo   string  `json:"dateTo"`
	NmIDs    []int64 `json:"nmIds"`
}
