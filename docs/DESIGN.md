# Design notes

This document explains how this problem statement can be implemented using Amazon Web Services and make the system production grade, solving the trial error problems.
## 1. Core services
 
| AWS service | What it does | Service used in this project |
|---|---|---|
| **Amazon RDS for PostgreSQL** | Managed PostgreSQL with Multi-AZ failover, automated backups and point-in-time restore | **PostgreSQL 16** container (`postgres:16-alpine`) |
| **RDS read replicas** / Multi-AZ DB cluster | Copies of the database in other Availability Zones: a standby takes over on failure, and read replicas serve reads (order history, reports) without loading the primary | **None** — one Postgres container, no replica |
| **AWS Amplify Hosting** | Builds the frontend from Git and serves it from a global CDN, with preview links per pull request | **nginx** serving the React build, baked into the gateway image |
| **Amazon SQS** | Fully managed message queue with dead-letter queues; no broker to run | **RabbitMQ 4** quorum queue + dead-letter queue |
| **Amazon EC2** (with Auto Scaling) | Virtual servers that can be added or removed automatically or on a schedule | **Docker containers**: 3 API replicas, 2 worker replicas |
| **Elastic Load Balancing** (Application Load Balancer) | Spreads traffic across healthy servers in several data centres | **nginx** `upstream` with `least_conn` |
| **Amazon CloudFront + Amazon S3 + AWS WAF** (CDN edge) | Static files stored in S3 and cached at edge locations worldwide; WAF filters bots and attacks at the edge before they reach the servers | **MinIO** bucket (S3-compatible) + **nginx edge cache** on `:8090` — optional local CDN simulation, no WAF |
 
## 2. Supporting services
 
| AWS service | What it does | Service used in this project |
|---|---|---|
| **Amazon ElastiCache** (Valkey / Redis) | Managed Redis with automatic failover to a replica | **Redis 7** container with append-only file |
| **Amazon CloudFront** (API cache) | Caches `/api/sale/status` for 1 second at the edge | **nginx** gateway 1-second micro-cache |
| **AWS WAF** rate-based rules + Bot Control | Blocks bots, known bad IPs and request floods | **nginx** user-agent filter and `limit_req` rate limits at the gateway |
| **Amazon RDS Proxy** | Pools database connections; keeps them open during a failover | Connection pool inside the Go app (`pgxpool`) |
| **AWS Secrets Manager** | Stores and rotates secrets, encrypted | **`.env` file** (ignored by git) |
| **Amazon Route 53** + **ACM** | DNS and free TLS certificates | `localhost`, no HTTPS | — |
| **Amazon CloudWatch** + **AWS X-Ray** | Metrics, logs, alarms and request tracing | `docker compose logs`, RabbitMQ dashboard |
| **Amazon ECR** | Private registry for Docker images | **GitHub Container Registry** (GHCR) |
 
---
 
## 3. Why AWS is more advantageous in production
 
The local stack proves the design is **correct**: zero oversell, idempotent retries, a database guard. What it cannot provide is **reliability at scale**, because everything runs on one machine. If that machine fails, the sale stops. AWS removes that single point of failure and takes over the work of running each component.
 
### 3.1 Fault tolerance: the sale survives failures
 
Locally, every component runs once on one machine, so any single failure stops the sale. On AWS, each layer has a spare in another Availability Zone (a separate data centre with its own power and network) and fails over automatically.
 
| Failure | Local result | AWS result |
|---|---|---|
| **Database server** | Orders stop until someone restarts the container | **RDS Multi-AZ** promotes the standby automatically; **RDS Proxy** holds application connections open during the switch |
| **Database data loss** | Only what is on the local disk; no backups | **Read replicas / standbys** keep live copies in other AZs; automated backups restore to any second in the retention window |
| **Redis** | Sale pauses; up to 1 s of writes lost | **ElastiCache Multi-AZ** promotes a replica; any writes lost in the switch are still caught by the Postgres guard (`sold < total`) |
| **Message broker** | One RabbitMQ node; if it is down, checkouts get `503` | **SQS** has no broker to fail: each message is stored across several Availability Zones |
| **A worker crashes mid-order** | RabbitMQ redelivers the message | Same: the message reappears after the SQS **visibility timeout**; the `reservation_id` primary key makes the retry harmless |
| **A poison message** | Moved to the RabbitMQ dead-letter queue | Moved to the **SQS DLQ** after 5 attempts; a **CloudWatch alarm** pages the on-call engineer, and **DLQ redrive** replays it after the fix |
| **An API server** | nginx skips it; no replacement | **ALB health checks** stop sending it traffic; **Auto Scaling** replaces it automatically |
| **A whole data centre** | Everything is down | Traffic shifts to the other two Availability Zones |
| **A traffic flood or DDoS** | nginx and the host are overwhelmed | **CloudFront**, **Shield** and **WAF** absorb and filter it at the edge, before it reaches the VPC |
 
 
### 3.2 Multi-AZ, multi-region and compatibility
 
