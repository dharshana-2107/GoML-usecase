#!/usr/bin/env bash
# checks the result of a sale against the docker compose stack.
# fails (exit 1) unless exactly STOCK phones were sold with no duplicates.
#
#   ./scripts/verify-sale.sh            # expects 500
#   STOCK=100 ./scripts/verify-sale.sh
set -euo pipefail

STOCK="${STOCK:-500}"
SKU="${SKU:-PHONE-1}"

q() { docker compose exec -T postgres psql -U app -d flashsale -tAc "$1" | tr -d '[:space:]'; }

# workers save orders in the background, so wait until the queue drains
echo "waiting for workers to finish..."
for _ in $(seq 1 60); do
  CONFIRMED=$(q "SELECT count(*) FROM orders WHERE sku='$SKU' AND status='confirmed'")
  [ "$CONFIRMED" -ge "$STOCK" ] && break
  sleep 1
done
sleep 3 # give any late (oversold) write a chance to show up

SOLD=$(q "SELECT sold FROM inventory WHERE sku='$SKU'")
CONFIRMED=$(q "SELECT count(*) FROM orders WHERE sku='$SKU' AND status='confirmed'")
REJECTED=$(q "SELECT count(*) FROM orders WHERE sku='$SKU' AND status='rejected'")
DUPES=$(q "SELECT count(*) FROM (SELECT user_id FROM orders WHERE sku='$SKU' AND status='confirmed' GROUP BY user_id HAVING count(*) > 1) d")
LEFT=$(docker compose exec -T redis redis-cli GET "sale:{$SKU}:stock" | tr -d '[:space:]')

REPORT=$(cat <<EOF
| check              | expected | actual |
|--------------------|----------|--------|
| inventory.sold     | $STOCK | $SOLD |
| confirmed orders   | $STOCK | $CONFIRMED |
| rejected by db     | 0 | $REJECTED |
| duplicate buyers   | 0 | $DUPES |
| redis stock left   | 0 | $LEFT |
EOF
)
echo "$REPORT"
# show the table on the github actions run page too
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  printf '### oversell check\n\n%s\n' "$REPORT" >> "$GITHUB_STEP_SUMMARY"
fi

if [ "$SOLD" = "$STOCK" ] && [ "$CONFIRMED" = "$STOCK" ] && [ "$DUPES" = "0" ] && [ "$LEFT" = "0" ]; then
  echo "PASS: exactly $STOCK sold, zero oversell"
else
  echo "FAIL: consistency check failed" >&2
  exit 1
fi
