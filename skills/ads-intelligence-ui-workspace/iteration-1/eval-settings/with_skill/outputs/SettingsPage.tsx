import React, { useState, useCallback, useEffect } from 'react';
import {
  Box,
  Typography,
  Paper,
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Button,
  TextField,
  Select,
  MenuItem,
  FormControl,
  InputLabel,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Chip,
  IconButton,
  Stack,
  Grid,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  CircularProgress,
  Tooltip,
  Switch,
  FormControlLabel,
  Divider,
  Alert,
  LinearProgress,
  Skeleton,
} from '@mui/material';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import DeleteIcon from '@mui/icons-material/Delete';
import RefreshIcon from '@mui/icons-material/Refresh';
import DownloadIcon from '@mui/icons-material/Download';
import AddIcon from '@mui/icons-material/Add';
import LinkIcon from '@mui/icons-material/Link';
import SearchIcon from '@mui/icons-material/Search';
import TrendingUpIcon from '@mui/icons-material/TrendingUp';
import TrendingDownIcon from '@mui/icons-material/TrendingDown';
import TrendingFlatIcon from '@mui/icons-material/TrendingFlat';
import PlayArrowIcon from '@mui/icons-material/PlayArrow';
import SaveIcon from '@mui/icons-material/Save';
import SettingsIcon from '@mui/icons-material/Settings';
import WorkIcon from '@mui/icons-material/Work';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { adsIntelligenceApi } from '../api/adsIntelligenceApi';
import type {
  ExportJob,
  JobRun,
  CreateExportRequest,
  Campaign,
} from '../types';

// ---------------------------------------------------------------------------
// Local types for entities not fully declared in the shared types file
// ---------------------------------------------------------------------------

interface Keyword {
  id: string;
  query: string;
  normalized: string;
  frequency: number;
  frequency_trend: string;
  cluster_id?: string;
  source: string;
}

interface KeywordCluster {
  id: string;
  name: string;
  main_keyword: string;
  keyword_count: number;
  total_frequency: number;
}

interface Competitor {
  id: string;
  product_id: string;
  competitor_nm_id: number;
  competitor_title: string;
  competitor_brand: string;
  competitor_price: number;
  competitor_rating: number;
  competitor_reviews_count: number;
  query: string;
  last_position: number;
  our_position: number;
  first_seen_at: string;
  last_seen_at: string;
}

interface Strategy {
  id: string;
  name: string;
  type: string;
  params: Record<string, unknown>;
  is_active: boolean;
  bindings?: Array<{ id: string; campaign_id?: string; product_id?: string }>;
  created_at: string;
}

interface ThresholdConfig {
  min_spend_for_recommendation: number;
  min_impressions_for_ctr: number;
  high_acos_threshold: number;
  low_ctr_threshold: number;
  position_drop_threshold: number;
  roas_target: number;
  [key: string]: number;
}

const THRESHOLD_LABELS: Record<string, string> = {
  min_spend_for_recommendation: 'Мин. расход для рекомендации (руб.)',
  min_impressions_for_ctr: 'Мин. показы для расчёта CTR',
  high_acos_threshold: 'Порог высокого ACoS (%)',
  low_ctr_threshold: 'Порог низкого CTR (%)',
  position_drop_threshold: 'Порог падения позиции',
  roas_target: 'Целевой ROAS',
};

const STRATEGY_TYPE_LABELS: Record<string, string> = {
  acos: 'ACoS',
  roas: 'ROAS',
  anti_sliv: 'Анти-слив',
  dayparting: 'Дейпартинг',
};

const ENTITY_TYPE_OPTIONS = [
  { value: 'campaigns', label: 'Кампании' },
  { value: 'products', label: 'Товары' },
  { value: 'phrases', label: 'Фразы' },
  { value: 'recommendations', label: 'Рекомендации' },
  { value: 'competitors', label: 'Конкуренты' },
  { value: 'keywords', label: 'Ключевые слова' },
];

const FORMAT_OPTIONS = [
  { value: 'csv', label: 'CSV' },
  { value: 'xlsx', label: 'XLSX' },
];

// ---------------------------------------------------------------------------
// Helper: status chip colour
// ---------------------------------------------------------------------------

function statusColor(
  status: string,
): 'success' | 'warning' | 'error' | 'info' | 'default' {
  switch (status) {
    case 'active':
    case 'completed':
    case 'ok':
      return 'success';
    case 'paused':
    case 'pending':
    case 'queued':
    case 'partial':
      return 'warning';
    case 'error':
    case 'failed':
      return 'error';
    case 'running':
    case 'processing':
      return 'info';
    default:
      return 'default';
  }
}

function statusLabel(status: string): string {
  const map: Record<string, string> = {
    active: 'Активна',
    completed: 'Завершена',
    ok: 'OK',
    paused: 'Пауза',
    pending: 'Ожидание',
    queued: 'В очереди',
    partial: 'Частично',
    error: 'Ошибка',
    failed: 'Ошибка',
    running: 'Выполняется',
    processing: 'Обработка',
    stopped: 'Остановлена',
  };
  return map[status] || status;
}

