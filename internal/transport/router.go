package transport

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RateLimitOpts configures per-user rate limiting. Zero values disable it.
type RateLimitOpts struct {
	RequestsPerSecond float64
	Burst             int
}

// RouterDeps holds all dependencies needed to construct the router.
// PermissionChecker re-exported for wiring in cmd without importing middleware.
type PermissionChecker = middleware.PermissionChecker

type RouterDeps struct {
	CORSAllowOrigins                    []string
	RateLimit                           RateLimitOpts
	JWTSecret                           string
	MembershipChecker                   middleware.MembershipChecker
	WorkspaceResolver                   middleware.WorkspaceResolver
	Authenticator                       middleware.Authenticator
	PermissionChecker                   middleware.PermissionChecker
	ManagerAccessVerifier               middleware.ManagerAccessVerifier
	ManagerAdsHandler                   *handler.ManagerAdsHandler
	ManagerAdAdvancedHandler            *handler.ManagerAdAdvancedHandler
	ManagerAdContextHandler             *handler.ManagerAdContextHandler
	ManagerAdOperationsHandler          *handler.ManagerAdOperationsHandler
	ManagerAdExecutionControlHandler    *handler.ManagerAdExecutionControlHandler
	ManagerPriceExecutionControlHandler *handler.ManagerPriceExecutionControlHandler
	ManagerLegacyPriceReviewHandler     *handler.ManagerLegacyPriceReviewHandler
	LegacyPriceDispatchHandler          *handler.LegacyPriceDispatchHandler
	ManagerPriceHandler                 *handler.ManagerPriceHandler
	DocsHandler                         *handler.DocsHandler
	HealthHandler                       *handler.HealthHandler
	AuthHandler                         *handler.AuthHandler
	WorkspaceHandler                    *handler.WorkspaceHandler
	SellerCabinetHandler                *handler.SellerCabinetHandler
	AdsReadHandler                      *handler.AdsReadHandler
	CampaignHandler                     *handler.CampaignHandler
	PhraseHandler                       *handler.PhraseHandler
	BidHandler                          *handler.BidHandler
	ProductHandler                      *handler.ProductHandler
	PositionHandler                     *handler.PositionHandler
	SERPHandler                         *handler.SERPHandler
	RecommendationHandler               *handler.RecommendationHandler
	ExportHandler                       *handler.ExportHandler
	ExtensionHandler                    *handler.ExtensionHandler
	AuditLogHandler                     *handler.AuditLogHandler
	JobRunHandler                       *handler.JobRunHandler
	EventsHandler                       *handler.EventsHandler
	WorkspaceSettingsHandler            *handler.WorkspaceSettingsHandler
	StrategyHandler                     *handler.StrategyHandler
	CampaignActionHandler               *handler.CampaignActionHandler
	SemanticsHandler                    *handler.SemanticsHandler
	CompetitorHandler                   *handler.CompetitorHandler
	DeliveryHandler                     *handler.DeliveryHandler
	SEOHandler                          *handler.SEOHandler
	ProductEventHandler                 *handler.ProductEventHandler
	ProductEconomicsHandler             *handler.ProductEconomicsHandler
	PriceHandler                        *handler.PriceHandler
	OzonHandler                         *handler.OzonHandler
}

// notImplemented is a placeholder handler returning 501 Not Implemented.
func notImplemented(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(`{"data":null,"errors":[{"code":"NOT_IMPLEMENTED","message":"not implemented"}]}`))
}

