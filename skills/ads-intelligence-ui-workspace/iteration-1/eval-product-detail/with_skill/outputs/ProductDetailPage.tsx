import React, { useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  LinearProgress,
  Paper,
  Stack,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tabs,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material';
import {
  ArrowBackOutlined,
  RefreshOutlined,
  TimelineOutlined,
  TrendingUpOutlined,
  TrendingDownOutlined,
  TrendingFlatOutlined,
  AddOutlined,
  TrackChangesOutlined,
  ErrorOutlineOutlined,
  CheckCircleOutlineOutlined,
  WarningAmberOutlined,
  InfoOutlined,
  InventoryOutlined,
  PhotoCameraOutlined,
  EditOutlined,
  LocalShippingOutlined,
  BrandingWatermarkOutlined,
  PriceChangeOutlined,
} from '@mui/icons-material';
import { toast } from 'sonner';
import { adsIntelligenceApi } from '../api/adsIntelligenceApi';
import { MetricCard } from '../components/MetricCard';
import type { Product, Position, PositionTrackingTarget, Recommendation } from '../types';

/* ------------------------------------------------------------------ */
/*  Helper: TabPanel                                                   */
/* ------------------------------------------------------------------ */

function TabPanel({
  children,
  value,
  index,
}: {
  children: React.ReactNode;
  value: number;
  index: number;
}) {
  return value === index ? <Box sx={{ pt: 2 }}>{children}</Box> : null;
}

/* ------------------------------------------------------------------ */
/*  Helper: event type labels & icons                                  */
/* ------------------------------------------------------------------ */

const EVENT_TYPE_LABELS: Record<string, string> = {
  price_change: 'Цена',
  photo_change: 'Фото',
  content_change: 'Контент',
  stock_change: 'Остатки',
  brand_change: 'Бренд',
  category_change: 'Категория',
  rating_change: 'Рейтинг',
  delivery_change: 'Доставка',
};

const EVENT_TYPE_COLORS: Record<string, 'default' | 'primary' | 'secondary' | 'error' | 'warning' | 'info' | 'success'> = {
  price_change: 'warning',
  photo_change: 'info',
  content_change: 'primary',
  stock_change: 'error',
  brand_change: 'secondary',
  category_change: 'secondary',
  rating_change: 'success',
  delivery_change: 'warning',
};

function getEventIcon(eventType: string) {
  switch (eventType) {
    case 'price_change':
      return <PriceChangeOutlined sx={{ fontSize: 18 }} />;
    case 'photo_change':
      return <PhotoCameraOutlined sx={{ fontSize: 18 }} />;
    case 'content_change':
      return <EditOutlined sx={{ fontSize: 18 }} />;
    case 'stock_change':
      return <InventoryOutlined sx={{ fontSize: 18 }} />;
    case 'delivery_change':
      return <LocalShippingOutlined sx={{ fontSize: 18 }} />;
    case 'brand_change':
      return <BrandingWatermarkOutlined sx={{ fontSize: 18 }} />;
    default:
      return <TimelineOutlined sx={{ fontSize: 18 }} />;
  }
}

/* ------------------------------------------------------------------ */
/*  Helper: SEO score color                                            */
/* ------------------------------------------------------------------ */

function seoScoreColor(score: number): string {
  if (score >= 70) return 'success.main';
  if (score >= 40) return 'warning.main';
  return 'error.main';
}

/* ------------------------------------------------------------------ */
/*  Helper: position delta chip                                        */
/* ------------------------------------------------------------------ */

function PositionDelta({ delta }: { delta?: number | null }) {
  if (delta == null || delta === 0) {
    return (
      <Chip
        icon={<TrendingFlatOutlined sx={{ fontSize: 14 }} />}
        label="0"
        size="small"
        variant="outlined"
        sx={{ borderRadius: 1, fontSize: '0.72rem' }}
      />
    );
  }
  // Negative delta = position improved (moved up), Positive delta = position worsened (moved down)
  const improved = delta < 0;
  return (
    <Chip
      icon={improved ? <TrendingUpOutlined sx={{ fontSize: 14 }} /> : <TrendingDownOutlined sx={{ fontSize: 14 }} />}
      label={improved ? `+${Math.abs(delta)}` : `-${Math.abs(delta)}`}
      size="small"
      color={improved ? 'success' : 'error'}
      variant="outlined"
      sx={{ borderRadius: 1, fontSize: '0.72rem' }}
    />
  );
}

/* ------------------------------------------------------------------ */
/*  Helper: format date                                                */
/* ------------------------------------------------------------------ */

function fmtDate(dateStr?: string | null): string {
  if (!dateStr) return '\u2014';
  return new Date(dateStr).toLocaleDateString('ru', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
  });
}

