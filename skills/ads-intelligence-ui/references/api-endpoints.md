# Complete API Endpoints

All endpoints are prefixed with `/ads-api` on frontend (proxied to `/api/v1` on backend).

## Overview & Cabinets
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /ads/overview | `getOverview({date_from, date_to, seller_cabinet_id?})` | Dashboard data (metrics, campaigns, products, queries) |
| GET | /seller-cabinets | `getSellerCabinets({page, per_page})` | List WB shops (merged with Sellico integrations) |
| POST | /seller-cabinets/{id}/sync | `triggerSync(id)` | Start data sync |

## Campaigns
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /ads/campaigns | `getCampaigns({seller_cabinet_id?, status?, name?, page, per_page})` | List with performance |
| GET | /ads/campaigns/{id} | `getCampaign(id, {date_from?, date_to?})` | Detail with related entities |
| GET | /campaigns/{id}/stats | `getCampaignStats(id)` | Daily stats array |
| GET | /campaigns/{id}/phrases | `getCampaignPhrases(id)` | Campaign phrases |
| GET | /campaigns/{id}/recommendations | `getCampaignRecommendations(id)` | Campaign recommendations |
| POST | /campaigns/{id}/start | `startCampaign(id)` | Start campaign via WB API |
| POST | /campaigns/{id}/pause | `pauseCampaign(id)` | Pause campaign |
| POST | /campaigns/{id}/stop | `stopCampaign(id)` | Stop campaign |
| POST | /campaigns/{id}/bids | `setBid(campaignId, placement, newBid)` | Manual bid set |
| GET | /campaigns/{id}/bid-history | `getBidHistory(campaignId)` | Bid change audit trail |
| GET | /campaigns/{id}/minus-phrases | (inline fetch) | Minus keywords |
| POST | /campaigns/{id}/minus-phrases | (inline fetch) | Add minus keyword |
| DELETE | /campaigns/{id}/minus-phrases/{phraseId} | (inline fetch) | Remove minus keyword |
| GET | /campaigns/{id}/plus-phrases | (inline fetch) | Plus keywords |
| POST | /campaigns/{id}/plus-phrases | (inline fetch) | Add plus keyword |

## Products
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /ads/products | `getProducts({seller_cabinet_id?, title?, page, per_page})` | List with performance |
| GET | /ads/products/{id} | `getProduct(id, {date_from?, date_to?})` | Detail with metrics |
| GET | /products/{id}/positions | `getProductPositions(id)` | Position history |
| GET | /products/{id}/competitors | `getProductCompetitors(productId)` | SERP competitors |
| GET | /products/{id}/seo | `getProductSEO(productId)` | SEO analysis scores+issues |
| GET | /products/{id}/events | `getProductEvents(productId)` | Change history |

## Phrases/Queries
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /ads/queries | `getPhrases({campaign_id?, product_id?, page, per_page})` | List with performance |
| GET | /ads/queries/{id} | `getPhrase(id, {date_from?, date_to?})` | Detail |
| GET | /phrases/{id}/stats | `getPhraseStats(id)` | Daily stats |
| GET | /phrases/{id}/bids | `getPhraseBids(id)` | Bid history |

## Recommendations
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /recommendations | `getRecommendations({status?, type?, severity?, campaign_id?, page, per_page})` | List recommendations |
| POST | /recommendations/{id}/apply | `applyRecommendation(id)` | Auto-apply bid change via WB API |
| POST | /recommendations/{id}/resolve | `resolveRecommendation(id)` | Mark as completed |
| POST | /recommendations/{id}/dismiss | `dismissRecommendation(id)` | Dismiss/hide |
| POST | /recommendations/generate | `generateRecommendations()` | Trigger generation |

## Keywords & Semantics
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /keywords | `getKeywords({search?, page, per_page})` | List keywords with frequency |
| POST | /keywords/collect | `collectKeywords()` | Import from phrases/SERP |
| POST | /keywords/cluster | `clusterKeywords()` | Auto-cluster by prefix |
| GET | /keyword-clusters | `getKeywordClusters()` | List clusters |

## Competitors
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /competitors | `getCompetitors({product_id?, page, per_page})` | List all competitors |
| GET | /products/{id}/competitors | `getProductCompetitors(productId)` | Product-specific competitors |
| POST | /competitors/extract | `extractCompetitors()` | Extract from SERP data |

## SEO & Delivery
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| POST | /seo/analyze | `analyzeSEO()` | Analyze all products |
| GET | /products/{id}/seo | `getProductSEO(productId)` | Get SEO scores |
| POST | /delivery/collect | `collectDelivery()` | Collect delivery data |
| GET | /product-events | `getWorkspaceEvents({event_type?, page, per_page})` | All product events |

## Strategies (Bid Automation)
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /strategies | `getStrategies()` | List strategies |
| POST | /strategies | `createStrategy({name, type, params, is_active, seller_cabinet_id})` | Create |
| DELETE | /strategies/{id} | `deleteStrategy(id)` | Delete |
| POST | /strategies/{id}/attach | `attachStrategy(id, {campaign_id?, product_id?})` | Link to entity |

## Exports & Jobs
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /exports | `getExports()` | List exports |
| POST | /exports | `createExport({entity_type, format, filters?})` | Create export |
| GET | /exports/{id}/download | `downloadExport(id)` | Download file |
| GET | /job-runs | `getJobRuns()` | List background jobs |
| POST | /job-runs/{id}/retry | `retryJob(id)` | Retry failed job |

## Positions & SERP
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /positions | `getPositions()` | List positions |
| GET | /positions/targets | `getPositionTargets()` | Tracking targets |
| POST | /positions/targets | `createPositionTarget({product_id, query, region})` | Create target |
| GET | /serp/history | `getSerpSnapshots()` | SERP snapshots |
| GET | /serp/{id} | `getSerpSnapshot(id)` | Snapshot detail |

## Settings
| Method | Path | Frontend Method | Description |
|--------|------|----------------|-------------|
| GET | /settings | (inline fetch) | Workspace settings |
| PUT | /settings | (inline fetch) | Update settings |
| GET | /settings/thresholds | (inline fetch) | Recommendation thresholds |
