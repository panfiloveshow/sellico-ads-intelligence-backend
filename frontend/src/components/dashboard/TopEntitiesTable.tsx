import { Card, CardContent, Stack, Typography, Skeleton, Table, TableBody, TableCell, TableHead, TableRow, Box } from "@mui/material";
import { useNavigate } from "react-router-dom";
import type { ReactNode } from "react";

import { formatCompact, formatMoney, formatNumber } from "@/lib/format/numbers";
import type {
  ProductAdsSummary,
  CampaignPerformanceSummary,
  QueryPerformanceSummary,
  AdsMetricsSummary,
} from "@/api/queries/ads";

interface BaseProps {
  title: string;
  loading?: boolean;
  /** Optional empty-state copy when items is [] and not loading. */
  emptyHint?: string;
}

interface TopProductsTableProps extends BaseProps {
  items?: ProductAdsSummary[];
}

interface TopCampaignsTableProps extends BaseProps {
  items?: CampaignPerformanceSummary[];
}

interface TopQueriesTableProps extends BaseProps {
  items?: QueryPerformanceSummary[];
}

/**
 * One of three "top entities" tables on the Command Center page.
 * Each row routes to the matching detail page on click.
 *
 * We render plain MUI <Table> instead of <DataGrid> at this scale —
 * 6-8 rows max, no virtualization needed, half the bundle size.
 * Switch to DataGrid in the dedicated /products list page (Sprint 7)
 * when filters/pagination/export are needed.
 */

export function TopProductsTable({ title, items, loading, emptyHint }: TopProductsTableProps) {
  const navigate = useNavigate();
  return (
    <TopShell
      title={title}
      loading={loading}
      isEmpty={!loading && (!items || items.length === 0)}
      emptyHint={emptyHint}
      headers={["Товар", "Расход", "Заказы", "ROAS"]}
    >
      {items?.map((item) => (
        <ClickableRow key={item.id} onClick={() => navigate(`/products/${item.id}`)}>
          <TableCell>
            <Typography noWrap>{item.title}</Typography>
            {item.brand && (
              <Typography variant="caption" color="text.secondary">{item.brand}</Typography>
            )}
          </TableCell>
          {numericCells(item.performance)}
        </ClickableRow>
      ))}
    </TopShell>
  );
}

export function TopCampaignsTable({ title, items, loading, emptyHint }: TopCampaignsTableProps) {
  const navigate = useNavigate();
  return (
    <TopShell
      title={title}
      loading={loading}
      isEmpty={!loading && (!items || items.length === 0)}
      emptyHint={emptyHint}
      headers={["Кампания", "Расход", "Заказы", "ROAS"]}
    >
      {items?.map((item) => (
        <ClickableRow key={item.id} onClick={() => navigate(`/campaigns/${item.id}`)}>
          <TableCell>
            <Typography noWrap>{item.name}</Typography>
            <Typography variant="caption" color="text.secondary">{item.cabinet_name}</Typography>
          </TableCell>
          {numericCells(item.performance)}
        </ClickableRow>
      ))}
    </TopShell>
  );
}

export function TopQueriesTable({ title, items, loading, emptyHint }: TopQueriesTableProps) {
  const navigate = useNavigate();
  return (
    <TopShell
      title={title}
      loading={loading}
      isEmpty={!loading && (!items || items.length === 0)}
      emptyHint={emptyHint}
      headers={["Фраза", "Расход", "Заказы", "ROAS"]}
    >
      {items?.map((item) => (
        <ClickableRow key={item.id} onClick={() => navigate(`/queries/${item.id}`)}>
          <TableCell>
            <Typography noWrap>{item.keyword}</Typography>
            <Typography variant="caption" color="text.secondary">{item.campaign_name}</Typography>
          </TableCell>
          {numericCells(item.performance)}
        </ClickableRow>
      ))}
    </TopShell>
  );
}

// --- internal helpers ---

function TopShell({
  title,
  loading,
  isEmpty,
  emptyHint,
  headers,
  children,
}: {
  title: string;
  loading?: boolean;
  isEmpty?: boolean;
  emptyHint?: string;
  headers: string[];
  children: ReactNode;
}) {
  return (
    <Card variant="outlined" sx={{ flex: 1, minWidth: 0 }}>
      <CardContent>
        <Typography variant="h3" sx={{ fontSize: "1rem", fontWeight: 600, mb: 1 }}>
          {title}
        </Typography>
        {loading ? (
          <Stack spacing={1}>
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} variant="text" height={36} />
            ))}
          </Stack>
        ) : isEmpty ? (
          <Box sx={{ py: 2 }}>
            <Typography variant="body2" color="text.disabled">
              {emptyHint ?? "Нет данных"}
            </Typography>
          </Box>
        ) : (
          <Table size="small">
            <TableHead>
              <TableRow>
                {headers.map((h) => (
                  <TableCell key={h} sx={{ color: "text.secondary", fontWeight: 600 }}>{h}</TableCell>
                ))}
              </TableRow>
            </TableHead>
            <TableBody>{children}</TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

function ClickableRow({ children, onClick }: { children: ReactNode; onClick: () => void }) {
  return (
    <TableRow
      hover
      onClick={onClick}
      sx={{ cursor: "pointer", "&:last-child td": { borderBottom: 0 } }}
    >
      {children}
    </TableRow>
  );
}

function numericCells(perf: AdsMetricsSummary): ReactNode {
  return (
    <>
      <TableCell align="right">{formatMoney(perf.spend)}</TableCell>
      <TableCell align="right">{formatNumber(perf.orders)}</TableCell>
      <TableCell align="right">{perf.roas != null ? perf.roas.toFixed(2) : formatCompact(null)}</TableCell>
    </>
  );
}
