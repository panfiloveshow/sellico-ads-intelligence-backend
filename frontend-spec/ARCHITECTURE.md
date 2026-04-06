# Sellico Ads Intelligence Frontend -- 3-Screen Architecture

## API Envelope Contract

Every backend response follows this shape:

```typescript
interface ApiResponse<T> {
  data: T;
  meta?: { page: number; per_page: number; total: number };
  errors?: { code: string; message: string; field?: string }[];
}
```

All workspace-scoped calls require the header `X-Workspace-ID: <uuid>`.
Date-range filter params: `?date_from=2024-01-01&date_to=2024-01-31`.

---

## Routing Map

```
/                          -> redirect to /dashboard
/dashboard                 -> Screen 1: CommandCenter
/campaigns/:id             -> Screen 2: EntityDetail (campaign mode)
/products/:id              -> Screen 2: EntityDetail (product mode)
/queries/:id               -> Screen 2: EntityDetail (query mode)
/settings                  -> Screen 3: Settings
/settings/:section         -> Screen 3: deep-link to section
```

---

## CSS Design System

```css
:root {
  /* --- Palette --- */
  --bg-primary: #FFFFFF;
  --bg-secondary: #F8F9FA;
  --bg-surface: #FFFFFF;
  --text-primary: #1A1A2E;
  --text-secondary: #6B7280;
  --border-color: #E5E7EB;
  --accent-blue: #2563EB;
  --accent-green: #16A34A;
  --accent-red: #DC2626;
  --accent-amber: #D97706;

  /* --- Typography scale (4px grid) --- */
  --text-xs: 0.75rem;
  --text-sm: 0.875rem;
  --text-base: 1rem;
  --text-lg: 1.125rem;
  --text-xl: 1.25rem;
  --text-2xl: 1.5rem;
  --text-3xl: 1.875rem;

  /* --- Spacing (8px grid) --- */
  --space-1: 4px;
  --space-2: 8px;
  --space-3: 12px;
  --space-4: 16px;
  --space-6: 24px;
  --space-8: 32px;
  --space-12: 48px;

  /* --- Layout --- */
  --sidebar-width: 240px;
  --header-height: 56px;
  --container-max: 1280px;

  /* --- Severity colors --- */
  --severity-critical: #DC2626;
  --severity-high: #EA580C;
  --severity-medium: #D97706;
  --severity-low: #2563EB;
}

[data-theme="dark"] {
  --bg-primary: #0F172A;
  --bg-secondary: #1E293B;
  --bg-surface: #1E293B;
  --text-primary: #F1F5F9;
  --text-secondary: #94A3B8;
  --border-color: #334155;
}
```

---

## File Structure

```
src/
  api/
    client.ts              # Axios instance, interceptors, workspace header injection
    endpoints.ts           # Typed API functions (one per backend route)
    types.ts               # TypeScript interfaces mirroring Go domain models
  hooks/
    useOverview.ts         # React Query hook for GET /ads/overview
    useCampaigns.ts        # React Query hook for GET /ads/campaigns
    useProducts.ts         # React Query hook for GET /ads/products
    useQueries.ts          # React Query hook for GET /ads/queries
    useCampaignDetail.ts   # React Query hook for campaign drilldown (stats, phrases, bids)
    useProductDetail.ts    # React Query hook for product drilldown (competitors, seo, events)
    useRecommendations.ts  # React Query hook for GET /recommendations
    useStrategies.ts       # React Query hook for GET /strategies CRUD
    useKeywords.ts         # React Query hook for keywords & clusters
    useCompetitors.ts      # React Query hook for competitors
    useExports.ts          # React Query hook for exports
    useJobRuns.ts          # React Query hook for job runs
    useSellerCabinets.ts   # React Query hook for seller cabinets
    useMutations.ts        # All POST/PATCH/DELETE mutation hooks
  screens/
    CommandCenter/
      CommandCenter.tsx
      MetricCards.tsx
      ActionCards.tsx
      CampaignTable.tsx
      QuickStats.tsx
    EntityDetail/
      EntityDetail.tsx
      CampaignDetail.tsx
      ProductDetail.tsx
      QueryDetail.tsx
      tabs/
        CampaignOverviewTab.tsx
        CampaignPhrasesTab.tsx
        CampaignProductsTab.tsx
        CampaignBidHistoryTab.tsx
        CampaignRecommendationsTab.tsx
        ProductOverviewTab.tsx
        ProductCompetitorsTab.tsx
        ProductSEOTab.tsx
        ProductEventsTab.tsx
        ProductPositionsTab.tsx
        QueryOverviewTab.tsx
    Settings/
      Settings.tsx
      StrategiesSection.tsx
      KeywordsSection.tsx
      CompetitorsSection.tsx
      ExportsSection.tsx
      JobsSection.tsx
  components/
    AppShell.tsx           # Header + sidebar + outlet
    ShopSelector.tsx       # Cabinet dropdown (global state)
    ThemeToggle.tsx        # Light/dark/system toggle
    DataTable.tsx          # Reusable sortable table
    MetricCard.tsx         # Single KPI card component
    SeverityChip.tsx       # Severity badge
    HealthStatusChip.tsx   # Health status badge
    DateRangePicker.tsx    # Period selector
    ConfirmDialog.tsx      # Confirmation modal
    EmptyState.tsx         # Zero-state placeholder
  providers/
    WorkspaceProvider.tsx  # Current workspace context
    ThemeProvider.tsx       # MUI theme + dark mode
  utils/
    formatters.ts          # formatRUB(), formatPercent(), formatROAS()
    queryKeys.ts           # React Query key factory
```

---

# SCREEN 1: COMMAND CENTER

## ASCII Wireframe

```
+------------------------------------------------------------------+
| [Logo]  Sellico Ads Intelligence    [Shop: v Dropdown] [Theme] [?]|
+------------------------------------------------------------------+
|                                                                    |
|  +-- Metric Cards (3) ------------------------------------+       |
|  | +----------------+ +----------------+ +----------------+|       |
|  | | Spend          | | Orders         | | ROAS           ||       |
|  | | 142,350 rub    | | 847            | | 5.2x           ||       |
|  | | +12% vs prev   | | -3% vs prev   | | +0.4 vs prev  ||       |
|  | +----------------+ +----------------+ +----------------+|       |
|  +---------------------------------------------------------+       |
|                                                                    |
|  +-- Action Cards (recommendations) ----------------------+       |
|  | [!] Campaign "Платья лето" has high ACoS (45%)         |       |
|  |     [Apply -20% bid]  [Pause Campaign]  [Dismiss]      |       |
|  |                                                         |       |
|  | [!] Keyword "платье миди" lost position (1 -> 8)       |       |
|  |     [Raise bid to 280]  [Dismiss]                      |       |
|  |                                                         |       |
|  | [i] Product 12345678 has SEO score 42/100              |       |
|  |     [View SEO]  [Dismiss]                              |       |
|  +---------------------------------------------------------+       |
|                                                                    |
|  +-- Campaign Table (sortable) ----------------------------+      |
|  | Name          | Status | Spend   | Orders | ROAS | Act  |      |
|  |---------------|--------|---------|--------|------|------|      |
|  | Платья лето   | active | 45,200  |  234   | 4.8x | ...  |      |
|  | Куртки зима   | active | 32,100  |  187   | 6.1x | ...  |      |
|  | Аксессуары    | paused | 12,400  |   43   | 2.1x | ...  |      |
|  |               |        |         |        |      |      |      |
|  |  [< 1 2 3 >] showing 1-20 of 56                  |      |      |
|  +---------------------------------------------------------+      |
|                                                                    |
|  +-- Quick Stats ---------+  +-- Top Queries ------------+       |
|  | Keywords:    1,247      |  | платье миди   234 clicks  |       |
|  | Competitors: 89         |  | куртка зимняя 187 clicks  |       |
|  | Avg SEO:     72/100     |  | аксессуары    43 clicks   |       |
|  +-------------------------+  +---------------------------+       |
+------------------------------------------------------------------+
```

