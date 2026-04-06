---
name: ads-intelligence-ui
description: |
  Generates complete React + MUI frontend pages for the Sellico Ads Intelligence module.
  Use this skill whenever the user wants to create, redesign, or add new UI screens for the
  Wildberries advertising intelligence tool. Triggers on: "redesign the frontend", "create UI for",
  "add a page for", "build the interface", "make a screen for", any mention of ads-intelligence
  frontend components, or requests to expose backend API functionality through a user interface.
  The skill knows ALL 80+ backend API endpoints and generates pixel-perfect MUI components
  that call them correctly. It produces complete, working React components — not sketches.
---

# Sellico Ads Intelligence UI Generator

You are building the frontend for a Wildberries (Russian e-commerce) advertising intelligence platform.
The backend is a Go microservice with 80+ API endpoints. Your job is to create React + MUI components
that expose ALL this functionality through a clean, card-based UI.

## Architecture: 3 Screens

The entire UI is organized into exactly 3 screens:

### Screen 1: Command Center (`/ads-intelligence`)
The daily dashboard. User opens it, sees what needs attention, takes action, moves on.

**Layout (top to bottom):**
1. **Header bar**: Shop dropdown (left) + Sync button + Period selector (right)
2. **Metric cards row**: 3-4 cards showing Spend, Orders, ROAS, CTR
3. **Quick stats strip**: chips showing keyword count, competitor count, SEO score, events, delivery issues
4. **Action feed**: recommendation cards with inline Apply/Pause/Dismiss buttons, sorted by severity
5. **Entity table**: switchable between Campaigns / Products / Phrases — sortable, filterable, clickable rows → drill down

The entity table is the KEY innovation: instead of separate pages for campaigns, products, and phrases,
ONE table with a tab switcher. Each row shows the entity name, key metrics (spend, orders, ROAS, CTR),
health status, and a recommendation badge count. Clicking a row navigates to the detail page.

### Screen 2: Entity Detail (`/ads-intelligence/campaigns/:id` or `/products/:id`)
Deep dive into a single campaign or product. Uses horizontal tabs for sub-data.

**Campaign Detail tabs:**
- Overview (info + related products + linked strategies)
- Phrases (table with inline bid editing + suggested bids)
- Bid History (table of all bid changes with reason/source)
- Recommendations (action cards for this campaign)
- Plus/Minus Phrases (keyword management)

**Product Detail tabs:**
- Overview (card info + related campaigns)
- Competitors (table from SERP: name, price, position, our position, query)
- SEO Analysis (scores + issues + recommendations)
- Events (change history: price, photo, content, stock, brand)
- Positions (position tracking targets + history)

### Screen 3: Settings (`/ads-intelligence/settings`)
Workspace configuration. Accordion sections:

- **Strategies** (bid automation rules: ACoS/ROAS/AntiSliv/Dayparting + create/delete + attach to campaign)
- **Keywords & Semantics** (keyword table + collect/cluster buttons + clusters display + frequency trends)
- **Competitors** (global competitor list + extract from SERP button)
- **Exports** (create export for any entity type + download history)
- **Jobs** (background task status + retry + schedule info)
- **Audit Logs** (action history)
- **Thresholds** (recommendation engine configuration per workspace)

## Design System

**Style: Card-based (like Trello/Jira cards)**
- Every data group is a card (`Paper variant="outlined"` with `borderRadius: 2.5-3`)
- Cards have a header line (title + badges) and content below
- Actions are buttons inside cards, not floating
- Tables are inside cards (Paper wrapping TableContainer)
- Minimal text descriptions — let data speak

**Colors:**
- Status: green=active/good, orange=warning/paused, red=error/critical, blue=info
- Trends: green=up/improving, red=down/declining, grey=flat
- SEO scores: green ≥70, orange 40-69, red <40
- ROAS: green ≥3, default 1-3, red <1

**Typography:**
- Metric values: `variant="h5" fontWeight={700}`
- Card titles: `variant="subtitle2" fontWeight={700}`
- Table cells: `variant="body2"`
- Captions/labels: `variant="caption" color="text.secondary"`

**Spacing:**
- Between cards: `spacing={2}` (16px)
- Inside cards: `p: 2` to `p: 2.5` (16-20px)
- Page padding: `p: { xs: 2, sm: 3 }` (16-24px)

## API Client

All API calls go through `/ads-api` proxy which maps to `http://localhost:8090/api/v1`.
The API client is at `modules/ads-intelligence/api/adsIntelligenceApi.ts`.

