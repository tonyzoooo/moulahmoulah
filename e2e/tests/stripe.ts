import type { Locator, Page } from "@playwright/test";

// Stripe test-mode cards — https://docs.stripe.com/testing
export const STRIPE_TEST_CARDS = {
  success: "4242424242424242",
  decline: "4000000000000002",
} as const;

export interface CardDetails {
  number: string;
  expiry?: string; // MMYY, any future date
  cvc?: string;
  name?: string;
  email?: string;
  postalCode?: string;
}

// Fill a field on Stripe's hosted Checkout page (checkout.stripe.com). The
// current page renders all inputs in the top document once the card accordion
// is expanded; older UIs render card fields inside __privateStripeFrame
// iframes — search both. Selectors are comma-combined so this helper survives
// Stripe DOM changes.
async function fillField(page: Page, css: string, value: string): Promise<void> {
  const deadline = Date.now() + 20_000;
  let lastError = "";
  while (Date.now() < deadline) {
    const locators: Locator[] = [page.locator(css).first()];
    for (const frame of page.frames()) {
      if (frame !== page.mainFrame()) locators.push(frame.locator(css).first());
    }
    for (const loc of locators) {
      try {
        if ((await loc.count()) > 0) {
          await loc.fill(value, { timeout: 5_000 });
          return;
        }
      } catch (e) {
        lastError = String(e);
      }
    }
    await page.waitForTimeout(250);
  }
  throw new Error(`no Stripe field matched ${css} (${lastError})`);
}

const CARD_NUMBER_FIELD =
  'input[name="cardNumber"], input[name="number"], input[autocomplete="cc-number"]';

export async function fillStripeCheckout(page: Page, card: CardDetails): Promise<void> {
  await page.waitForURL(/checkout\.stripe\.com/, { timeout: 30_000 });

  await fillField(page, 'input[name="email"], #email', card.email ?? "test@example.com");
  // Blur the email field — Stripe mounts the payment form once contact info is set.
  await page.locator('input[name="email"]').press("Tab").catch(() => {});

  // The card fields only mount once the card accordion is expanded. The
  // accordion's click-area button can be zero-size, so click the item itself
  // (force) and re-check — toggling at most a few times covers both states.
  for (let attempt = 0; attempt < 4; attempt++) {
    if (await page.locator(CARD_NUMBER_FIELD).first().isVisible().catch(() => false)) break;
    await page
      .locator('[data-testid="card-accordion-item"]')
      .first()
      .click({ force: true })
      .catch(() => {});
    await page.waitForTimeout(3_000);
  }

  await fillField(page, CARD_NUMBER_FIELD, card.number);
  await fillField(
    page,
    'input[name="cardExpiry"], input[name="expiry"], input[autocomplete="cc-exp"]',
    card.expiry ?? "1230",
  );
  await fillField(
    page,
    'input[name="cardCvc"], input[name="cvc"], input[autocomplete="cc-csc"]',
    card.cvc ?? "123",
  );
  await fillField(
    page,
    'input[name="billingName"], input[autocomplete="cc-name"]',
    card.name ?? "Test Customer",
  );

  const postal = page
    .locator('input[name="billingPostalCode"], input[autocomplete="postal-code"]')
    .first();
  if (await postal.isVisible().catch(() => false)) {
    await postal.fill(card.postalCode ?? "75001");
  }
}

export async function submitStripeCheckout(page: Page): Promise<void> {
  await page
    .locator('[data-testid="hosted-payment-submit-button"], button[type="submit"]')
    .first()
    .click();
}
