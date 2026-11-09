.PHONY: dev-backend dev-frontend build check e2e e2e-headed

# Backend (Go on :3000). Load env first: set -a; . ./.env; set +a; make dev-backend
dev-backend:
	cd backend && go run .

# Frontend (Vite dev server on :5173, proxies /api to :3000)
dev-frontend:
	cd frontend && npm run dev

# Production: build the frontend, copy it into the backend, build one binary
build:
	cd frontend && npm ci && npm run build
	mkdir -p backend/dist
	cp -r frontend/dist/. backend/dist/
	cd backend && go build -o ../moulahmoulah .

check:
	cd backend && gofmt -l . && go vet ./...
	cd frontend && npx biome check .

# E2E: Playwright drives a real Stripe test-mode checkout in a browser.
# Needs .env at the repo root with STRIPE_SECRET_KEY=sk_test_...
e2e:
	cd e2e && npm ci && npx playwright install chromium && npx playwright test

e2e-headed:
	cd e2e && npx playwright test --headed