## Component Hierarchy

```
<CommandCenter>
  <MetricCards overview={AdsOverview} />
    <MetricCard label="Spend" value={spend} delta={delta.spend} format="rub" />
    <MetricCard label="Orders" value={orders} delta={delta.orders} format="number" />
    <MetricCard label="ROAS" value={roas} delta={delta.roas} format="multiplier" />
  <ActionCards items={overview.attention} onApply={} onDismiss={} />
    <ActionCard item={AttentionItem} />
      <Button onClick={handleApply}>  -- maps to POST /recommendations/{id}/apply
      <Button onClick={handleDismiss}> -- maps to POST /recommendations/{id}/dismiss
  <CampaignTable campaigns={campaignSummaries} onRowClick={navigateToCampaign} />
    <DataTable
      columns={campaignColumns}
      data={campaigns}
      sortable={true}
      onSort={handleSort}
      pagination={{ page, perPage, total }}
    />
  <QuickStats overview={overview} />
</CommandCenter>
```

## Data Flow

```
On mount:
  1. useSellerCabinets()    -> GET /api/v1/seller-cabinets
     Populates ShopSelector dropdown.

  2. useOverview(cabinetId?, dateRange)
     -> GET /api/v1/ads/overview?seller_cabinet_id={id}&date_from=...&date_to=...
     Returns: AdsOverview (performance_compare, cabinets, attention, top_products,
              top_campaigns, top_queries, totals)

  3. useCampaigns(cabinetId?, dateRange, sort, page)
     -> GET /api/v1/ads/campaigns?seller_cabinet_id={id}&date_from=...&sort_by=spend&sort_dir=desc&page=1&per_page=20
     Returns: CampaignPerformanceSummary[]

  4. useRecommendations({ status: 'active', limit: 5 })
     -> GET /api/v1/recommendations?status=active&per_page=5
     Returns: Recommendation[] for ActionCards (only active ones)

Mutations:
  - Apply recommendation: POST /api/v1/recommendations/{id}/apply
    Invalidates: ['overview'], ['recommendations'], ['campaigns']
  - Dismiss recommendation: POST /api/v1/recommendations/{id}/dismiss
    Invalidates: ['recommendations']
  - Sync cabinet: POST /api/v1/seller-cabinets/{id}/sync
    Invalidates: ['overview'], ['campaigns']
```

## Key Interactions

| User Action | Component | API Call | Invalidation |
|---|---|---|---|
| Select shop from dropdown | ShopSelector | -- (local filter) | Refetch overview, campaigns |
| Change date range | DateRangePicker | -- (local filter) | Refetch overview, campaigns |
| Click "Apply" on recommendation | ActionCard | POST /recommendations/{id}/apply | overview, recommendations, campaigns |
| Click "Dismiss" on recommendation | ActionCard | POST /recommendations/{id}/dismiss | recommendations |
| Click campaign row | CampaignTable | navigate('/campaigns/:id') | -- |
| Sort campaign table | CampaignTable | GET /ads/campaigns?sort_by=... | -- |
| Click page in pagination | CampaignTable | GET /ads/campaigns?page=2 | -- |

---

# SCREEN 2: ENTITY DETAIL

## Route Resolution

```typescript
// Router config
{ path: '/campaigns/:id', element: <EntityDetail entityType="campaign" /> }
{ path: '/products/:id',  element: <EntityDetail entityType="product" /> }
{ path: '/queries/:id',   element: <EntityDetail entityType="query" /> }
```

## ASCII Wireframe -- Campaign Detail

```
+------------------------------------------------------------------+
| [<- Back]  Campaign: Платья лето         Status: active  [Pause] |
|            Cabinet: Мой магазин WB       Budget: 5,000/day       |
+------------------------------------------------------------------+
| [Overview] [Phrases] [Products] [Bid History] [Recommendations]   |
+------------------------------------------------------------------+
|                                                                    |
| TAB: Overview                                                      |
|  +-- Period Stats Chart --------+  +-- Summary Cards --------+    |
|  | [Line chart: spend, orders]  |  | Impressions: 45,200      |    |
|  | [Date range: 7d 14d 30d]    |  | Clicks: 1,847            |    |
|  |                              |  | CTR: 4.1%                |    |
|  |                              |  | CPC: 24.5 rub            |    |
|  +------------------------------+  | Conv Rate: 12.7%         |    |
|                                     +----------------------------+  |
|                                                                    |
| TAB: Phrases                                                       |
|  +--------------------------------------------------------------+ |
|  | Keyword          | Bid  | Impressions | Clicks | Spend | CTR  | |
|  |------------------|------|-------------|--------|-------|------| |
|  | платье миди      | 250  | 12,400      | 512    | 12,800| 4.1% | |
|  | платье летнее    | 180  | 8,200       | 328    | 5,904 | 4.0% | |
|  | [+ Add phrase]  [- Minus phrase]                               | |
|  +--------------------------------------------------------------+ |
|                                                                    |
| TAB: Bid History                                                   |
|  +--------------------------------------------------------------+ |
|  | Date       | Old Bid | New Bid | Reason        | Source      | |
|  |------------|---------|---------|---------------|-------------| |
|  | 2024-03-15 |   250   |   220   | ACoS > 30%   | strategy    | |
|  | 2024-03-14 |   200   |   250   | Position drop | manual      | |
|  +--------------------------------------------------------------+ |
+------------------------------------------------------------------+
```

## ASCII Wireframe -- Product Detail

```
+------------------------------------------------------------------+
| [<- Back]  Product: Платье миди 12345678     [WB link]           |
|            Cabinet: Мой магазин WB                                |
+------------------------------------------------------------------+
| [Overview] [Competitors] [SEO] [Events] [Positions]              |
+------------------------------------------------------------------+
|                                                                    |
| TAB: Overview                                                      |
|  +-- Product Card -------+  +-- Performance ---------+            |
|  | [Image]               |  | Campaigns: 3            |            |
|  | Brand: MyBrand        |  | Queries: 24             |            |
|  | Price: 2,490 rub      |  | Spend: 45,200 rub       |            |
|  | Category: Платья      |  | Orders: 234             |            |
|  +-----------------------+  +-------------------------+            |
|                                                                    |
| TAB: Competitors                                                   |
|  +--------------------------------------------------------------+ |
|  | NM ID      | Title           | Price | Rating | Position     | |
|  |------------|-----------------|-------|--------|----------- --| |
|  | 98765432   | Конкурент платье| 2,290 | 4.7    | 3            | |
|  | 87654321   | Аналог платье   | 1,990 | 4.5    | 7            | |
|  +--------------------------------------------------------------+ |
|  [Extract competitors from SERP]                                   |
|                                                                    |
| TAB: SEO                                                           |
|  +--------------------------------------------------------------+ |
|  | Overall Score: 72/100                                         | |
|  | Title Score: 85  | Description Score: 60 | Keywords Score: 71 | |
|  |                                                                | |
|  | Issues:                                                        | |
|  | [HIGH]   Missing keyword "летнее" in title                    | |
|  | [MEDIUM] Description too short (< 500 chars)                  | |
|  |                                                                | |
|  | Keyword Coverage:                                              | |
|  | [x] платье  [x] миди  [ ] летнее  [ ] женское                | |
|  +--------------------------------------------------------------+ |
|                                                                    |
| TAB: Events                                                        |
|  +--------------------------------------------------------------+ |
|  | Date       | Type          | Field   | Old        | New       | |
|  |------------|---------------|---------|------------|-----------|  |
|  | 2024-03-15 | price_change  | price   | 2,790      | 2,490     | |
|  | 2024-03-10 | content_change| title   | Платье ... | Платье ...| |
|  +--------------------------------------------------------------+ |
|                                                                    |
| TAB: Positions                                                     |
|  +--------------------------------------------------------------+ |
|  | Query           | Region | Position | Page | Checked        | |
|  |-----------------|--------|----------|------|----------------| |
|  | платье миди     | Moscow | 5        | 1    | 2024-03-15     | |
|  | платье женское  | Moscow | 12       | 1    | 2024-03-15     | |
|  +--------------------------------------------------------------+ |
+------------------------------------------------------------------+
```

