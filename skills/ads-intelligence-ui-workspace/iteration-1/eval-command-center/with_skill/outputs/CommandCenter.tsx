import React, { useState, useMemo, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'react-hot-toast';
import {
  Box,
  Paper,
  Typography,
  Select,
  MenuItem,
  FormControl,
  InputLabel,
  Button,
  Chip,
  Tabs,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TableSortLabel,
  Stack,
  Skeleton,
  IconButton,
  Tooltip,
  useMediaQuery,
  useTheme,
  Grid,
  Divider,
  alpha,
} from '@mui/material';
import type { SelectChangeEvent } from '@mui/material';
import SyncIcon from '@mui/icons-material/Sync';
import TrendingUpIcon from '@mui/icons-material/TrendingUp';
import TrendingDownIcon from '@mui/icons-material/TrendingDown';
import TrendingFlatIcon from '@mui/icons-material/TrendingFlat';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import PauseCircleOutlineIcon from '@mui/icons-material/PauseCircleOutline';
import VisibilityOffIcon from '@mui/icons-material/VisibilityOff';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import StorefrontIcon from '@mui/icons-material/Storefront';
import CampaignIcon from '@mui/icons-material/Campaign';
import InventoryIcon from '@mui/icons-material/Inventory2';
import SearchIcon from '@mui/icons-material/Search';
import PeopleIcon from '@mui/icons-material/People';
import SpeedIcon from '@mui/icons-material/Speed';
import AttachMoneyIcon from '@mui/icons-material/AttachMoney';
import ShoppingCartIcon from '@mui/icons-material/ShoppingCart';
import ShowChartIcon from '@mui/icons-material/ShowChart';
import { adsIntelligenceApi } from '@/modules/ads-intelligence/api/adsIntelligenceApi';
import type {
  AdsOverview,
  SellerCabinet,
  Campaign,
  Product,
  Phrase,
  Recommendation,
  PaginatedResponse,
} from '@/modules/ads-intelligence/types';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatNumber(n: number | undefined | null): string {
  if (n == null) return '--';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)} M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)} K`;
  return n.toLocaleString('ru-RU');
}

function formatCurrency(n: number | undefined | null): string {
  if (n == null) return '--';
  return n.toLocaleString('ru-RU', { style: 'currency', currency: 'RUB', maximumFractionDigits: 0 });
}

function formatPercent(n: number | undefined | null, decimals = 1): string {
  if (n == null) return '--';
  return `${n.toFixed(decimals)}%`;
}

function formatRoas(n: number | undefined | null): string {
  if (n == null || n === 0) return '--';
  return n.toFixed(2);
}

function deltaColor(delta: number): 'success.main' | 'error.main' | 'text.secondary' {
  if (delta > 0) return 'success.main';
  if (delta < 0) return 'error.main';
  return 'text.secondary';
}

function deltaSign(delta: number): string {
  if (delta > 0) return '+';
  return '';
}

function trendIcon(trend: string) {
  switch (trend) {
    case 'improving':
      return <TrendingUpIcon fontSize="small" sx={{ color: 'success.main' }} />;
    case 'declining':
      return <TrendingDownIcon fontSize="small" sx={{ color: 'error.main' }} />;
    default:
      return <TrendingFlatIcon fontSize="small" sx={{ color: 'text.secondary' }} />;
  }
}

function severityColor(severity: string): 'error' | 'warning' | 'info' | 'default' {
  switch (severity) {
    case 'critical':
      return 'error';
    case 'high':
      return 'error';
    case 'medium':
      return 'warning';
    default:
      return 'info';
  }
}

function severityBorderColor(severity: string): string {
  switch (severity) {
    case 'critical':
      return '#d32f2f';
    case 'high':
      return '#f44336';
    case 'medium':
      return '#ff9800';
    default:
      return '#2196f3';
  }
}

function healthChip(status?: string) {
  if (!status) return null;
  const color =
    status === 'healthy' || status === 'active'
      ? 'success'
      : status === 'warning' || status === 'paused'
        ? 'warning'
        : status === 'critical' || status === 'error' || status === 'stopped'
          ? 'error'
          : 'default';
  const label =
    status === 'active'
      ? 'Активна'
      : status === 'paused'
        ? 'Пауза'
        : status === 'stopped'
          ? 'Остановлена'
          : status === 'healthy'
            ? 'Норма'
            : status === 'warning'
              ? 'Внимание'
              : status === 'critical'
                ? 'Критично'
                : status;
  return <Chip label={label} color={color as any} size="small" variant="outlined" />;
}

function computeRoas(revenue?: number, spend?: number): number {
  if (!spend || spend === 0) return 0;
  return (revenue ?? 0) / spend;
}

// ---------------------------------------------------------------------------
// Date helpers
// ---------------------------------------------------------------------------

function getDateRange(days: number) {
  const to = new Date();
  const from = new Date();
  from.setDate(from.getDate() - days);
  return {
    date_from: from.toISOString().slice(0, 10),
    date_to: to.toISOString().slice(0, 10),
  };
}

// ---------------------------------------------------------------------------
// Sort helpers
// ---------------------------------------------------------------------------

type Order = 'asc' | 'desc';

function descendingComparator<T>(a: T, b: T, orderBy: keyof T): number {
  const aVal = a[orderBy] ?? 0;
  const bVal = b[orderBy] ?? 0;
  if (bVal < aVal) return -1;
  if (bVal > aVal) return 1;
  return 0;
}

function getComparator<T>(order: Order, orderBy: keyof T) {
  return order === 'desc'
    ? (a: T, b: T) => descendingComparator(a, b, orderBy)
    : (a: T, b: T) => -descendingComparator(a, b, orderBy);
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

// ---- Metric Card ----

interface MetricCardProps {
  title: string;
  value: string;
  delta?: number;
  deltaLabel?: string;
  trend?: string;
  icon: React.ReactNode;
  loading?: boolean;
  color?: string;
}

function MetricCard({ title, value, delta, deltaLabel, trend, icon, loading, color }: MetricCardProps) {
  if (loading) {
    return (
      <Paper variant="outlined" sx={{ p: 2.5, borderRadius: 3, flex: 1, minWidth: 200 }}>
        <Skeleton variant="text" width="60%" height={20} />
        <Skeleton variant="text" width="80%" height={40} sx={{ mt: 1 }} />
        <Skeleton variant="text" width="50%" height={18} sx={{ mt: 0.5 }} />
      </Paper>
    );
  }

  return (
    <Paper
      variant="outlined"
      sx={{
        p: 2.5,
        borderRadius: 3,
        flex: 1,
        minWidth: 200,
        borderLeft: color ? `4px solid ${color}` : undefined,
      }}
    >
      <Stack direction="row" alignItems="center" spacing={1} sx={{ mb: 1 }}>
        {icon}
        <Typography variant="caption" color="text.secondary" fontWeight={600}>
          {title}
        </Typography>
      </Stack>
      <Typography variant="h5" fontWeight={700}>
        {value}
      </Typography>
      {delta !== undefined && (
        <Stack direction="row" alignItems="center" spacing={0.5} sx={{ mt: 0.5 }}>
          {trend && trendIcon(trend)}
          <Typography variant="caption" sx={{ color: deltaColor(delta) }}>
            {deltaSign(delta)}
            {deltaLabel || formatPercent(delta)}
          </Typography>
          <Typography variant="caption" color="text.secondary">
            vs. пред. период
          </Typography>
        </Stack>
      )}
    </Paper>
  );
}

// ---- Quick Stats Strip ----

interface QuickStatsProps {
  overview?: AdsOverview;
  loading?: boolean;
}

function QuickStatsStrip({ overview, loading }: QuickStatsProps) {
  if (loading) {
    return (
      <Stack direction="row" spacing={1} flexWrap="wrap" sx={{ mb: 2 }}>
        {[1, 2, 3, 4, 5].map((i) => (
          <Skeleton key={i} variant="rounded" width={130} height={32} sx={{ borderRadius: 4 }} />
        ))}
      </Stack>
    );
  }

  const totals = overview?.totals;
  const items = [
    {
      icon: <CampaignIcon fontSize="small" />,
      label: `${totals?.active_campaigns ?? 0} / ${totals?.campaigns ?? 0} кампаний`,
      tooltip: 'Активные / Всего кампаний',
    },
    {
      icon: <InventoryIcon fontSize="small" />,
      label: `${totals?.products ?? 0} товаров`,
      tooltip: 'Всего товаров',
    },
    {
      icon: <SearchIcon fontSize="small" />,
      label: `${totals?.queries ?? 0} фраз`,
      tooltip: 'Всего поисковых фраз',
    },
    {
      icon: <PeopleIcon fontSize="small" />,
      label: `${totals?.cabinets ?? 0} кабинетов`,
      tooltip: 'Подключенные кабинеты',
    },
    {
      icon: <WarningAmberIcon fontSize="small" />,
      label: `${totals?.attention_items ?? 0} внимание`,
      tooltip: 'Требуют внимания',
      color: (totals?.attention_items ?? 0) > 0 ? 'warning.main' : undefined,
    },
  ];

  return (
    <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap sx={{ mb: 2 }}>
      {items.map((item, idx) => (
        <Tooltip key={idx} title={item.tooltip} arrow>
          <Chip
            icon={item.icon}
            label={item.label}
            variant="outlined"
            size="small"
            sx={{
              borderRadius: 2,
              px: 0.5,
              ...(item.color ? { borderColor: item.color, color: item.color } : {}),
            }}
          />
        </Tooltip>
      ))}
    </Stack>
  );
}

// ---- Recommendation Card ----

interface RecommendationCardProps {
  rec: Recommendation;
  onApply: (id: string) => void;
  onPause: (id: string) => void;
  onDismiss: (id: string) => void;
  onNavigate: (rec: Recommendation) => void;
  applyLoading: boolean;
  pauseLoading: boolean;
  dismissLoading: boolean;
}

function RecommendationCard({
  rec,
  onApply,
  onPause,
  onDismiss,
  onNavigate,
  applyLoading,
  pauseLoading,
  dismissLoading,
}: RecommendationCardProps) {
  const severityIcon =
    rec.severity === 'critical' || rec.severity === 'high' ? (
      <ErrorOutlineIcon fontSize="small" color="error" />
    ) : rec.severity === 'medium' ? (
      <WarningAmberIcon fontSize="small" color="warning" />
    ) : (
      <InfoOutlinedIcon fontSize="small" color="info" />
    );

  const showApply = ['raise_bid', 'lower_bid'].includes(rec.type);
  const showPause = ['high_spend_low_orders', 'low_ctr'].includes(rec.type);
  const showDetails = [
    'position_drop',
    'new_competitor',
    'optimize_seo',
    'improve_title',
    'price_optimization',
    'stock_alert',
    'delivery_issue',
  ].includes(rec.type);

  return (
    <Paper
      variant="outlined"
      sx={{
        p: 2,
        borderRadius: 2.5,
        borderLeft: `4px solid ${severityBorderColor(rec.severity)}`,
        mb: 1.5,
      }}
    >
      <Stack direction="row" alignItems="flex-start" spacing={1}>
        {severityIcon}
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Stack direction="row" alignItems="center" spacing={1} flexWrap="wrap">
            <Typography variant="subtitle2" fontWeight={700} noWrap>
              {rec.title || rec.type}
            </Typography>
            <Chip
              label={rec.severity}
              color={severityColor(rec.severity)}
              size="small"
              sx={{ height: 20, fontSize: '0.7rem' }}
            />
            {rec.entity_name && (
              <Typography variant="caption" color="text.secondary" noWrap>
                {rec.entity_name}
              </Typography>
            )}
          </Stack>
          <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
            {rec.description}
          </Typography>
          {rec.potential_effect && (
            <Typography variant="caption" color="text.secondary" sx={{ mt: 0.25, display: 'block' }}>
              {rec.potential_effect}
            </Typography>
          )}
        </Box>
      </Stack>

      <Stack direction="row" spacing={1} sx={{ mt: 1.5 }} flexWrap="wrap" useFlexGap>
        {showApply && (
          <Button
            size="small"
            variant="contained"
            color="primary"
            disabled={applyLoading}
            onClick={() => onApply(rec.id)}
            startIcon={<CheckCircleOutlineIcon />}
          >
            {applyLoading ? '...' : 'Применить ставку'}
          </Button>
        )}
        {showPause && (
          <Button
            size="small"
            variant="outlined"
            color="warning"
            disabled={pauseLoading}
            onClick={() => onPause(rec.id)}
            startIcon={<PauseCircleOutlineIcon />}
          >
            {pauseLoading ? '...' : 'Пауза'}
          </Button>
        )}
        {showDetails && (
          <Button
            size="small"
            variant="outlined"
            onClick={() => onNavigate(rec)}
            startIcon={<OpenInNewIcon />}
          >
            Подробнее
          </Button>
        )}
        <Button
          size="small"
          variant="text"
          color="inherit"
          disabled={dismissLoading}
          onClick={() => onDismiss(rec.id)}
          startIcon={<VisibilityOffIcon />}
          sx={{ color: 'text.secondary' }}
        >
          {dismissLoading ? '...' : 'Скрыть'}
        </Button>
      </Stack>
    </Paper>
  );
}

// ---- Sortable Table Head Cell ----

interface SortableCellProps {
  label: string;
  field: string;
  order: Order;
  orderBy: string;
  onSort: (field: string) => void;
  align?: 'left' | 'right' | 'center';
  width?: number | string;
}

function SortableCell({ label, field, order, orderBy, onSort, align = 'left', width }: SortableCellProps) {
  return (
    <TableCell align={align} sx={{ fontWeight: 700, whiteSpace: 'nowrap', width }}>
      <TableSortLabel active={orderBy === field} direction={orderBy === field ? order : 'asc'} onClick={() => onSort(field)}>
        {label}
      </TableSortLabel>
    </TableCell>
  );
}

// ---------------------------------------------------------------------------
// Main Component
// ---------------------------------------------------------------------------

export default function CommandCenter() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down('md'));

  // ---- State ----
  const [selectedCabinetId, setSelectedCabinetId] = useState<string>('all');
  const [entityTab, setEntityTab] = useState(0);
  const [campaignOrder, setCampaignOrder] = useState<Order>('desc');
  const [campaignOrderBy, setCampaignOrderBy] = useState<string>('spend');
  const [productOrder, setProductOrder] = useState<Order>('desc');
  const [productOrderBy, setProductOrderBy] = useState<string>('spend');
  const [phraseOrder, setPhraseOrder] = useState<Order>('desc');
  const [phraseOrderBy, setPhraseOrderBy] = useState<string>('spent');

  const dateRange = useMemo(() => getDateRange(7), []);

  // ---- Queries ----
  const cabinetId = selectedCabinetId === 'all' ? undefined : selectedCabinetId;

  const { data: cabinets, isLoading: cabinetsLoading } = useQuery({
    queryKey: ['seller-cabinets'],
    queryFn: () => adsIntelligenceApi.getSellerCabinets({ page: 1, per_page: 100 }),
  });

  const { data: overview, isLoading: overviewLoading } = useQuery({
    queryKey: ['ads-overview', dateRange.date_from, dateRange.date_to, cabinetId],
    queryFn: () =>
      adsIntelligenceApi.getOverview({
        date_from: dateRange.date_from,
        date_to: dateRange.date_to,
        seller_cabinet_id: cabinetId,
      }),
  });

  const { data: recommendations, isLoading: recsLoading } = useQuery({
    queryKey: ['recommendations', 'active', cabinetId],
    queryFn: () =>
      adsIntelligenceApi.getRecommendations({
        status: 'active',
        page: 1,
        per_page: 10,
      }),
  });

  // ---- Mutations ----
  const syncMutation = useMutation({
    mutationFn: (id: string) => adsIntelligenceApi.triggerSync(id),
    onSuccess: () => {
      toast.success('Синхронизация запущена');
      queryClient.invalidateQueries({ queryKey: ['ads-overview'] });
      queryClient.invalidateQueries({ queryKey: ['seller-cabinets'] });
    },
    onError: () => toast.error('Не удалось запустить синхронизацию'),
  });

  const applyMutation = useMutation({
    mutationFn: (id: string) => adsIntelligenceApi.applyRecommendation(id),
    onSuccess: () => {
      toast.success('Рекомендация применена');
      queryClient.invalidateQueries({ queryKey: ['recommendations'] });
      queryClient.invalidateQueries({ queryKey: ['ads-overview'] });
    },
    onError: () => toast.error('Не удалось применить рекомендацию'),
  });

  const pauseCampaignMutation = useMutation({
    mutationFn: (id: string) => adsIntelligenceApi.pauseCampaign(id),
    onSuccess: () => {
      toast.success('Кампания поставлена на паузу');
      queryClient.invalidateQueries({ queryKey: ['ads-overview'] });
      queryClient.invalidateQueries({ queryKey: ['recommendations'] });
    },
    onError: () => toast.error('Не удалось поставить кампанию на паузу'),
  });

  const dismissMutation = useMutation({
    mutationFn: (id: string) => adsIntelligenceApi.dismissRecommendation(id),
    onSuccess: () => {
      toast.success('Рекомендация скрыта');
      queryClient.invalidateQueries({ queryKey: ['recommendations'] });
    },
    onError: () => toast.error('Не удалось скрыть рекомендацию'),
  });

  // ---- Handlers ----
  const handleCabinetChange = useCallback((e: SelectChangeEvent<string>) => {
    setSelectedCabinetId(e.target.value);
  }, []);

  const handleSync = useCallback(() => {
    if (cabinetId) {
      syncMutation.mutate(cabinetId);
    } else if (cabinets?.data?.length) {
      cabinets.data.forEach((c) => syncMutation.mutate(c.id));
    }
  }, [cabinetId, cabinets, syncMutation]);

  const handleRecNavigate = useCallback(
    (rec: Recommendation) => {
      if (rec.entity_type === 'campaign' && rec.entity_id) {
        navigate(`/ads-intelligence/campaigns/${rec.entity_id}`);
      } else if (rec.entity_type === 'product' && rec.entity_id) {
        navigate(`/ads-intelligence/products/${rec.entity_id}`);
      }
    },
    [navigate],
  );

  const handleRecPause = useCallback(
    (recId: string) => {
      const rec = recommendations?.data?.find((r) => r.id === recId);
      if (rec?.entity_type === 'campaign' && rec.entity_id) {
        pauseCampaignMutation.mutate(rec.entity_id);
      }
    },
    [recommendations, pauseCampaignMutation],
  );

  const handleCampaignSort = useCallback(
    (field: string) => {
      const isAsc = campaignOrderBy === field && campaignOrder === 'asc';
      setCampaignOrder(isAsc ? 'desc' : 'asc');
      setCampaignOrderBy(field);
    },
    [campaignOrder, campaignOrderBy],
  );

  const handleProductSort = useCallback(
    (field: string) => {
      const isAsc = productOrderBy === field && productOrder === 'asc';
      setProductOrder(isAsc ? 'desc' : 'asc');
      setProductOrderBy(field);
    },
    [productOrder, productOrderBy],
  );

  const handlePhraseSort = useCallback(
    (field: string) => {
      const isAsc = phraseOrderBy === field && phraseOrder === 'asc';
      setPhraseOrder(isAsc ? 'desc' : 'asc');
      setPhraseOrderBy(field);
    },
    [phraseOrder, phraseOrderBy],
  );

  // ---- Derived data ----
  const perf = overview?.performance_compare;
  const currentSpend = perf?.current?.spend ?? 0;
  const currentOrders = perf?.current?.orders ?? 0;
  const currentRevenue = perf?.current?.revenue ?? 0;
  const roas = computeRoas(currentRevenue, currentSpend);
  const deltaSpend = perf?.delta?.spend ?? 0;
  const deltaOrders = perf?.delta?.orders ?? 0;
  const prevRoas = computeRoas(perf?.previous?.revenue, perf?.previous?.spend);
  const deltaRoas = prevRoas > 0 ? ((roas - prevRoas) / prevRoas) * 100 : 0;
  const trend = perf?.trend ?? 'flat';

  const activeRecs = useMemo(() => {
    const recs = recommendations?.data ?? [];
    return [...recs].sort((a, b) => {
      const severityRank: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3 };
      return (severityRank[a.severity] ?? 4) - (severityRank[b.severity] ?? 4);
    });
  }, [recommendations]);

  const sortedCampaigns = useMemo(() => {
    const campaigns = overview?.top_campaigns ?? [];
    type CampaignSortable = Campaign & { spend: number; orders: number; roas: number; ctr: number };
    const enriched: CampaignSortable[] = campaigns.map((c) => ({
      ...c,
      spend: c.performance?.spend ?? 0,
      orders: c.performance?.orders ?? 0,
      roas: computeRoas(c.performance?.revenue, c.performance?.spend),
      ctr: c.performance?.ctr ?? 0,
    }));
    return enriched.sort(getComparator(campaignOrder, campaignOrderBy as keyof CampaignSortable));
  }, [overview?.top_campaigns, campaignOrder, campaignOrderBy]);

  const sortedProducts = useMemo(() => {
    const products = overview?.top_products ?? [];
    type ProductSortable = Product & { spend: number; orders: number; roas: number; ctr: number };
    const enriched: ProductSortable[] = products.map((p) => ({
      ...p,
      spend: p.performance?.spend ?? 0,
      orders: p.performance?.orders ?? 0,
      roas: computeRoas(p.performance?.revenue, p.performance?.spend),
      ctr: p.performance?.ctr ?? 0,
    }));
    return enriched.sort(getComparator(productOrder, productOrderBy as keyof ProductSortable));
  }, [overview?.top_products, productOrder, productOrderBy]);

  const sortedPhrases = useMemo(() => {
    const phrases = overview?.top_queries ?? [];
    return [...phrases].sort(getComparator(phraseOrder, phraseOrderBy as keyof Phrase));
  }, [overview?.top_queries, phraseOrder, phraseOrderBy]);

  const isLoading = overviewLoading;

  // ---------------------------------------------------------------------------
  // Render
  // ---------------------------------------------------------------------------

  return (
    <Box sx={{ p: { xs: 2, sm: 3 }, maxWidth: 1400, mx: 'auto' }}>
      {/* ---- Header bar ---- */}
      <Stack
        direction={{ xs: 'column', sm: 'row' }}
        alignItems={{ xs: 'stretch', sm: 'center' }}
        justifyContent="space-between"
        spacing={2}
        sx={{ mb: 3 }}
      >
        <Stack direction="row" alignItems="center" spacing={2}>
          <StorefrontIcon color="primary" />
          <Typography variant="h6" fontWeight={700}>
            Рекламная аналитика
          </Typography>
        </Stack>

        <Stack direction="row" alignItems="center" spacing={1.5} flexWrap="wrap" useFlexGap>
          <FormControl size="small" sx={{ minWidth: 220 }}>
            <InputLabel id="cabinet-select-label">Магазин</InputLabel>
            <Select
              labelId="cabinet-select-label"
              value={selectedCabinetId}
              label="Магазин"
              onChange={handleCabinetChange}
            >
              <MenuItem value="all">Все магазины</MenuItem>
              {(cabinets?.data ?? []).map((cab) => (
                <MenuItem key={cab.id} value={cab.id}>
                  {cab.name}
                </MenuItem>
              ))}
            </Select>
          </FormControl>

          <Tooltip title="Синхронизировать данные">
            <span>
              <Button
                variant="outlined"
                size="small"
                startIcon={<SyncIcon />}
                onClick={handleSync}
                disabled={syncMutation.isPending}
              >
                {syncMutation.isPending ? 'Синхр...' : 'Обновить'}
              </Button>
            </span>
          </Tooltip>
        </Stack>
      </Stack>

      {/* ---- Metric Cards ---- */}
      <Stack
        direction={{ xs: 'column', sm: 'row' }}
        spacing={2}
        sx={{ mb: 2 }}
      >
        <MetricCard
          title="Расход"
          value={formatCurrency(currentSpend)}
          delta={deltaSpend}
          deltaLabel={formatCurrency(deltaSpend)}
          trend={trend}
          icon={<AttachMoneyIcon fontSize="small" color="primary" />}
          loading={isLoading}
          color={theme.palette.primary.main}
        />
        <MetricCard
          title="Заказы"
          value={formatNumber(currentOrders)}
          delta={deltaOrders}
          deltaLabel={`${deltaSign(deltaOrders)}${formatNumber(Math.abs(deltaOrders))}`}
          trend={trend}
          icon={<ShoppingCartIcon fontSize="small" sx={{ color: '#4caf50' }} />}
          loading={isLoading}
          color="#4caf50"
        />
        <MetricCard
          title="ROAS"
          value={formatRoas(roas)}
          delta={deltaRoas}
          trend={roas >= 3 ? 'improving' : roas < 1 ? 'declining' : 'flat'}
          icon={<ShowChartIcon fontSize="small" sx={{ color: roas >= 3 ? '#4caf50' : roas < 1 ? '#f44336' : '#ff9800' }} />}
          loading={isLoading}
          color={roas >= 3 ? '#4caf50' : roas < 1 ? '#f44336' : '#ff9800'}
        />
      </Stack>

      {/* ---- Quick Stats Strip ---- */}
      <QuickStatsStrip overview={overview} loading={isLoading} />

      {/* ---- Action Feed (Recommendations) ---- */}
      <Paper variant="outlined" sx={{ p: 2, borderRadius: 3, mb: 2 }}>
        <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ mb: 1.5 }}>
          <Typography variant="subtitle2" fontWeight={700}>
            Рекомендации
          </Typography>
          {activeRecs.length > 0 && (
            <Chip label={`${activeRecs.length}`} size="small" color="warning" />
          )}
        </Stack>

        {recsLoading ? (
          <Stack spacing={1}>
            {[1, 2, 3].map((i) => (
              <Skeleton key={i} variant="rounded" height={80} sx={{ borderRadius: 2 }} />
            ))}
          </Stack>
        ) : activeRecs.length === 0 ? (
          <Typography variant="body2" color="text.secondary" sx={{ py: 3, textAlign: 'center' }}>
            Нет активных рекомендаций
          </Typography>
        ) : (
          activeRecs.map((rec) => (
            <RecommendationCard
              key={rec.id}
              rec={rec}
              onApply={(id) => applyMutation.mutate(id)}
              onPause={handleRecPause}
              onDismiss={(id) => dismissMutation.mutate(id)}
              onNavigate={handleRecNavigate}
              applyLoading={applyMutation.isPending}
              pauseLoading={pauseCampaignMutation.isPending}
              dismissLoading={dismissMutation.isPending}
            />
          ))
        )}
      </Paper>

      {/* ---- Entity Table with Tab Switcher ---- */}
      <Paper variant="outlined" sx={{ borderRadius: 3, overflow: 'hidden' }}>
        <Box sx={{ borderBottom: 1, borderColor: 'divider' }}>
          <Tabs
            value={entityTab}
            onChange={(_, v) => setEntityTab(v)}
            variant={isMobile ? 'fullWidth' : 'standard'}
            sx={{ px: 2 }}
          >
            <Tab
              label={
                <Stack direction="row" alignItems="center" spacing={0.5}>
                  <CampaignIcon fontSize="small" />
                  <span>Кампании</span>
                  {!isLoading && (
                    <Chip
                      label={sortedCampaigns.length}
                      size="small"
                      sx={{ height: 20, fontSize: '0.7rem' }}
                    />
                  )}
                </Stack>
              }
            />
            <Tab
              label={
                <Stack direction="row" alignItems="center" spacing={0.5}>
                  <InventoryIcon fontSize="small" />
                  <span>Товары</span>
                  {!isLoading && (
                    <Chip
                      label={sortedProducts.length}
                      size="small"
                      sx={{ height: 20, fontSize: '0.7rem' }}
                    />
                  )}
                </Stack>
              }
            />
            <Tab
              label={
                <Stack direction="row" alignItems="center" spacing={0.5}>
                  <SearchIcon fontSize="small" />
                  <span>Фразы</span>
                  {!isLoading && (
                    <Chip
                      label={sortedPhrases.length}
                      size="small"
                      sx={{ height: 20, fontSize: '0.7rem' }}
                    />
                  )}
                </Stack>
              }
            />
          </Tabs>
        </Box>

        {/* ---- Campaigns Table ---- */}
        {entityTab === 0 && (
          <TableContainer sx={{ maxHeight: 520 }}>
            {isLoading ? (
              <Box sx={{ p: 2 }}>
                {[1, 2, 3, 4, 5].map((i) => (
                  <Skeleton key={i} variant="rounded" height={48} sx={{ mb: 1, borderRadius: 1.5 }} />
                ))}
              </Box>
            ) : sortedCampaigns.length === 0 ? (
              <Typography variant="body2" color="text.secondary" sx={{ p: 4, textAlign: 'center' }}>
                Нет данных по кампаниям
              </Typography>
            ) : (
              <Table size="small" stickyHeader>
                <TableHead>
                  <TableRow>
                    <SortableCell label="Название" field="name" order={campaignOrder} orderBy={campaignOrderBy} onSort={handleCampaignSort} />
                    <SortableCell label="Статус" field="status" order={campaignOrder} orderBy={campaignOrderBy} onSort={handleCampaignSort} width={100} />
                    <SortableCell label="Расход" field="spend" order={campaignOrder} orderBy={campaignOrderBy} onSort={handleCampaignSort} align="right" />
                    <SortableCell label="Заказы" field="orders" order={campaignOrder} orderBy={campaignOrderBy} onSort={handleCampaignSort} align="right" />
                    <SortableCell label="ROAS" field="roas" order={campaignOrder} orderBy={campaignOrderBy} onSort={handleCampaignSort} align="right" />
                    <SortableCell label="CTR" field="ctr" order={campaignOrder} orderBy={campaignOrderBy} onSort={handleCampaignSort} align="right" />
                    {!isMobile && (
                      <TableCell align="center" sx={{ fontWeight: 700, width: 100 }}>
                        Здоровье
                      </TableCell>
                    )}
                  </TableRow>
                </TableHead>
                <TableBody>
                  {sortedCampaigns.map((c: any) => (
                    <TableRow
                      key={c.id}
                      hover
                      sx={{ cursor: 'pointer', '&:last-child td': { borderBottom: 0 } }}
                      onClick={() => navigate(`/ads-intelligence/campaigns/${c.id}`)}
                    >
                      <TableCell>
                        <Typography variant="body2" fontWeight={600} noWrap sx={{ maxWidth: 280 }}>
                          {c.name}
                        </Typography>
                        {c.cabinet_name && (
                          <Typography variant="caption" color="text.secondary">
                            {c.cabinet_name}
                          </Typography>
                        )}
                      </TableCell>
                      <TableCell>{healthChip(c.status)}</TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{formatCurrency(c.spend)}</Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{formatNumber(c.orders)}</Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography
                          variant="body2"
                          sx={{
                            color: c.roas >= 3 ? 'success.main' : c.roas < 1 ? 'error.main' : 'text.primary',
                            fontWeight: 600,
                          }}
                        >
                          {formatRoas(c.roas)}
                        </Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{formatPercent(c.ctr)}</Typography>
                      </TableCell>
                      {!isMobile && <TableCell align="center">{healthChip(c.health_status)}</TableCell>}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </TableContainer>
        )}

        {/* ---- Products Table ---- */}
        {entityTab === 1 && (
          <TableContainer sx={{ maxHeight: 520 }}>
            {isLoading ? (
              <Box sx={{ p: 2 }}>
                {[1, 2, 3, 4, 5].map((i) => (
                  <Skeleton key={i} variant="rounded" height={48} sx={{ mb: 1, borderRadius: 1.5 }} />
                ))}
              </Box>
            ) : sortedProducts.length === 0 ? (
              <Typography variant="body2" color="text.secondary" sx={{ p: 4, textAlign: 'center' }}>
                Нет данных по товарам
              </Typography>
            ) : (
              <Table size="small" stickyHeader>
                <TableHead>
                  <TableRow>
                    <SortableCell label="Товар" field="title" order={productOrder} orderBy={productOrderBy} onSort={handleProductSort} />
                    <SortableCell label="Расход" field="spend" order={productOrder} orderBy={productOrderBy} onSort={handleProductSort} align="right" />
                    <SortableCell label="Заказы" field="orders" order={productOrder} orderBy={productOrderBy} onSort={handleProductSort} align="right" />
                    <SortableCell label="ROAS" field="roas" order={productOrder} orderBy={productOrderBy} onSort={handleProductSort} align="right" />
                    <SortableCell label="CTR" field="ctr" order={productOrder} orderBy={productOrderBy} onSort={handleProductSort} align="right" />
                    {!isMobile && (
                      <>
                        <SortableCell label="Цена" field="price" order={productOrder} orderBy={productOrderBy} onSort={handleProductSort} align="right" />
                        <TableCell align="center" sx={{ fontWeight: 700, width: 100 }}>
                          Здоровье
                        </TableCell>
                      </>
                    )}
                  </TableRow>
                </TableHead>
                <TableBody>
                  {sortedProducts.map((p: any) => (
                    <TableRow
                      key={p.id}
                      hover
                      sx={{ cursor: 'pointer', '&:last-child td': { borderBottom: 0 } }}
                      onClick={() => navigate(`/ads-intelligence/products/${p.id}`)}
                    >
                      <TableCell>
                        <Stack direction="row" alignItems="center" spacing={1.5}>
                          {p.image_url && (
                            <Box
                              component="img"
                              src={p.image_url}
                              alt={p.title}
                              sx={{ width: 36, height: 36, borderRadius: 1, objectFit: 'cover', flexShrink: 0 }}
                            />
                          )}
                          <Box sx={{ minWidth: 0 }}>
                            <Typography variant="body2" fontWeight={600} noWrap sx={{ maxWidth: 260 }}>
                              {p.title}
                            </Typography>
                            {p.brand && (
                              <Typography variant="caption" color="text.secondary">
                                {p.brand}
                              </Typography>
                            )}
                          </Box>
                        </Stack>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{formatCurrency(p.spend)}</Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{formatNumber(p.orders)}</Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography
                          variant="body2"
                          sx={{
                            color: p.roas >= 3 ? 'success.main' : p.roas < 1 ? 'error.main' : 'text.primary',
                            fontWeight: 600,
                          }}
                        >
                          {formatRoas(p.roas)}
                        </Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{formatPercent(p.ctr)}</Typography>
                      </TableCell>
                      {!isMobile && (
                        <>
                          <TableCell align="right">
                            <Typography variant="body2">{p.price ? formatCurrency(p.price) : '--'}</Typography>
                          </TableCell>
                          <TableCell align="center">{healthChip(p.health_status)}</TableCell>
                        </>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </TableContainer>
        )}

        {/* ---- Phrases Table ---- */}
        {entityTab === 2 && (
          <TableContainer sx={{ maxHeight: 520 }}>
            {isLoading ? (
              <Box sx={{ p: 2 }}>
                {[1, 2, 3, 4, 5].map((i) => (
                  <Skeleton key={i} variant="rounded" height={48} sx={{ mb: 1, borderRadius: 1.5 }} />
                ))}
              </Box>
            ) : sortedPhrases.length === 0 ? (
              <Typography variant="body2" color="text.secondary" sx={{ p: 4, textAlign: 'center' }}>
                Нет данных по фразам
              </Typography>
            ) : (
              <Table size="small" stickyHeader>
                <TableHead>
                  <TableRow>
                    <SortableCell label="Фраза" field="text" order={phraseOrder} orderBy={phraseOrderBy} onSort={handlePhraseSort} />
                    <SortableCell label="Кампания" field="campaign_name" order={phraseOrder} orderBy={phraseOrderBy} onSort={handlePhraseSort} />
                    <SortableCell label="Ставка" field="bid" order={phraseOrder} orderBy={phraseOrderBy} onSort={handlePhraseSort} align="right" />
                    <SortableCell label="Расход" field="spent" order={phraseOrder} orderBy={phraseOrderBy} onSort={handlePhraseSort} align="right" />
                    <SortableCell label="Клики" field="clicks" order={phraseOrder} orderBy={phraseOrderBy} onSort={handlePhraseSort} align="right" />
                    <SortableCell label="CTR" field="ctr" order={phraseOrder} orderBy={phraseOrderBy} onSort={handlePhraseSort} align="right" />
                    {!isMobile && (
                      <TableCell align="center" sx={{ fontWeight: 700, width: 100 }}>
                        Здоровье
                      </TableCell>
                    )}
                  </TableRow>
                </TableHead>
                <TableBody>
                  {sortedPhrases.map((ph) => (
                    <TableRow
                      key={ph.id}
                      hover
                      sx={{ '&:last-child td': { borderBottom: 0 } }}
                    >
                      <TableCell>
                        <Typography variant="body2" fontWeight={600} noWrap sx={{ maxWidth: 260 }}>
                          {ph.text}
                        </Typography>
                      </TableCell>
                      <TableCell>
                        <Typography
                          variant="body2"
                          noWrap
                          sx={{
                            maxWidth: 200,
                            cursor: ph.campaign_id ? 'pointer' : 'default',
                            '&:hover': ph.campaign_id ? { textDecoration: 'underline' } : {},
                          }}
                          onClick={(e) => {
                            if (ph.campaign_id) {
                              e.stopPropagation();
                              navigate(`/ads-intelligence/campaigns/${ph.campaign_id}`);
                            }
                          }}
                        >
                          {ph.campaign_name || '--'}
                        </Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{ph.bid != null ? `${ph.bid} P` : '--'}</Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{formatCurrency(ph.spent ?? ph.performance?.spend)}</Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{formatNumber(ph.clicks ?? ph.performance?.clicks)}</Typography>
                      </TableCell>
                      <TableCell align="right">
                        <Typography variant="body2">{formatPercent(ph.ctr ?? ph.performance?.ctr)}</Typography>
                      </TableCell>
                      {!isMobile && <TableCell align="center">{healthChip(ph.health_status)}</TableCell>}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </TableContainer>
        )}
      </Paper>
    </Box>
  );
}