Auth headers are injected automatically:
- `Authorization: Bearer <sellico-token>`
- `X-Workspace-ID: <workspace-id>`
- `X-User-Token: Bearer <sellico-token>`

See `references/api-endpoints.md` for the complete list of 65+ API methods.

## Component Patterns

### Data Fetching
```tsx
const { data, isLoading } = useQuery({
  queryKey: ['unique-key', ...params],
  queryFn: () => adsIntelligenceApi.methodName(params),
  enabled: !!requiredParam,
});
```

### Mutations with Toast
```tsx
const mutation = useMutation({
  mutationFn: () => adsIntelligenceApi.doAction(id),
  onSuccess: () => {
    toast.success('Done');
    queryClient.invalidateQueries({ queryKey: ['affected-key'] });
  },
  onError: () => toast.error('Failed'),
});
```

### Recommendation Action Card
Every recommendation must have inline action buttons:
- `raise_bid` / `lower_bid` → "Применить ставку" button (calls `applyRecommendation`)
- `high_spend_low_orders` / `low_ctr` → "Пауза" button (calls `pauseCampaign`)
- `position_drop` / `new_competitor` → "Подробнее" button (navigates to entity)
- `optimize_seo` / `improve_title` → "Открыть SEO" button (navigates to product SEO tab)
- `price_optimization` → "Конкуренты" button (navigates to product competitors tab)
- `stock_alert` / `delivery_issue` → "Подробнее" button (navigates to product events tab)
- ALL types → "Скрыть" button (calls `dismissRecommendation`)

### Entity Table with Tab Switcher
```tsx
<Tabs value={entityTab} onChange={(_, v) => setEntityTab(v)}>
  <Tab label="Кампании" />
  <Tab label="Товары" />
  <Tab label="Фразы" />
</Tabs>
{entityTab === 0 && <CampaignsTable campaigns={data.top_campaigns} />}
{entityTab === 1 && <ProductsTable products={data.top_products} />}
{entityTab === 2 && <PhrasesTable phrases={data.top_queries} />}
```

## Critical Rules

1. **Every API endpoint must have a UI**. If the backend has an endpoint, the frontend must call it somewhere.
2. **No separate pages for entities** — campaigns, products, phrases are tabs within the entity table on Command Center, or tabs within Entity Detail.
3. **Recommendations are INLINE**, not a separate page. They appear in the action feed on Command Center and as banners on Entity Detail.
4. **All text in Russian** (this is for Russian WB sellers).
5. **Mobile-responsive** — Stack cards vertically on xs/sm, use horizontal stacks on md+.
6. **Use `adsIntelligenceApi` for ALL calls** — never write raw `fetch()`.
7. **Toast for all mutations** — success and error.
8. **Navigate with `useNavigate`** — campaign row click → `/ads-intelligence/campaigns/:id`.

## File Structure

```
modules/ads-intelligence/
├── api/adsIntelligenceApi.ts    (API client — DO NOT rewrite, only extend)
├── types/index.ts               (Types — extend as needed)
├── pages/
│   ├── CommandCenter.tsx         (Screen 1)
│   ├── CampaignDetailPage.tsx   (Screen 2 — campaign)
│   ├── ProductDetailPage.tsx    (Screen 2 — product)
│   └── SettingsPage.tsx         (Screen 3)
├── components/
│   ├── ShopDropdown.tsx         (Shop selector dropdown)
│   ├── MetricCard.tsx           (Single metric display)
│   ├── QuickStats.tsx           (Stats strip)
│   ├── RecommendationCard.tsx   (Action card for recommendations)
│   ├── EntityTable.tsx          (Switchable campaigns/products/phrases table)
│   ├── CampaignRow.tsx          (Campaign table row)
│   ├── ProductRow.tsx           (Product table row)
│   ├── PhraseRow.tsx            (Phrase table row)
│   ├── BidHistoryTable.tsx      (Bid changes table)
│   ├── CompetitorTable.tsx      (Competitor comparison table)
│   ├── SEOScoreCard.tsx         (SEO analysis display)
│   ├── EventTimeline.tsx        (Product event history)
│   └── StrategyCard.tsx         (Strategy configuration card)
├── hooks/
│   ├── useAdsPeriod.ts          (Date range management)
│   ├── useShopSelection.ts      (Shop state management)
│   └── useEntityNavigation.ts   (Navigate to entity detail)
└── AdsIntelligenceLayout.tsx    (Layout — minimal nav)
```
