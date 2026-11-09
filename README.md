# moulahmoulah

A proof-of-concept that walks a payment all the way through the chain:
customer → payment processor → card network → issuing bank → acquirer.

No real money is ever moved. Stripe runs strictly in test mode (free).

Currently implemented: **Stripe only** (hosted Checkout). Adyen and the fake
in-house processor are on the roadmap.

## The flow

```
Customer checkout -> Merchant backend (create payment)
                  -> Stripe Checkout (hosted page, test cards)
                  -> [Stripe compresses: card network -> issuer -> acquirer]
                  -> redirect back to merchant
                  -> success / failure page
```

Every processor implements the same lifecycle:

```
CREATED -> AUTHORIZED -> CAPTURED (success)
              \-> DECLINED / FAILED
```

## Architecture

**Backend: bare Go (stdlib only, zero dependencies)**

- Go 1.22+ `net/http` with method+path routing (`GET /api/orders/{id}`) — no router framework
- Stripe called via plain REST with `net/http` — no SDK
- Serves the built frontend as a single binary in production

**Frontend: Vite + React + TypeScript**

- Plain CSS (no Tailwind), dark theme
- React Router: `/`, `/success/:orderId`, `/failure/:orderId`

**Tooling: Biome** for all TS/TSX; `gofmt` / `go vet` on the Go side.

## Repo layout

```
moulahmoulah/
├── backend/                  # Go, stdlib only
│   ├── go.mod
│   ├── main.go               # net/http server, routes, SPA static serving
│   ├── handlers.go           # /api/catalog, /api/checkout, /api/orders/:id
│   ├── catalog.go            # products from Open Food Facts + disk cache
│   ├── store.go              # in-memory order store (+mutex)
│   └── processors/
│       ├── processor.go      # Processor interface + registry
│       └── stripe.go         # Stripe Checkout via plain REST
├── frontend/                 # Vite + React + TypeScript
│   ├── package.json
│   ├── vite.config.ts        # dev proxy /api -> localhost:3000
│   ├── index.html
│   └── src/
│       ├── App.tsx
│       ├── api.ts            # typed fetch client
│       ├── styles.css
│       └── pages/            # Catalog, Success, Failure (all keyed by data-testid)
├── e2e/                      # Playwright end-to-end payment tests
│   ├── playwright.config.ts  # starts backend + Vite itself, records video
│   └── tests/                # checkout.spec.ts + Stripe hosted-page helper
├── biome.json
├── Makefile
└── .env.example
```

## Products

The catalog is **real product data from the [Open Food Facts](https://world.openfoodfacts.org) API** (barcode, name, brand, size, image), and **real prices from the [Open Prices](https://prices.openfoodfacts.org) API** (user-contributed in-store prices; most recent EUR price per barcode wins — e.g. Coca-Cola 1 L ≈ €1.79). Products without a recorded price fall back to a stable synthetic price: the quantity ("1,5 L", "500 g") is parsed and multiplied by a per-kg/per-L rate derived from the barcode. The `priceSource` field on each product says which one it is. The catalog is fetched once and cached to `backend/data/catalog.json` (gitignored, versioned) — restarts load from the cache and never refetch; delete the file (or bump the cache version) to refetch.

The Stripe Checkout page renders one line per basket item (name, image, unit price, quantity) instead of a single aggregated line.

## Quick start

```bash
# 0. Configure (test keys only)
cp .env.example .env   # then fill in STRIPE_SECRET_KEY (sk_test_...)

# 1. Backend (Go on :3000) — load .env into the environment first
set -a; . ./.env; set +a
make dev-backend

# 2. Frontend (Vite dev server on :5173, proxies /api)
make dev-frontend
```

Open http://localhost:5173, add something to the cart, and pay with a Stripe
test card:

- `4242 4242 4242 4242` — succeeds
- `4000 0000 0000 0002` — declines

Any expiry in the future, any CVC, any postal code.

Production: a single binary serves everything.

```bash
make build
BASE_URL=http://localhost:3000 STRIPE_SECRET_KEY=sk_test_... ./moulahmoulah
```

## E2E tests (Playwright)

`e2e/` drives a real payment in a browser, all the way through Stripe test mode:

```
basket → Pay with Stripe → checkout.stripe.com (test card 4242…) → /success/:id (CAPTURED)
```

```bash
make e2e          # installs deps + chromium, then runs the suite
make e2e-headed   # watch it run in a visible browser
```

The Playwright config starts both servers itself — Go backend on :3000 (with
`STRIPE_SECRET_KEY` read from the repo-root `.env`) and Vite on :5173 — and
reuses them if they are already running. Selectors only use the `data-testid`
attributes in the React app (`add-to-basket-<id>`, `pay-stripe`, `cart-total`,
`payment-heading`, `order-status`, …), never CSS classes, so markup changes
don't break the tests.

Every run is **recorded to video** (default `E2E_VIDEO=on`) into
`e2e/test-results/<spec>/<test>/video.webm`; set `E2E_VIDEO=off` or
`E2E_VIDEO=retain-on-failure` to change that. The HTML report lands in
`e2e/playwright-report/` — view it with `cd e2e && npm run report`.

## API

| Method | Path | Description |
| --- | --- | --- |
| GET | `/api/catalog` | Product catalog (prices in minor units) |
| POST | `/api/checkout` | `{processorId, items}` → `{orderId, url}` — redirect the customer to `url` |
| GET | `/api/orders/{id}` | Order with live status refreshed from the processor |
| POST | `/api/orders/{id}/capture` | Capture an authorized payment (AUTHORIZED → CAPTURED) |

Stripe sessions are created with **automatic capture** so the hosted page
redirects to `success_url` right after payment (with `capture_method=manual`
the session completes but stays unpaid and Checkout does not redirect). The
PaymentIntent is filled via `payment_intent_data`: `description`,
`statement_descriptor_suffix` (`MOULAHMOULAH`), and metadata (`order_id`,
`items`, `item_count`, `amount`, `currency`). `POST /api/orders/{id}/capture`
still exists for PIs that end up in `requires_capture` (AUTHORIZED).

## Processor interface

```go
type Processor interface {
    ID() string
    Label() string
    CreatePayment(req PaymentRequest) (PaymentResult, error)
    GetStatus(providerRef string) (Status, error)
    Capture(providerRef string) error
}
```

Adding another processor (Adyen, PayPal, Mollie…) = adding one file to
`backend/processors/` that calls `Register(...)`.

## Roadmap

- [ ] Adyen (Payment Links / Drop-in, test mode)
- [ ] Fake in-house processor with the full chain exposed (tokenization → 3DS → network → issuer auth → capture → T+2 settlement)
- [ ] Webhook endpoints + signature verification (Stripe)
- [ ] Refunds & partial captures
- [ ] Dispute / chargeback simulation

## Notes

- Test/sandbox credentials only — never commit live keys.
- Demo code: not production-hardened. Real payments require PCI review, idempotency keys, retries, reconciliation, and more.

## License

[MIT](LICENSE) — Copyright (c) 2026 tonyzoooo
