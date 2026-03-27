package wb

import (
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
)

const dateFmt = "2006-01-02"

// mapWBStatus converts a WB API integer status code to a domain string status.
func mapWBStatus(code int) string {
	switch code {
	case 4:
		return "ready"
	case 7:
		return "completed"
	case 8:
		return "declined"
	case 9:
		return "active"
	case 11:
		return "paused"
	default:
		return "unknown"
	}
}

// mapBidType converts a WB API integer bid type to a domain string.
func mapBidType(bt int) string {
	switch bt {
	case 0:
		return domain.BidTypeManual
	case 1:
		return domain.BidTypeUnified
	default:
		return domain.BidTypeManual
	}
}

// rublesToKopecks converts a float64 ruble amount to int64 kopecks.
func rublesToKopecks(rubles float64) int64 {
	return int64(math.Round(rubles * 100))
}

// MapCampaignDTO converts a WBCampaignDTO to a domain Campaign.
func MapCampaignDTO(dto WBCampaignDTO, workspaceID, sellerCabinetID uuid.UUID) domain.Campaign {
	now := time.Now()
	return domain.Campaign{
		ID:              uuid.New(),
		WorkspaceID:     workspaceID,
		SellerCabinetID: sellerCabinetID,
		WBCampaignID:    int64(dto.AdvertID),
		Name:            dto.Name,
		Status:          mapWBStatus(dto.Status),
		CampaignType:    dto.Type,
		BidType:         mapBidType(dto.BidType),
		PaymentType:     dto.PaymentType,
		DailyBudget:     dto.DailyBudget,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// MapCampaignStatDTO converts a WBCampaignStatDTO to a domain CampaignStat.
func MapCampaignStatDTO(dto WBCampaignStatDTO, campaignID uuid.UUID) (domain.CampaignStat, error) {
	date, err := time.Parse(dateFmt, dto.Date)
	if err != nil {
		return domain.CampaignStat{}, fmt.Errorf("parse campaign stat date %q: %w", dto.Date, err)
	}

	now := time.Now()
	return domain.CampaignStat{
		ID:          uuid.New(),
		CampaignID:  campaignID,
		Date:        date,
		Impressions: dto.Views,
		Clicks:      dto.Clicks,
		Spend:       rublesToKopecks(dto.Sum),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// MapSearchClusterDTO converts a WBSearchClusterDTO to a domain Phrase.
func MapSearchClusterDTO(dto WBSearchClusterDTO, campaignID, workspaceID uuid.UUID) domain.Phrase {
	now := time.Now()

	var keyword string
	if len(dto.Keywords) > 0 {
		keyword = dto.Keywords[0]
	}

	count := dto.Count
	bid := dto.Bid

	return domain.Phrase{
		ID:          uuid.New(),
		CampaignID:  campaignID,
		WorkspaceID: workspaceID,
		WBClusterID: dto.ClusterID,
		Keyword:     keyword,
		Count:       &count,
		CurrentBid:  &bid,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// MapSearchClusterStatDTO converts a WBSearchClusterStatDTO to a domain PhraseStat.
func MapSearchClusterStatDTO(dto WBSearchClusterStatDTO, phraseID uuid.UUID) (domain.PhraseStat, error) {
	date, err := time.Parse(dateFmt, dto.Date)
	if err != nil {
		return domain.PhraseStat{}, fmt.Errorf("parse cluster stat date %q: %w", dto.Date, err)
	}

	now := time.Now()
	return domain.PhraseStat{
		ID:          uuid.New(),
		PhraseID:    phraseID,
		Date:        date,
		Impressions: dto.Views,
		Clicks:      dto.Clicks,
		Spend:       rublesToKopecks(dto.Sum),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// MapProductDTO converts a WBProductDTO to a domain Product.
func MapProductDTO(dto WBProductDTO, workspaceID, sellerCabinetID uuid.UUID) domain.Product {
	now := time.Now()
	brand := dto.Brand
	return domain.Product{
		ID:              uuid.New(),
		WorkspaceID:     workspaceID,
		SellerCabinetID: sellerCabinetID,
		WBProductID:     dto.NmID,
		Title:           dto.Title,
		Brand:           &brand,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// MapSalesFunnelDTO converts a WBSalesFunnelDTO to a domain CampaignStat,
// populating the Orders and Revenue fields. Other stat fields (Impressions,
// Clicks, Spend) are left at zero — they come from the campaign stats import.
func MapSalesFunnelDTO(dto WBSalesFunnelDTO, campaignID uuid.UUID) (domain.CampaignStat, error) {
	date, err := time.Parse(dateFmt, dto.Date)
	if err != nil {
		return domain.CampaignStat{}, fmt.Errorf("parse sales funnel date %q: %w", dto.Date, err)
	}

	now := time.Now()
	orders := dto.Orders
	revenue := rublesToKopecks(dto.OrdersSum)

	return domain.CampaignStat{
		ID:         uuid.New(),
		CampaignID: campaignID,
		Date:       date,
		Orders:     &orders,
		Revenue:    &revenue,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}
