# Security headers
add_header X-Content-Type-Options "nosniff" always;
add_header X-Frame-Options "DENY" always;
add_header X-XSS-Protection "1; mode=block" always;
add_header Referrer-Policy "strict-origin-when-cross-origin" always;

# Request body limits
client_max_body_size 10m;

# Timeouts
proxy_connect_timeout 10s;
proxy_send_timeout    30s;
proxy_read_timeout    60s;

# Common proxy headers
proxy_set_header Host              $host;
proxy_set_header X-Real-IP         $remote_addr;
proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Request-ID      $request_id;

# Health probes — no rate limit
location /health/ {
    proxy_pass http://api_backend;
}

# Auth endpoints — strict rate limit
location /api/v1/auth/ {
    limit_req zone=api_auth burst=10 nodelay;
    limit_req_status 429;
    proxy_pass http://api_backend;
}

# OpenAPI & docs — no rate limit
location = /openapi.yaml {
    proxy_pass http://api_backend;
}
location = /docs {
    proxy_pass http://api_backend;
}

# All other API routes — general rate limit
location /api/ {
    limit_req zone=api_general burst=60 nodelay;
    limit_req_status 429;
    proxy_pass http://api_backend;
}

# SSE events endpoint — disable buffering for real-time streaming
location /api/v1/events {
    limit_req zone=api_general burst=10 nodelay;
    proxy_pass http://api_backend;
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 3600s;
    chunked_transfer_encoding off;
}

# Block /metrics from external access (Prometheus scrapes internally)
location = /metrics {
    return 403 '{"data":null,"errors":[{"code":"FORBIDDEN","message":"forbidden"}]}';
    add_header Content-Type application/json;
}

# Default — 404
location / {
    return 404 '{"data":null,"errors":[{"code":"NOT_FOUND","message":"not found"}]}';
    add_header Content-Type application/json;
}