## Component Hierarchy -- Campaign

```
<EntityDetail entityType="campaign">
  <CampaignDetail campaignId={id}>
    <CampaignHeader campaign={data} onStart={} onPause={} onStop={} />
    <Tabs value={tab} onChange={setTab}>
      <Tab label="Overview" />
      <Tab label="Phrases" />
      <Tab label="Products" />
      <Tab label="Bid History" />
      <Tab label="Recommendations" />
    </Tabs>

    {tab === 0 && <CampaignOverviewTab campaignId={id} dateRange={range} />}
      -> uses: GET /api/v1/campaigns/{id}/stats?date_from=...&date_to=...
      -> renders: line chart (MUI/recharts) + summary metric cards
      -> data type: CampaignStat[] with date, impressions, clicks, spend, orders, revenue

    {tab === 1 && <CampaignPhrasesTab campaignId={id} />}
      -> uses: GET /api/v1/campaigns/{id}/phrases?page=1&per_page=50
      -> renders: DataTable with phrase performance
      -> actions:
         - Click phrase row -> could expand inline or navigate
         - "Add plus phrase" -> POST /api/v1/campaigns/{id}/plus-phrases
         - "Add minus phrase" -> POST /api/v1/campaigns/{id}/minus-phrases
      -> data type: Phrase[] with keyword, current_bid, plus PhraseStat metrics

    {tab === 2 && <CampaignProductsTab campaignId={id} />}
      -> uses: GET /api/v1/ads/campaigns/{id} (related_products from summary)
      -> renders: product cards with image + metrics
      -> click product -> navigate('/products/:id')

    {tab === 3 && <CampaignBidHistoryTab campaignId={id} />}
      -> uses: GET /api/v1/campaigns/{id}/bid-history?page=1&per_page=50
      -> renders: DataTable with audit trail
      -> data type: BidChange[] with old_bid, new_bid, reason, source, created_at

    {tab === 4 && <CampaignRecommendationsTab campaignId={id} />}
      -> uses: GET /api/v1/campaigns/{id}/recommendations?status=active
      -> renders: recommendation cards with Apply/Dismiss
      -> mutations:
         - POST /api/v1/recommendations/{id}/apply
         - POST /api/v1/recommendations/{id}/dismiss
  </CampaignDetail>
</EntityDetail>
```

## Component Hierarchy -- Product

```
<EntityDetail entityType="product">
  <ProductDetail productId={id}>
    <ProductHeader product={data} />
    <Tabs>
      <Tab label="Overview" />
      <Tab label="Competitors" />
      <Tab label="SEO" />
      <Tab label="Events" />
      <Tab label="Positions" />
    </Tabs>

    {tab === 0 && <ProductOverviewTab productId={id} dateRange={range} />}
      -> uses: GET /api/v1/ads/products/{id}?date_from=...&date_to=...
      -> data type: ProductAdsSummary (performance, related_campaigns, top_queries, etc.)

    {tab === 1 && <ProductCompetitorsTab productId={id} />}
      -> uses: GET /api/v1/products/{id}/competitors
      -> renders: competitor DataTable
      -> action: POST /api/v1/competitors/extract { product_id }
      -> data type: Competitor[]

    {tab === 2 && <ProductSEOTab productId={id} />}
      -> uses: GET /api/v1/products/{id}/seo
      -> renders: score cards + issues list + keyword coverage grid
      -> action: POST /api/v1/seo/analyze { product_id }
      -> data type: SEOAnalysis

    {tab === 3 && <ProductEventsTab productId={id} />}
      -> uses: GET /api/v1/products/{id}/events?page=1&per_page=50
      -> renders: timeline DataTable
      -> data type: ProductEvent[]

    {tab === 4 && <ProductPositionsTab productId={id} />}
      -> uses: GET /api/v1/products/{id}/positions?page=1&per_page=50
      -> renders: position history DataTable
      -> action: POST /api/v1/positions/targets { product_id, query, region }
      -> data type: Position[]
  </ProductDetail>
</EntityDetail>
```

## Data Flow Summary -- Entity Detail

```
Campaign Detail mount (campaignId from URL):
  Parallel:
    1. GET /api/v1/ads/campaigns/{id}?date_from=...&date_to=...
       -> CampaignPerformanceSummary (header + overview tab)
    2. GET /api/v1/campaigns/{id}/stats?date_from=...&date_to=...
       -> CampaignStat[] (overview chart)

  On tab switch (lazy-loaded):
    Phrases:         GET /api/v1/campaigns/{id}/phrases
    Bid History:     GET /api/v1/campaigns/{id}/bid-history
    Recommendations: GET /api/v1/campaigns/{id}/recommendations

Product Detail mount (productId from URL):
  Parallel:
    1. GET /api/v1/ads/products/{id}?date_from=...&date_to=...
       -> ProductAdsSummary (header + overview tab)

  On tab switch (lazy-loaded):
    Competitors: GET /api/v1/products/{id}/competitors
    SEO:         GET /api/v1/products/{id}/seo
    Events:      GET /api/v1/products/{id}/events
    Positions:   GET /api/v1/products/{id}/positions
```

---

# SCREEN 3: SETTINGS

## ASCII Wireframe

```
+------------------------------------------------------------------+
| [Logo]  Settings                              [Shop: v] [Theme]   |
+------------------------------------------------------------------+
| +-- Sidebar ------+ +-- Content Area -------------------------+  |
| | Strategies       | |                                          | |
| | Keywords & SEO   | |  STRATEGIES                              | |
| | Competitors      | |  +--------------------------------------+| |
| | Exports          | |  | Name         | Type    | Active | Act || |
| | Jobs             | |  |--------------|---------|--------|-----|| |
| |                  | |  | ACoS Control | acos    |  yes   | Edit|| |
| |                  | |  | Night Pause  | daypart |  yes   | Edit|| |
| |                  | |  | Anti-Sliv    | antisliv|  no    | Edit|| |
| |                  | |  +--------------------------------------+| |
| |                  | |  [+ Create Strategy]                      | |
| |                  | |                                          | |
| |                  | |  Bindings for "ACoS Control":            | |
| |                  | |  - Campaign: Платья лето    [Detach]     | |
| |                  | |  - Campaign: Куртки зима    [Detach]     | |
| |                  | |  [+ Attach to campaign]                   | |
| +------------------+ +------------------------------------------+|
|                                                                    |
| +-- KEYWORDS & SEO -------------------------------------------+   |
| |  Keywords (1,247 total)           [Collect from WB]          |   |
| |  +----------------------------------------------------------+|   |
| |  | Query              | Freq  | Trend   | Cluster           ||   |
| |  |--------------------|-------|---------|-------------------||   |
| |  | платье миди        | 12400 | rising  | Платья             ||   |
| |  | платье летнее      | 8200  | stable  | Платья             ||   |
| |  +----------------------------------------------------------+|   |
| |                                                              |   |
| |  Clusters (24 total)             [Auto-cluster]              |   |
| |  +----------------------------------------------------------+|   |
| |  | Cluster Name | Main Keyword    | Keywords | Total Freq   ||   |
| |  |--------------|-----------------|----------|------------ --||   |
| |  | Платья       | платье миди     | 18       | 45,200       ||   |
| |  | Куртки       | куртка зимняя   | 12       | 28,400       ||   |
| |  +----------------------------------------------------------+|   |
| +--------------------------------------------------------------+   |
|                                                                    |
| +-- COMPETITORS -----------------------------------------------+  |
| |  Tracked Competitors (89 total)  [Extract from SERP]          | |
| |  +----------------------------------------------------------+| |
| |  | Product       | Competitor      | Price | Pos | Last Seen || |
| |  |---------------|-----------------|-------|-----|-----------||| |
| |  | Платье миди   | Конкурент A     | 2290  | 3   | 2024-03-15||
| |  +----------------------------------------------------------+| |
| +--------------------------------------------------------------+  |
|                                                                    |
| +-- EXPORTS ---------------------------------------------------+  |
| |  [+ New Export]                                               | |
| |  +----------------------------------------------------------+| |
| |  | Type       | Format | Status    | Created    | Download   || |
| |  |------------|--------|-----------|------------|------------ || |
| |  | campaigns  | xlsx   | completed | 2024-03-15 | [Download] || |
| |  | keywords   | csv    | pending   | 2024-03-15 | --         || |
| |  +----------------------------------------------------------+| |
| +--------------------------------------------------------------+  |
|                                                                    |
| +-- JOBS (Background Tasks) ----------------------------------+   |
| |  +----------------------------------------------------------+|   |
| |  | Task Type        | Status    | Started    | Duration     ||   |
| |  |------------------|-----------|------------|------------ --||   |
| |  | sync_campaigns   | completed | 2024-03-15 | 45s          ||   |
| |  | generate_recs    | running   | 2024-03-15 | ...          ||   |
| |  | seo_analysis     | failed    | 2024-03-14 | 12s  [Retry]||   |
| |  +----------------------------------------------------------+|   |
| +--------------------------------------------------------------+   |
+------------------------------------------------------------------+
```