// NewRouter creates and returns a fully configured chi.Router.
func NewRouter(deps RouterDeps) chi.Router {
	r := chi.NewRouter()
	authMiddleware := middleware.Auth(deps.JWTSecret)
	if deps.Authenticator != nil {
		authMiddleware = middleware.SharedOrLocalAuth(deps.Authenticator, deps.JWTSecret)
	}

	tenantMiddleware := middleware.TenantScope(deps.MembershipChecker)
	if deps.WorkspaceResolver != nil {
		tenantMiddleware = middleware.SharedTenantScope(deps.WorkspaceResolver, deps.MembershipChecker)
	}

	// CRM RBAC: базовый view-гейт на workspace-scope, manage — на записи.
	viewPermission := middleware.RequirePermission(deps.PermissionChecker, "marketing.view")
	managePermission := middleware.RequirePermission(deps.PermissionChecker, "marketing.manage")

	// --- Global middleware ---
	r.Use(middleware.CORS(middleware.CORSConfig{AllowOrigins: deps.CORSAllowOrigins}))
	r.Use(middleware.RequestID)
	r.Use(middleware.Metrics)
	r.Use(middleware.Logging)
	r.Use(middleware.Recovery)

	// --- Prometheus metrics (public, outside /api/v1) ---
	r.Handle("/metrics", promhttp.Handler())

	// --- Health (public, outside /api/v1) ---
	if deps.DocsHandler != nil {
		r.Get("/openapi.yaml", deps.DocsHandler.Spec)
		r.Get("/docs", deps.DocsHandler.Index)
	} else {
		r.Get("/openapi.yaml", notImplemented)
		r.Get("/docs", notImplemented)
	}
	if deps.HealthHandler != nil {
		r.Get("/health/live", deps.HealthHandler.Live)
		r.Get("/health/ready", deps.HealthHandler.Ready)
	} else {
		r.Get("/health/live", notImplemented)
		r.Get("/health/ready", notImplemented)
	}

	// Private service attestation; its handler authenticates the exact body with
	// a separate HMAC credential. A browser/personal bearer cannot grant it.
	if deps.LegacyPriceDispatchHandler != nil {
		r.Post("/internal/v1/prices/dispatch-authorizations", deps.LegacyPriceDispatchHandler.Authorize)
	}

	// --- /api/v1 group ---
	r.Route("/api/v1", func(v1 chi.Router) {
		var rateLimitMiddleware func(http.Handler) http.Handler
		var ipRateLimitMiddleware func(http.Handler) http.Handler
		if deps.RateLimit.RequestsPerSecond > 0 {
			rlCfg := middleware.RateLimitConfig{
				RequestsPerSecond: deps.RateLimit.RequestsPerSecond,
				Burst:             deps.RateLimit.Burst,
			}
			rateLimitMiddleware = middleware.RateLimit(rlCfg)
			// Separate instance (own bucket map) keyed by client IP, applied
			// before auth so unauthenticated floods are capped before the
			// upstream Sellico auth call — see DoS-amplification guard below.
			ipRateLimitMiddleware = middleware.RateLimit(rlCfg)
		}
		// --- Public auth routes ---
		v1.Route("/auth", func(auth chi.Router) {
			if rateLimitMiddleware != nil {
				auth.Use(rateLimitMiddleware)
			}
			if deps.AuthHandler != nil {
				auth.Post("/register", deps.AuthHandler.Register)
				auth.Post("/login", deps.AuthHandler.Login)
				auth.Post("/refresh", deps.AuthHandler.Refresh)
				auth.Post("/logout", deps.AuthHandler.Logout)
				auth.With(authMiddleware).Get("/me", deps.AuthHandler.Me)
			} else {
				auth.Post("/register", notImplemented)
				auth.Post("/login", notImplemented)
				auth.Post("/refresh", notImplemented)
				auth.Post("/logout", notImplemented)
				auth.With(authMiddleware).Get("/me", notImplemented)
			}
		})

		// --- Protected routes (require auth) ---
		v1.Group(func(protected chi.Router) {
			// IP-keyed limiter runs before auth so a flood of garbage tokens is
			// throttled before each request fans out an upstream Sellico auth call.
			if ipRateLimitMiddleware != nil {
				protected.Use(ipRateLimitMiddleware)
			}
			protected.Use(authMiddleware)
			// Per-user limiter runs after auth (keyed by resolved user ID).
			if rateLimitMiddleware != nil {
				protected.Use(rateLimitMiddleware)
			}

			// Manager reads require a fresh personal CRM identity and exact store mapping.
			protected.Group(func(manager chi.Router) {
				manager.Use(middleware.ManagerWorkspaceHeaders)
				manager.Use(tenantMiddleware)
				manager.Use(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.view", "integrations.view"))
				if deps.ManagerAdsHandler != nil {
					manager.Get("/manager/ads/campaigns", deps.ManagerAdsHandler.Campaigns)
				} else {
					manager.Get("/manager/ads/campaigns", notImplemented)
				}
				if deps.ManagerAdAdvancedHandler != nil {
					manager.Get("/manager/ads/attribution", deps.ManagerAdAdvancedHandler.Attribution)
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage")).Post("/manager/ads/operations/{id}/resolution/prepare", deps.ManagerAdAdvancedHandler.PrepareResolution)
					manager.Get("/manager/ads/operations/{id}/resolution/{resolutionId}", deps.ManagerAdAdvancedHandler.ResolutionReceipt)
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage")).Post("/manager/ads/operations/{id}/resolution/{resolutionId}/approve", deps.ManagerAdAdvancedHandler.ApproveResolution)
					manager.Post("/manager/ads/operations/{id}/resolution/{resolutionId}/cancel", deps.ManagerAdAdvancedHandler.CancelResolution)
				} else {
					manager.Get("/manager/ads/attribution", notImplemented)
					manager.Post("/manager/ads/operations/{id}/resolution/prepare", notImplemented)
					manager.Get("/manager/ads/operations/{id}/resolution/{resolutionId}", notImplemented)
					manager.Post("/manager/ads/operations/{id}/resolution/{resolutionId}/approve", notImplemented)
					manager.Post("/manager/ads/operations/{id}/resolution/{resolutionId}/cancel", notImplemented)
				}
				if deps.ManagerAdContextHandler != nil {
					manager.Get("/manager/ads/campaigns/{id}/context", deps.ManagerAdContextHandler.Context)
					manager.Get("/manager/ads/campaigns/{id}/bid-context", deps.ManagerAdContextHandler.ProductBidContext)
					manager.Get("/manager/ads/campaigns/{id}/budget-context", deps.ManagerAdContextHandler.CampaignBudgetContext)
				} else {
					manager.Get("/manager/ads/campaigns/{id}/context", notImplemented)
					manager.Get("/manager/ads/campaigns/{id}/bid-context", notImplemented)
					manager.Get("/manager/ads/campaigns/{id}/budget-context", notImplemented)
				}
				if deps.ManagerLegacyPriceReviewHandler != nil {
					legacyReviews := manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "products.view", "unit_economics.view"))
					legacyReviews.Get("/manager/prices/legacy-journal/context", deps.ManagerLegacyPriceReviewHandler.Context)
					legacyReviews.Get("/manager/prices/legacy-reviews/{id}", deps.ManagerLegacyPriceReviewHandler.Receipt)
					legacyReviews.Post("/manager/prices/legacy-reviews/{id}/cancel", deps.ManagerLegacyPriceReviewHandler.Cancel)
					legacyReviews.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage", "unit_economics.calculate")).Post("/manager/prices/legacy-reviews", deps.ManagerLegacyPriceReviewHandler.Prepare)
					legacyReviews.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage", "unit_economics.calculate")).Post("/manager/prices/legacy-reviews/{id}/approve", deps.ManagerLegacyPriceReviewHandler.Approve)
				} else {
					manager.Get("/manager/prices/legacy-journal/context", notImplemented)
					manager.Get("/manager/prices/legacy-reviews/{id}", notImplemented)
					manager.Post("/manager/prices/legacy-reviews/{id}/cancel", notImplemented)
					manager.Post("/manager/prices/legacy-reviews", notImplemented)
					manager.Post("/manager/prices/legacy-reviews/{id}/approve", notImplemented)
				}
				if deps.ManagerPriceExecutionControlHandler != nil {
					priceControls := manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "products.view", "unit_economics.view"))
					priceControls.Get("/manager/prices/execution-controls/context", deps.ManagerPriceExecutionControlHandler.Context)
					priceControls.Get("/manager/prices/execution-controls/{id}", deps.ManagerPriceExecutionControlHandler.Receipt)
					priceControls.Post("/manager/prices/execution-controls/{id}/cancel", deps.ManagerPriceExecutionControlHandler.Cancel)
					priceControls.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage", "unit_economics.calculate")).Post("/manager/prices/execution-controls", deps.ManagerPriceExecutionControlHandler.Prepare)
					priceControls.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage", "unit_economics.calculate")).Post("/manager/prices/execution-controls/{id}/approve", deps.ManagerPriceExecutionControlHandler.Approve)
				} else {
					manager.Get("/manager/prices/execution-controls/context", notImplemented)
					manager.Get("/manager/prices/execution-controls/{id}", notImplemented)
					manager.Post("/manager/prices/execution-controls/{id}/cancel", notImplemented)
					manager.Post("/manager/prices/execution-controls", notImplemented)
					manager.Post("/manager/prices/execution-controls/{id}/approve", notImplemented)
				}
				if deps.ManagerAdExecutionControlHandler != nil {
					manager.Get("/manager/ads/execution-controls/context", deps.ManagerAdExecutionControlHandler.Context)
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage")).Post("/manager/ads/execution-controls", deps.ManagerAdExecutionControlHandler.Prepare)
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage")).Post("/manager/ads/execution-controls/{id}/approve", deps.ManagerAdExecutionControlHandler.Approve)
					manager.Post("/manager/ads/execution-controls/{id}/cancel", deps.ManagerAdExecutionControlHandler.Cancel)
					manager.Get("/manager/ads/execution-controls/{id}", deps.ManagerAdExecutionControlHandler.Receipt)
				} else {
					manager.Get("/manager/ads/execution-controls/context", notImplemented)
					manager.Post("/manager/ads/execution-controls", notImplemented)
					manager.Post("/manager/ads/execution-controls/{id}/approve", notImplemented)
					manager.Post("/manager/ads/execution-controls/{id}/cancel", notImplemented)
					manager.Get("/manager/ads/execution-controls/{id}", notImplemented)
				}
				if deps.ManagerAdOperationsHandler != nil {
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage")).Post("/manager/ads/operations", deps.ManagerAdOperationsHandler.Prepare)
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage")).Post("/manager/ads/bid-operations", deps.ManagerAdOperationsHandler.PrepareBid)
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage")).Post("/manager/ads/budget-operations", deps.ManagerAdOperationsHandler.PrepareBudget)
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "marketing.manage")).Post("/manager/ads/operations/{id}/approve", deps.ManagerAdOperationsHandler.Approve)
					manager.Post("/manager/ads/operations/{id}/cancel", deps.ManagerAdOperationsHandler.Cancel)
					manager.Get("/manager/ads/operations/{id}", deps.ManagerAdOperationsHandler.Receipt)
				} else {
					manager.Post("/manager/ads/operations", notImplemented)
					manager.Post("/manager/ads/bid-operations", notImplemented)
					manager.Post("/manager/ads/budget-operations", notImplemented)
					manager.Post("/manager/ads/operations/{id}/approve", notImplemented)
					manager.Post("/manager/ads/operations/{id}/cancel", notImplemented)
					manager.Get("/manager/ads/operations/{id}", notImplemented)
				}
				if deps.ManagerPriceHandler != nil {
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "products.view", "unit_economics.view", "unit_economics.calculate", "marketing.manage")).Post("/manager/prices/operations", deps.ManagerPriceHandler.Apply)
					manager.With(middleware.RequireManagerPermissions(deps.ManagerAccessVerifier, "unit_economics.view")).Get("/manager/prices/operations/{id}", deps.ManagerPriceHandler.Receipt)
				} else {
					manager.Post("/manager/prices/operations", notImplemented)
					manager.Get("/manager/prices/operations/{id}", notImplemented)
				}
			})

			// Workspaces — create/list (no tenant scope needed)
			if deps.WorkspaceHandler != nil {
				protected.Post("/workspaces", deps.WorkspaceHandler.Create)
				protected.Get("/workspaces", deps.WorkspaceHandler.List)
			} else {
				protected.Post("/workspaces", notImplemented)
				protected.Get("/workspaces", notImplemented)
			}

			// Workspace by ID — requires tenant scope via URL param
			protected.Route("/workspaces/{workspaceId}", func(ws chi.Router) {
				ws.Use(tenantMiddleware)
				ws.Use(viewPermission)

				if deps.WorkspaceHandler != nil {
					ws.Get("/", deps.WorkspaceHandler.Get)
				} else {
					ws.Get("/", notImplemented)
				}
				if deps.AdsReadHandler != nil {
					ws.Get("/ads-intelligence/wb/data-health", deps.AdsReadHandler.DataHealth)
					ws.Get("/ads-intelligence/wb/debug/normquery", deps.AdsReadHandler.DebugNormQuery)
				} else {
					ws.Get("/ads-intelligence/wb/data-health", notImplemented)
					ws.Get("/ads-intelligence/wb/debug/normquery", notImplemented)
				}

				// Members management — owner/manager only
				ws.Route("/members", func(members chi.Router) {
					if deps.WorkspaceHandler != nil {
						members.Get("/", deps.WorkspaceHandler.ListMembers)
					} else {
						members.Get("/", notImplemented)
					}

					members.Group(func(writeMembers chi.Router) {
						writeMembers.Use(middleware.RequirePermission(deps.PermissionChecker, "users.edit"))
						if deps.WorkspaceHandler != nil {
							writeMembers.Post("/", deps.WorkspaceHandler.InviteMember)
							writeMembers.Patch("/{memberId}", deps.WorkspaceHandler.UpdateMemberRole)
							writeMembers.Delete("/{memberId}", deps.WorkspaceHandler.RemoveMember)
						} else {
							writeMembers.Post("/", notImplemented)
							writeMembers.Patch("/{memberId}", notImplemented)
							writeMembers.Delete("/{memberId}", notImplemented)
						}
					})
				})
			})

			// --- Workspace-scoped routes (auth + tenant via X-Workspace-ID header) ---
			protected.Group(func(scoped chi.Router) {
				scoped.Use(tenantMiddleware)
				scoped.Use(viewPermission)

				// Seller Cabinets — write requires owner/manager
				scoped.Route("/seller-cabinets", func(sc chi.Router) {
					sc.Use(managePermission)
					if deps.SellerCabinetHandler != nil {
						sc.Post("/", deps.SellerCabinetHandler.Create)
						sc.Get("/", deps.SellerCabinetHandler.List)
						sc.Get("/{id}", deps.SellerCabinetHandler.Get)
						sc.Get("/{id}/campaigns", deps.SellerCabinetHandler.ListCampaigns)
						sc.Get("/{id}/products", deps.SellerCabinetHandler.ListProducts)
						sc.Get("/{id}/communication/reputation", deps.SellerCabinetHandler.CommunicationReputation)
						sc.Post("/{id}/sync", deps.SellerCabinetHandler.TriggerSync)
						sc.Delete("/{id}", deps.SellerCabinetHandler.Delete)
					} else {
						sc.Post("/", notImplemented)
						sc.Get("/", notImplemented)
						sc.Get("/{id}", notImplemented)
						sc.Get("/{id}/campaigns", notImplemented)
						sc.Get("/{id}/products", notImplemented)
						sc.Get("/{id}/communication/reputation", notImplemented)
						sc.Post("/{id}/sync", notImplemented)
						sc.Delete("/{id}", notImplemented)
					}
				})
				scoped.Route("/cabinets", func(sc chi.Router) {
					sc.Use(managePermission)
					if deps.SellerCabinetHandler != nil {
						sc.Post("/", deps.SellerCabinetHandler.Create)
						sc.Get("/", deps.SellerCabinetHandler.List)
						sc.Get("/{id}", deps.SellerCabinetHandler.Get)
						sc.Get("/{id}/campaigns", deps.SellerCabinetHandler.ListCampaigns)
						sc.Get("/{id}/products", deps.SellerCabinetHandler.ListProducts)
						sc.Get("/{id}/communication/reputation", deps.SellerCabinetHandler.CommunicationReputation)
						sc.Post("/{id}/sync", deps.SellerCabinetHandler.TriggerSync)
						sc.Delete("/{id}", deps.SellerCabinetHandler.Delete)
					} else {
						sc.Post("/", notImplemented)
						sc.Get("/", notImplemented)
						sc.Get("/{id}", notImplemented)
						sc.Get("/{id}/campaigns", notImplemented)
						sc.Get("/{id}/products", notImplemented)
						sc.Get("/{id}/communication/reputation", notImplemented)
						sc.Post("/{id}/sync", notImplemented)
						sc.Delete("/{id}", notImplemented)
					}
				})

				// Campaigns — read-only
				scoped.Route("/ads", func(a chi.Router) {
					if deps.AdsReadHandler != nil {
						a.Get("/overview", deps.AdsReadHandler.Overview)
						a.Get("/reports/client-audit", deps.AdsReadHandler.ClientAuditReport)
						a.Get("/data-health", deps.AdsReadHandler.DataHealth)
						a.Get("/products", deps.AdsReadHandler.ListProducts)
						a.Get("/products/{id}", deps.AdsReadHandler.GetProduct)
						a.Get("/campaigns", deps.AdsReadHandler.ListCampaigns)
						a.Get("/campaigns/{id}", deps.AdsReadHandler.GetCampaign)
						a.Get("/queries", deps.AdsReadHandler.ListQueries)
						a.Get("/queries/{id}", deps.AdsReadHandler.GetQuery)
						a.Get("/debug/normquery", deps.AdsReadHandler.DebugNormQuery)
					} else {
						a.Get("/overview", notImplemented)
						a.Get("/reports/client-audit", notImplemented)
						a.Get("/data-health", notImplemented)
						a.Get("/products", notImplemented)
						a.Get("/products/{id}", notImplemented)
						a.Get("/campaigns", notImplemented)
						a.Get("/campaigns/{id}", notImplemented)
						a.Get("/queries", notImplemented)
						a.Get("/queries/{id}", notImplemented)
						a.Get("/debug/normquery", notImplemented)
					}
				})

				// Campaigns — read-only
				scoped.Route("/campaigns", func(c chi.Router) {
					if deps.CampaignHandler != nil {
						c.Get("/", deps.CampaignHandler.List)
						if deps.CampaignActionHandler != nil {
							c.With(middleware.RequireFinancialAccess()).Post("/", deps.CampaignActionHandler.Create)
						}
						c.Get("/{id}", deps.CampaignHandler.Get)
						c.Get("/{id}/stats", deps.CampaignHandler.GetStats)
						c.Get("/{id}/daily-stats", deps.CampaignHandler.GetStats)
						c.Get("/{id}/phrases", deps.CampaignHandler.ListPhrases)
						c.Get("/{id}/recommendations", deps.CampaignHandler.ListRecommendations)
					} else {
						c.Get("/", notImplemented)
						c.Get("/{id}", notImplemented)
						c.Get("/{id}/stats", notImplemented)
						c.Get("/{id}/daily-stats", notImplemented)
						c.Get("/{id}/phrases", notImplemented)
						c.Get("/{id}/recommendations", notImplemented)
					}
				})

				// Phrases
				scoped.Route("/phrases", func(p chi.Router) {
					if deps.PhraseHandler != nil {
						p.Get("/", deps.PhraseHandler.List)
						p.Get("/{id}", deps.PhraseHandler.Get)
						p.Get("/{id}/stats", deps.PhraseHandler.GetStats)
						p.Get("/{id}/daily-stats", deps.PhraseHandler.GetStats)
						p.Get("/{id}/bids", deps.PhraseHandler.ListBids)
						p.Get("/{id}/recommendations", deps.PhraseHandler.ListRecommendations)
					} else {
						p.Get("/", notImplemented)
						p.Get("/{id}", notImplemented)
						p.Get("/{id}/stats", notImplemented)
						p.Get("/{id}/daily-stats", notImplemented)
						p.Get("/{id}/bids", notImplemented)
						p.Get("/{id}/recommendations", notImplemented)
					}
				})

				// Bids
				scoped.Route("/bids", func(b chi.Router) {
					if deps.BidHandler != nil {
						b.With(managePermission).Post("/history", deps.BidHandler.Create)
						b.Get("/history", deps.BidHandler.ListHistory)
						b.Get("/estimates", deps.BidHandler.GetEstimate)
					} else {
						b.With(managePermission).Post("/history", notImplemented)
						b.Get("/history", notImplemented)
						b.Get("/estimates", notImplemented)
					}
				})

				// Products
				scoped.Route("/products", func(p chi.Router) {
					if deps.ProductHandler != nil {
						p.Get("/", deps.ProductHandler.List)
						p.Get("/{id}", deps.ProductHandler.Get)
						p.Get("/{id}/positions", deps.ProductHandler.ListPositions)
						p.Get("/{id}/recommendations", deps.ProductHandler.ListRecommendations)
					} else {
						p.Get("/", notImplemented)
						p.Get("/{id}", notImplemented)
						p.Get("/{id}/positions", notImplemented)
						p.Get("/{id}/recommendations", notImplemented)
					}
				})

				// Positions
				scoped.Route("/positions", func(pos chi.Router) {
					if deps.PositionHandler != nil {
						pos.With(managePermission).Post("/targets", deps.PositionHandler.CreateTarget)
						pos.Get("/targets", deps.PositionHandler.ListTargets)
						pos.With(managePermission).Post("/", deps.PositionHandler.Create)
						pos.Get("/", deps.PositionHandler.List)
						pos.Get("/history", deps.PositionHandler.List)
						pos.Get("/aggregate", deps.PositionHandler.Aggregate)
					} else {
						pos.With(managePermission).Post("/targets", notImplemented)
						pos.Get("/targets", notImplemented)
						pos.With(managePermission).Post("/", notImplemented)
						pos.Get("/", notImplemented)
						pos.Get("/history", notImplemented)
						pos.Get("/aggregate", notImplemented)
					}
				})

				// SERP Snapshots
				scoped.Route("/serp-snapshots", func(serp chi.Router) {
					if deps.SERPHandler != nil {
						serp.With(managePermission).Post("/", deps.SERPHandler.Create)
						serp.Get("/", deps.SERPHandler.List)
						serp.Get("/{id}", deps.SERPHandler.Get)
					} else {
						serp.With(managePermission).Post("/", notImplemented)
						serp.Get("/", notImplemented)
						serp.Get("/{id}", notImplemented)
					}
				})
				scoped.Route("/serp", func(serp chi.Router) {
					if deps.SERPHandler != nil {
						serp.Get("/history", deps.SERPHandler.List)
						serp.Get("/{id}", deps.SERPHandler.Get)
					} else {
						serp.Get("/history", notImplemented)
						serp.Get("/{id}", notImplemented)
					}
				})

				// Recommendations — PATCH requires write access
				scoped.Route("/recommendations", func(rec chi.Router) {
					if deps.RecommendationHandler != nil {
						rec.Get("/", deps.RecommendationHandler.List)
						rec.Get("/{id}", deps.RecommendationHandler.Get)
						rec.With(managePermission).Post("/generate", deps.RecommendationHandler.TriggerGenerate)
						rec.With(managePermission).Patch("/{id}", deps.RecommendationHandler.UpdateStatus)
						rec.With(managePermission).Post("/{id}/resolve", deps.RecommendationHandler.Resolve)
						rec.With(managePermission).Post("/{id}/dismiss", deps.RecommendationHandler.Dismiss)
					} else {
						rec.Get("/", notImplemented)
						rec.Get("/{id}", notImplemented)
						rec.With(managePermission).Post("/generate", notImplemented)
						rec.With(managePermission).Patch("/{id}", notImplemented)
						rec.With(managePermission).Post("/{id}/resolve", notImplemented)
						rec.With(managePermission).Post("/{id}/dismiss", notImplemented)
					}
				})

				// Strategies (bid automation)
				scoped.Route("/strategies", func(st chi.Router) {
					st.Use(middleware.RequireFinancialAccess())
					if deps.StrategyHandler != nil {
						st.Get("/", deps.StrategyHandler.List)
						st.Post("/", deps.StrategyHandler.Create)
						st.Get("/{id}", deps.StrategyHandler.Get)
						st.Get("/{id}/activity", deps.StrategyHandler.Activity)
						st.Get("/{id}/runs", deps.StrategyHandler.ListEvaluationRuns)
						st.Get("/{id}/runs/{runId}", deps.StrategyHandler.GetEvaluationRun)
						st.Post("/{id}/run", deps.StrategyHandler.TriggerRun)
						st.Post("/{id}/runs", deps.StrategyHandler.TriggerRun)
						st.Put("/{id}/rollout", deps.StrategyHandler.UpdateRollout)
						st.Get("/{id}/shadow-decisions", deps.StrategyHandler.ListShadowDecisions)
						st.Put("/{id}", deps.StrategyHandler.Update)
						st.Delete("/{id}", deps.StrategyHandler.Delete)
						st.Post("/{id}/attach", deps.StrategyHandler.Attach)
						st.Delete("/{id}/bindings/{bindingId}", deps.StrategyHandler.Detach)
						st.Put("/{id}/bindings/{bindingId}/rollout", deps.StrategyHandler.UpdateBindingRollout)
					} else {
						st.Get("/", notImplemented)
						st.Post("/", notImplemented)
					}
				})

				// Campaign actions (start/pause/stop/bids)
				if deps.CampaignActionHandler != nil {
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/start", deps.CampaignActionHandler.Start)
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/pause", deps.CampaignActionHandler.Pause)
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/stop", deps.CampaignActionHandler.Stop)
					scoped.With(middleware.RequireFinancialAccess()).Patch("/campaigns/{id}/name", deps.CampaignActionHandler.Rename)
					scoped.With(middleware.RequireFinancialAccess()).Delete("/campaigns/{id}", deps.CampaignActionHandler.Delete)
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/bids", deps.CampaignActionHandler.SetBid)
					scoped.With(middleware.RequireFinancialAccess()).Patch("/campaigns/{id}/products", deps.CampaignActionHandler.UpdateProducts)
					scoped.Get("/campaigns/{id}/bids/min", deps.CampaignActionHandler.MinimumBids)
					scoped.Get("/campaigns/{id}/daily-limit", deps.CampaignActionHandler.GetDailyLimit)
					scoped.With(middleware.RequireFinancialAccess()).Put("/campaigns/{id}/daily-limit", deps.CampaignActionHandler.UpdateDailyLimit)
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/cluster-bids", deps.CampaignActionHandler.SetClusterBid)
					scoped.With(middleware.RequireFinancialAccess()).Delete("/campaigns/{id}/cluster-bids", deps.CampaignActionHandler.DeleteClusterBid)
					scoped.Get("/campaigns/{id}/cluster-minus", deps.CampaignActionHandler.GetClusterMinus)
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/cluster-minus", deps.CampaignActionHandler.SetClusterMinus)
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/budget/deposit", deps.CampaignActionHandler.DepositBudget)
					scoped.Get("/campaigns/{id}/bid-history", deps.CampaignActionHandler.BidHistory)
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/bid-history/{changeId}/rollback", deps.CampaignActionHandler.RollbackBidChange)
					scoped.Get("/campaigns/{id}/minus-phrases", deps.CampaignActionHandler.ListMinusPhrases)
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/minus-phrases", deps.CampaignActionHandler.AddMinusPhrase)
					scoped.With(middleware.RequireFinancialAccess()).Delete("/campaigns/{id}/minus-phrases/{phraseId}", deps.CampaignActionHandler.DeleteMinusPhrase)
					scoped.Get("/campaigns/{id}/plus-phrases", deps.CampaignActionHandler.ListPlusPhrases)
					scoped.With(middleware.RequireFinancialAccess()).Post("/campaigns/{id}/plus-phrases", deps.CampaignActionHandler.AddPlusPhrase)
					scoped.With(middleware.RequireFinancialAccess()).Delete("/campaigns/{id}/plus-phrases/{phraseId}", deps.CampaignActionHandler.DeletePlusPhrase)
					scoped.With(middleware.RequireFinancialAccess()).Post("/recommendations/{id}/apply", deps.CampaignActionHandler.ApplyRecommendation)
				}

				// Semantics & Keywords
				if deps.SemanticsHandler != nil {
					scoped.Get("/keywords", deps.SemanticsHandler.ListKeywords)
					scoped.With(managePermission).Post("/keywords/collect", deps.SemanticsHandler.CollectKeywords)
					scoped.With(managePermission).Post("/keywords/cluster", deps.SemanticsHandler.AutoCluster)
					scoped.Get("/keyword-clusters", deps.SemanticsHandler.ListClusters)
				}

				// Product Events
				if deps.ProductEventHandler != nil {
					scoped.Get("/product-events", deps.ProductEventHandler.ListByWorkspace)
					scoped.Get("/products/{id}/events", deps.ProductEventHandler.ListByProduct)
				}

				// Product economics — manual/CSV inputs for margin-aware decisions
				scoped.Route("/product-economics", func(economics chi.Router) {
					if deps.ProductEconomicsHandler != nil {
						economics.Get("/", deps.ProductEconomicsHandler.List)
						economics.With(managePermission).Post("/import", deps.ProductEconomicsHandler.Import)
					} else {
						economics.Get("/", notImplemented)
						economics.With(managePermission).Post("/import", notImplemented)
					}
				})

				// Repricer — WB product prices
				scoped.Route("/prices", func(prices chi.Router) {
					if deps.PriceHandler != nil {
						prices.Get("/", deps.PriceHandler.List)
						prices.With(managePermission).Post("/sync", deps.PriceHandler.TriggerSync)
						prices.With(managePermission).Post("/bulk", deps.PriceHandler.Bulk)
						prices.Get("/quarantine", deps.PriceHandler.ListQuarantine)
						prices.Get("/cabinets-status", deps.PriceHandler.CabinetsStatus)
						prices.Get("/heatmap", deps.PriceHandler.Heatmap)
						prices.Get("/health", deps.PriceHandler.Health)
						prices.With(managePermission).Post("/pause", deps.PriceHandler.Pause)
					} else {
						prices.Get("/", notImplemented)
						prices.With(managePermission).Post("/sync", notImplemented)
						prices.With(managePermission).Post("/bulk", notImplemented)
						prices.Get("/quarantine", notImplemented)
						prices.Get("/cabinets-status", notImplemented)
						prices.Get("/heatmap", notImplemented)
						prices.Get("/health", notImplemented)
						prices.With(managePermission).Post("/pause", notImplemented)
					}
				})
				scoped.Route("/price-changes", func(pc chi.Router) {
					if deps.PriceHandler != nil {
						pc.Get("/", deps.PriceHandler.ListChanges)
						pc.With(managePermission).Post("/{changeId}/rollback", deps.PriceHandler.Rollback)
					} else {
						pc.Get("/", notImplemented)
						pc.With(managePermission).Post("/{changeId}/rollback", notImplemented)
					}
				})
				if deps.PriceHandler != nil {
					scoped.Get("/price-upload-tasks", deps.PriceHandler.ListUploadTasks)
					scoped.With(managePermission).Post("/repricer/run", deps.PriceHandler.Run)
				}
				scoped.Route("/price-schedules", func(ps chi.Router) {
					if deps.PriceHandler != nil {
						ps.Get("/", deps.PriceHandler.ListSchedules)
						ps.With(managePermission).Post("/", deps.PriceHandler.CreateSchedule)
						ps.With(managePermission).Delete("/{scheduleId}", deps.PriceHandler.CancelSchedule)
					} else {
						ps.Get("/", notImplemented)
						ps.With(managePermission).Post("/", notImplemented)
						ps.With(managePermission).Delete("/{scheduleId}", notImplemented)
					}
				})

				// Ozon module (phase 1: reads; phase 2: management writes)
				scoped.Route("/ozon", func(oz chi.Router) {
					if deps.OzonHandler != nil {
						oz.Get("/campaigns", deps.OzonHandler.ListCampaigns)
						oz.Get("/campaigns/{id}", deps.OzonHandler.GetCampaign)
						oz.Get("/campaigns/{id}/stats", deps.OzonHandler.CampaignStats)
						oz.Get("/campaigns/{id}/bids/competitive", deps.OzonHandler.CompetitiveBids)
						oz.Get("/campaigns/{id}/insights", deps.OzonHandler.CampaignProductInsights)
						oz.Get("/prices", deps.OzonHandler.ListPrices)
						oz.Get("/bid-changes", deps.OzonHandler.ListBidChanges)
						oz.Get("/cpo/overview", deps.OzonHandler.CPOOverview)
						oz.Get("/cpo/products", deps.OzonHandler.ListCPOProducts)
						oz.Get("/cpo/orders", deps.OzonHandler.ListCPOOrders)
						oz.Get("/limits", deps.OzonHandler.BidLimits)
						oz.With(managePermission).Post("/campaigns/{id}/activate", deps.OzonHandler.ActivateCampaign)
						oz.With(managePermission).Post("/campaigns/{id}/deactivate", deps.OzonHandler.DeactivateCampaign)
						oz.With(managePermission).Patch("/campaigns/{id}/budget", deps.OzonHandler.UpdateBudget)
						oz.With(managePermission).Put("/campaigns/{id}/products/bids", deps.OzonHandler.SetProductBids)
						oz.With(managePermission).Post("/cpo/enable", deps.OzonHandler.EnableCPO)
						oz.With(managePermission).Post("/cpo/disable", deps.OzonHandler.DisableCPO)
						oz.With(managePermission).Post("/cpo/bids", deps.OzonHandler.SetCPOBids)
						oz.With(managePermission).Post("/cpo/rate", deps.OzonHandler.SetCPORate)
						oz.With(managePermission).Post("/cpo/activate", deps.OzonHandler.ActivateCPO)
						oz.With(managePermission).Post("/cpo/deactivate", deps.OzonHandler.DeactivateCPO)
						oz.With(managePermission).Post("/sync", deps.OzonHandler.TriggerSync)
						// Phase 4: repricer (price changes audit, manual bulk, rollback)
						oz.Get("/price-changes", deps.OzonHandler.ListPriceChanges)
						oz.Post("/price-changes/{id}/reconcile", deps.OzonHandler.ReconcilePriceChange)
						oz.With(managePermission).Post("/price-changes/{id}/rollback", deps.OzonHandler.RollbackPriceChange)
						oz.With(managePermission).Post("/prices/bulk", deps.OzonHandler.BulkPrices)
						oz.With(managePermission).Post("/repricer/run", deps.OzonHandler.TriggerRepricerRun)
						// Phase 5: repricer parity (calendar, pause, health)
						oz.Get("/price-schedules", deps.OzonHandler.ListPriceSchedules)
						oz.With(managePermission).Post("/price-schedules", deps.OzonHandler.CreatePriceSchedule)
						oz.With(managePermission).Delete("/price-schedules/{id}", deps.OzonHandler.CancelPriceSchedule)
						oz.With(managePermission).Post("/prices/pause", deps.OzonHandler.PauseRepricer)
						oz.Get("/repricer/health", deps.OzonHandler.RepricerHealth)
						// Phase 6: postings-based 7×24 heatmap (peak-hours strategy)
						oz.Get("/prices/heatmap", deps.OzonHandler.PricesHeatmap)
						// Search queries (phrases report mirror)
						oz.Get("/search-queries", deps.OzonHandler.ListSearchQueries)
						// Phase 3: AI autopilot (runs, decisions, copilot approvals)
						oz.Get("/ai/runs", deps.OzonHandler.AIListRuns)
						oz.Get("/ai/impact", deps.OzonHandler.AIImpact)
						oz.Get("/ai/readiness", deps.OzonHandler.AIReadiness)
						oz.Get("/ai/weekly-report", deps.OzonHandler.AIWeeklyReport)
						oz.Get("/ai/decisions", deps.OzonHandler.AIListDecisions)
						oz.With(managePermission).Post("/ai/run", deps.OzonHandler.AITriggerRun)
						oz.With(managePermission).Post("/ai/decisions/{id}/approve", deps.OzonHandler.AIApproveDecision)
						oz.With(managePermission).Post("/ai/decisions/{id}/reject", deps.OzonHandler.AIRejectDecision)
						oz.With(managePermission).Post("/ai/decisions/approve-batch", deps.OzonHandler.AIApproveDecisionsBatch)
						oz.With(managePermission).Post("/ai/decisions/reject-batch", deps.OzonHandler.AIRejectDecisionsBatch)
					} else {
						oz.Get("/campaigns", notImplemented)
						oz.Get("/campaigns/{id}", notImplemented)
						oz.Get("/campaigns/{id}/stats", notImplemented)
						oz.Get("/prices", notImplemented)
						oz.With(managePermission).Post("/sync", notImplemented)
					}
				})

				// SEO Analysis
				if deps.SEOHandler != nil {
					scoped.With(managePermission).Post("/seo/analyze", deps.SEOHandler.AnalyzeAll)
					scoped.Get("/products/{id}/seo", deps.SEOHandler.GetProductAnalysis)
				}

				// Delivery Data
				if deps.DeliveryHandler != nil {
					scoped.With(managePermission).Post("/delivery/collect", deps.DeliveryHandler.Collect)
				}

				// Competitors
				if deps.CompetitorHandler != nil {
					scoped.Get("/competitors", deps.CompetitorHandler.List)
					scoped.Get("/products/{id}/competitors", deps.CompetitorHandler.ListByProduct)
					scoped.With(managePermission).Post("/competitors/extract", deps.CompetitorHandler.Extract)
				}

				// Exports — POST requires write access
				scoped.Route("/exports", func(exp chi.Router) {
					if deps.ExportHandler != nil {
						exp.Get("/", deps.ExportHandler.List)
						exp.With(managePermission).Post("/", deps.ExportHandler.Create)
						exp.Get("/{id}", deps.ExportHandler.Get)
						exp.Get("/{id}/download", deps.ExportHandler.Download)
					} else {
						exp.Get("/", notImplemented)
						exp.With(managePermission).Post("/", notImplemented)
						exp.Get("/{id}", notImplemented)
						exp.Get("/{id}/download", notImplemented)
					}
				})

				// Audit Logs — owner/manager only
				if deps.AuditLogHandler != nil {
					scoped.With(managePermission).Get("/audit-logs", deps.AuditLogHandler.List)
				} else {
					scoped.With(managePermission).Get("/audit-logs", notImplemented)
				}

				// Job Runs
				scoped.Route("/job-runs", func(jobRuns chi.Router) {
					if deps.JobRunHandler != nil {
						jobRuns.Get("/", deps.JobRunHandler.List)
						jobRuns.Get("/{id}", deps.JobRunHandler.Get)
						jobRuns.With(managePermission).Post("/{id}/retry", deps.JobRunHandler.Retry)
					} else {
						jobRuns.Get("/", notImplemented)
						jobRuns.Get("/{id}", notImplemented)
						jobRuns.With(managePermission).Post("/{id}/retry", notImplemented)
					}
				})

				// Workspace Settings
				scoped.Route("/settings", func(settings chi.Router) {
					settings.Use(managePermission)
					if deps.WorkspaceSettingsHandler != nil {
						settings.Get("/", deps.WorkspaceSettingsHandler.GetSettings)
						settings.Put("/", deps.WorkspaceSettingsHandler.UpdateSettings)
						settings.Get("/thresholds", deps.WorkspaceSettingsHandler.GetThresholds)
					} else {
						settings.Get("/", notImplemented)
						settings.Put("/", notImplemented)
						settings.Get("/thresholds", notImplemented)
					}
				})

				// SSE Events
				if deps.EventsHandler != nil {
					scoped.Get("/events", deps.EventsHandler.Stream)
				} else {
					scoped.Get("/events", notImplemented)
				}

				// Extension
				scoped.Route("/extension", func(ext chi.Router) {
					if deps.ExtensionHandler != nil {
						ext.Post("/sessions", deps.ExtensionHandler.CreateSession)
						ext.Post("/session/start", deps.ExtensionHandler.CreateSession)
						ext.Post("/token/exchange", deps.ExtensionHandler.ExchangeToken)
						ext.Post("/context", deps.ExtensionHandler.CreateContext)
						ext.Post("/page-context", deps.ExtensionHandler.CreatePageContext)
						ext.Post("/bid-snapshots", deps.ExtensionHandler.CreateBidSnapshots)
						ext.Post("/position-snapshots", deps.ExtensionHandler.CreatePositionSnapshots)
						ext.Post("/ui-signals", deps.ExtensionHandler.CreateUISignals)
						ext.Post("/network-captures/batch", deps.ExtensionHandler.CreateNetworkCaptures)
						ext.Post("/dom-row-snapshots", deps.ExtensionHandler.CreateDOMRowSnapshots)
						ext.Get("/version", deps.ExtensionHandler.Version)
						ext.Get("/widgets/search", deps.ExtensionHandler.SearchWidget)
						ext.Get("/widgets/product", deps.ExtensionHandler.ProductWidget)
						ext.Get("/widgets/campaign", deps.ExtensionHandler.CampaignWidget)
						ext.Get("/evidence-summary", deps.ExtensionHandler.EvidenceSummary)
						ext.Get("/evidence-debug", deps.ExtensionHandler.EvidenceDebug)
						ext.Get("/evidence-debug/report", deps.ExtensionHandler.EvidenceSupportReport)
					} else {
						ext.Post("/sessions", notImplemented)
						ext.Post("/session/start", notImplemented)
						ext.Post("/token/exchange", notImplemented)
						ext.Post("/context", notImplemented)
						ext.Post("/page-context", notImplemented)
						ext.Post("/bid-snapshots", notImplemented)
						ext.Post("/position-snapshots", notImplemented)
						ext.Post("/ui-signals", notImplemented)
						ext.Post("/network-captures/batch", notImplemented)
						ext.Post("/dom-row-snapshots", notImplemented)
						ext.Get("/version", notImplemented)
						ext.Get("/widgets/search", notImplemented)
						ext.Get("/widgets/product", notImplemented)
						ext.Get("/widgets/campaign", notImplemented)
						ext.Get("/evidence-summary", notImplemented)
						ext.Get("/evidence-debug", notImplemented)
						ext.Get("/evidence-debug/report", notImplemented)
					}
				})
			})
		})
	})

	return r
}