function formatDate(dateStr?: string | null): string {
  if (!dateStr) return '\u2014';
  try {
    return new Date(dateStr).toLocaleString('ru-RU', {
      day: '2-digit',
      month: '2-digit',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  } catch {
    return dateStr;
  }
}

function TrendIcon({ trend }: { trend: string }) {
  if (trend === 'up' || trend === 'improving')
    return <TrendingUpIcon fontSize="small" sx={{ color: 'success.main' }} />;
  if (trend === 'down' || trend === 'declining')
    return <TrendingDownIcon fontSize="small" sx={{ color: 'error.main' }} />;
  return <TrendingFlatIcon fontSize="small" sx={{ color: 'text.secondary' }} />;
}

// ===========================================================================
// Section 1: Strategies
// ===========================================================================

function StrategiesSection() {
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [attachOpen, setAttachOpen] = useState(false);
  const [selectedStrategyId, setSelectedStrategyId] = useState('');
  const [attachCampaignId, setAttachCampaignId] = useState('');

  // Form state
  const [formName, setFormName] = useState('');
  const [formType, setFormType] = useState<string>('acos');
  const [formActive, setFormActive] = useState(true);
  const [formCabinetId, setFormCabinetId] = useState('');
  const [formTargetAcos, setFormTargetAcos] = useState('30');
  const [formTargetRoas, setFormTargetRoas] = useState('3');
  const [formMaxBid, setFormMaxBid] = useState('500');
  const [formMinBid, setFormMinBid] = useState('50');
  const [formTimezone, setFormTimezone] = useState('Europe/Moscow');
  const [formActiveHours, setFormActiveHours] = useState('9-23');

  const { data: strategiesData, isLoading } = useQuery({
    queryKey: ['strategies'],
    queryFn: () => adsIntelligenceApi.getStrategies(),
  });

  const { data: campaignsData } = useQuery({
    queryKey: ['campaigns-for-attach'],
    queryFn: () => adsIntelligenceApi.getCampaigns({ page: 1, per_page: 100 }),
    enabled: attachOpen,
  });

  const { data: cabinetsData } = useQuery({
    queryKey: ['cabinets-for-strategy'],
    queryFn: () => adsIntelligenceApi.getSellerCabinets({ page: 1, per_page: 50 }),
    enabled: createOpen,
  });

  const createMutation = useMutation({
    mutationFn: (data: {
      name: string;
      type: string;
      params: Record<string, unknown>;
      is_active: boolean;
      seller_cabinet_id: string;
    }) => adsIntelligenceApi.createStrategy(data),
    onSuccess: () => {
      toast.success('Стратегия создана');
      queryClient.invalidateQueries({ queryKey: ['strategies'] });
      setCreateOpen(false);
      resetForm();
    },
    onError: () => toast.error('Не удалось создать стратегию'),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => adsIntelligenceApi.deleteStrategy(id),
    onSuccess: () => {
      toast.success('Стратегия удалена');
      queryClient.invalidateQueries({ queryKey: ['strategies'] });
    },
    onError: () => toast.error('Не удалось удалить стратегию'),
  });

  const attachMutation = useMutation({
    mutationFn: (data: { strategyId: string; campaign_id: string }) =>
      adsIntelligenceApi.attachStrategy(data.strategyId, {
        campaign_id: data.campaign_id,
      }),
    onSuccess: () => {
      toast.success('Стратегия привязана к кампании');
      queryClient.invalidateQueries({ queryKey: ['strategies'] });
      setAttachOpen(false);
    },
    onError: () => toast.error('Не удалось привязать стратегию'),
  });

  function resetForm() {
    setFormName('');
    setFormType('acos');
    setFormActive(true);
    setFormCabinetId('');
    setFormTargetAcos('30');
    setFormTargetRoas('3');
    setFormMaxBid('500');
    setFormMinBid('50');
  }

  function buildParams(): Record<string, unknown> {
    switch (formType) {
      case 'acos':
        return {
          target_acos: Number(formTargetAcos),
          max_bid: Number(formMaxBid),
          min_bid: Number(formMinBid),
        };
      case 'roas':
        return {
          target_roas: Number(formTargetRoas),
          max_bid: Number(formMaxBid),
          min_bid: Number(formMinBid),
        };
      case 'anti_sliv':
        return {
          max_spend_without_order: Number(formMaxBid),
          action: 'pause',
        };
      case 'dayparting':
        return {
          timezone: formTimezone,
          active_hours: formActiveHours,
        };
      default:
        return {};
    }
  }

  function handleCreate() {
    if (!formName.trim() || !formCabinetId) {
      toast.error('Заполните название и выберите кабинет');
      return;
    }
    createMutation.mutate({
      name: formName,
      type: formType,
      params: buildParams(),
      is_active: formActive,
      seller_cabinet_id: formCabinetId,
    });
  }

  const strategies: Strategy[] = (strategiesData as any)?.data || [];

  return (
    <>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant="subtitle2" fontWeight={700}>
          Список стратегий
        </Typography>
        <Button
          variant="contained"
          size="small"
          startIcon={<AddIcon />}
          onClick={() => setCreateOpen(true)}
        >
          Создать стратегию
        </Button>
      </Box>

      {isLoading ? (
        <Stack spacing={1}>
          {[1, 2, 3].map((i) => (
            <Skeleton key={i} variant="rectangular" height={60} sx={{ borderRadius: 2 }} />
          ))}
        </Stack>
      ) : strategies.length === 0 ? (
        <Alert severity="info">Стратегий пока нет. Создайте первую стратегию автоставок.</Alert>
      ) : (
        <Stack spacing={1.5}>
          {strategies.map((strategy) => (
            <Paper
              key={strategy.id}
              variant="outlined"
              sx={{ p: 2, borderRadius: 2.5 }}
            >
              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <Box>
                  <Stack direction="row" spacing={1} alignItems="center">
                    <Typography variant="subtitle2" fontWeight={700}>
                      {strategy.name}
                    </Typography>
                    <Chip
                      label={STRATEGY_TYPE_LABELS[strategy.type] || strategy.type}
                      size="small"
                      color="info"
                      variant="outlined"
                    />
                    <Chip
                      label={strategy.is_active ? 'Активна' : 'Выключена'}
                      size="small"
                      color={strategy.is_active ? 'success' : 'default'}
                    />
                  </Stack>
                  <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5, display: 'block' }}>
                    Создана: {formatDate(strategy.created_at)}
                  </Typography>
                  {strategy.params && (
                    <Stack direction="row" spacing={1} sx={{ mt: 1, flexWrap: 'wrap' }}>
                      {Object.entries(strategy.params).map(([key, val]) => (
                        <Chip
                          key={key}
                          label={`${key}: ${val}`}
                          size="small"
                          variant="outlined"
                          sx={{ fontSize: '0.7rem' }}
                        />
                      ))}
                    </Stack>
                  )}
                  {strategy.bindings && strategy.bindings.length > 0 && (
                    <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5, display: 'block' }}>
                      Привязки: {strategy.bindings.length} кампаний
                    </Typography>
                  )}
                </Box>
                <Stack direction="row" spacing={0.5}>
                  <Tooltip title="Привязать к кампании">
                    <IconButton
                      size="small"
                      onClick={() => {
                        setSelectedStrategyId(strategy.id);
                        setAttachOpen(true);
                      }}
                    >
                      <LinkIcon fontSize="small" />
                    </IconButton>
                  </Tooltip>
                  <Tooltip title="Удалить">
                    <IconButton
                      size="small"
                      color="error"
                      onClick={() => deleteMutation.mutate(strategy.id)}
                      disabled={deleteMutation.isPending}
                    >
                      <DeleteIcon fontSize="small" />
                    </IconButton>
                  </Tooltip>
                </Stack>
              </Box>
            </Paper>
          ))}
        </Stack>
      )}

      {/* Create Strategy Dialog */}
      <Dialog open={createOpen} onClose={() => setCreateOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>Создать стратегию автоставок</DialogTitle>
        <DialogContent>
          <Stack spacing={2} sx={{ mt: 1 }}>
            <TextField
              label="Название"
              value={formName}
              onChange={(e) => setFormName(e.target.value)}
              fullWidth
              size="small"
            />
            <FormControl fullWidth size="small">
              <InputLabel>Тип стратегии</InputLabel>
              <Select
                value={formType}
                label="Тип стратегии"
                onChange={(e) => setFormType(e.target.value)}
              >
                <MenuItem value="acos">ACoS (целевой рекламный расход)</MenuItem>
                <MenuItem value="roas">ROAS (целевой возврат на рекламу)</MenuItem>
                <MenuItem value="anti_sliv">Анти-слив (стоп при высоком расходе)</MenuItem>
                <MenuItem value="dayparting">Дейпартинг (по времени суток)</MenuItem>
              </Select>
            </FormControl>
            <FormControl fullWidth size="small">
              <InputLabel>Кабинет</InputLabel>
              <Select
                value={formCabinetId}
                label="Кабинет"
                onChange={(e) => setFormCabinetId(e.target.value)}
              >
                {(cabinetsData?.data || []).map((cab) => (
                  <MenuItem key={cab.id} value={cab.id}>
                    {cab.name}
                  </MenuItem>
                ))}
              </Select>
            </FormControl>

            <Divider />

            {/* Dynamic params based on type */}
            {formType === 'acos' && (
              <>
                <TextField
                  label="Целевой ACoS (%)"
                  type="number"
                  value={formTargetAcos}
                  onChange={(e) => setFormTargetAcos(e.target.value)}
                  size="small"
                />
                <TextField
                  label="Макс. ставка (руб.)"
                  type="number"
                  value={formMaxBid}
                  onChange={(e) => setFormMaxBid(e.target.value)}
                  size="small"
                />
                <TextField
                  label="Мин. ставка (руб.)"
                  type="number"
                  value={formMinBid}
                  onChange={(e) => setFormMinBid(e.target.value)}
                  size="small"
                />
              </>
            )}

            {formType === 'roas' && (
              <>
                <TextField
                  label="Целевой ROAS"
                  type="number"
                  value={formTargetRoas}
                  onChange={(e) => setFormTargetRoas(e.target.value)}
                  size="small"
                />
                <TextField
                  label="Макс. ставка (руб.)"
                  type="number"
                  value={formMaxBid}
                  onChange={(e) => setFormMaxBid(e.target.value)}
                  size="small"
                />
                <TextField
                  label="Мин. ставка (руб.)"
                  type="number"
                  value={formMinBid}
                  onChange={(e) => setFormMinBid(e.target.value)}
                  size="small"
                />
              </>
            )}

            {formType === 'anti_sliv' && (
              <TextField
                label="Макс. расход без заказа (руб.)"
                type="number"
                value={formMaxBid}
                onChange={(e) => setFormMaxBid(e.target.value)}
                size="small"
              />
            )}

            {formType === 'dayparting' && (
              <>
                <TextField
                  label="Часовой пояс"
                  value={formTimezone}
                  onChange={(e) => setFormTimezone(e.target.value)}
                  size="small"
                />
                <TextField
                  label="Активные часы (напр. 9-23)"
                  value={formActiveHours}
                  onChange={(e) => setFormActiveHours(e.target.value)}
                  size="small"
                  helperText="Диапазон часов, когда кампании активны"
                />
              </>
            )}

            <FormControlLabel
              control={
                <Switch checked={formActive} onChange={(e) => setFormActive(e.target.checked)} />
              }
              label="Активна сразу после создания"
            />
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCreateOpen(false)}>Отмена</Button>
          <Button
            variant="contained"
            onClick={handleCreate}
            disabled={createMutation.isPending}
            startIcon={createMutation.isPending ? <CircularProgress size={16} /> : undefined}
          >
            Создать
          </Button>
        </DialogActions>
      </Dialog>

      {/* Attach Strategy to Campaign Dialog */}
      <Dialog open={attachOpen} onClose={() => setAttachOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>Привязать стратегию к кампании</DialogTitle>
        <DialogContent>
          <FormControl fullWidth size="small" sx={{ mt: 1 }}>
            <InputLabel>Кампания</InputLabel>
            <Select
              value={attachCampaignId}
              label="Кампания"
              onChange={(e) => setAttachCampaignId(e.target.value)}
            >
              {(campaignsData?.data || []).map((c: Campaign) => (
                <MenuItem key={c.id} value={c.id}>
                  {c.name}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setAttachOpen(false)}>Отмена</Button>
          <Button
            variant="contained"
            onClick={() =>
              attachMutation.mutate({
                strategyId: selectedStrategyId,
                campaign_id: attachCampaignId,
              })
            }
            disabled={!attachCampaignId || attachMutation.isPending}
            startIcon={attachMutation.isPending ? <CircularProgress size={16} /> : undefined}
          >
            Привязать
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}

// ===========================================================================
// Section 2: Keywords
// ===========================================================================

function KeywordsSection() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(1);

  const { data: keywordsData, isLoading } = useQuery({
    queryKey: ['keywords', search, page],
    queryFn: () =>
      adsIntelligenceApi.getKeywords({ search: search || undefined, page, per_page: 20 }),
  });

  const { data: clustersData, isLoading: clustersLoading } = useQuery({
    queryKey: ['keyword-clusters'],
    queryFn: () => adsIntelligenceApi.getKeywordClusters({ page: 1, per_page: 50 }),
  });

  const collectMutation = useMutation({
    mutationFn: () => adsIntelligenceApi.collectKeywords(),
    onSuccess: (result) => {
      toast.success(`Импортировано ключевых слов: ${result.imported}`);
      queryClient.invalidateQueries({ queryKey: ['keywords'] });
    },
    onError: () => toast.error('Не удалось собрать ключевые слова'),
  });

  const clusterMutation = useMutation({
    mutationFn: () => adsIntelligenceApi.clusterKeywords(),
    onSuccess: (result) => {
      toast.success(`Кластеров создано: ${result.clusters_created}`);
      queryClient.invalidateQueries({ queryKey: ['keyword-clusters'] });
    },
    onError: () => toast.error('Не удалось кластеризовать'),
  });

  const keywords: Keyword[] = (keywordsData as any)?.data || [];
  const clusters: KeywordCluster[] = (clustersData as any)?.data || [];

  return (
    <>
      {/* Action buttons */}
      <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ mb: 2 }}>
        <TextField
          placeholder="Поиск ключевых слов..."
          size="small"
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setPage(1);
          }}
          InputProps={{
            startAdornment: <SearchIcon fontSize="small" sx={{ mr: 0.5, color: 'text.secondary' }} />,
          }}
          sx={{ flex: 1 }}
        />
        <Button
          variant="outlined"
          size="small"
          onClick={() => collectMutation.mutate()}
          disabled={collectMutation.isPending}
          startIcon={collectMutation.isPending ? <CircularProgress size={16} /> : <AddIcon />}
        >
          Собрать
        </Button>
        <Button
          variant="outlined"
          size="small"
          onClick={() => clusterMutation.mutate()}
          disabled={clusterMutation.isPending}
          startIcon={clusterMutation.isPending ? <CircularProgress size={16} /> : <WorkIcon />}
        >
          Кластеризовать
        </Button>
      </Stack>

      {/* Keywords table */}
      <Paper variant="outlined" sx={{ borderRadius: 2.5, mb: 2 }}>
        <TableContainer>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>
                    Ключевое слово
                  </Typography>
                </TableCell>
                <TableCell align="right">
                  <Typography variant="caption" fontWeight={700}>
                    Частотность
                  </Typography>
                </TableCell>
                <TableCell align="center">
                  <Typography variant="caption" fontWeight={700}>
                    Тренд
                  </Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>
                    Источник
                  </Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>
                    Кластер
                  </Typography>
                </TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {isLoading ? (
                [1, 2, 3, 4, 5].map((i) => (
                  <TableRow key={i}>
                    <TableCell colSpan={5}>
                      <Skeleton variant="text" />
                    </TableCell>
                  </TableRow>
                ))
              ) : keywords.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={5} align="center">
                    <Typography variant="body2" color="text.secondary" sx={{ py: 2 }}>
                      Ключевые слова не найдены. Нажмите "Собрать" для импорта.
                    </Typography>
                  </TableCell>
                </TableRow>
              ) : (
                keywords.map((kw) => (
                  <TableRow key={kw.id} hover>
                    <TableCell>
                      <Typography variant="body2">{kw.query}</Typography>
                      <Typography variant="caption" color="text.secondary">
                        {kw.normalized}
                      </Typography>
                    </TableCell>
                    <TableCell align="right">
                      <Typography variant="body2" fontWeight={600}>
                        {kw.frequency.toLocaleString('ru-RU')}
                      </Typography>
                    </TableCell>
                    <TableCell align="center">
                      <TrendIcon trend={kw.frequency_trend} />
                    </TableCell>
                    <TableCell>
                      <Chip label={kw.source} size="small" variant="outlined" />
                    </TableCell>
                    <TableCell>
                      {kw.cluster_id ? (
                        <Chip
                          label={
                            clusters.find((c) => c.id === kw.cluster_id)?.name || kw.cluster_id
                          }
                          size="small"
                          color="info"
                          variant="outlined"
                        />
                      ) : (
                        <Typography variant="caption" color="text.secondary">
                          &mdash;
                        </Typography>
                      )}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </TableContainer>
      </Paper>

      {/* Clusters display */}
      <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1 }}>
        Кластеры ключевых слов
      </Typography>
      {clustersLoading ? (
        <Skeleton variant="rectangular" height={80} sx={{ borderRadius: 2 }} />
      ) : clusters.length === 0 ? (
        <Alert severity="info" sx={{ borderRadius: 2 }}>
          Кластеров пока нет. Нажмите "Кластеризовать" для автоматической группировки.
        </Alert>
      ) : (
        <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', gap: 1 }}>
          {clusters.map((cluster) => (
            <Paper
              key={cluster.id}
              variant="outlined"
              sx={{ p: 1.5, borderRadius: 2, minWidth: 180 }}
            >
              <Typography variant="body2" fontWeight={700}>
                {cluster.name}
              </Typography>
              <Typography variant="caption" color="text.secondary">
                Главное: {cluster.main_keyword}
              </Typography>
              <Stack direction="row" spacing={1} sx={{ mt: 0.5 }}>
                <Chip
                  label={`${cluster.keyword_count} слов`}
                  size="small"
                  variant="outlined"
                />
                <Chip
                  label={`${cluster.total_frequency.toLocaleString('ru-RU')} частотность`}
                  size="small"
                  variant="outlined"
                  color="info"
                />
              </Stack>
            </Paper>
          ))}
        </Stack>
      )}
    </>
  );
}