## Component Hierarchy

```
<Settings>
  <SettingsSidebar activeSection={section} onSelect={setSection} />
  <SettingsContent>

    {section === 'strategies' && <StrategiesSection />}
      <DataTable columns={strategyColumns} data={strategies} />
      <CreateStrategyDialog onSubmit={createStrategy} />
        Form fields:
          name: string
          type: 'acos' | 'roas' | 'antisliv' | 'dayparting'
          seller_cabinet_id: uuid (select)
          params: StrategyParams (conditional form based on type)
      <StrategyBindings strategyId={selectedId} />
        <DataTable columns={bindingColumns} data={bindings} />
        <AttachDialog onSubmit={attachBinding} />
          Form: campaign_id (select) or product_id (select)

    {section === 'keywords' && <KeywordsSection />}
      <KeywordsTable />
        -> GET /api/v1/keywords?page=1&per_page=50
      <Button onClick={collectKeywords} />
        -> POST /api/v1/keywords/collect
      <ClustersTable />
        -> GET /api/v1/keyword-clusters
      <Button onClick={autoCluster} />
        -> POST /api/v1/keywords/cluster

    {section === 'competitors' && <CompetitorsSection />}
      <DataTable data={competitors} />
        -> GET /api/v1/competitors?page=1&per_page=50
      <ExtractCompetitorsDialog />
        -> POST /api/v1/competitors/extract

    {section === 'exports' && <ExportsSection />}
      <DataTable data={exports} />
        -> GET /api/v1/exports
      <CreateExportDialog />
        -> POST /api/v1/exports
        Form: entity_type (select), format ('csv'|'xlsx'), filters (JSON)
      <DownloadButton exportId={id} />
        -> GET /api/v1/exports/{id}/download

    {section === 'jobs' && <JobsSection />}
      <DataTable data={jobRuns} />
        -> GET /api/v1/job-runs?page=1&per_page=50
      <RetryButton jobId={id} />
        -> POST /api/v1/job-runs/{id}/retry

  </SettingsContent>
</Settings>
```

## Data Flow

```
Settings mount:
  1. GET /api/v1/strategies           -> Strategy[]
  2. GET /api/v1/seller-cabinets      -> SellerCabinet[] (for strategy forms)

On section switch (lazy):
  keywords:     GET /api/v1/keywords + GET /api/v1/keyword-clusters
  competitors:  GET /api/v1/competitors
  exports:      GET /api/v1/exports
  jobs:         GET /api/v1/job-runs

Mutations:
  Strategies:
    - POST   /api/v1/strategies                       (create)
    - PUT    /api/v1/strategies/{id}                   (update)
    - DELETE /api/v1/strategies/{id}                   (delete)
    - POST   /api/v1/strategies/{id}/attach            (bind to campaign)
    - DELETE /api/v1/strategies/{id}/bindings/{bid}     (unbind)

  Keywords:
    - POST /api/v1/keywords/collect                    (trigger WB scrape)
    - POST /api/v1/keywords/cluster                    (auto-cluster)

  Competitors:
    - POST /api/v1/competitors/extract                 (extract from SERP)

  Exports:
    - POST /api/v1/exports                             (create new export)
    - GET  /api/v1/exports/{id}/download               (download file)

  Jobs:
    - POST /api/v1/job-runs/{id}/retry                 (retry failed job)
```

---

# TYPESCRIPT TYPE DEFINITIONS (src/api/types.ts)

