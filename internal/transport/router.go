package transport

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
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
type RouterDeps struct {
	CORSAllowOrigins []string
	RateLimit        RateLimitOpts
	JWTSecret             string
	MembershipChecker     middleware.MembershipChecker
	WorkspaceResolver     middleware.WorkspaceResolver
	Authenticator         middleware.Authenticator
	DocsHandler           *handler.DocsHandler
	HealthHandler         *handler.HealthHandler
	AuthHandler           *handler.AuthHandler
	WorkspaceHandler      *handler.WorkspaceHandler
	SellerCabinetHandler  *handler.SellerCabinetHandler
	AdsReadHandler        *handler.AdsReadHandler
	CampaignHandler       *handler.CampaignHandler
	PhraseHandler         *handler.PhraseHandler
	BidHandler            *handler.BidHandler
	ProductHandler        *handler.ProductHandler
	PositionHandler       *handler.PositionHandler
	SERPHandler           *handler.SERPHandler
	RecommendationHandler *handler.RecommendationHandler
	ExportHandler         *handler.ExportHandler
	ExtensionHandler      *handler.ExtensionHandler
	AuditLogHandler       *handler.AuditLogHandler
	JobRunHandler         *handler.JobRunHandler
	EventsHandler            *handler.EventsHandler
	WorkspaceSettingsHandler *handler.WorkspaceSettingsHandler
	StrategyHandler          *handler.StrategyHandler
	CampaignActionHandler    *handler.CampaignActionHandler
	SemanticsHandler         *handler.SemanticsHandler
	CompetitorHandler        *handler.CompetitorHandler
	DeliveryHandler          *handler.DeliveryHandler
	SEOHandler               *handler.SEOHandler
	ProductEventHandler      *handler.ProductEventHandler
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
		authMiddleware = middleware.SharedAuth(deps.Authenticator)
	}

	tenantMiddleware := middleware.TenantScope(deps.MembershipChecker)
	if deps.WorkspaceResolver != nil {
		tenantMiddleware = middleware.SharedTenantScope(deps.WorkspaceResolver)
	}

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

	// --- /api/v1 group ---
	r.Route("/api/v1", func(v1 chi.Router) {
		if deps.RateLimit.RequestsPerSecond > 0 {
			v1.Use(middleware.RateLimit(middleware.RateLimitConfig{
				RequestsPerSecond: deps.RateLimit.RequestsPerSecond,
				Burst:             deps.RateLimit.Burst,
			}))
		}
		// --- Public auth routes ---
		v1.Route("/auth", func(auth chi.Router) {
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
			protected.Use(authMiddleware)

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

				if deps.WorkspaceHandler != nil {
					ws.Get("/", deps.WorkspaceHandler.Get)
				} else {
					ws.Get("/", notImplemented)
				}

				// Members management — owner/manager only
				ws.Route("/members", func(members chi.Router) {
					if deps.WorkspaceHandler != nil {
						members.Get("/", deps.WorkspaceHandler.ListMembers)
					} else {
						members.Get("/", notImplemented)
					}

					members.Group(func(writeMembers chi.Router) {
						writeMembers.Use(middleware.RequireRole(domain.RoleOwner, domain.RoleManager))
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

				// Seller Cabinets — write requires owner/manager
				scoped.Route("/seller-cabinets", func(sc chi.Router) {
					sc.Use(middleware.RequireWriteAccess())
					if deps.SellerCabinetHandler != nil {
						sc.Post("/", deps.SellerCabinetHandler.Create)
						sc.Get("/", deps.SellerCabinetHandler.List)
						sc.Get("/{id}", deps.SellerCabinetHandler.Get)
						sc.Get("/{id}/campaigns", deps.SellerCabinetHandler.ListCampaigns)
						sc.Get("/{id}/products", deps.SellerCabinetHandler.ListProducts)
						sc.Post("/{id}/sync", deps.SellerCabinetHandler.TriggerSync)
						sc.Delete("/{id}", deps.SellerCabinetHandler.Delete)
					} else {
						sc.Post("/", notImplemented)
						sc.Get("/", notImplemented)
						sc.Get("/{id}", notImplemented)
						sc.Get("/{id}/campaigns", notImplemented)
						sc.Get("/{id}/products", notImplemented)
						sc.Post("/{id}/sync", notImplemented)
						sc.Delete("/{id}", notImplemented)
					}
				})
				scoped.Route("/cabinets", func(sc chi.Router) {
					sc.Use(middleware.RequireWriteAccess())
					if deps.SellerCabinetHandler != nil {
						sc.Post("/", deps.SellerCabinetHandler.Create)
						sc.Get("/", deps.SellerCabinetHandler.List)
						sc.Get("/{id}", deps.SellerCabinetHandler.Get)
						sc.Get("/{id}/campaigns", deps.SellerCabinetHandler.ListCampaigns)
						sc.Get("/{id}/products", deps.SellerCabinetHandler.ListProducts)
						sc.Post("/{id}/sync", deps.SellerCabinetHandler.TriggerSync)
						sc.Delete("/{id}", deps.SellerCabinetHandler.Delete)
					} else {
						sc.Post("/", notImplemented)
						sc.Get("/", notImplemented)
						sc.Get("/{id}", notImplemented)
						sc.Get("/{id}/campaigns", notImplemented)
						sc.Get("/{id}/products", notImplemented)
						sc.Post("/{id}/sync", notImplemented)
						sc.Delete("/{id}", notImplemented)
					}
				})

				// Campaigns — read-only
				scoped.Route("/ads", func(a chi.Router) {
					if deps.AdsReadHandler != nil {
						a.Get("/overview", deps.AdsReadHandler.Overview)
						a.Get("/products", deps.AdsReadHandler.ListProducts)
						a.Get("/products/{id}", deps.AdsReadHandler.GetProduct)
						a.Get("/campaigns", deps.AdsReadHandler.ListCampaigns)
						a.Get("/campaigns/{id}", deps.AdsReadHandler.GetCampaign)
						a.Get("/queries", deps.AdsReadHandler.ListQueries)
						a.Get("/queries/{id}", deps.AdsReadHandler.GetQuery)
					} else {
						a.Get("/overview", notImplemented)
						a.Get("/products", notImplemented)
						a.Get("/products/{id}", notImplemented)
						a.Get("/campaigns", notImplemented)
						a.Get("/campaigns/{id}", notImplemented)
						a.Get("/queries", notImplemented)
						a.Get("/queries/{id}", notImplemented)
					}
				})

				// Campaigns — read-only
				scoped.Route("/campaigns", func(c chi.Router) {
					if deps.CampaignHandler != nil {
						c.Get("/", deps.CampaignHandler.List)
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
						b.With(middleware.RequireWriteAccess()).Post("/history", deps.BidHandler.Create)
						b.Get("/history", deps.BidHandler.ListHistory)
						b.Get("/estimates", deps.BidHandler.GetEstimate)
					} else {
						b.With(middleware.RequireWriteAccess()).Post("/history", notImplemented)
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
						pos.With(middleware.RequireWriteAccess()).Post("/targets", deps.PositionHandler.CreateTarget)
						pos.Get("/targets", deps.PositionHandler.ListTargets)
						pos.With(middleware.RequireWriteAccess()).Post("/", deps.PositionHandler.Create)
						pos.Get("/", deps.PositionHandler.List)
						pos.Get("/history", deps.PositionHandler.List)
						pos.Get("/aggregate", deps.PositionHandler.Aggregate)
					} else {
						pos.With(middleware.RequireWriteAccess()).Post("/targets", notImplemented)
						pos.Get("/targets", notImplemented)
						pos.With(middleware.RequireWriteAccess()).Post("/", notImplemented)
						pos.Get("/", notImplemented)
						pos.Get("/history", notImplemented)
						pos.Get("/aggregate", notImplemented)
					}
				})

				// SERP Snapshots
				scoped.Route("/serp-snapshots", func(serp chi.Router) {
					if deps.SERPHandler != nil {
						serp.With(middleware.RequireWriteAccess()).Post("/", deps.SERPHandler.Create)
						serp.Get("/", deps.SERPHandler.List)
						serp.Get("/{id}", deps.SERPHandler.Get)
					} else {
						serp.With(middleware.RequireWriteAccess()).Post("/", notImplemented)
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
						rec.With(middleware.RequireWriteAccess()).Post("/generate", deps.RecommendationHandler.TriggerGenerate)
						rec.With(middleware.RequireWriteAccess()).Patch("/{id}", deps.RecommendationHandler.UpdateStatus)
						rec.With(middleware.RequireWriteAccess()).Post("/{id}/resolve", deps.RecommendationHandler.Resolve)
						rec.With(middleware.RequireWriteAccess()).Post("/{id}/dismiss", deps.RecommendationHandler.Dismiss)
					} else {
						rec.Get("/", notImplemented)
						rec.Get("/{id}", notImplemented)
						rec.With(middleware.RequireWriteAccess()).Post("/generate", notImplemented)
						rec.With(middleware.RequireWriteAccess()).Patch("/{id}", notImplemented)
						rec.With(middleware.RequireWriteAccess()).Post("/{id}/resolve", notImplemented)
						rec.With(middleware.RequireWriteAccess()).Post("/{id}/dismiss", notImplemented)
					}
				})

				// Strategies (bid automation)
				scoped.Route("/strategies", func(st chi.Router) {
					st.Use(middleware.RequireWriteAccess())
					if deps.StrategyHandler != nil {
						st.Get("/", deps.StrategyHandler.List)
						st.Post("/", deps.StrategyHandler.Create)
						st.Get("/{id}", deps.StrategyHandler.Get)
						st.Put("/{id}", deps.StrategyHandler.Update)
						st.Delete("/{id}", deps.StrategyHandler.Delete)
						st.Post("/{id}/attach", deps.StrategyHandler.Attach)
						st.Delete("/{id}/bindings/{bindingId}", deps.StrategyHandler.Detach)
					} else {
						st.Get("/", notImplemented)
						st.Post("/", notImplemented)
					}
				})

				// Campaign actions (start/pause/stop/bids)
				if deps.CampaignActionHandler != nil {
					scoped.Post("/campaigns/{id}/start", deps.CampaignActionHandler.Start)
					scoped.Post("/campaigns/{id}/pause", deps.CampaignActionHandler.Pause)
					scoped.Post("/campaigns/{id}/stop", deps.CampaignActionHandler.Stop)
					scoped.Post("/campaigns/{id}/bids", deps.CampaignActionHandler.SetBid)
					scoped.Get("/campaigns/{id}/bid-history", deps.CampaignActionHandler.BidHistory)
					scoped.Get("/campaigns/{id}/minus-phrases", deps.CampaignActionHandler.ListMinusPhrases)
					scoped.Post("/campaigns/{id}/minus-phrases", deps.CampaignActionHandler.AddMinusPhrase)
					scoped.Delete("/campaigns/{id}/minus-phrases/{phraseId}", deps.CampaignActionHandler.DeleteMinusPhrase)
					scoped.Get("/campaigns/{id}/plus-phrases", deps.CampaignActionHandler.ListPlusPhrases)
					scoped.Post("/campaigns/{id}/plus-phrases", deps.CampaignActionHandler.AddPlusPhrase)
					scoped.Delete("/campaigns/{id}/plus-phrases/{phraseId}", deps.CampaignActionHandler.DeletePlusPhrase)
					scoped.Post("/recommendations/{id}/apply", deps.CampaignActionHandler.ApplyRecommendation)
				}

				// Semantics & Keywords
				if deps.SemanticsHandler != nil {
					scoped.Get("/keywords", deps.SemanticsHandler.ListKeywords)
					scoped.Post("/keywords/collect", deps.SemanticsHandler.CollectKeywords)
					scoped.Post("/keywords/cluster", deps.SemanticsHandler.AutoCluster)
					scoped.Get("/keyword-clusters", deps.SemanticsHandler.ListClusters)
				}

				// Product Events
				if deps.ProductEventHandler != nil {
					scoped.Get("/product-events", deps.ProductEventHandler.ListByWorkspace)
					scoped.Get("/products/{id}/events", deps.ProductEventHandler.ListByProduct)
				}

				// SEO Analysis
				if deps.SEOHandler != nil {
					scoped.Post("/seo/analyze", deps.SEOHandler.AnalyzeAll)
					scoped.Get("/products/{id}/seo", deps.SEOHandler.GetProductAnalysis)
				}

				// Delivery Data
				if deps.DeliveryHandler != nil {
					scoped.Post("/delivery/collect", deps.DeliveryHandler.Collect)
				}

				// Competitors
				if deps.CompetitorHandler != nil {
					scoped.Get("/competitors", deps.CompetitorHandler.List)
					scoped.Get("/products/{id}/competitors", deps.CompetitorHandler.ListByProduct)
					scoped.Post("/competitors/extract", deps.CompetitorHandler.Extract)
				}

				// Exports — POST requires write access
				scoped.Route("/exports", func(exp chi.Router) {
					if deps.ExportHandler != nil {
						exp.Get("/", deps.ExportHandler.List)
						exp.With(middleware.RequireWriteAccess()).Post("/", deps.ExportHandler.Create)
						exp.Get("/{id}", deps.ExportHandler.Get)
						exp.Get("/{id}/download", deps.ExportHandler.Download)
					} else {
						exp.Get("/", notImplemented)
						exp.With(middleware.RequireWriteAccess()).Post("/", notImplemented)
						exp.Get("/{id}", notImplemented)
						exp.Get("/{id}/download", notImplemented)
					}
				})

				// Audit Logs — owner/manager only
				if deps.AuditLogHandler != nil {
					scoped.With(middleware.RequireRole(domain.RoleOwner, domain.RoleManager)).
						Get("/audit-logs", deps.AuditLogHandler.List)
				} else {
					scoped.With(middleware.RequireRole(domain.RoleOwner, domain.RoleManager)).
						Get("/audit-logs", notImplemented)
				}

				// Job Runs
				scoped.Route("/job-runs", func(jobRuns chi.Router) {
					if deps.JobRunHandler != nil {
						jobRuns.Get("/", deps.JobRunHandler.List)
						jobRuns.Get("/{id}", deps.JobRunHandler.Get)
						jobRuns.With(middleware.RequireWriteAccess()).Post("/{id}/retry", deps.JobRunHandler.Retry)
					} else {
						jobRuns.Get("/", notImplemented)
						jobRuns.Get("/{id}", notImplemented)
						jobRuns.With(middleware.RequireWriteAccess()).Post("/{id}/retry", notImplemented)
					}
				})

				// Workspace Settings
				scoped.Route("/settings", func(settings chi.Router) {
					settings.Use(middleware.RequireRole(domain.RoleOwner, domain.RoleManager))
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
						ext.Post("/context", deps.ExtensionHandler.CreateContext)
						ext.Post("/page-context", deps.ExtensionHandler.CreatePageContext)
						ext.Post("/bid-snapshots", deps.ExtensionHandler.CreateBidSnapshots)
						ext.Post("/position-snapshots", deps.ExtensionHandler.CreatePositionSnapshots)
						ext.Post("/ui-signals", deps.ExtensionHandler.CreateUISignals)
						ext.Post("/network-captures/batch", deps.ExtensionHandler.CreateNetworkCaptures)
						ext.Get("/version", deps.ExtensionHandler.Version)
						ext.Get("/widgets/search", deps.ExtensionHandler.SearchWidget)
						ext.Get("/widgets/product", deps.ExtensionHandler.ProductWidget)
						ext.Get("/widgets/campaign", deps.ExtensionHandler.CampaignWidget)
					} else {
						ext.Post("/sessions", notImplemented)
						ext.Post("/session/start", notImplemented)
						ext.Post("/context", notImplemented)
						ext.Post("/page-context", notImplemented)
						ext.Post("/bid-snapshots", notImplemented)
						ext.Post("/position-snapshots", notImplemented)
						ext.Post("/ui-signals", notImplemented)
						ext.Post("/network-captures/batch", notImplemented)
						ext.Get("/version", notImplemented)
						ext.Get("/widgets/search", notImplemented)
						ext.Get("/widgets/product", notImplemented)
						ext.Get("/widgets/campaign", notImplemented)
					}
				})
			})
		})
	})

	return r
}
