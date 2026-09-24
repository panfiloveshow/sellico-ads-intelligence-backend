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