```typescript
// --- Primitives ---

type UUID = string;
type ISO8601 = string;

// --- API Envelope ---

interface ApiResponse<T> {
  data: T;
  meta?: PaginationMeta;
  errors?: ApiError[];
}

interface PaginationMeta {
  page: number;
  per_page: number;
  total: number;
}

interface ApiError {
  code: string;
  message: string;
  field?: string;
}

// --- Auth ---

interface AuthTokens {
  access_token: string;
  refresh_token: string;
}

interface User {
  id: UUID;
  email: string;
  name: string;
  created_at: ISO8601;
  updated_at: ISO8601;
}

// --- Workspace ---

interface Workspace {
  id: UUID;
  name: string;
  slug: string;
  created_at: ISO8601;
  updated_at: ISO8601;
}

// --- Seller Cabinet ---

interface SellerCabinet {
  id: UUID;
  workspace_id: UUID;
  name: string;
  status: 'active' | 'inactive' | 'error';
  source: string;
  external_integration_id?: string;
  integration_type?: string;
  last_synced_at?: ISO8601;
  last_auto_sync?: AutoSyncSummary;
  created_at: ISO8601;
  updated_at: ISO8601;
}

interface AutoSyncSummary {
  job_run_id: UUID;
  status: string;
  result_state: string;
  freshness_state: string;
  finished_at?: ISO8601;
  cabinets: number;
  campaigns: number;
  campaign_stats: number;
  phrases: number;
  phrase_stats: number;
  products: number;
  sync_issues: number;
}

// --- Ads Overview ---

interface AdsOverview {
  last_auto_sync?: AutoSyncSummary;
  performance_compare?: AdsPeriodCompare;
  evidence?: SourceEvidence;
  cabinets: CabinetSummary[];
  attention: AttentionItem[];
  top_products: ProductAdsSummary[];
  top_campaigns: CampaignPerformanceSummary[];
  top_queries: QueryPerformanceSummary[];
  totals: AdsOverviewTotals;
}

interface AdsOverviewTotals {
  cabinets: number;
  products: number;
  campaigns: number;
  queries: number;
  active_campaigns: number;
  attention_items: number;
}

interface AdsMetricsSummary {
  impressions: number;
  clicks: number;
  spend: number;
  orders: number;
  revenue: number;
  ctr: number;
  cpc: number;
  conversion_rate: number;
  data_mode: string;
}

interface AdsMetricsDelta {
  impressions: number;
  clicks: number;
  spend: number;
  orders: number;
  revenue: number;
  ctr: number;
  cpc: number;
  conversion_rate: number;
}

interface AdsPeriodCompare {
  current: AdsMetricsSummary;
  previous: AdsMetricsSummary;
  delta: AdsMetricsDelta;
  trend: string;
}

interface SourceEvidence {
  source: string;
  captured_at?: ISO8601;
  freshness_state: string;
  confidence: number;
  coverage: string;
  confirmed_in_cabinet: boolean;
}

// --- Attention Items (Recommendations on Dashboard) ---

interface AttentionItem {
  type: string;
  title: string;
  description: string;
  severity: 'low' | 'medium' | 'high' | 'critical';
  action_label: string;
  action_path: string;
  source_type: string;
  source_id?: string;
}

// --- Cabinet Summary ---

interface CabinetSummary {
  id: string;
  cabinet_id: UUID;
  integration_id?: string;
  integration_name: string;
  cabinet_name: string;
  status: string;
  freshness_state: string;
  last_sync?: ISO8601;
  last_auto_sync?: AutoSyncSummary;
  campaigns_count: number;
  products_count: number;
  queries_count: number;
  active_campaigns_count: number;
}

// --- Campaign Performance ---

interface CampaignPerformanceSummary {
  id: UUID;
  workspace_id: UUID;
  seller_cabinet_id: UUID;
  integration_id?: string;
  integration_name: string;
  cabinet_name: string;
  wb_campaign_id: number;
  name: string;
  status: string;
  campaign_type: number;
  bid_type: 'manual' | 'unified';
  payment_type: 'cpm' | 'cpc';
  daily_budget?: number;
  last_sync?: ISO8601;
  health_status: string;
  health_reason?: string;
  primary_action?: string;
  freshness_state: string;
  performance: AdsMetricsSummary;
  period_compare?: AdsPeriodCompare;
  related_products: AdsEntityRef[];
  top_queries: AdsEntityRef[];
  waste_queries: AdsEntityRef[];
  winning_queries: AdsEntityRef[];
  evidence?: SourceEvidence;
  created_at: ISO8601;
  updated_at: ISO8601;
}

// --- Product Performance ---

interface ProductAdsSummary {
  id: UUID;
  workspace_id: UUID;
  seller_cabinet_id: UUID;
  integration_id?: string;
  integration_name: string;
  cabinet_name: string;
  wb_product_id: number;
  title: string;
  brand?: string;
  category?: string;
  image_url?: string;
  price?: number;
  campaigns_count: number;
  queries_count: number;
  health_status: string;
  health_reason?: string;
  primary_action?: string;
  freshness_state: string;
  performance: AdsMetricsSummary;
  period_compare?: AdsPeriodCompare;
  related_campaigns: AdsEntityRef[];
  top_queries: AdsEntityRef[];
  waste_queries: AdsEntityRef[];
  winning_queries: AdsEntityRef[];
  evidence?: SourceEvidence;
  data_coverage_note?: string;
  created_at: ISO8601;
  updated_at: ISO8601;
}

// --- Query Performance ---

interface QueryPerformanceSummary {
  id: UUID;
  workspace_id: UUID;
  campaign_id: UUID;
  seller_cabinet_id: UUID;
  integration_id?: string;
  integration_name: string;
  cabinet_name: string;
  campaign_name: string;
  wb_campaign_id: number;
  wb_cluster_id: number;
  keyword: string;
  current_bid?: number;
  cluster_size?: number;
  source: string;
  signal_category: string;
  health_status: string;
  health_reason?: string;
  primary_action?: string;
  freshness_state: string;
  performance: AdsMetricsSummary;
  period_compare?: AdsPeriodCompare;
  priority_score: number;
  related_products: AdsEntityRef[];
  evidence?: SourceEvidence;
  created_at: ISO8601;
  updated_at: ISO8601;
}

interface AdsEntityRef {
  id: UUID;
  label: string;
  wb_id?: number;
  count?: number;
  source?: string;
}

// --- Campaign Stats ---

interface CampaignStat {
  id: UUID;
  campaign_id: UUID;
  date: ISO8601;
  impressions: number;
  clicks: number;
  spend: number;
  orders?: number;
  revenue?: number;
  created_at: ISO8601;
  updated_at: ISO8601;
}

// --- Phrases ---

interface Phrase {
  id: UUID;
  campaign_id: UUID;
  workspace_id: UUID;
  wb_cluster_id: number;
  keyword: string;
  count?: number;
  current_bid?: number;
  created_at: ISO8601;
  updated_at: ISO8601;
}

// --- Bid Change (audit) ---

interface BidChange {
  id: UUID;
  workspace_id: UUID;
  seller_cabinet_id: UUID;
  campaign_id: UUID;
  product_id?: UUID;
  phrase_id?: UUID;
  strategy_id?: UUID;
  recommendation_id?: UUID;
  placement: string;
  old_bid: number;
  new_bid: number;
  reason: string;
  source: 'strategy' | 'recommendation' | 'manual';
  acos?: number;
  roas?: number;
  created_at: ISO8601;
}

// --- Campaign Phrase (plus/minus) ---

interface CampaignPhrase {
  id: UUID;
  campaign_id: UUID;
  phrase: string;
  created_at: ISO8601;
}

// --- Recommendation ---

type RecommendationType =
  | 'bid_adjustment' | 'raise_bid' | 'lower_bid'
  | 'position_drop' | 'low_ctr' | 'high_spend_low_orders'
  | 'new_competitor' | 'disable_phrase' | 'add_minus_phrase'
  | 'improve_title' | 'improve_description' | 'optimize_seo'
  | 'price_optimization' | 'photo_improvement'
  | 'delivery_issue' | 'stock_alert';

type Severity = 'low' | 'medium' | 'high' | 'critical';

interface Recommendation {
  id: UUID;
  workspace_id: UUID;
  campaign_id?: UUID;
  phrase_id?: UUID;
  product_id?: UUID;
  seller_cabinet_id?: UUID;
  title: string;
  description: string;
  type: RecommendationType;
  severity: Severity;
  confidence: number;
  source_metrics: Record<string, unknown>;
  next_action?: string;
  status: 'active' | 'completed' | 'dismissed';
  evidence?: SourceEvidence;
  created_at: ISO8601;
  updated_at: ISO8601;
}

// --- Strategy ---

type StrategyType = 'acos' | 'roas' | 'antisliv' | 'dayparting';

interface StrategyParams {
  target_acos?: number;
  target_roas?: number;
  max_acos?: number;
  base_multiplier?: number;
  hourly_multipliers?: Record<string, number>;
  weekday_multipliers?: Record<string, number>;
  min_bid?: number;
  max_bid?: number;
  max_change_percent?: number;
  min_clicks?: number;
  lookback_days?: number;
}

interface Strategy {
  id: UUID;
  workspace_id: UUID;
  seller_cabinet_id: UUID;
  name: string;
  type: StrategyType;
  params: StrategyParams;
  is_active: boolean;
  created_at: ISO8601;
  updated_at: ISO8601;
  bindings?: StrategyBinding[];
}

interface StrategyBinding {
  id: UUID;
  strategy_id: UUID;
  campaign_id?: UUID;
  product_id?: UUID;
  created_at: ISO8601;
}

// --- Keyword / SEO ---

interface Keyword {
  id: UUID;
  workspace_id: UUID;
  query: string;
  normalized: string;
  frequency: number;
  frequency_trend: 'rising' | 'falling' | 'stable';
  cluster_id?: UUID;
  source: string;
  created_at: ISO8601;
  updated_at: ISO8601;
}

interface KeywordCluster {
  id: UUID;
  workspace_id: UUID;
  name: string;
  main_keyword: string;
  keyword_count: number;
  total_frequency: number;
  created_at: ISO8601;
  updated_at: ISO8601;
  keywords?: Keyword[];
}

interface SEOAnalysis {
  id: UUID;
  workspace_id: UUID;
  product_id: UUID;
  title_score: number;
  description_score: number;
  keywords_score: number;
  overall_score: number;
  title_issues: SEOIssue[];
  description_issues: SEOIssue[];
  keyword_coverage: Record<string, boolean>;
  recommendations: SEORecommendation[];
  analyzed_at: ISO8601;
}

interface SEOIssue {
  type: string;
  severity: Severity;
  message: string;
  field: string;
}

interface SEORecommendation {
  type: string;
  priority: number;
  message: string;
  suggestion: string;
}

// --- Competitor ---

interface Competitor {
  id: UUID;
  workspace_id: UUID;
  product_id: UUID;
  competitor_nm_id: number;
  competitor_title: string;
  competitor_brand?: string;
  competitor_price?: number;
  competitor_rating?: number;
  competitor_reviews_count?: number;
  competitor_image_url?: string;
  query: string;
  region?: string;
  first_seen_at: ISO8601;
  last_seen_at: ISO8601;
  last_position?: number;
}

// --- Product Event ---

interface ProductEvent {
  id: UUID;
  workspace_id: UUID;
  product_id: UUID;
  event_type: string;
  field_name?: string;
  old_value?: string;
  new_value?: string;
  metadata?: Record<string, unknown>;
  detected_at: ISO8601;
  source: string;
}

// --- Position ---

interface Position {
  id: UUID;
  workspace_id: UUID;
  product_id: UUID;
  query: string;
  region: string;
  position: number;
  page: number;
  source: string;
  checked_at: ISO8601;
  created_at: ISO8601;
}

interface PositionTrackingTarget {
  id: UUID;
  workspace_id: UUID;
  product_id: UUID;
  product_title: string;
  query: string;
  region: string;
  is_active: boolean;
  baseline_position?: number;
  latest_position?: number;
  latest_page?: number;
  latest_checked_at?: ISO8601;
  delta?: number;
  sample_count: number;
  alert_candidate: boolean;
  alert_severity?: string;
  created_at: ISO8601;
  updated_at: ISO8601;
}

// --- Export ---

interface Export {
  id: UUID;
  workspace_id: UUID;
  user_id: UUID;
  entity_type: string;
  format: 'csv' | 'xlsx';
  status: 'pending' | 'processing' | 'completed' | 'failed';
  file_path?: string;
  error_message?: string;
  filters?: Record<string, unknown>;
  created_at: ISO8601;
  updated_at: ISO8601;
}

// --- Job Run ---

interface JobRun {
  id: UUID;
  workspace_id?: UUID;
  task_type: string;
  status: 'running' | 'completed' | 'failed';
  started_at: ISO8601;
  finished_at?: ISO8601;
  error_message?: string;
  metadata?: Record<string, unknown>;
  evidence?: SourceEvidence;
  created_at: ISO8601;
}
```

