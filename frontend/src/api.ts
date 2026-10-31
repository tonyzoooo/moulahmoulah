export interface Product {
  id: string; // Open Food Facts barcode
  name: string;
  description: string;
  /** Price in minor units (cents for EUR). */
  price: number;
  currency: string;
  imageUrl?: string;
}

export type OrderStatus = "CREATED" | "AUTHORIZED" | "CAPTURED" | "DECLINED" | "FAILED";

export interface Order {
  id: string;
  items: Product[];
  amount: number;
  currency: string;
  processorId: string;
  providerRef?: string;
  status: OrderStatus;
  createdAt: string;
}

export interface CheckoutResponse {
  orderId: string;
  url: string;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init);
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    throw new Error(body?.error ?? `request failed with HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

export function getCatalog(): Promise<Product[]> {
  return request<Product[]>("/api/catalog");
}

export function checkout(
  processorId: string,
  items: Record<string, number>,
): Promise<CheckoutResponse> {
  return request<CheckoutResponse>("/api/checkout", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ processorId, items }),
  });
}

export function getOrder(id: string): Promise<Order> {
  return request<Order>(`/api/orders/${id}`);
}

export function captureOrder(id: string): Promise<Order> {
  return request<Order>(`/api/orders/${id}/capture`, { method: "POST" });
}

export function formatPrice(minorUnits: number, currency: string): string {
  const major = (minorUnits / 100).toFixed(2);
  return currency.toLowerCase() === "eur" ? `€${major}` : `${major} ${currency.toUpperCase()}`;
}
