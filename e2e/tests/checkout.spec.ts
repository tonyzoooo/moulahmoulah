import { expect, test } from "@playwright/test";
import { fillStripeCheckout, STRIPE_TEST_CARDS, submitStripeCheckout } from "./stripe";

interface CatalogProduct {
  id: string;
  name: string;
  price: number; // minor units (cents for EUR)
  currency: string;
}

function eur(minorUnits: number): string {
  return `€${(minorUnits / 100).toFixed(2)}`;
}

// Pick a product from the API so the test does not depend on catalog contents.
async function firstProduct(baseURL: string): Promise<CatalogProduct> {
  const res = await fetch(new URL("/api/catalog", baseURL));
  if (!res.ok) throw new Error(`catalog request failed: HTTP ${res.status}`);
  const products = (await res.json()) as CatalogProduct[];
  if (products.length === 0) throw new Error("catalog is empty");
  return products[0];
}

test("e2e payment: basket → Stripe test checkout → captured success page", async ({
  page,
}, testInfo) => {
  const baseURL = testInfo.project.use.baseURL as string;
  const product = await firstProduct(baseURL);

  await page.goto("/");
  await page.getByTestId(`add-to-basket-${product.id}`).click();
  // Bump the quantity through the basket controls.
  await page.getByTestId(`qty-increase-${product.id}`).click();

  await expect(page.getByTestId("cart-badge")).toHaveText("Basket: 2 items");
  await expect(page.getByTestId(`qty-value-${product.id}`)).toHaveText("2");
  await expect(page.getByTestId(`cart-line-total-${product.id}`)).toHaveText(eur(product.price * 2));
  await expect(page.getByTestId("cart-total")).toContainText(eur(product.price * 2));

  await page.getByTestId("pay-stripe").click();

  // Off to Stripe's hosted Checkout page; pay with the success test card.
  await fillStripeCheckout(page, { number: STRIPE_TEST_CARDS.success });
  await submitStripeCheckout(page);

  // Stripe redirects back to the merchant's success page…
  await page.waitForURL(/\/success\/[0-9a-f]+/, { timeout: 60_000 });
  const orderId = new URL(page.url()).pathname.split("/").pop() as string;

  // …which polls the backend until the payment reaches CAPTURED.
  await expect(page.getByTestId("payment-heading")).toHaveText("Payment captured", {
    timeout: 30_000,
  });
  await expect(page.getByTestId("order-status")).toHaveText("CAPTURED");
  await expect(page.getByTestId("order-id")).toHaveText(orderId);
  await expect(page.getByTestId("order-amount")).toContainText(eur(product.price * 2));
});

test("declined card: Stripe shows the decline inline, no redirect", async ({ page }, testInfo) => {
  const baseURL = testInfo.project.use.baseURL as string;
  const product = await firstProduct(baseURL);

  await page.goto("/");
  await page.getByTestId(`add-to-basket-${product.id}`).click();
  await page.getByTestId("pay-stripe").click();

  await fillStripeCheckout(page, { number: STRIPE_TEST_CARDS.decline });
  await submitStripeCheckout(page);

  // The generic-decline card is rejected on the hosted page itself.
  await expect(page.getByText(/declined/i).first()).toBeVisible({ timeout: 30_000 });
  expect(new URL(page.url()).hostname).toContain("checkout.stripe.com");
});
