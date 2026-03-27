package transport

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/domain"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/handler"
	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/middleware"
)

// RouterDeps holds all dependencies needed to construct the router.
type RouterDeps struct {
	JWTSecret             string
	MembershipChecker     middleware.MembershipChecker
	HealthHandler         *handler.HealthHandler
	AuthHandler           *handler.AuthHandler
	WorkspaceHandler      *handler.WorkspaceHandler
	SellerCabinetHandler  *handler.SellerCabinetHandler
	CampaignHandler       *handler.CampaignHandler
	PhraseHandler         *handler.PhraseHandler
	ProductHandler        *handler.ProductHandler
	PositionHandler       *handler.PositionHandler
	SERPHandler           *handler.SERPHandler
	RecommendationHandler *handler.RecommendationHandler
	ExportHandler         *handler.ExportHandler
	ExtensionHandler      *handler.ExtensionHandler
	AuditLogHandler       *handler.AuditLogHandler
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

	// --- Global middleware ---
	r.Use(middleware.RequestID)
	r.Use(middleware.Logging)
	r.Use(middleware.Recovery)

	// --- Health (public, outside /api/v1) ---
	if deps.HealthHandler != nil {
		r.Get("/health/live", deps.HealthHandler.Live)
		r.Get("/health/ready", deps.HealthHandler.Ready)
	} else {
		r.Get("/health/live", notImplemented)
		r.Get("/health/ready", notImplemented)
	}

	// --- /api/v1 group ---
	r.Route("/api/v1", func(v1 chi.Router) {
		// --- Public auth routes ---
		v1.Route("/auth", func(auth chi.Router) {
			if deps.AuthHandler != nil {
				auth.Post("/register", deps.AuthHandler.Register)
				auth.Post("/login", deps.AuthHandler.Login)
				auth.Post("/refresh", deps.AuthHandler.Refresh)
				auth.Post("/logout", deps.AuthHandler.Logout)
			} else {
				auth.Post("/register", notImplemented)
				auth.Post("/login", notImplemented)
				auth.Post("/refresh", notImplemented)
				auth.Post("/logout", notImplemented)
			}
		})

		// --- Protected routes (require auth) ---
		v1.Group(func(protected chi.Router) {
			protected.Use(middleware.Auth(deps.JWTSecret))

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
				ws.Use(middleware.TenantScope(deps.MembershipChecker))

				if deps.WorkspaceHandler != nil {
					ws.Get("/", deps.WorkspaceHandler.Get)
				} else {
					ws.Get("/", notImplemented)
				}

				// Members management — owner/manager only
				ws.Route("/members", func(members chi.Router) {
					members.Use(middleware.RequireRole(domain.RoleOwner, domain.RoleManager))
					if deps.WorkspaceHandler != nil {
						members.Post("/", deps.WorkspaceHandler.InviteMember)
						members.Patch("/{memberId}", deps.WorkspaceHandler.UpdateMemberRole)
						members.Delete("/{memberId}", deps.WorkspaceHandler.RemoveMember)
					} else {
						members.Post("/", notImplemented)
						members.Patch("/{memberId}", notImplemented)
						members.Delete("/{memberId}", notImplemented)
					}
				})
			})

			// --- Workspace-scoped routes (auth + tenant via X-Workspace-ID header) ---
			protected.Group(func(scoped chi.Router) {
				scoped.Use(middleware.TenantScope(deps.MembershipChecker))

				// Seller Cabinets — write requires owner/manager
				scoped.Route("/seller-cabinets", func(sc chi.Router) {
					sc.Use(middleware.RequireWriteAccess())
					if deps.SellerCabinetHandler != nil {
						sc.Post("/", deps.SellerCabinetHandler.Create)
						sc.Get("/", deps.SellerCabinetHandler.List)
						sc.Get("/{id}", deps.SellerCabinetHandler.Get)
						sc.Delete("/{id}", deps.SellerCabinetHandler.Delete)
					} else {
						sc.Post("/", notImplemented)
						sc.Get("/", notImplemented)
						sc.Get("/{id}", notImplemented)
						sc.Delete("/{id}", notImplemented)
					}
				})

				// Campaigns — read-only
				scoped.Route("/campaigns", func(c chi.Router) {
					if deps.CampaignHandler != nil {
						c.Get("/", deps.CampaignHandler.List)
						c.Get("/{id}", deps.CampaignHandler.Get)
						c.Get("/{id}/stats", deps.CampaignHandler.GetStats)
						c.Get("/{id}/phrases", deps.CampaignHandler.ListPhrases)
					} else {
						c.Get("/", notImplemented)
						c.Get("/{id}", notImplemented)
						c.Get("/{id}/stats", notImplemented)
						c.Get("/{id}/phrases", notImplemented)
					}
				})

				// Phrases
				scoped.Route("/phrases", func(p chi.Router) {
					if deps.PhraseHandler != nil {
						p.Get("/{id}", deps.PhraseHandler.Get)
						p.Get("/{id}/stats", deps.PhraseHandler.GetStats)
						p.Get("/{id}/bids", deps.PhraseHandler.ListBids)
					} else {
						p.Get("/{id}", notImplemented)
						p.Get("/{id}/stats", notImplemented)
						p.Get("/{id}/bids", notImplemented)
					}
				})

				// Products
				scoped.Route("/products", func(p chi.Router) {
					if deps.ProductHandler != nil {
						p.Get("/", deps.ProductHandler.List)
						p.Get("/{id}", deps.ProductHandler.Get)
						p.Get("/{id}/positions", deps.ProductHandler.ListPositions)
					} else {
						p.Get("/", notImplemented)
						p.Get("/{id}", notImplemented)
						p.Get("/{id}/positions", notImplemented)
					}
				})

				// Positions
				scoped.Route("/positions", func(pos chi.Router) {
					if deps.PositionHandler != nil {
						pos.Get("/", deps.PositionHandler.List)
						pos.Get("/aggregate", deps.PositionHandler.Aggregate)
					} else {
						pos.Get("/", notImplemented)
						pos.Get("/aggregate", notImplemented)
					}
				})

				// SERP Snapshots
				scoped.Route("/serp-snapshots", func(serp chi.Router) {
					if deps.SERPHandler != nil {
						serp.Get("/", deps.SERPHandler.List)
						serp.Get("/{id}", deps.SERPHandler.Get)
					} else {
						serp.Get("/", notImplemented)
						serp.Get("/{id}", notImplemented)
					}
				})

				// Recommendations — PATCH requires write access
				scoped.Route("/recommendations", func(rec chi.Router) {
					if deps.RecommendationHandler != nil {
						rec.Get("/", deps.RecommendationHandler.List)
						rec.With(middleware.RequireWriteAccess()).Patch("/{id}", deps.RecommendationHandler.UpdateStatus)
					} else {
						rec.Get("/", notImplemented)
						rec.With(middleware.RequireWriteAccess()).Patch("/{id}", notImplemented)
					}
				})

				// Exports — POST requires write access
				scoped.Route("/exports", func(exp chi.Router) {
					if deps.ExportHandler != nil {
						exp.With(middleware.RequireWriteAccess()).Post("/", deps.ExportHandler.Create)
						exp.Get("/{id}", deps.ExportHandler.Get)
						exp.Get("/{id}/download", deps.ExportHandler.Download)
					} else {
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

				// Extension
				scoped.Route("/extension", func(ext chi.Router) {
					if deps.ExtensionHandler != nil {
						ext.Post("/sessions", deps.ExtensionHandler.CreateSession)
						ext.Post("/context", deps.ExtensionHandler.CreateContext)
						ext.Get("/version", deps.ExtensionHandler.Version)
					} else {
						ext.Post("/sessions", notImplemented)
						ext.Post("/context", notImplemented)
						ext.Get("/version", notImplemented)
					}
				})
			})
		})
	})

	return r
}
