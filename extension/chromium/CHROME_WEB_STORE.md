# Chrome Web Store — Submission Checklist

## Pre-submission

- [ ] `manifest.json` version bumped from previous published version
- [ ] PNG icons exist at `icons/icon16.png`, `icon48.png`, `icon128.png`
      (run `extension/chromium/icons/generate-icons.sh`; needs `imagemagick`)
- [ ] Promo tile (440 × 280 PNG, no transparency) — see `marketing/store-assets/`
- [ ] 5 screenshots (1280 × 800 PNG) showing key flows:
      1. Login on options page
      2. Live panel inside a WB campaign page
      3. Bid snapshot card
      4. Position tracker view
      5. Sellico dashboard relating the captured data
- [ ] Privacy policy live at https://sellico.ru/privacy/extension (mirrors PRIVACY.md)
- [ ] Latest CRX built via `make pack-extension` (uses scripts/pack-extension.sh)

## Listing copy (RU)

**Название**: Sellico Ads Intelligence
**Краткое описание (132 chars)**:
> Виджет рекомендаций ставок и захват сигналов из кабинета Wildberries прямо в браузере. Для подписчиков Sellico.

**Подробное описание**:
> Расширение Sellico Ads Intelligence интегрируется с вашим кабинетом продавца Wildberries и:
>
> • Показывает живые рекомендации Sellico по ставкам прямо рядом с полем ставки
> • Захватывает изменения ставок и позиций для аналитики в Sellico
> • Привязывает действия в кабинете к данным аналитики (без ручного экспорта)
>
> Требуется активная подписка Sellico (sellico.ru). Расширение работает ТОЛЬКО на страницах seller.wildberries.ru и cmp.wildberries.ru — никаких других сайтов не видит.
>
> Политика конфиденциальности: https://sellico.ru/privacy/extension

## Listing copy (EN)

**Name**: Sellico Ads Intelligence
**Short description**:
> Bid recommendations widget and signal capture for Wildberries Seller Cabinet. Sellico subscribers only.

**Detailed description**:
> Sellico Ads Intelligence integrates with your Wildberries Seller Cabinet to:
>
> • Show live Sellico bid recommendations next to the bid input field
> • Capture bid and position changes for Sellico's analytics
> • Link cabinet actions to analytics data (no manual export)
>
> Requires an active Sellico subscription (sellico.ru). Works ONLY on seller.wildberries.ru and cmp.wildberries.ru pages — no other site is accessed.
>
> Privacy policy: https://sellico.ru/privacy/extension

## Categories

- Primary: **Productivity**
- Single-purpose: "WB Ads enrichment for Sellico subscribers"

## Permissions justification (asked at review)

| Permission | Justification |
|------------|---------------|
| `storage` | Persist user's Sellico backend URL + access token between sessions. |
| `cookies` | Share Sellico session cookie so extension uses the same auth as the website. |
| `host_permissions: *.wildberries.ru/*` | Inject the panel and listen for bid/position events. |
| `host_permissions: *.sellico.ru/*` | Send captured events to user's Sellico workspace. |
| `optional_host_permissions: localhost` | Only granted if user opts in for self-hosted Sellico backend. |

Remote code: NONE. All scripts are bundled in the package.

## After submission

Track review status in Chrome Web Store dashboard. Typical review time
3-7 days. Respond to reviewer comments within 24h to avoid the listing
going back to the queue.

After publication:
- Update `extension/chromium/README.md` with the listing URL
- Add the listing URL to `frontend` settings page (banner with install link)
- Tag the release: `git tag extension-v1.0.0 && git push --tags`
