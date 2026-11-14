<?php
// Isolated cross-runtime fixture. All native marketplace requests are faked;
// the only allowed real HTTP destination is the owning test's local TLS server.
if (getenv('APP_ENV') !== 'testing') exit(90);
require getcwd().'/vendor/autoload.php';
$app = require getcwd().'/bootstrap/app.php';
$app->make(\Illuminate\Contracts\Console\Kernel::class)->bootstrap();

use App\Domains\Ozon\Api\OzonClient;
use App\Domains\Wildberries\Api\WildberriesClient;
use Illuminate\Support\Facades\Http;

$url = getenv('SELYA_GATE_URL');
if (! preg_match('~^https://127\.0\.0\.1:[0-9]+/internal/v1/prices/dispatch-authorizations$~D', $url)) exit(91);
config(['services.price_dispatch.url' => $url, 'services.price_dispatch.secret' => getenv('SELYA_GATE_SECRET'), 'services.price_dispatch.legacy_enabled' => true,
    'logging.default' => 'null', 'logging.channels.null' => ['driver' => 'monolog', 'handler' => \Monolog\Handler\NullHandler::class]]);
Http::globalOptions(['verify' => getenv('SELYA_GATE_CERT')]);
Http::preventStrayRequests();
Http::allowStrayRequests([$url]);
$native = 0;
$fake = function ($r) use (&$native) {
    $native++;
    if (str_contains($r->url(), 'ozon.ru')) return Http::response(['result' => [['product_id' => 101, 'offer_id' => 'isolated', 'updated' => true, 'errors' => []]]]);
    return Http::response(['error' => false, 'data' => ['id' => 321]]);
};
Http::fake(['https://api-seller.ozon.ru/*' => $fake, 'https://discounts-prices-api.wildberries.ru/*' => $fake]);
$payload = rtrim(strtr(base64_encode(json_encode(['sid' => '11111111-2222-3333-4444-555555555555'])), '+/', '-_'), '=');
$ozon = new OzonClient('555', 'test-native-key', '');
$wb = new WildberriesClient('e30.'.$payload.'.test-signature');
if (getenv('SELYA_GATE_MODE') === 'read') {
    $a = $ozon->post('/v5/product/info/prices', ['limit' => 1]);
    $b = $wb->pricesGet('/api/v2/list/goods/filter', ['limit' => 1]);
    echo json_encode(['native_requests_simulated' => $native, 'read_arrays' => is_array($a) && is_array($b)]);
} else {
    $a = $ozon->post('/v1/product/import/prices', ['prices' => [['product_id' => 101, 'price' => '123.45']]]);
    $b = $wb->pricesPost('/api/v2/upload/task', ['data' => [['nmID' => 101, 'price' => 123, 'discount' => 0]]]);
    echo json_encode(['native_requests_simulated' => $native, 'ozon_status' => $a['_sellico_price_dispatch']['status'] ?? null,
        'wb_status' => $b['_sellico_price_dispatch']['status'] ?? null]);
}
