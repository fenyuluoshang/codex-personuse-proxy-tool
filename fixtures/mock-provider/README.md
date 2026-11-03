# Mock provider fixture

This local-only server is the A3 test fixture for the project. It accepts no
real credentials and does not persist request bodies. It records only the last
request's method, path, model, byte count, SHA-256 digest, and whether sensitive
headers were present. The Authorization value is represented by a digest only.

Run it from this directory:

```powershell
go run . -addr 127.0.0.1:18080
```

Useful endpoints:

- `GET /healthz` — liveness.
- `GET /v1/models` and `GET /backend-api/codex/models` — a small model catalog.
- `POST /v1/responses` and `/backend-api/codex/responses` — streaming SSE by default.
- `GET /__mock/config` — inspect current test behavior.
- `PUT /__mock/config` — change behavior without restarting.
- `GET /__mock/requests` — inspect redacted metadata only.

Examples:

```powershell
# Return HTTP 503 for the next requests.
Invoke-RestMethod http://127.0.0.1:18080/__mock/config -Method Put `
  -ContentType 'application/json' -Body '{"status":503}'

# Exercise small SSE chunks and a delay between them.
Invoke-RestMethod http://127.0.0.1:18080/__mock/config -Method Put `
  -ContentType 'application/json' -Body '{"status":200,"stream":true,"chunk_size":8,"chunk_delay_ms":25}'

# Switch to a complete JSON response.
Invoke-RestMethod http://127.0.0.1:18080/__mock/config -Method Put `
  -ContentType 'application/json' -Body '{"stream":false}'
```

SSE uses Responses lifecycle events, from `response.created` to
`response.completed`, including text deltas and the final assistant message.
`chunk_size` controls the number of bytes written and flushed at a time, so JSON
and SSE boundaries can cross network fragments. It does not split response JSON
into text deltas. This supports the real CLI probe in `scripts/verify-codex.py`.

The fixture binds to loopback by default. Do not change `-addr` to a public
interface in shared or untrusted environments.
