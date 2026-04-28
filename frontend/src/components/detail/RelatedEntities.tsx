import { Card, CardContent, List, ListItemButton, ListItemText, Stack, Typography, Skeleton } from "@mui/material";
import { useNavigate } from "react-router-dom";

import type { AdsEntityRef } from "@/api/queries/ads";

interface RelatedEntitiesProps {
  title: string;
  /** Where to navigate when an item is clicked: prefix is joined with item.id. */
  hrefPrefix: string;
  items?: AdsEntityRef[];
  emptyHint?: string;
  loading?: boolean;
}

/**
 * Generic "related X" panel on detail pages — works for related campaigns
 * on a product, related products on a campaign, top/waste/winning queries
 * on either. Keeps URL routing symmetric: clicking a related item routes
 * via `${hrefPrefix}/${ref.id}`, so a campaign's "related products" list
 * navigates to /products/:id and vice versa.
 *
 * Empty list hides the card entirely unless `emptyHint` is provided —
 * detail pages tend to have several of these and we don't want a wall of
 * empty boxes for a cabinet that's just been added.
 */
export function RelatedEntities({ title, hrefPrefix, items, emptyHint, loading }: RelatedEntitiesProps) {
  const navigate = useNavigate();

  if (!loading && (!items || items.length === 0) && !emptyHint) return null;

  return (
    <Card variant="outlined">
      <CardContent>
        <Typography variant="h3" sx={{ fontSize: "1rem", fontWeight: 600, mb: 1 }}>
          {title}
        </Typography>
        {loading ? (
          <Stack spacing={1}>
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} variant="text" height={32} />
            ))}
          </Stack>
        ) : !items || items.length === 0 ? (
          <Typography variant="body2" color="text.disabled">
            {emptyHint}
          </Typography>
        ) : (
          <List dense disablePadding>
            {items.map((ref) => (
              <ListItemButton key={ref.id} onClick={() => navigate(`${hrefPrefix}/${ref.id}`)}>
                <ListItemText
                  primary={ref.label}
                  secondary={ref.count != null ? `${ref.count}` : undefined}
                  primaryTypographyProps={{ noWrap: true }}
                />
              </ListItemButton>
            ))}
          </List>
        )}
      </CardContent>
    </Card>
  );
}