function fmtDateTime(dateStr?: string | null): string {
  if (!dateStr) return '\u2014';
  return new Date(dateStr).toLocaleString('ru', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

/* ================================================================== */
/*  MAIN COMPONENT                                                     */
/* ================================================================== */

export const ProductDetailPage: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [tab, setTab] = useState(0);

  /* --- Create Position Target dialog --- */
  const [targetDialogOpen, setTargetDialogOpen] = useState(false);
  const [newTargetQuery, setNewTargetQuery] = useState('');
  const [newTargetRegion, setNewTargetRegion] = useState('');

  /* ==================================== */
  /*  DATA FETCHING                        */
  /* ==================================== */

  // Product detail (always loaded)
  const { data: product, isLoading: productLoading } = useQuery({
    queryKey: ['ads-product', id],
    queryFn: () => adsIntelligenceApi.getProduct(id!),
    enabled: !!id,
  });

  // Tab 1: Competitors
  const { data: competitors, isLoading: competitorsLoading } = useQuery({
    queryKey: ['ads-product-competitors', id],
    queryFn: () => adsIntelligenceApi.getProductCompetitors(id!),
    enabled: !!id && tab === 1,
  });

  // Tab 2: SEO
  const { data: seoAnalysis, isLoading: seoLoading } = useQuery({
    queryKey: ['ads-product-seo', id],
    queryFn: () => adsIntelligenceApi.getProductSEO(id!),
    enabled: !!id && tab === 2,
  });

  // Tab 3: Events
  const { data: events, isLoading: eventsLoading } = useQuery({
    queryKey: ['ads-product-events', id],
    queryFn: () => adsIntelligenceApi.getProductEvents(id!),
    enabled: !!id && tab === 3,
  });

  // Tab 4: Positions - targets
  const { data: positionTargets, isLoading: targetsLoading } = useQuery({
    queryKey: ['ads-position-targets', id],
    queryFn: () => adsIntelligenceApi.getPositionTargets({ product_id: id }),
    enabled: !!id && tab === 4,
  });

  // Tab 4: Positions - history
  const { data: positionHistory, isLoading: positionsLoading } = useQuery({
    queryKey: ['ads-product-positions', id],
    queryFn: () => adsIntelligenceApi.getProductPositions(id!),
    enabled: !!id && tab === 4,
  });

  // Tab 0: Recommendations for overview
  const { data: recsData } = useQuery({
    queryKey: ['ads-product-recs', id],
    queryFn: () =>
      adsIntelligenceApi.getRecommendations({
        product_id: id,
        status: 'active',
        page: 1,
        per_page: 10,
      }),
    enabled: !!id && tab === 0,
  });

  // Tab 0: Related campaigns
  const { data: relatedCampaigns } = useQuery({
    queryKey: ['ads-product-campaigns', id],
    queryFn: () =>
      adsIntelligenceApi.getCampaigns({
        product_id: id,
        page: 1,
        per_page: 20,
      }),
    enabled: !!id && tab === 0,
  });

  /* ==================================== */
  /*  MUTATIONS                            */
  /* ==================================== */

  const extractCompetitorsMutation = useMutation({
    mutationFn: () => adsIntelligenceApi.extractCompetitors(),
    onSuccess: () => {
      toast.success('Конкуренты обновлены');
      queryClient.invalidateQueries({ queryKey: ['ads-product-competitors', id] });
    },
    onError: () => toast.error('Ошибка обновления конкурентов'),
  });

  const analyzeSEOMutation = useMutation({
    mutationFn: () => adsIntelligenceApi.analyzeSEO(),
    onSuccess: () => {
      toast.success('SEO-анализ запущен');
      queryClient.invalidateQueries({ queryKey: ['ads-product-seo', id] });
    },
    onError: () => toast.error('Ошибка запуска анализа'),
  });

  const createTargetMutation = useMutation({
    mutationFn: (data: { product_id: string; query: string; region: string }) =>
      adsIntelligenceApi.createPositionTarget(data),
    onSuccess: () => {
      toast.success('Цель отслеживания создана');
      queryClient.invalidateQueries({ queryKey: ['ads-position-targets', id] });
      setTargetDialogOpen(false);
      setNewTargetQuery('');
      setNewTargetRegion('');
    },
    onError: () => toast.error('Ошибка создания цели'),
  });

  const dismissRecMutation = useMutation({
    mutationFn: (recId: string) => adsIntelligenceApi.dismissRecommendation(recId),
    onSuccess: () => {
      toast.success('Рекомендация скрыта');
      queryClient.invalidateQueries({ queryKey: ['ads-product-recs', id] });
    },
    onError: () => toast.error('Ошибка'),
  });

  /* ==================================== */
  /*  LOADING STATE                        */
  /* ==================================== */

  if (productLoading || !product) {
    return (
      <Box display="flex" justifyContent="center" py={6}>
        <CircularProgress />
      </Box>
    );
  }

  const perf = product.performance;

  /* Compute trend info from period_compare */
  const pc = product.period_compare;
  const spendTrend = pc
    ? pc.delta.spend > 0
      ? 'up'
      : pc.delta.spend < 0
        ? 'down'
        : ('flat' as const)
    : undefined;
  const ordersTrend = pc
    ? pc.delta.orders > 0
      ? 'up'
      : pc.delta.orders < 0
        ? 'down'
        : ('flat' as const)
    : undefined;
  const ctrTrend = pc
    ? pc.delta.ctr > 0
      ? 'up'
      : pc.delta.ctr < 0
        ? 'down'
        : ('flat' as const)
    : undefined;

  const spendChange = pc && pc.previous.spend > 0
    ? `${((pc.delta.spend / pc.previous.spend) * 100).toFixed(0)}%`
    : undefined;
  const ordersChange = pc && pc.previous.orders > 0
    ? `${((pc.delta.orders / pc.previous.orders) * 100).toFixed(0)}%`
    : undefined;
  const ctrChange = pc && pc.previous.ctr > 0
    ? `${((pc.delta.ctr / pc.previous.ctr) * 100).toFixed(0)}%`
    : undefined;

  /* ==================================== */
  /*  RENDER                               */
  /* ==================================== */

  return (
    <Box sx={{ p: { xs: 2, sm: 3 } }}>
      {/* ============================== */}
      {/*  HEADER                         */}
      {/* ============================== */}
      <Stack direction="row" spacing={2} alignItems="flex-start" sx={{ mb: 2.5 }}>
        <IconButton onClick={() => navigate('/ads-intelligence')} sx={{ mt: 0.5 }}>
          <ArrowBackOutlined />
        </IconButton>
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Stack direction="row" spacing={1.5} alignItems="center" flexWrap="wrap">
            {product.image_url && (
              <Box
                component="img"
                src={product.image_url}
                alt={product.title}
                sx={{
                  width: 48,
                  height: 48,
                  borderRadius: 1.5,
                  objectFit: 'cover',
                  border: '1px solid',
                  borderColor: 'divider',
                }}
              />
            )}
            <Box sx={{ minWidth: 0 }}>
              <Typography variant="h6" fontWeight={700} noWrap>
                {product.title}
              </Typography>
              <Stack direction="row" spacing={1} sx={{ mt: 0.5 }} flexWrap="wrap" useFlexGap>
                {product.brand && (
                  <Chip label={product.brand} size="small" variant="outlined" />
                )}
                {product.category && (
                  <Chip label={product.category} size="small" variant="outlined" />
                )}
                {product.wb_product_id && (
                  <Chip
                    label={`nmID: ${product.wb_product_id}`}
                    size="small"
                    variant="outlined"
                    sx={{ fontFamily: 'monospace', fontSize: '0.72rem' }}
                  />
                )}
                {product.price != null && (
                  <Chip
                    label={`${product.price.toLocaleString('ru')} \u20BD`}
                    size="small"
                    color="primary"
                    variant="outlined"
                  />
                )}
                {product.health_status && (
                  <Chip
                    label={product.health_status}
                    size="small"
                    color={
                      product.health_status === 'healthy'
                        ? 'success'
                        : product.health_status === 'warning'
                          ? 'warning'
                          : 'error'
                    }
                    sx={{ borderRadius: 1 }}
                  />
                )}
              </Stack>
            </Box>
          </Stack>
        </Box>
      </Stack>

      {/* ============================== */}
      {/*  METRIC CARDS                    */}
      {/* ============================== */}
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} sx={{ mb: 3 }}>
        <MetricCard
          label="Расход"
          value={perf ? `\u20BD${(perf.spend || 0).toLocaleString('ru')}` : '\u2014'}
          change={spendChange}
          trend={spendTrend}
        />
        <MetricCard
          label="Заказы"
          value={perf ? String(perf.orders || 0) : '\u2014'}
          change={ordersChange}
          trend={ordersTrend}
        />
        <MetricCard
          label="ROAS"
          value={
            perf && perf.spend > 0
              ? `${(perf.revenue / perf.spend).toFixed(1)}x`
              : '\u2014'
          }
        />
        <MetricCard
          label="CTR"
          value={perf && perf.ctr ? `${(perf.ctr * 100).toFixed(1)}%` : '\u2014'}
          change={ctrChange}
          trend={ctrTrend}
        />
      </Stack>

      {/* ============================== */}
      {/*  TABS                            */}
      {/* ============================== */}
      <Tabs
        value={tab}
        onChange={(_, v) => setTab(v)}
        variant="scrollable"
        scrollButtons="auto"
        sx={{ borderBottom: 1, borderColor: 'divider' }}
      >
        <Tab label="Обзор" sx={{ textTransform: 'none', fontWeight: 600 }} />
        <Tab label="Конкуренты" sx={{ textTransform: 'none', fontWeight: 600 }} />
        <Tab label="SEO" sx={{ textTransform: 'none', fontWeight: 600 }} />
        <Tab label="События" sx={{ textTransform: 'none', fontWeight: 600 }} />
        <Tab label="Позиции" sx={{ textTransform: 'none', fontWeight: 600 }} />
      </Tabs>

      {/* ====================================================== */}
      {/*  TAB 0: Overview                                        */}
      {/* ====================================================== */}
      <TabPanel value={tab} index={0}>
        <Stack spacing={2}>
          {/* Product info card */}
          <Paper variant="outlined" sx={{ p: 2.5, borderRadius: 2.5 }}>
            <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1 }}>
              Карточка товара
            </Typography>
            <Stack
              direction={{ xs: 'column', md: 'row' }}
              spacing={3}
              alignItems="flex-start"
            >
              {product.image_url && (
                <Box
                  component="img"
                  src={product.image_url}
                  alt={product.title}
                  sx={{
                    width: 120,
                    height: 120,
                    borderRadius: 2,
                    objectFit: 'cover',
                    border: '1px solid',
                    borderColor: 'divider',
                  }}
                />
              )}
              <Stack spacing={0.5} sx={{ flex: 1 }}>
                <Typography variant="body2">
                  <strong>Название:</strong> {product.title}
                </Typography>
                <Typography variant="body2">
                  <strong>Цена:</strong>{' '}
                  {product.price != null ? `${product.price.toLocaleString('ru')} \u20BD` : '\u2014'}
                </Typography>
                <Typography variant="body2">
                  <strong>Категория:</strong> {product.category || '\u2014'}
                </Typography>
                <Typography variant="body2">
                  <strong>Бренд:</strong> {product.brand || '\u2014'}
                </Typography>
                {product.wb_product_id && (
                  <Typography variant="body2">
                    <strong>nmID:</strong> {product.wb_product_id}
                  </Typography>
                )}
                {product.campaigns_count != null && (
                  <Typography variant="body2">
                    <strong>Кампаний:</strong> {product.campaigns_count}
                  </Typography>
                )}
                {product.queries_count != null && (
                  <Typography variant="body2">
                    <strong>Запросов:</strong> {product.queries_count}
                  </Typography>
                )}
                {product.data_coverage_note && (
                  <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5 }}>
                    {product.data_coverage_note}
                  </Typography>
                )}
              </Stack>
            </Stack>
          </Paper>

          {/* Related campaigns */}
          {relatedCampaigns && relatedCampaigns.data.length > 0 && (
            <Paper variant="outlined" sx={{ p: 2.5, borderRadius: 2.5 }}>
              <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1 }}>
                Связанные кампании ({relatedCampaigns.data.length})
              </Typography>
              <Stack spacing={0.5}>
                {relatedCampaigns.data.map((c) => (
                  <Stack
                    key={c.id}
                    direction="row"
                    spacing={1}
                    alignItems="center"
                    sx={{
                      cursor: 'pointer',
                      '&:hover': { bgcolor: 'action.hover' },
                      borderRadius: 1,
                      px: 1,
                      py: 0.5,
                    }}
                    onClick={() => navigate(`/ads-intelligence/campaigns/${c.id}`)}
                  >
                    <Typography
                      variant="body2"
                      sx={{ color: 'primary.main', fontWeight: 500 }}
                    >
                      {c.name}
                    </Typography>
                    <Chip
                      label={c.status}
                      size="small"
                      color={c.status === 'active' ? 'success' : 'warning'}
                      sx={{ height: 18, fontSize: '0.68rem', borderRadius: 1 }}
                    />
                    {c.performance && (
                      <Typography variant="caption" color="text.secondary">
                        Расход: {c.performance.spend.toLocaleString('ru')} \u20BD
                      </Typography>
                    )}
                  </Stack>
                ))}
              </Stack>
            </Paper>
          )}

          {/* Related queries */}
          {product.top_queries && product.top_queries.length > 0 && (
            <Paper variant="outlined" sx={{ p: 2.5, borderRadius: 2.5 }}>
              <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1 }}>
                Топ запросы ({product.top_queries.length})
              </Typography>
              <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
                {product.top_queries.map((q) => (
                  <Chip
                    key={q.id}
                    label={q.label}
                    size="small"
                    variant="outlined"
                    sx={{ borderRadius: 1 }}
                  />
                ))}
              </Stack>
            </Paper>
          )}

          {/* Waste queries */}
          {product.waste_queries && product.waste_queries.length > 0 && (
            <Paper variant="outlined" sx={{ p: 2.5, borderRadius: 2.5 }}>
              <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1 }}>
                Убыточные запросы ({product.waste_queries.length})
              </Typography>
              <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
                {product.waste_queries.map((q) => (
                  <Chip
                    key={q.id}
                    label={q.label}
                    size="small"
                    color="error"
                    variant="outlined"
                    sx={{ borderRadius: 1 }}
                  />
                ))}
              </Stack>
            </Paper>
          )}

          {/* Active recommendations banner */}
          {recsData && recsData.data.length > 0 && (
            <Paper variant="outlined" sx={{ p: 2.5, borderRadius: 2.5 }}>
              <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1.5 }}>
                Активные рекомендации ({recsData.data.length})
              </Typography>
              <Stack spacing={1.5}>
                {recsData.data.map((rec) => (
                  <Alert
                    key={rec.id}
                    severity={
                      rec.severity === 'critical' || rec.severity === 'high'
                        ? 'error'
                        : rec.severity === 'medium'
                          ? 'warning'
                          : 'info'
                    }
                    variant="outlined"
                    sx={{ borderRadius: 2, '.MuiAlert-message': { width: '100%' } }}
                    action={
                      <Button
                        size="small"
                        color="inherit"
                        onClick={() => dismissRecMutation.mutate(rec.id)}
                        sx={{ textTransform: 'none', fontSize: '0.75rem' }}
                      >
                        Скрыть
                      </Button>
                    }
                  >
                    <Typography variant="body2" fontWeight={600}>
                      {rec.title || rec.description}
                    </Typography>
                    {rec.next_action && (
                      <Typography variant="caption" color="text.secondary">
                        {rec.next_action}
                      </Typography>
                    )}
                  </Alert>
                ))}
              </Stack>
            </Paper>
          )}
        </Stack>
      </TabPanel>

      {/* ====================================================== */}
      {/*  TAB 1: Competitors                                     */}
      {/* ====================================================== */}
      <TabPanel value={tab} index={1}>
        <Stack
          direction="row"
          justifyContent="space-between"
          alignItems="center"
          sx={{ mb: 2 }}
        >
          <Typography variant="subtitle2" fontWeight={700}>
            Конкуренты по SERP
          </Typography>
          <Button
            size="small"
            startIcon={<RefreshOutlined />}
            onClick={() => extractCompetitorsMutation.mutate()}
            disabled={extractCompetitorsMutation.isPending}
            sx={{ textTransform: 'none' }}
          >
            {extractCompetitorsMutation.isPending ? 'Обновление...' : 'Обновить'}
          </Button>
        </Stack>

        {competitorsLoading && <LinearProgress sx={{ mb: 2 }} />}

        {!competitorsLoading && (!competitors?.data?.length) ? (
          <Paper
            variant="outlined"
            sx={{ p: 4, borderRadius: 2.5, textAlign: 'center' }}
          >
            <TrackChangesOutlined sx={{ fontSize: 48, color: 'text.disabled', mb: 1 }} />
            <Typography variant="body2" color="text.secondary">
              Нет данных о конкурентах. Нажмите "Обновить" для извлечения из SERP.
            </Typography>
          </Paper>
        ) : (
          competitors?.data && competitors.data.length > 0 && (
            <TableContainer
              component={Paper}
              variant="outlined"
              sx={{ borderRadius: 2.5 }}
            >
              <Table size="small">
                <TableHead>
                  <TableRow>
                    <TableCell sx={{ fontWeight: 600 }}>Конкурент</TableCell>
                    <TableCell align="right" sx={{ fontWeight: 600 }}>
                      Цена (конкурент)
                    </TableCell>
                    <TableCell align="right" sx={{ fontWeight: 600 }}>
                      Цена (наша)
                    </TableCell>
                    <TableCell align="right" sx={{ fontWeight: 600 }}>
                      Позиция (их)
                    </TableCell>
                    <TableCell align="right" sx={{ fontWeight: 600 }}>
                      Позиция (наша)
                    </TableCell>
                    <TableCell sx={{ fontWeight: 600 }}>Запрос</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {competitors.data.map((comp: {
                    id: string;
                    competitor_nm_id: number;
                    competitor_title: string;
                    competitor_price: number;
                    last_position: number;
                    our_position: number;
                    query: string;
                  }) => {
                    const weAreBetter = comp.our_position < comp.last_position;
                    const priceDiff =
                      product.price != null && comp.competitor_price
                        ? product.price - comp.competitor_price
                        : null;
                    return (
                      <TableRow key={comp.id} hover>
                        <TableCell>
                          <Stack spacing={0}>
                            <Typography
                              variant="body2"
                              fontWeight={500}
                              noWrap
                              sx={{ maxWidth: 250 }}
                            >
                              {comp.competitor_title}
                            </Typography>
                            <Typography variant="caption" color="text.secondary">
                              nmID: {comp.competitor_nm_id}
                            </Typography>
                          </Stack>
                        </TableCell>
                        <TableCell align="right">
                          <Typography variant="body2">
                            {comp.competitor_price
                              ? `${comp.competitor_price.toLocaleString('ru')} \u20BD`
                              : '\u2014'}
                          </Typography>
                        </TableCell>
                        <TableCell align="right">
                          <Stack
                            direction="row"
                            spacing={0.5}
                            alignItems="center"
                            justifyContent="flex-end"
                          >
                            <Typography variant="body2">
                              {product.price != null
                                ? `${product.price.toLocaleString('ru')} \u20BD`
                                : '\u2014'}
                            </Typography>
                            {priceDiff != null && priceDiff !== 0 && (
                              <Typography
                                variant="caption"
                                color={priceDiff < 0 ? 'success.main' : 'error.main'}
                                fontWeight={600}
                              >
                                ({priceDiff > 0 ? '+' : ''}
                                {priceDiff.toLocaleString('ru')})
                              </Typography>
                            )}
                          </Stack>
                        </TableCell>
                        <TableCell align="right">
                          <Typography variant="body2" fontFamily="monospace">
                            #{comp.last_position}
                          </Typography>
                        </TableCell>
                        <TableCell align="right">
                          <Typography
                            variant="body2"
                            fontFamily="monospace"
                            fontWeight={600}
                            color={weAreBetter ? 'success.main' : 'error.main'}
                          >
                            #{comp.our_position}
                          </Typography>
                        </TableCell>
                        <TableCell>
                          <Chip
                            label={comp.query}
                            size="small"
                            variant="outlined"
                            sx={{
                              borderRadius: 1,
                              maxWidth: 200,
                              fontSize: '0.72rem',
                            }}
                          />
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </TableContainer>
          )
        )}
      </TabPanel>

      {/* ====================================================== */}
      {/*  TAB 2: SEO                                             */}
      {/* ====================================================== */}
      <TabPanel value={tab} index={2}>
        <Stack
          direction="row"
          justifyContent="space-between"
          alignItems="center"
          sx={{ mb: 2 }}
        >
          <Typography variant="subtitle2" fontWeight={700}>
            SEO-анализ
          </Typography>
          <Button
            size="small"
            startIcon={<RefreshOutlined />}
            onClick={() => analyzeSEOMutation.mutate()}
            disabled={analyzeSEOMutation.isPending}
            sx={{ textTransform: 'none' }}
          >
            {analyzeSEOMutation.isPending ? 'Анализ...' : 'Анализировать'}
          </Button>
        </Stack>

        {seoLoading && <LinearProgress sx={{ mb: 2 }} />}

        {!seoLoading && !seoAnalysis ? (
          <Paper
            variant="outlined"
            sx={{ p: 4, borderRadius: 2.5, textAlign: 'center' }}
          >
            <ErrorOutlineOutlined
              sx={{ fontSize: 48, color: 'text.disabled', mb: 1 }}
            />
            <Typography variant="body2" color="text.secondary">
              SEO-анализ ещё не проводился. Нажмите "Анализировать" для запуска.
            </Typography>
          </Paper>
        ) : (
          seoAnalysis && (
            <Stack spacing={2}>
              {/* Score cards */}
              <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}>
                <Paper
                  variant="outlined"
                  sx={{
                    p: 2.5,
                    borderRadius: 2.5,
                    flex: 1,
                    textAlign: 'center',
                  }}
                >
                  <Typography
                    variant="caption"
                    color="text.secondary"
                    fontWeight={500}
                    sx={{ textTransform: 'uppercase', letterSpacing: '0.05em' }}
                  >
                    Общий балл
                  </Typography>
                  <Typography
                    variant="h4"
                    fontWeight={700}
                    color={seoScoreColor(seoAnalysis.overall_score)}
                    sx={{ mt: 0.5 }}
                  >
                    {seoAnalysis.overall_score}
                  </Typography>
                  <LinearProgress
                    variant="determinate"
                    value={seoAnalysis.overall_score}
                    color={
                      seoAnalysis.overall_score >= 70
                        ? 'success'
                        : seoAnalysis.overall_score >= 40
                          ? 'warning'
                          : 'error'
                    }
                    sx={{ mt: 1, borderRadius: 1, height: 6 }}
                  />
                </Paper>
                <Paper
                  variant="outlined"
                  sx={{
                    p: 2.5,
                    borderRadius: 2.5,
                    flex: 1,
                    textAlign: 'center',
                  }}
                >
                  <Typography
                    variant="caption"
                    color="text.secondary"
                    fontWeight={500}
                    sx={{ textTransform: 'uppercase', letterSpacing: '0.05em' }}
                  >
                    Заголовок
                  </Typography>
                  <Typography
                    variant="h4"
                    fontWeight={700}
                    color={seoScoreColor(seoAnalysis.title_score)}
                    sx={{ mt: 0.5 }}
                  >
                    {seoAnalysis.title_score}
                  </Typography>
                  <LinearProgress
                    variant="determinate"
                    value={seoAnalysis.title_score}
                    color={
                      seoAnalysis.title_score >= 70
                        ? 'success'
                        : seoAnalysis.title_score >= 40
                          ? 'warning'
                          : 'error'
                    }
                    sx={{ mt: 1, borderRadius: 1, height: 6 }}
                  />
                </Paper>
                <Paper
                  variant="outlined"
                  sx={{
                    p: 2.5,
                    borderRadius: 2.5,
                    flex: 1,
                    textAlign: 'center',
                  }}
                >
                  <Typography
                    variant="caption"
                    color="text.secondary"
                    fontWeight={500}
                    sx={{ textTransform: 'uppercase', letterSpacing: '0.05em' }}
                  >
                    Ключевые слова
                  </Typography>
                  <Typography
                    variant="h4"
                    fontWeight={700}
                    color={seoScoreColor(seoAnalysis.keywords_score)}
                    sx={{ mt: 0.5 }}
                  >
                    {seoAnalysis.keywords_score}
                  </Typography>
                  <LinearProgress
                    variant="determinate"
                    value={seoAnalysis.keywords_score}
                    color={
                      seoAnalysis.keywords_score >= 70
                        ? 'success'
                        : seoAnalysis.keywords_score >= 40
                          ? 'warning'
                          : 'error'
                    }
                    sx={{ mt: 1, borderRadius: 1, height: 6 }}
                  />
                </Paper>
              </Stack>

              {/* Issues list */}
              {seoAnalysis.title_issues?.length > 0 && (
                <Paper variant="outlined" sx={{ p: 2.5, borderRadius: 2.5 }}>
                  <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1.5 }}>
                    Проблемы ({seoAnalysis.title_issues.length})
                  </Typography>
                  <Stack spacing={1}>
                    {seoAnalysis.title_issues.map(
                      (
                        issue: { type: string; severity: string; message: string },
                        i: number,
                      ) => (
                        <Alert
                          key={i}
                          severity={
                            issue.severity === 'high'
                              ? 'error'
                              : issue.severity === 'medium'
                                ? 'warning'
                                : 'info'
                          }
                          icon={
                            issue.severity === 'high' ? (
                              <ErrorOutlineOutlined fontSize="small" />
                            ) : issue.severity === 'medium' ? (
                              <WarningAmberOutlined fontSize="small" />
                            ) : (
                              <InfoOutlined fontSize="small" />
                            )
                          }
                          sx={{ borderRadius: 1.5 }}
                        >
                          <Stack direction="row" spacing={1} alignItems="center">
                            <Chip
                              label={issue.type}
                              size="small"
                              sx={{
                                height: 20,
                                fontSize: '0.68rem',
                                borderRadius: 1,
                              }}
                            />
                            <Typography variant="body2">{issue.message}</Typography>
                          </Stack>
                        </Alert>
                      ),
                    )}
                  </Stack>
                </Paper>
              )}

              {/* Recommendations list */}
              {seoAnalysis.recommendations?.length > 0 && (
                <Paper variant="outlined" sx={{ p: 2.5, borderRadius: 2.5 }}>
                  <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1.5 }}>
                    Рекомендации ({seoAnalysis.recommendations.length})
                  </Typography>
                  <Stack spacing={1}>
                    {seoAnalysis.recommendations.map(
                      (
                        rec: {
                          type: string;
                          priority: number;
                          message: string;
                          suggestion?: string;
                        },
                        i: number,
                      ) => (
                        <Paper
                          key={i}
                          variant="outlined"
                          sx={{
                            p: 1.5,
                            borderRadius: 2,
                            display: 'flex',
                            alignItems: 'flex-start',
                            gap: 1.5,
                          }}
                        >
                          <CheckCircleOutlineOutlined
                            sx={{
                              fontSize: 20,
                              mt: 0.25,
                              color:
                                rec.priority <= 1
                                  ? 'error.main'
                                  : rec.priority <= 3
                                    ? 'warning.main'
                                    : 'info.main',
                            }}
                          />
                          <Box sx={{ flex: 1, minWidth: 0 }}>
                            <Stack
                              direction="row"
                              spacing={1}
                              alignItems="center"
                              sx={{ mb: 0.5 }}
                            >
                              <Chip
                                label={rec.type}
                                size="small"
                                sx={{
                                  height: 20,
                                  fontSize: '0.68rem',
                                  borderRadius: 1,
                                }}
                              />
                              <Chip
                                label={`Приоритет: ${rec.priority}`}
                                size="small"
                                variant="outlined"
                                sx={{
                                  height: 20,
                                  fontSize: '0.68rem',
                                  borderRadius: 1,
                                }}
                              />
                            </Stack>
                            <Typography variant="body2">{rec.message}</Typography>
                            {rec.suggestion && (
                              <Typography
                                variant="caption"
                                color="primary.main"
                                sx={{ mt: 0.5, display: 'block', fontWeight: 500 }}
                              >
                                {rec.suggestion}
                              </Typography>
                            )}
                          </Box>
                        </Paper>
                      ),
                    )}
                  </Stack>
                </Paper>
              )}
            </Stack>
          )
        )}
      </TabPanel>

      {/* ====================================================== */}
      {/*  TAB 3: Events                                          */}
      {/* ====================================================== */}
      <TabPanel value={tab} index={3}>
        <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 2 }}>
          История изменений
        </Typography>

        {eventsLoading && <LinearProgress sx={{ mb: 2 }} />}

        {!eventsLoading && (!events?.data?.length) ? (
          <Paper
            variant="outlined"
            sx={{ p: 4, borderRadius: 2.5, textAlign: 'center' }}
          >
            <TimelineOutlined sx={{ fontSize: 48, color: 'text.disabled', mb: 1 }} />
            <Typography variant="body2" color="text.secondary">
              Нет событий. Данные появятся после следующей синхронизации.
            </Typography>
          </Paper>
        ) : (
          events?.data && events.data.length > 0 && (
            <Stack spacing={1.5}>
              {events.data.map(
                (event: {
                  id: string;
                  event_type: string;
                  field_name: string;
                  old_value: string;
                  new_value: string;
                  detected_at: string;
                  source: string;
                }) => (
                  <Paper
                    key={event.id}
                    variant="outlined"
                    sx={{
                      p: 2,
                      borderRadius: 2,
                      display: 'flex',
                      alignItems: 'flex-start',
                      gap: 2,
                    }}
                  >
                    {/* Timeline dot + icon */}
                    <Box
                      sx={{
                        width: 36,
                        height: 36,
                        borderRadius: '50%',
                        bgcolor: 'action.hover',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        flexShrink: 0,
                      }}
                    >
                      {getEventIcon(event.event_type)}
                    </Box>

                    {/* Content */}
                    <Box sx={{ flex: 1, minWidth: 0 }}>
                      <Stack
                        direction="row"
                        spacing={1}
                        alignItems="center"
                        flexWrap="wrap"
                        useFlexGap
                        sx={{ mb: 0.5 }}
                      >
                        <Chip
                          label={
                            EVENT_TYPE_LABELS[event.event_type] || event.event_type
                          }
                          size="small"
                          color={EVENT_TYPE_COLORS[event.event_type] || 'default'}
                          variant="outlined"
                          sx={{ borderRadius: 1, fontSize: '0.72rem' }}
                        />
                        {event.field_name && (
                          <Typography
                            variant="caption"
                            color="text.secondary"
                            fontWeight={500}
                          >
                            {event.field_name}
                          </Typography>
                        )}
                        <Typography variant="caption" color="text.secondary">
                          {fmtDateTime(event.detected_at)}
                        </Typography>
                      </Stack>

                      <Stack
                        direction="row"
                        spacing={1}
                        alignItems="center"
                        flexWrap="wrap"
                        useFlexGap
                      >
                        <Typography
                          variant="body2"
                          color="text.secondary"
                          noWrap
                          sx={{ maxWidth: 200, textDecoration: 'line-through' }}
                        >
                          {event.old_value || '\u2014'}
                        </Typography>
                        <Typography variant="body2" color="text.secondary">
                          {'\u2192'}
                        </Typography>
                        <Typography
                          variant="body2"
                          fontWeight={600}
                          noWrap
                          sx={{ maxWidth: 200 }}
                        >
                          {event.new_value || '\u2014'}
                        </Typography>
                      </Stack>

                      {event.source && (
                        <Typography
                          variant="caption"
                          color="text.secondary"
                          sx={{ mt: 0.25, display: 'block' }}
                        >
                          Источник: {event.source}
                        </Typography>
                      )}
                    </Box>
                  </Paper>
                ),
              )}
            </Stack>
          )
        )}
      </TabPanel>

      {/* ====================================================== */}
      {/*  TAB 4: Positions                                       */}
      {/* ====================================================== */}
      <TabPanel value={tab} index={4}>
        {/* Position Tracking Targets */}
        <Stack
          direction="row"
          justifyContent="space-between"
          alignItems="center"
          sx={{ mb: 2 }}
        >
          <Typography variant="subtitle2" fontWeight={700}>
            Цели отслеживания
          </Typography>
          <Button
            size="small"
            startIcon={<AddOutlined />}
            onClick={() => setTargetDialogOpen(true)}
            sx={{ textTransform: 'none' }}
          >
            Добавить
          </Button>
        </Stack>

        {targetsLoading && <LinearProgress sx={{ mb: 2 }} />}

        {!targetsLoading &&
          (!positionTargets?.data?.length ? (
            <Paper
              variant="outlined"
              sx={{ p: 3, borderRadius: 2.5, textAlign: 'center', mb: 3 }}
            >
              <TrackChangesOutlined
                sx={{ fontSize: 40, color: 'text.disabled', mb: 1 }}
              />
              <Typography variant="body2" color="text.secondary">
                Нет целей отслеживания. Добавьте запрос и регион для мониторинга
                позиций.
              </Typography>
            </Paper>
          ) : (
            <TableContainer
              component={Paper}
              variant="outlined"
              sx={{ borderRadius: 2.5, mb: 3 }}
            >
              <Table size="small">
                <TableHead>
                  <TableRow>
                    <TableCell sx={{ fontWeight: 600 }}>Запрос</TableCell>
                    <TableCell sx={{ fontWeight: 600 }}>Регион</TableCell>
                    <TableCell align="right" sx={{ fontWeight: 600 }}>
                      Базовая позиция
                    </TableCell>
                    <TableCell align="right" sx={{ fontWeight: 600 }}>
                      Текущая позиция
                    </TableCell>
                    <TableCell align="center" sx={{ fontWeight: 600 }}>
                      Изменение
                    </TableCell>
                    <TableCell align="center" sx={{ fontWeight: 600 }}>
                      Статус
                    </TableCell>
                    <TableCell sx={{ fontWeight: 600 }}>Обновлено</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {positionTargets.data.map((target: PositionTrackingTarget) => (
                    <TableRow key={target.id} hover>
                      <TableCell>
                        <Typography variant="body2" fontWeight={500}>
                          {target.query}
                        </Typography>
                      </TableCell>
                      <TableCell>
                        <Chip
                          label={target.region}
                          size="small"
                          variant="outlined"
                          sx={{ borderRadius: 1, fontSize: '0.72rem' }}
                        />
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2" fontFamily="monospace">
                          {target.baseline_position != null
                            ? `#${target.baseline_position}`
                            : '\u2014'}
                        </Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography
                          variant="body2"
                          fontFamily="monospace"
                          fontWeight={600}
                        >
                          {target.latest_position != null
                            ? `#${target.latest_position}`
                            : '\u2014'}
                        </Typography>
                        {target.latest_page != null && (
                          <Typography variant="caption" color="text.secondary">
                            (стр. {target.latest_page})
                          </Typography>
                        )}
                      </TableCell>
                      <TableCell align="center">
                        <PositionDelta delta={target.delta} />
                      </TableCell>
                      <TableCell align="center">
                        <Stack
                          direction="row"
                          spacing={0.5}
                          alignItems="center"
                          justifyContent="center"
                        >
                          {target.is_active ? (
                            <Chip
                              label="Активна"
                              size="small"
                              color="success"
                              sx={{
                                height: 20,
                                fontSize: '0.68rem',
                                borderRadius: 1,
                              }}
                            />
                          ) : (
                            <Chip
                              label="Отключена"
                              size="small"
                              variant="outlined"
                              sx={{
                                height: 20,
                                fontSize: '0.68rem',
                                borderRadius: 1,
                              }}
                            />
                          )}
                          {target.alert_candidate && (
                            <Tooltip title={`Алерт: ${target.alert_severity || 'unknown'}`}>
                              <WarningAmberOutlined
                                sx={{
                                  fontSize: 16,
                                  color:
                                    target.alert_severity === 'critical'
                                      ? 'error.main'
                                      : 'warning.main',
                                }}
                              />
                            </Tooltip>
                          )}
                        </Stack>
                      </TableCell>
                      <TableCell>
                        <Typography variant="caption" color="text.secondary">
                          {fmtDateTime(target.latest_checked_at)}
                        </Typography>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
          ))}

        {/* Position History */}
        <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 2 }}>
          История позиций
        </Typography>

        {positionsLoading && <LinearProgress sx={{ mb: 2 }} />}

        {!positionsLoading &&
          (!positionHistory?.data?.length ? (
            <Paper
              variant="outlined"
              sx={{ p: 3, borderRadius: 2.5, textAlign: 'center' }}
            >
              <Typography variant="body2" color="text.secondary">
                Нет данных по позициям. Добавьте цели отслеживания для сбора данных.
              </Typography>
            </Paper>
          ) : (
            <TableContainer
              component={Paper}
              variant="outlined"
              sx={{ borderRadius: 2.5 }}
            >
              <Table size="small">
                <TableHead>
                  <TableRow>
                    <TableCell sx={{ fontWeight: 600 }}>Запрос</TableCell>
                    <TableCell sx={{ fontWeight: 600 }}>Регион</TableCell>
                    <TableCell align="right" sx={{ fontWeight: 600 }}>
                      Позиция
                    </TableCell>
                    <TableCell align="right" sx={{ fontWeight: 600 }}>
                      Страница
                    </TableCell>
                    <TableCell sx={{ fontWeight: 600 }}>Источник</TableCell>
                    <TableCell sx={{ fontWeight: 600 }}>Дата</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {positionHistory.data.map((pos: Position) => (
                    <TableRow key={pos.id} hover>
                      <TableCell>
                        <Typography variant="body2" fontWeight={500}>
                          {pos.query}
                        </Typography>
                      </TableCell>
                      <TableCell>
                        <Typography variant="body2">
                          {pos.region || '\u2014'}
                        </Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography
                          variant="body2"
                          fontFamily="monospace"
                          fontWeight={600}
                        >
                          #{pos.position}
                        </Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">
                          {pos.page != null ? pos.page : '\u2014'}
                        </Typography>
                      </TableCell>
                      <TableCell>
                        {pos.source && (
                          <Chip
                            label={pos.source}
                            size="small"
                            variant="outlined"
                            sx={{ borderRadius: 1, fontSize: '0.68rem' }}
                          />
                        )}
                      </TableCell>
                      <TableCell>
                        <Typography variant="caption" color="text.secondary">
                          {fmtDateTime(pos.checked_at || pos.created_at)}
                        </Typography>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
          ))}
      </TabPanel>

      {/* ====================================================== */}
      {/*  DIALOG: Create Position Target                         */}
      {/* ====================================================== */}
      <Dialog
        open={targetDialogOpen}
        onClose={() => setTargetDialogOpen(false)}
        maxWidth="sm"
        fullWidth
      >
        <DialogTitle sx={{ fontWeight: 700 }}>
          Новая цель отслеживания
        </DialogTitle>
        <DialogContent>
          <Stack spacing={2} sx={{ mt: 1 }}>
            <TextField
              label="Поисковый запрос"
              placeholder="например: кроссовки мужские"
              value={newTargetQuery}
              onChange={(e) => setNewTargetQuery(e.target.value)}
              fullWidth
              size="small"
              autoFocus
            />
            <TextField
              label="Регион"
              placeholder="например: Москва"
              value={newTargetRegion}
              onChange={(e) => setNewTargetRegion(e.target.value)}
              fullWidth
              size="small"
            />
          </Stack>
        </DialogContent>
        <DialogActions sx={{ px: 3, pb: 2 }}>
          <Button
            onClick={() => setTargetDialogOpen(false)}
            sx={{ textTransform: 'none' }}
          >
            Отмена
          </Button>
          <Button
            variant="contained"
            disabled={
              !newTargetQuery.trim() ||
              !newTargetRegion.trim() ||
              createTargetMutation.isPending
            }
            onClick={() =>
              createTargetMutation.mutate({
                product_id: id!,
                query: newTargetQuery.trim(),
                region: newTargetRegion.trim(),
              })
            }
            sx={{ textTransform: 'none' }}
          >
            {createTargetMutation.isPending ? 'Создание...' : 'Создать'}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
};