// ===========================================================================
// Section 3: Competitors
// ===========================================================================

function CompetitorsSection() {
  const queryClient = useQueryClient();

  const { data: competitorsData, isLoading } = useQuery({
    queryKey: ['competitors-global'],
    queryFn: () => adsIntelligenceApi.getCompetitors({ page: 1, per_page: 50 }),
  });

  const extractMutation = useMutation({
    mutationFn: () => adsIntelligenceApi.extractCompetitors(),
    onSuccess: (result) => {
      toast.success(`Найдено конкурентов: ${result.competitors_found}`);
      queryClient.invalidateQueries({ queryKey: ['competitors-global'] });
    },
    onError: () => toast.error('Не удалось извлечь конкурентов'),
  });

  const competitors: Competitor[] = (competitorsData as any)?.data || [];

  return (
    <>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant="subtitle2" fontWeight={700}>
          Список конкурентов
        </Typography>
        <Button
          variant="outlined"
          size="small"
          onClick={() => extractMutation.mutate()}
          disabled={extractMutation.isPending}
          startIcon={extractMutation.isPending ? <CircularProgress size={16} /> : <SearchIcon />}
        >
          Извлечь из SERP
        </Button>
      </Box>

      <Paper variant="outlined" sx={{ borderRadius: 2.5 }}>
        <TableContainer>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Конкурент</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Бренд</Typography>
                </TableCell>
                <TableCell align="right">
                  <Typography variant="caption" fontWeight={700}>Цена</Typography>
                </TableCell>
                <TableCell align="right">
                  <Typography variant="caption" fontWeight={700}>Рейтинг</Typography>
                </TableCell>
                <TableCell align="right">
                  <Typography variant="caption" fontWeight={700}>Отзывы</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Запрос</Typography>
                </TableCell>
                <TableCell align="center">
                  <Typography variant="caption" fontWeight={700}>Позиция (их / наша)</Typography>
                </TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {isLoading ? (
                [1, 2, 3].map((i) => (
                  <TableRow key={i}>
                    <TableCell colSpan={7}>
                      <Skeleton variant="text" />
                    </TableCell>
                  </TableRow>
                ))
              ) : competitors.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} align="center">
                    <Typography variant="body2" color="text.secondary" sx={{ py: 2 }}>
                      Конкурентов пока нет. Нажмите "Извлечь из SERP" для поиска.
                    </Typography>
                  </TableCell>
                </TableRow>
              ) : (
                competitors.map((comp) => (
                  <TableRow key={comp.id} hover>
                    <TableCell>
                      <Typography variant="body2">{comp.competitor_title}</Typography>
                      <Typography variant="caption" color="text.secondary">
                        NM ID: {comp.competitor_nm_id}
                      </Typography>
                    </TableCell>
                    <TableCell>
                      <Typography variant="body2">{comp.competitor_brand || '\u2014'}</Typography>
                    </TableCell>
                    <TableCell align="right">
                      <Typography variant="body2">
                        {comp.competitor_price
                          ? `${comp.competitor_price.toLocaleString('ru-RU')} \u20BD`
                          : '\u2014'}
                      </Typography>
                    </TableCell>
                    <TableCell align="right">
                      <Typography variant="body2">
                        {comp.competitor_rating?.toFixed(1) || '\u2014'}
                      </Typography>
                    </TableCell>
                    <TableCell align="right">
                      <Typography variant="body2">
                        {comp.competitor_reviews_count?.toLocaleString('ru-RU') || '\u2014'}
                      </Typography>
                    </TableCell>
                    <TableCell>
                      <Typography variant="body2">{comp.query}</Typography>
                    </TableCell>
                    <TableCell align="center">
                      <Stack direction="row" spacing={0.5} justifyContent="center" alignItems="center">
                        <Chip
                          label={comp.last_position}
                          size="small"
                          color={comp.last_position < comp.our_position ? 'error' : 'default'}
                        />
                        <Typography variant="caption">/</Typography>
                        <Chip
                          label={comp.our_position}
                          size="small"
                          color={comp.our_position <= comp.last_position ? 'success' : 'warning'}
                        />
                      </Stack>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </TableContainer>
      </Paper>
    </>
  );
}

// ===========================================================================
// Section 4: Exports
// ===========================================================================

function ExportsSection() {
  const queryClient = useQueryClient();
  const [entityType, setEntityType] = useState('campaigns');
  const [format, setFormat] = useState<'csv' | 'xlsx'>('xlsx');

  const { data: exportsData, isLoading } = useQuery({
    queryKey: ['exports'],
    queryFn: () => adsIntelligenceApi.getExports({ page: 1, per_page: 50 }),
  });

  const createMutation = useMutation({
    mutationFn: (data: CreateExportRequest) => adsIntelligenceApi.createExport(data),
    onSuccess: () => {
      toast.success('Экспорт создан и обрабатывается');
      queryClient.invalidateQueries({ queryKey: ['exports'] });
    },
    onError: () => toast.error('Не удалось создать экспорт'),
  });

  const downloadMutation = useMutation({
    mutationFn: async (exportItem: ExportJob) => {
      const blob = await adsIntelligenceApi.downloadExport(exportItem.id);
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = exportItem.file_name || `export-${exportItem.entity_type}.${exportItem.format}`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      window.URL.revokeObjectURL(url);
    },
    onSuccess: () => toast.success('Файл скачан'),
    onError: () => toast.error('Не удалось скачать файл'),
  });

  const exports: ExportJob[] = exportsData?.data || [];

  return (
    <>
      {/* Create export form */}
      <Paper variant="outlined" sx={{ p: 2, borderRadius: 2.5, mb: 2 }}>
        <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1.5 }}>
          Создать экспорт
        </Typography>
        <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} alignItems="flex-end">
          <FormControl size="small" sx={{ minWidth: 200 }}>
            <InputLabel>Тип данных</InputLabel>
            <Select
              value={entityType}
              label="Тип данных"
              onChange={(e) => setEntityType(e.target.value)}
            >
              {ENTITY_TYPE_OPTIONS.map((opt) => (
                <MenuItem key={opt.value} value={opt.value}>
                  {opt.label}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          <FormControl size="small" sx={{ minWidth: 120 }}>
            <InputLabel>Формат</InputLabel>
            <Select
              value={format}
              label="Формат"
              onChange={(e) => setFormat(e.target.value as 'csv' | 'xlsx')}
            >
              {FORMAT_OPTIONS.map((opt) => (
                <MenuItem key={opt.value} value={opt.value}>
                  {opt.label}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          <Button
            variant="contained"
            size="small"
            onClick={() =>
              createMutation.mutate({ entity_type: entityType, format })
            }
            disabled={createMutation.isPending}
            startIcon={createMutation.isPending ? <CircularProgress size={16} /> : <AddIcon />}
          >
            Создать
          </Button>
        </Stack>
      </Paper>

      {/* Export history */}
      <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1 }}>
        История экспортов
      </Typography>
      <Paper variant="outlined" sx={{ borderRadius: 2.5 }}>
        <TableContainer>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Тип</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Формат</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Статус</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Создан</Typography>
                </TableCell>
                <TableCell align="right">
                  <Typography variant="caption" fontWeight={700}>Действие</Typography>
                </TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {isLoading ? (
                [1, 2, 3].map((i) => (
                  <TableRow key={i}>
                    <TableCell colSpan={5}>
                      <Skeleton variant="text" />
                    </TableCell>
                  </TableRow>
                ))
              ) : exports.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={5} align="center">
                    <Typography variant="body2" color="text.secondary" sx={{ py: 2 }}>
                      Экспортов пока нет
                    </Typography>
                  </TableCell>
                </TableRow>
              ) : (
                exports.map((exp) => (
                  <TableRow key={exp.id} hover>
                    <TableCell>
                      <Typography variant="body2">
                        {ENTITY_TYPE_OPTIONS.find((o) => o.value === exp.entity_type)?.label ||
                          exp.entity_type}
                      </Typography>
                    </TableCell>
                    <TableCell>
                      <Chip
                        label={exp.format.toUpperCase()}
                        size="small"
                        variant="outlined"
                      />
                    </TableCell>
                    <TableCell>
                      <Chip
                        label={statusLabel(exp.status)}
                        size="small"
                        color={statusColor(exp.status)}
                      />
                      {exp.status === 'processing' && exp.progress != null && (
                        <LinearProgress
                          variant="determinate"
                          value={exp.progress}
                          sx={{ mt: 0.5, height: 3, borderRadius: 1 }}
                        />
                      )}
                    </TableCell>
                    <TableCell>
                      <Typography variant="body2">{formatDate(exp.created_at)}</Typography>
                    </TableCell>
                    <TableCell align="right">
                      {exp.status === 'completed' && (
                        <Tooltip title="Скачать">
                          <IconButton
                            size="small"
                            color="primary"
                            onClick={() => downloadMutation.mutate(exp)}
                            disabled={downloadMutation.isPending}
                          >
                            <DownloadIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                      )}
                      {exp.status === 'failed' && exp.error_message && (
                        <Tooltip title={exp.error_message}>
                          <Typography variant="caption" color="error">
                            Ошибка
                          </Typography>
                        </Tooltip>
                      )}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </TableContainer>
      </Paper>
    </>
  );
}

// ===========================================================================
// Section 5: Job Runs
// ===========================================================================

function JobRunsSection() {
  const queryClient = useQueryClient();

  const { data: jobsData, isLoading } = useQuery({
    queryKey: ['job-runs'],
    queryFn: () => adsIntelligenceApi.getJobRuns({ page: 1, per_page: 50 }),
    refetchInterval: 15000, // auto-refresh every 15s
  });

  const retryMutation = useMutation({
    mutationFn: (id: string) => adsIntelligenceApi.retryJob(id),
    onSuccess: () => {
      toast.success('Задача перезапущена');
      queryClient.invalidateQueries({ queryKey: ['job-runs'] });
    },
    onError: () => toast.error('Не удалось перезапустить задачу'),
  });

  const jobs: JobRun[] = jobsData?.data || [];

  const taskTypeLabels: Record<string, string> = {
    sync_campaigns: 'Синхр. кампаний',
    sync_products: 'Синхр. товаров',
    sync_phrases: 'Синхр. фраз',
    sync_stats: 'Синхр. статистики',
    sync_all: 'Полная синхронизация',
    collect_keywords: 'Сбор ключевых слов',
    cluster_keywords: 'Кластеризация',
    extract_competitors: 'Извлечение конкурентов',
    analyze_seo: 'SEO анализ',
    generate_recommendations: 'Генерация рекомендаций',
    collect_delivery: 'Сбор доставки',
    export: 'Экспорт',
    collect_positions: 'Сбор позиций',
  };

  return (
    <>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
        <Typography variant="subtitle2" fontWeight={700}>
          Фоновые задачи
        </Typography>
        <Tooltip title="Обновить">
          <IconButton
            size="small"
            onClick={() => queryClient.invalidateQueries({ queryKey: ['job-runs'] })}
          >
            <RefreshIcon fontSize="small" />
          </IconButton>
        </Tooltip>
      </Box>

      <Paper variant="outlined" sx={{ borderRadius: 2.5 }}>
        <TableContainer>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Задача</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Статус</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Результат</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Начало</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Окончание</Typography>
                </TableCell>
                <TableCell>
                  <Typography variant="caption" fontWeight={700}>Ошибка</Typography>
                </TableCell>
                <TableCell align="right">
                  <Typography variant="caption" fontWeight={700}>Действие</Typography>
                </TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {isLoading ? (
                [1, 2, 3, 4].map((i) => (
                  <TableRow key={i}>
                    <TableCell colSpan={7}>
                      <Skeleton variant="text" />
                    </TableCell>
                  </TableRow>
                ))
              ) : jobs.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} align="center">
                    <Typography variant="body2" color="text.secondary" sx={{ py: 2 }}>
                      Нет фоновых задач
                    </Typography>
                  </TableCell>
                </TableRow>
              ) : (
                jobs.map((job) => (
                  <TableRow key={job.id} hover>
                    <TableCell>
                      <Typography variant="body2">
                        {taskTypeLabels[job.task_type] || job.task_type}
                      </Typography>
                    </TableCell>
                    <TableCell>
                      <Chip
                        label={statusLabel(job.status)}
                        size="small"
                        color={statusColor(job.status)}
                      />
                      {job.status === 'running' && job.progress != null && (
                        <LinearProgress
                          variant="determinate"
                          value={job.progress}
                          sx={{ mt: 0.5, height: 3, borderRadius: 1 }}
                        />
                      )}
                    </TableCell>
                    <TableCell>
                      {job.result_state ? (
                        <Chip
                          label={statusLabel(job.result_state)}
                          size="small"
                          variant="outlined"
                          color={statusColor(job.result_state)}
                        />
                      ) : (
                        <Typography variant="caption" color="text.secondary">
                          &mdash;
                        </Typography>
                      )}
                    </TableCell>
                    <TableCell>
                      <Typography variant="body2">{formatDate(job.started_at)}</Typography>
                    </TableCell>
                    <TableCell>
                      <Typography variant="body2">{formatDate(job.finished_at)}</Typography>
                    </TableCell>
                    <TableCell>
                      {job.error_message ? (
                        <Tooltip title={job.error_message}>
                          <Typography
                            variant="caption"
                            color="error"
                            sx={{
                              maxWidth: 200,
                              display: 'block',
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                            }}
                          >
                            {job.error_message}
                          </Typography>
                        </Tooltip>
                      ) : (
                        <Typography variant="caption" color="text.secondary">
                          &mdash;
                        </Typography>
                      )}
                    </TableCell>
                    <TableCell align="right">
                      {(job.status === 'failed' || job.status === 'completed') && (
                        <Tooltip title="Перезапустить">
                          <IconButton
                            size="small"
                            color="primary"
                            onClick={() => retryMutation.mutate(job.id)}
                            disabled={retryMutation.isPending}
                          >
                            <RefreshIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                      )}
                      {job.status === 'running' && (
                        <CircularProgress size={16} />
                      )}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </TableContainer>
      </Paper>

      {/* Schedule info */}
      <Paper variant="outlined" sx={{ p: 2, borderRadius: 2.5, mt: 2 }}>
        <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 1 }}>
          Расписание
        </Typography>
        <Stack spacing={0.5}>
          <Typography variant="body2" color="text.secondary">
            Синхронизация данных: каждые 6 часов (автоматически)
          </Typography>
          <Typography variant="body2" color="text.secondary">
            Генерация рекомендаций: ежедневно в 06:00 МСК
          </Typography>
          <Typography variant="body2" color="text.secondary">
            Сбор позиций: каждые 12 часов
          </Typography>
          <Typography variant="body2" color="text.secondary">
            SEO анализ: еженедельно (понедельник, 03:00 МСК)
          </Typography>
        </Stack>
      </Paper>
    </>
  );
}

// ===========================================================================
// Section 6: Recommendation Thresholds
// ===========================================================================

function ThresholdsSection() {
  const queryClient = useQueryClient();
  const [thresholds, setThresholds] = useState<ThresholdConfig | null>(null);
  const [isDirty, setIsDirty] = useState(false);

  const { data: thresholdsData, isLoading } = useQuery({
    queryKey: ['settings-thresholds'],
    queryFn: async () => {
      const response = await fetch('/ads-api/settings/thresholds', {
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('accessToken') || ''}`,
          'X-Workspace-ID': localStorage.getItem('currentWorkspaceId') || '',
        },
      });
      if (!response.ok) throw new Error('Failed to load thresholds');
      const json = await response.json();
      return json.data as ThresholdConfig;
    },
  });

  useEffect(() => {
    if (thresholdsData && !thresholds) {
      setThresholds(thresholdsData);
    }
  }, [thresholdsData, thresholds]);

  const saveMutation = useMutation({
    mutationFn: async (data: ThresholdConfig) => {
      const response = await fetch('/ads-api/settings/thresholds', {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('accessToken') || ''}`,
          'X-Workspace-ID': localStorage.getItem('currentWorkspaceId') || '',
        },
        body: JSON.stringify(data),
      });
      if (!response.ok) throw new Error('Failed to save thresholds');
      return response.json();
    },
    onSuccess: () => {
      toast.success('Пороги сохранены');
      setIsDirty(false);
      queryClient.invalidateQueries({ queryKey: ['settings-thresholds'] });
    },
    onError: () => toast.error('Не удалось сохранить пороги'),
  });

  function handleChange(key: string, value: string) {
    if (!thresholds) return;
    setThresholds({ ...thresholds, [key]: Number(value) });
    setIsDirty(true);
  }

  if (isLoading) {
    return (
      <Stack spacing={1.5}>
        {[1, 2, 3, 4, 5, 6].map((i) => (
          <Skeleton key={i} variant="rectangular" height={50} sx={{ borderRadius: 2 }} />
        ))}
      </Stack>
    );
  }

  if (!thresholds) {
    return (
      <Alert severity="warning">
        Не удалось загрузить пороги рекомендаций. Проверьте доступность сервера.
      </Alert>
    );
  }

  const knownKeys = Object.keys(THRESHOLD_LABELS);
  const allKeys = [
    ...knownKeys.filter((k) => k in thresholds),
    ...Object.keys(thresholds).filter((k) => !knownKeys.includes(k)),
  ];

  return (
    <>
      <Typography variant="subtitle2" fontWeight={700} sx={{ mb: 2 }}>
        Настройки порогов рекомендательного движка
      </Typography>
      <Grid container spacing={2}>
        {allKeys.map((key) => (
          <Grid item xs={12} sm={6} md={4} key={key}>
            <TextField
              label={THRESHOLD_LABELS[key] || key}
              type="number"
              size="small"
              fullWidth
              value={thresholds[key] ?? ''}
              onChange={(e) => handleChange(key, e.target.value)}
              InputLabelProps={{ shrink: true }}
            />
          </Grid>
        ))}
      </Grid>
      <Box sx={{ mt: 2, display: 'flex', justifyContent: 'flex-end' }}>
        <Button
          variant="contained"
          startIcon={saveMutation.isPending ? <CircularProgress size={16} /> : <SaveIcon />}
          onClick={() => thresholds && saveMutation.mutate(thresholds)}
          disabled={!isDirty || saveMutation.isPending}
        >
          Сохранить пороги
        </Button>
      </Box>
    </>
  );
}

// ===========================================================================
// Main Settings Page
// ===========================================================================

export default function SettingsPage() {
  const [expanded, setExpanded] = useState<string | false>('strategies');

  const handleAccordionChange =
    (panel: string) => (_: React.SyntheticEvent, isExpanded: boolean) => {
      setExpanded(isExpanded ? panel : false);
    };

  const sections = [
    {
      id: 'strategies',
      title: 'Стратегии автоставок',
      icon: <SettingsIcon fontSize="small" />,
      badge: undefined,
      content: <StrategiesSection />,
    },
    {
      id: 'keywords',
      title: 'Ключевые слова',
      icon: <SearchIcon fontSize="small" />,
      content: <KeywordsSection />,
    },
    {
      id: 'competitors',
      title: 'Конкуренты',
      icon: <TrendingUpIcon fontSize="small" />,
      content: <CompetitorsSection />,
    },
    {
      id: 'exports',
      title: 'Экспорты',
      icon: <DownloadIcon fontSize="small" />,
      content: <ExportsSection />,
    },
    {
      id: 'jobs',
      title: 'Фоновые задачи',
      icon: <PlayArrowIcon fontSize="small" />,
      content: <JobRunsSection />,
    },
    {
      id: 'thresholds',
      title: 'Пороги рекомендаций',
      icon: <WorkIcon fontSize="small" />,
      content: <ThresholdsSection />,
    },
  ];

  return (
    <Box sx={{ p: { xs: 2, sm: 3 }, maxWidth: 1200, mx: 'auto' }}>
      {/* Page header */}
      <Box sx={{ mb: 3 }}>
        <Typography variant="h5" fontWeight={700}>
          Настройки
        </Typography>
        <Typography variant="body2" color="text.secondary">
          Конфигурация рабочего пространства Ads Intelligence
        </Typography>
      </Box>

      {/* Accordion sections */}
      <Stack spacing={1}>
        {sections.map((section) => (
          <Accordion
            key={section.id}
            expanded={expanded === section.id}
            onChange={handleAccordionChange(section.id)}
            disableGutters
            sx={{
              borderRadius: '12px !important',
              border: '1px solid',
              borderColor: 'divider',
              '&:before': { display: 'none' },
              '&.Mui-expanded': {
                margin: 0,
              },
              overflow: 'hidden',
            }}
          >
            <AccordionSummary
              expandIcon={<ExpandMoreIcon />}
              sx={{
                '& .MuiAccordionSummary-content': {
                  alignItems: 'center',
                  gap: 1,
                },
              }}
            >
              {section.icon}
              <Typography variant="subtitle2" fontWeight={700}>
                {section.title}
              </Typography>
            </AccordionSummary>
            <AccordionDetails sx={{ p: 2.5 }}>
              {section.content}
            </AccordionDetails>
          </Accordion>
        ))}
      </Stack>
    </Box>
  );
}
