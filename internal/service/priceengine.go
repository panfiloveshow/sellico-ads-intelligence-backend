package service

import "github.com/rs/zerolog"

// PriceEngine computes repricer decisions from product economics, stock, sales
// velocity and ad signals. Pure functions live alongside it (see Phase 4).
type PriceEngine struct {
	logger zerolog.Logger
}

// NewPriceEngine creates a PriceEngine.
func NewPriceEngine(logger zerolog.Logger) *PriceEngine {
	return &PriceEngine{logger: logger.With().Str("component", "price_engine").Logger()}
}
