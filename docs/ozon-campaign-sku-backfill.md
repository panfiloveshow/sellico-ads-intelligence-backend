# Historical Ozon campaign SKU spend

The regular Ozon sync loads the last 14 days for running SKU campaigns. Use
`ozon-campaign-sku-backfill` to recover a historical month for a campaign that
has since become inactive. It requests the Ozon Performance campaign objects
report, verifies the response against locally stored daily campaign spend,
and replaces only the selected campaign/date window.

Run a dry run first (default):

```sh
docker exec sellico-api-1 /app/bin/ozon-campaign-sku-backfill \
  --cabinet CABINET_UUID --campaigns 30013812,32543493 \
  --from 2026-08-01 --to 2026-08-31
```

Then repeat with `--apply`. Each campaign is imported in its own database
transaction. The command reports `sku_spend`, `campaign_spend`, and `residual`.
The residual remains unassigned: campaign totals can include spend that the
per-SKU report does not attribute. This command does not change Ozon finance
transactions or store profit.

Only SKU campaigns with positive daily campaign stats are accepted. The
cabinet's existing encrypted Performance API credentials are used; credentials
are never printed.

## All-SKU-promo orders

The separate `ozon-cpo-backfill` command restores the promoted orders behind
an `ALL_SKU_PROMO` campaign:

```sh
docker exec sellico-api-1 /app/bin/ozon-cpo-backfill \
  --cabinet CABINET_UUID --campaign 28452972 \
  --from 2026-08-01 --to 2026-08-31
```

Repeat with `--apply` after the dry run reconciles. The Ozon report may include
orders dated after the requested UTC end time. The command filters by the date
on each report row, and requires the selected order charges to equal the daily
campaign spend to the kopeck before replacing that date window. The finance
service's accounting transactions are not modified.