---

# API CLIENT (src/api/endpoints.ts)

```typescript
import { api } from './client';
import type { ApiResponse, PaginationMeta } from './types';

// --- Common params ---

interface DateRangeParams {
  date_from?: string;  // YYYY-MM-DD
  date_to?: string;
}

interface PaginationParams {
  page?: number;
  per_page?: number;
}

interface SortParams {
  sort_by?: string;
  sort_dir?: 'asc' | 'desc';
}

// --- Seller Cabinets ---

export const sellerCabinets = {
  list: () =>
    api.get<ApiResponse<SellerCabinet[]>>('/seller-cabinets'),

  get: (id: string) =>
    api.get<ApiResponse<SellerCabinet>>(`/seller-cabinets/${id}`),

  create: (data: { name: string; api_token: string }) =>
    api.post<ApiResponse<SellerCabinet>>('/seller-cabinets', data),

  sync: (id: string) =>
    api.post<ApiResponse<null>>(`/seller-cabinets/${id}/sync`),

  delete: (id: string) =>
    api.delete<ApiResponse<null>>(`/seller-cabinets/${id}`),
};

// --- Ads Overview ---

export const adsOverview = {
  get: (params: DateRangeParams & { seller_cabinet_id?: string }) =>
    api.get<ApiResponse<AdsOverview>>('/ads/overview', { params }),

  listCampaigns: (params: DateRangeParams & PaginationParams & SortParams & {
    seller_cabinet_id?: string;
  }) =>
    api.get<ApiResponse<CampaignPerformanceSummary[]>>('/ads/campaigns', { params }),

  getCampaign: (id: string, params: DateRangeParams) =>
    api.get<ApiResponse<CampaignPerformanceSummary>>(`/ads/campaigns/${id}`, { params }),

  listProducts: (params: DateRangeParams & PaginationParams & SortParams & {
    seller_cabinet_id?: string;
    title?: string;
    view?: string;
  }) =>
    api.get<ApiResponse<ProductAdsSummary[]>>('/ads/products', { params }),

  getProduct: (id: string, params: DateRangeParams) =>
    api.get<ApiResponse<ProductAdsSummary>>(`/ads/products/${id}`, { params }),

  listQueries: (params: DateRangeParams & PaginationParams & SortParams & {
    seller_cabinet_id?: string;
    campaign_id?: string;
  }) =>
    api.get<ApiResponse<QueryPerformanceSummary[]>>('/ads/queries', { params }),

  getQuery: (id: string, params: DateRangeParams) =>
    api.get<ApiResponse<QueryPerformanceSummary>>(`/ads/queries/${id}`, { params }),
};

// --- Campaigns ---

export const campaigns = {
  list: (params: PaginationParams) =>
    api.get<ApiResponse<Campaign[]>>('/campaigns', { params }),

  get: (id: string) =>
    api.get<ApiResponse<Campaign>>(`/campaigns/${id}`),

  getStats: (id: string, params: DateRangeParams) =>
    api.get<ApiResponse<CampaignStat[]>>(`/campaigns/${id}/stats`, { params }),

  listPhrases: (id: string, params: PaginationParams) =>
    api.get<ApiResponse<Phrase[]>>(`/campaigns/${id}/phrases`, { params }),

  listRecommendations: (id: string, params: PaginationParams & { status?: string }) =>
    api.get<ApiResponse<Recommendation[]>>(`/campaigns/${id}/recommendations`, { params }),
};

// --- Campaign Actions ---

export const campaignActions = {
  start: (id: string) =>
    api.post<ApiResponse<{ status: string }>>(`/campaigns/${id}/start`),

  pause: (id: string) =>
    api.post<ApiResponse<{ status: string }>>(`/campaigns/${id}/pause`),

  stop: (id: string) =>
    api.post<ApiResponse<{ status: string }>>(`/campaigns/${id}/stop`),

  setBid: (id: string, data: { placement: string; new_bid: number }) =>
    api.post<ApiResponse<BidChange>>(`/campaigns/${id}/bids`, data),

  bidHistory: (id: string, params: PaginationParams) =>
    api.get<ApiResponse<BidChange[]>>(`/campaigns/${id}/bid-history`, { params }),

  listMinusPhrases: (id: string) =>
    api.get<ApiResponse<CampaignPhrase[]>>(`/campaigns/${id}/minus-phrases`),

  addMinusPhrase: (id: string, data: { phrase: string }) =>
    api.post<ApiResponse<CampaignPhrase>>(`/campaigns/${id}/minus-phrases`, data),

  deleteMinusPhrase: (campaignId: string, phraseId: string) =>
    api.delete<ApiResponse<null>>(`/campaigns/${campaignId}/minus-phrases/${phraseId}`),

  listPlusPhrases: (id: string) =>
    api.get<ApiResponse<CampaignPhrase[]>>(`/campaigns/${id}/plus-phrases`),

  addPlusPhrase: (id: string, data: { phrase: string }) =>
    api.post<ApiResponse<CampaignPhrase>>(`/campaigns/${id}/plus-phrases`, data),

  deletePlusPhrase: (campaignId: string, phraseId: string) =>
    api.delete<ApiResponse<null>>(`/campaigns/${campaignId}/plus-phrases/${phraseId}`),
};

// --- Products ---

export const products = {
  list: (params: PaginationParams) =>
    api.get<ApiResponse<Product[]>>('/products', { params }),

  get: (id: string) =>
    api.get<ApiResponse<Product>>(`/products/${id}`),

  positions: (id: string, params: PaginationParams) =>
    api.get<ApiResponse<Position[]>>(`/products/${id}/positions`, { params }),

  recommendations: (id: string, params: PaginationParams) =>
    api.get<ApiResponse<Recommendation[]>>(`/products/${id}/recommendations`, { params }),

  competitors: (id: string) =>
    api.get<ApiResponse<Competitor[]>>(`/products/${id}/competitors`),

  seo: (id: string) =>
    api.get<ApiResponse<SEOAnalysis>>(`/products/${id}/seo`),

  events: (id: string, params: PaginationParams) =>
    api.get<ApiResponse<ProductEvent[]>>(`/products/${id}/events`, { params }),
};

// --- Recommendations ---

export const recommendations = {
  list: (params: PaginationParams & {
    status?: string;
    type?: string;
    severity?: string;
    campaign_id?: string;
    phrase_id?: string;
    product_id?: string;
  }) =>
    api.get<ApiResponse<Recommendation[]>>('/recommendations', { params }),

  get: (id: string) =>
    api.get<ApiResponse<Recommendation>>(`/recommendations/${id}`),

  apply: (id: string) =>
    api.post<ApiResponse<BidChange>>(`/recommendations/${id}/apply`),

  resolve: (id: string) =>
    api.post<ApiResponse<Recommendation>>(`/recommendations/${id}/resolve`),

  dismiss: (id: string) =>
    api.post<ApiResponse<Recommendation>>(`/recommendations/${id}/dismiss`),

  generate: () =>
    api.post<ApiResponse<{ job_run_id: string }>>('/recommendations/generate'),
};

// --- Strategies ---

export const strategies = {
  list: (params?: PaginationParams) =>
    api.get<ApiResponse<Strategy[]>>('/strategies', { params }),

  get: (id: string) =>
    api.get<ApiResponse<Strategy>>(`/strategies/${id}`),

  create: (data: Omit<Strategy, 'id' | 'workspace_id' | 'created_at' | 'updated_at'>) =>
    api.post<ApiResponse<Strategy>>('/strategies', data),

  update: (id: string, data: Partial<Strategy>) =>
    api.put<ApiResponse<Strategy>>(`/strategies/${id}`, data),

  delete: (id: string) =>
    api.delete<ApiResponse<null>>(`/strategies/${id}`),

  attach: (id: string, data: { campaign_id?: string; product_id?: string }) =>
    api.post<ApiResponse<StrategyBinding>>(`/strategies/${id}/attach`, data),

  detach: (strategyId: string, bindingId: string) =>
    api.delete<ApiResponse<null>>(`/strategies/${strategyId}/bindings/${bindingId}`),
};

// --- Keywords & SEO ---

export const keywords = {
  list: (params: PaginationParams) =>
    api.get<ApiResponse<Keyword[]>>('/keywords', { params }),

  collect: () =>
    api.post<ApiResponse<{ job_run_id: string }>>('/keywords/collect'),

  cluster: () =>
    api.post<ApiResponse<{ clusters_created: number }>>('/keywords/cluster'),

  listClusters: (params?: PaginationParams) =>
    api.get<ApiResponse<KeywordCluster[]>>('/keyword-clusters', { params }),
};

export const seo = {
  analyze: () =>
    api.post<ApiResponse<{ job_run_id: string }>>('/seo/analyze'),
};

// --- Competitors ---

export const competitors = {
  list: (params: PaginationParams) =>
    api.get<ApiResponse<Competitor[]>>('/competitors', { params }),

  extract: (data: { product_id?: string; query?: string }) =>
    api.post<ApiResponse<{ extracted: number }>>('/competitors/extract', data),
};

// --- Exports ---

export const exports_ = {
  list: (params: PaginationParams) =>
    api.get<ApiResponse<Export[]>>('/exports', { params }),

  create: (data: { entity_type: string; format: 'csv' | 'xlsx'; filters?: object }) =>
    api.post<ApiResponse<Export>>('/exports', data),

  get: (id: string) =>
    api.get<ApiResponse<Export>>(`/exports/${id}`),

  download: (id: string) =>
    api.get(`/exports/${id}/download`, { responseType: 'blob' }),
};

// --- Job Runs ---

export const jobRuns = {
  list: (params: PaginationParams) =>
    api.get<ApiResponse<JobRun[]>>('/job-runs', { params }),

  get: (id: string) =>
    api.get<ApiResponse<JobRun>>(`/job-runs/${id}`),

  retry: (id: string) =>
    api.post<ApiResponse<JobRun>>(`/job-runs/${id}/retry`),
};

// --- Positions ---

export const positions = {
  list: (params: PaginationParams) =>
    api.get<ApiResponse<Position[]>>('/positions', { params }),

  targets: (params: PaginationParams) =>
    api.get<ApiResponse<PositionTrackingTarget[]>>('/positions/targets', { params }),

  createTarget: (data: { product_id: string; query: string; region: string }) =>
    api.post<ApiResponse<PositionTrackingTarget>>('/positions/targets', data),

  aggregate: (params: { product_id?: string; query?: string; region?: string }) =>
    api.get<ApiResponse<PositionAggregate[]>>('/positions/aggregate', { params }),
};
```

