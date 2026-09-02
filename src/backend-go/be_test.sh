#!/bin/sh
# Backend connectivity probes: IPv4 vs IPv6 vs localhost, plus CORS preflight.
echo "=== 127.0.0.1 (IPv4) /api/domains ==="
curl -s -m 5 -o /tmp/d4.json -w "HTTP %{http_code} in %{time_total}s\n" http://127.0.0.1:8000/api/domains
echo "bytes:"; wc -c /tmp/d4.json
echo ""
echo "=== [::1] (IPv6) /api/domains ==="
curl -s -m 5 -o /tmp/d6.json -w "HTTP %{http_code} in %{time_total}s\n" "http://[::1]:8000/api/domains"
echo "bytes:"; wc -c /tmp/d6.json
echo ""
echo "=== localhost (may try IPv6 first) ==="
curl -s -m 5 -o /tmp/dl.json -w "HTTP %{http_code} in %{time_total}s\n" http://localhost:8000/api/domains
echo "bytes:"; wc -c /tmp/dl.json
echo ""
echo "=== OPTIONS preflight via localhost (mimics webkit fetch) ==="
curl -s -m 5 -o /dev/null -w "preflight HTTP %{http_code}\n" -X OPTIONS -H "Origin: wails://" -H "Access-Control-Request-Method: GET" http://localhost:8000/api/domains
echo ""
echo "=== curl stream endpoint (5s) ==="
timeout 6 curl -s -m 5 -N -H "Content-Type: application/json" -H "Accept: text/event-stream" -X POST http://localhost:8000/api/messages/stream -d '{"message":"ping"}' -o /tmp/stream_curl.txt 2>&1
echo "curl stream exit=$?"; wc -l /tmp/stream_curl.txt; head -20 /tmp/stream_curl.txt
