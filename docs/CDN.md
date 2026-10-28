# Local CDN simulation

In production the static site sits in object storage (S3) behind a CDN, so 100k page loads at 8 PM never reach the application servers. This setup reproduces that on one machine.

```
browser ──► cdn edge (nginx :8090) ──► minio "s3" bucket     static files (cached at the edge)
                      │
                      └──────────────► origin gateway (nginx) ──► go api ...   /api/* (not cached)
```

| Piece | Plays the role of | File |
|---|---|---|
| `minio` (Chainguard build) | Amazon S3 | `docker-compose.cdn.yml` |
| `static-upload` | the deploy step that uploads `dist/` to S3 with the AWS CLI | `cdn/Dockerfile.upload`, `cdn/upload.sh` |
| `cdn` | CloudFront / Cloudflare edge | `cdn/nginx.conf` |

## Caching rules

| Path | Edge cache | Why |
|---|---|---|
| `/assets/*` | 1 year, `immutable` | file names contain a content hash, so a change means a new name |
| `/index.html`, `/` | 30 seconds | points to the newest hashed files; new deploys show up within 30 s |
| `/api/sale/status` | 1 second | same answer for everyone; 100k pollers become ~1 origin request per second |
| other `/api/*` | never | per-user answers (tokens, checkout, orders) must never be shared |

`proxy_cache_lock` collapses simultaneous misses: if 1,000 users ask for a file that just expired, the edge fetches it from the origin once. Unknown paths fall back to `index.html` so client-side routes work.

The edge forwards the real client IP in `X-Forwarded-For`, so the origin gateway still rate-limits per user rather than per CDN node.

## Run it

```bash
# .env needs MINIO_ROOT_PASSWORD as well (see .env.example)
docker compose -f docker-compose.yml -f docker-compose.cdn.yml up -d --build
docker compose run --rm seed -start-in 0s
```

Browse through the CDN at **http://localhost:8090**. The origin is still reachable directly at http://localhost:8080 for comparison. The MinIO console is at http://localhost:9001 (user `flashadmin`, password from `.env`), bound to this machine only.

## See the cache work

```bash
# find the current js file name
ASSET=$(curl -s http://localhost:8090/ | grep -o 'assets/[^"]*\.js' | head -1)

curl -sI "http://localhost:8090/$ASSET" | grep -i x-cache-status   # MISS (first time)
curl -sI "http://localhost:8090/$ASSET" | grep -i x-cache-status   # HIT  (served from the edge)

docker compose -f docker-compose.yml -f docker-compose.cdn.yml logs cdn --tail=20
```

The `cdn` log shows `cache=HIT` / `cache=MISS` for every request.

## What changes in real production

- **The origin is locked down:** only the CDN's IP ranges may reach the gateway, and the gateway's `set_real_ip_from` lists only those ranges.
- **The bucket is private:** the CDN reads it with an origin access identity instead of a public-read policy.
- **TLS and bot protection** are terminated at the edge.
- **Deploys** upload new hashed assets first, then `index.html`, then purge `index.html` from the CDN.