**Multi-AZ by default.** RDS, ElastiCache, SQS, the load balancer and the EC2 groups all span 3 Availability Zones. This needs no change to the application code.
 
**Multi-region when needed.** If the sale must survive the loss of an entire AWS region:
 
- **Route 53 health checks** fail DNS over to a standby region.
- A **cross-region RDS read replica** (or Aurora Global Database) keeps the database copied to the second region, ready to be promoted.
- **CloudFront** and **Amplify** already serve the frontend from edge locations worldwide, so the page stays fast for users everywhere.
One region with 3 Availability Zones covers most failures, so multi-region is a future option rather than a launch requirement.
 
**Compatible with the tools used locally.** The project was built on open-source tools that have AWS-managed, protocol-compatible equivalents, so most of the code moves over unchanged:
 
| Local tool | AWS equivalent | Changes required in the code |
|---|---|---|
| PostgreSQL 16 | RDS for PostgreSQL (or Aurora PostgreSQL-Compatible) | Connection string and TLS only; schema, constraints and queries are identical |
| Redis 7 + Lua scripts | ElastiCache for Valkey / Redis OSS | Endpoint and TLS only; `reserve.lua` and `release.lua` run unchanged |
| MinIO | Amazon S3 | Nothing; MinIO speaks the S3 API, and `cdn/upload.sh` already uses the AWS CLI |
| Docker images | EC2 / ECS, images stored in ECR | Nothing; the same `api` and `worker` images run as-is |
| RabbitMQ | Amazon MQ for RabbitMQ (no change) or SQS (small rewrite) | Choice between zero change and a fully serverless queue |
| nginx `least_conn` | ALB *least outstanding requests* | Configuration only |
 
It also runs on **Graviton (ARM)** instances: Go cross-compiles for ARM with one build flag, giving better price-performance than x86.
 
### 3.3 Scale: 
 
- **EC2 scheduled scaling** adds servers at 7:30 PM and removes them after the sale, so the extra capacity costs money only for the sale window.
- **ALB capacity reservation** pre-warms the load balancer for the 10,000-requests-per-second jump, instead of letting it scale up mid-spike.
- **CloudFront** serves the page and the cached sale status from edge locations worldwide, so 100,000 browsers polling the status cost the servers about one request per second.
- **Amplify** serves the React app from a CDN, so page loads at 8 PM never reach the servers at all.
### 3.4 Security: protection before traffic reaches the servers
 
- **AWS WAF + Bot Control** blocks bots and abusive IPs at the edge, with browser challenges that headless scripts fail. Locally, nginx only checks the user-agent string, which any script can fake.
- **AWS Shield Standard** protects against network-level DDoS attacks at no extra cost.
- **Secrets Manager** replaces a plain `.env` file: secrets are encrypted, access is logged, and database passwords rotate automatically.
- **Private networking (VPC):** the database and cache have no internet access at all; only the load balancer is reachable, and only through CloudFront.
### 3.5 Operations: less to run by hand
 
| Task | Local | AWS |
|---|---|---|
| Database backups | Manual | Automatic, with restore to any second in the retention window |
| Scaling reads | Not possible | Add an **RDS read replica**; send reporting and order-history queries to it |
| Patching Postgres, Redis, RabbitMQ | Manual image updates | Managed by AWS |
| Noticing a problem during the sale | Watching logs | **CloudWatch alarms** page the on-call engineer (e.g. any message in the dead-letter queue) |
| Tracing a slow request | Not possible | **X-Ray** shows the time spent in each service |
| Frontend deploys | Rebuild the nginx image | **Amplify** deploys on every push, with a preview link per pull request |
 
### 3.6 Room to grow
 
 
- **Virtual Waiting Room on AWS** — admit buyers in fair batches instead of all at once.
- **EventBridge Scheduler + Lambda** — release unpaid reservations after 10 minutes.
- **Step Functions** — a payment flow that refunds and releases the unit automatically if a step fails.
- **Amazon SES** — order confirmation emails.
- **Amazon Cognito** — verified sign-up and multi-factor authentication.
---
 
## 4. Trade-offs
 
AWS is the better choice for the production sale, but it is not free of costs:
 
| Trade-off | Detail |
|---|---|
| **Cost** | Managed services cost more than containers on one machine, especially Multi-AZ databases and WAF Bot Control. Scheduled scaling keeps the peak cost limited to the sale window. |
| **Vendor lock-in** | SQS, Amplify and WAF are AWS-specific. Keeping the queue behind the existing `Publisher` interface limits the impact. |
| **Local development** | Docker Compose stays the way to develop and test locally, so both setups must be kept in sync. |
 
 
