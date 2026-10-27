// k6 load test: thousands of different users rush the checkout at once.
//
//   docker compose run --rm seed -start-in 0s
//   docker compose --profile loadtest run --rm k6
//
// passes only if exactly 500 users got a reservation and no request was
// dropped (no timeouts, no connection errors, no 502/504).
import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Rate } from "k6/metrics";
import exec from "k6/execution";

const BASE = __ENV.BASE_URL || "http://localhost:8080";
const USERS = Number(__ENV.USERS || 20000);
const STOCK = Number(__ENV.STOCK || 500);

const reserved = new Counter("reserved");
const soldOut = new Counter("sold_out");
const rateLimited = new Counter("rate_limited");
const dropped = new Rate("dropped"); // timeouts, connection errors, 502, 504

export const options = {
  scenarios: {
    rush: {
      executor: "shared-iterations",
      vus: Number(__ENV.VUS || 1000),
      iterations: USERS,
      maxDuration: "3m",
    },
  },
  thresholds: {
    reserved: [`count==${STOCK}`], // zero oversell, zero undersell
    dropped: ["rate==0"], // every request got a real answer
    "http_req_duration{name:checkout}": ["p(95)<800"],
  },
};

// 409 (sold out) and 429 (slow down) are correct answers, not failures
http.setResponseCallback(http.expectedStatuses(200, 202, 409, 429));

export default function () {
  const i = exec.scenario.iterationInTest;
  const headers = {
    "X-User-ID": `load_${i}`,
    // each fake user comes from its own ip, like real users behind a cdn
    "X-Forwarded-For": `10.${(i >> 16) & 255}.${(i >> 8) & 255}.${i & 255}`,
    "User-Agent": "Mozilla/5.0 (k6 load test)",
  };

  const tokenRes = request("POST", "/api/sale/token", headers, "token");
  if (tokenRes.status !== 200) return;
  headers["X-Sale-Token"] = tokenRes.json("token");
  headers["Idempotency-Key"] = uuidv4(); // same key on every retry below

  const res = request("POST", "/api/checkout", headers, "checkout");
  check(res, { "checkout answered": (r) => [202, 409, 429].includes(r.status) });
  if (res.status === 202) reserved.add(1);
  else if (res.status === 409) soldOut.add(1);
  else if (res.status === 429) rateLimited.add(1);
}

// sends a request and retries "come back later" answers like the real app does
function request(method, path, headers, name) {
  let res;
  for (let attempt = 0; attempt < 4; attempt++) {
    res = http.request(method, BASE + path, null, { headers, tags: { name }, timeout: "5s" });
    dropped.add(res.status === 0 || res.status === 502 || res.status === 504);
    if (res.status !== 503 && res.status !== 429) return res;
    sleep(0.2 * 2 ** attempt * (0.5 + Math.random() / 2)); // backoff with jitter
  }
  return res;
}

function uuidv4() {
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    return (c === "x" ? r : (r & 0x3) | 0x8).toString(16);
  });
}