---

# REACT QUERY KEY FACTORY (src/utils/queryKeys.ts)

```typescript
export const queryKeys = {
  overview: (cabinetId?: string, dateRange?: [string, string]) =>
    ['overview', { cabinetId, dateRange }] as const,

  cabinets: () => ['cabinets'] as const,

  campaigns: {
    list: (filters: Record<string, unknown>) => ['campaigns', 'list', filters] as const,
    detail: (id: string) => ['campaigns', 'detail', id] as const,
    stats: (id: string, dateRange: [string, string]) => ['campaigns', 'stats', id, dateRange] as const,
    phrases: (id: string) => ['campaigns', 'phrases', id] as const,
    bidHistory: (id: string) => ['campaigns', 'bidHistory', id] as const,
    recommendations: (id: string) => ['campaigns', 'recommendations', id] as const,
    minusPhrases: (id: string) => ['campaigns', 'minusPhrases', id] as const,
    plusPhrases: (id: string) => ['campaigns', 'plusPhrases', id] as const,
  },

  products: {
    list: (filters: Record<string, unknown>) => ['products', 'list', filters] as const,
    detail: (id: string) => ['products', 'detail', id] as const,
    competitors: (id: string) => ['products', 'competitors', id] as const,
    seo: (id: string) => ['products', 'seo', id] as const,
    events: (id: string) => ['products', 'events', id] as const,
    positions: (id: string) => ['products', 'positions', id] as const,
  },

  queries: {
    list: (filters: Record<string, unknown>) => ['queries', 'list', filters] as const,
    detail: (id: string) => ['queries', 'detail', id] as const,
  },

  recommendations: (filters?: Record<string, unknown>) =>
    ['recommendations', filters] as const,

  strategies: () => ['strategies'] as const,
  keywords: () => ['keywords'] as const,
  clusters: () => ['keyword-clusters'] as const,
  competitors: () => ['competitors'] as const,
  exports: () => ['exports'] as const,
  jobRuns: () => ['job-runs'] as const,
  positions: () => ['positions'] as const,
};
```

---

# API CLIENT SETUP (src/api/client.ts)

```typescript
import axios from 'axios';

export const api = axios.create({
  baseURL: import.meta.env.VITE_API_URL || 'http://localhost:8080/api/v1',
  headers: { 'Content-Type': 'application/json' },
});

// Inject auth token
api.interceptors.request.use((config) => {
  const token = localStorage.getItem('access_token');
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }

  // Inject workspace header from global state
  const workspaceId = localStorage.getItem('workspace_id');
  if (workspaceId) {
    config.headers['X-Workspace-ID'] = workspaceId;
  }

  return config;
});

// Handle 401 -> refresh token
api.interceptors.response.use(
  (response) => response,
  async (error) => {
    if (error.response?.status === 401) {
      const refreshToken = localStorage.getItem('refresh_token');
      if (refreshToken) {
        try {
          const { data } = await axios.post(
            `${api.defaults.baseURL}/auth/refresh`,
            { refresh_token: refreshToken }
          );
          localStorage.setItem('access_token', data.data.access_token);
          localStorage.setItem('refresh_token', data.data.refresh_token);
          error.config.headers.Authorization = `Bearer ${data.data.access_token}`;
          return api.request(error.config);
        } catch {
          localStorage.clear();
          window.location.href = '/login';
        }
      }
    }
    return Promise.reject(error);
  }
);
```

---

# EXAMPLE HOOK: useOverview (src/hooks/useOverview.ts)

