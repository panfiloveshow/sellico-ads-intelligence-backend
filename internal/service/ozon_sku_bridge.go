package service

import (
	"context"

	"github.com/google/uuid"

	sqlcgen "github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/repository/sqlc"
)

// ozonSKUBridge решает раздвоение идентификаторов Ozon: кампании оперируют
// «рекламным» SKU, а зеркала цен/стоков/продаж — «продажным». Общий ключ —
// артикул (offer_id) из ozon_products. Мост резолвит для каждого рекламного
// SKU его артикул, имя, строку цен, остаток и продажный SKU.
//
// Все карты ключуются РЕКЛАМНЫМ SKU (тем, которым оперируют кампании и ИИ).
// Отсутствие ключа = «не измерено». Для кабинетов, где идентификаторы
// совпадают, прямое совпадение по SKU срабатывает раньше моста.
type ozonSKUBridge struct {
	offerBySKU    map[int64]string
	nameBySKU     map[int64]string
	priceBySKU    map[int64]sqlcgen.OzonProductPrice
	stockBySKU    map[int64]int64
	salesSKUBySKU map[int64]int64
}

// buildOzonSKUBridge собирает мост для набора рекламных SKU одного кабинета.
// Best-effort: любая недоступная таблица просто оставляет свою карту пустой.
func (s *OzonAIManagerService) buildOzonSKUBridge(ctx context.Context, cabinetID uuid.UUID, skus []int64) *ozonSKUBridge {
	bridge := &ozonSKUBridge{
		offerBySKU:    map[int64]string{},
		nameBySKU:     map[int64]string{},
		priceBySKU:    map[int64]sqlcgen.OzonProductPrice{},
		stockBySKU:    map[int64]int64{},
		salesSKUBySKU: map[int64]int64{},
	}
	if len(skus) == 0 {
		return bridge
	}

	// 1. Рекламный SKU → артикул/имя (каталог ozon_products).
	info := ozonProductInfoBySKU(ctx, s.queries, s.logger, cabinetID, skus)
	offers := make([]string, 0, len(info))
	for sku, row := range info {
		if offer := pgTextValue(row.OfferID); offer != "" {
			bridge.offerBySKU[sku] = offer
			offers = append(offers, offer)
		}
		if name := pgTextValue(row.Name); name != "" {
			bridge.nameBySKU[sku] = name
		}
	}

	// 2. Цены: прямое совпадение по SKU + по артикулу.
	priceDirect := map[int64]sqlcgen.OzonProductPrice{}
	priceByOffer := map[string]sqlcgen.OzonProductPrice{}
	if rows, err := s.queries.ListOzonProductPricesBySkus(ctx, sqlcgen.ListOzonProductPricesBySkusParams{
		SellerCabinetID: uuidToPgtype(cabinetID), Skus: skus,
	}); err == nil {
		for _, row := range rows {
			priceDirect[row.Sku] = row
		}
	}
	if len(offers) > 0 {
		if rows, err := s.queries.ListOzonProductPricesByOffers(ctx, sqlcgen.ListOzonProductPricesByOffersParams{
			SellerCabinetID: uuidToPgtype(cabinetID), Offers: offers,
		}); err == nil {
			for _, row := range rows {
				if offer := pgTextValue(row.OfferID); offer != "" {
					priceByOffer[offer] = row
				}
			}
		}
	}

	// 3. Остатки: карты по продажному SKU и по артикулу.
	stockBySalesSKU := map[int64]sqlcgen.OzonProductStock{}
	stockByOffer := map[string]sqlcgen.OzonProductStock{}
	if rows, err := s.queries.ListOzonProductStocksByCabinet(ctx, uuidToPgtype(cabinetID)); err == nil {
		for _, row := range rows {
			stockBySalesSKU[row.Sku] = row
			if offer := pgTextValue(row.OfferID); offer != "" {
				stockByOffer[offer] = row
			}
		}
	} else {
		s.logger.Warn().Err(err).Msg("sku bridge: stocks read failed")
	}

	// 4. Резолв на рекламный SKU: прямое совпадение → артикул.
	for _, sku := range skus {
		offer := bridge.offerBySKU[sku]
		if row, ok := priceDirect[sku]; ok {
			bridge.priceBySKU[sku] = row
		} else if offer != "" {
			if row, ok := priceByOffer[offer]; ok {
				bridge.priceBySKU[sku] = row
			}
		}
		if row, ok := stockBySalesSKU[sku]; ok {
			bridge.stockBySKU[sku] = int64(row.Present)
			bridge.salesSKUBySKU[sku] = row.Sku
		} else if offer != "" {
			if row, ok := stockByOffer[offer]; ok {
				bridge.stockBySKU[sku] = int64(row.Present)
				bridge.salesSKUBySKU[sku] = row.Sku
			}
		}
		// Продажный SKU: сток надёжнее, цены — запасной источник.
		if _, ok := bridge.salesSKUBySKU[sku]; !ok {
			if row, ok := bridge.priceBySKU[sku]; ok {
				bridge.salesSKUBySKU[sku] = row.Sku
			}
		}
	}
	return bridge
}