```typescript
import { useQuery } from '@tanstack/react-query';
import { adsOverview } from '../api/endpoints';
import { queryKeys } from '../utils/queryKeys';

export function useOverview(cabinetId?: string, dateRange?: [string, string]) {
  return useQuery({
    queryKey: queryKeys.overview(cabinetId, dateRange),
    queryFn: async () => {
      const { data } = await adsOverview.get({
        seller_cabinet_id: cabinetId,
        date_from: dateRange?.[0],
        date_to: dateRange?.[1],
      });
      return data.data;
    },
    staleTime: 60_000,         // 1 min before refetch
    refetchInterval: 300_000,  // auto-refresh every 5 min
  });
}
```

---

# EXAMPLE HOOK: useMutations (src/hooks/useMutations.ts)

```typescript
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { recommendations, campaignActions, strategies, keywords } from '../api/endpoints';
import { queryKeys } from '../utils/queryKeys';

export function useApplyRecommendation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => recommendations.apply(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['overview'] });
      qc.invalidateQueries({ queryKey: ['recommendations'] });
      qc.invalidateQueries({ queryKey: ['campaigns'] });
    },
  });
}

export function useDismissRecommendation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => recommendations.dismiss(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['recommendations'] });
    },
  });
}

export function useCampaignStart() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => campaignActions.start(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['campaigns'] });
      qc.invalidateQueries({ queryKey: ['overview'] });
    },
  });
}

export function useCampaignPause() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => campaignActions.pause(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['campaigns'] });
      qc.invalidateQueries({ queryKey: ['overview'] });
    },
  });
}

export function useSetBid(campaignId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { placement: string; new_bid: number }) =>
      campaignActions.setBid(campaignId, data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.campaigns.bidHistory(campaignId) });
      qc.invalidateQueries({ queryKey: queryKeys.campaigns.detail(campaignId) });
    },
  });
}

export function useCreateStrategy() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: strategies.create,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.strategies() });
    },
  });
}

export function useCollectKeywords() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: keywords.collect,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.keywords() });
      qc.invalidateQueries({ queryKey: queryKeys.jobRuns() });
    },
  });
}
```

---

# FORMATTERS (src/utils/formatters.ts)

```typescript
/** Format kopecks to rubles: 14235000 -> "142 350 rub" */
export function formatRUB(kopecks: number): string {
  const rubles = kopecks / 100;
  return new Intl.NumberFormat('ru-RU', {
    style: 'currency',
    currency: 'RUB',
    maximumFractionDigits: 0,
  }).format(rubles);
}

/** Format as percentage: 0.041 -> "4.1%" */
export function formatPercent(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

/** Format ROAS multiplier: 5.2 -> "5.2x" */
export function formatROAS(value: number): string {
  return `${value.toFixed(1)}x`;
}

/** Format delta as signed percentage: 0.12 -> "+12%" */
export function formatDelta(current: number, previous: number): string {
  if (previous === 0) return current > 0 ? '+100%' : '0%';
  const delta = ((current - previous) / previous) * 100;
  const sign = delta >= 0 ? '+' : '';
  return `${sign}${delta.toFixed(0)}%`;
}

/** Relative time: "5 min ago", "2 hours ago" */
export function formatRelativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const minutes = Math.floor(diff / 60000);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}
```

---

# IMPLEMENTATION PRIORITY ORDER

```
Phase 1: Foundation (Day 1)
  1. src/api/client.ts          -- Axios instance with interceptors
  2. src/api/types.ts           -- All TypeScript interfaces
  3. src/api/endpoints.ts       -- All endpoint functions
  4. src/utils/queryKeys.ts     -- Query key factory
  5. src/utils/formatters.ts    -- Number/date formatters

Phase 2: Shared Components (Day 1-2)
  6. src/components/AppShell.tsx          -- Layout frame
  7. src/components/ShopSelector.tsx      -- Cabinet dropdown
  8. src/components/ThemeToggle.tsx       -- Dark mode toggle
  9. src/components/DataTable.tsx         -- Sortable, paginated table
  10. src/components/MetricCard.tsx       -- KPI card
  11. src/components/SeverityChip.tsx     -- Badge component
  12. src/components/DateRangePicker.tsx  -- Period selector

Phase 3: Screen 1 -- Command Center (Day 2-3)
  13. src/hooks/useOverview.ts
  14. src/hooks/useCampaigns.ts
  15. src/hooks/useRecommendations.ts
  16. src/hooks/useMutations.ts (apply/dismiss)
  17. src/screens/CommandCenter/MetricCards.tsx
  18. src/screens/CommandCenter/ActionCards.tsx
  19. src/screens/CommandCenter/CampaignTable.tsx
  20. src/screens/CommandCenter/QuickStats.tsx
  21. src/screens/CommandCenter/CommandCenter.tsx

Phase 4: Screen 2 -- Entity Detail (Day 3-5)
  22. src/hooks/useCampaignDetail.ts
  23. src/hooks/useProductDetail.ts
  24. src/screens/EntityDetail/CampaignDetail.tsx
  25. src/screens/EntityDetail/tabs/CampaignOverviewTab.tsx
  26. src/screens/EntityDetail/tabs/CampaignPhrasesTab.tsx
  27. src/screens/EntityDetail/tabs/CampaignBidHistoryTab.tsx
  28. src/screens/EntityDetail/tabs/CampaignRecommendationsTab.tsx
  29. src/screens/EntityDetail/ProductDetail.tsx
  30. src/screens/EntityDetail/tabs/ProductOverviewTab.tsx
  31. src/screens/EntityDetail/tabs/ProductCompetitorsTab.tsx
  32. src/screens/EntityDetail/tabs/ProductSEOTab.tsx
  33. src/screens/EntityDetail/tabs/ProductEventsTab.tsx
  34. src/screens/EntityDetail/tabs/ProductPositionsTab.tsx

Phase 5: Screen 3 -- Settings (Day 5-6)
  35. src/hooks/useStrategies.ts
  36. src/hooks/useKeywords.ts
  37. src/hooks/useCompetitors.ts
  38. src/hooks/useExports.ts
  39. src/hooks/useJobRuns.ts
  40. src/screens/Settings/StrategiesSection.tsx
  41. src/screens/Settings/KeywordsSection.tsx
  42. src/screens/Settings/CompetitorsSection.tsx
  43. src/screens/Settings/ExportsSection.tsx
  44. src/screens/Settings/JobsSection.tsx
  45. src/screens/Settings/Settings.tsx
```

---

# CRITICAL NOTES FOR THE DEVELOPER

1. **All money values from the backend are in kopecks** (1/100 RUB). Always divide by 100 before display. The `formatRUB()` utility handles this.

2. **Workspace ID injection** is handled globally via the Axios interceptor reading from `localStorage`. The developer does not need to pass it per-call.

3. **Date range defaults**: If no date range is selected, default to the last 7 days. The backend accepts `date_from` and `date_to` as query params in YYYY-MM-DD format.

4. **Pagination**: The backend returns `meta: { page, per_page, total }` alongside `data`. The DataTable component should use these for pagination controls.

5. **Campaign status values**: `active`, `paused`, `stopped`, `archived`. Only `active` campaigns can be paused. Only `paused` can be started.

6. **Recommendation flow**: The `attention` array on `AdsOverview` provides dashboard-level action items. For applying bid recommendations, use `POST /recommendations/{id}/apply` which returns the `BidChange` that was executed.

7. **Strategy types**: `acos` (target ACoS), `roas` (target ROAS), `antisliv` (anti-drain with max ACoS cap), `dayparting` (time-based bid multipliers). Each type uses different fields from `StrategyParams`.

8. **Sort params**: Backend accepts `sort_by` (field name) and `sort_dir` (`asc`|`desc`). Default sort for campaigns is `spend desc`.

9. **Health status values**: `healthy`, `warning`, `critical`, `unknown`. Map to green/amber/red/gray chips.

10. **Freshness state**: `fresh`, `stale`, `unknown`. Shows data recency. `stale` means the sync is overdue.
