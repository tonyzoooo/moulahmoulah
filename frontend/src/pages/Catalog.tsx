import { useEffect, useMemo, useState } from "react";
import { checkout, formatPrice, getCatalog, type Product } from "../api";

// Registered processors on the backend. Stripe only, for now.
const PROCESSORS = [{ id: "stripe", label: "Stripe (test mode)" }];

export default function Catalog() {
  const [products, setProducts] = useState<Product[]>([]);
  const [cart, setCart] = useState<Record<string, number>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getCatalog()
      .then(setProducts)
      .catch((e: Error) => setError(e.message));
  }, []);

  const byId = useMemo(() => new Map(products.map((p) => [p.id, p])), [products]);

  const lines = useMemo(
    () =>
      Object.entries(cart)
        .filter(([, qty]) => qty > 0)
        .map(([id, qty]) => ({ product: byId.get(id), qty }))
        .filter((l): l is { product: Product; qty: number } => l.product !== undefined),
    [cart, byId],
  );

  const total = useMemo(() => lines.reduce((sum, l) => sum + l.product.price * l.qty, 0), [lines]);
  const count = useMemo(() => lines.reduce((sum, l) => sum + l.qty, 0), [lines]);

  function setQty(id: string, qty: number) {
    setCart((c) => {
      const next = { ...c, [id]: qty };
      if (qty <= 0) delete next[id]; // remove from cart at zero
      return next;
    });
  }

  function add(id: string) {
    setCart((c) => ({ ...c, [id]: (c[id] ?? 0) + 1 }));
  }

  function remove(id: string) {
    setCart((c) => {
      const next = { ...c };
      delete next[id];
      return next;
    });
  }

  async function pay(processorId: string) {
    setBusy(true);
    setError(null);
    try {
      const res = await checkout(processorId, cart);
      window.location.href = res.url; // redirect to the hosted payment page
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  }

  return (
    <main>
      <header className="shop-header">
        <div>
          <h1>moulahmoulah</h1>
          <p className="tagline">
            A payment walked all the way through the chain. No real money moves.
          </p>
        </div>
        <div className="cart-badge" aria-live="polite">
          Basket: {count} {count === 1 ? "item" : "items"}
        </div>
      </header>
      {error && <p className="error">{error}</p>}

      <div className="shop-layout">
        <section className="grid">
          {products.map((p) => (
            <article key={p.id} className="card">
              {p.imageUrl && <img src={p.imageUrl} alt={p.name} className="thumb" loading="lazy" />}
              <h2>{p.name}</h2>
              <p>{p.description}</p>
              <p className="price">{formatPrice(p.price, p.currency)}</p>
              <button type="button" onClick={() => add(p.id)}>
                {cart[p.id] ? `Add to basket (${cart[p.id]})` : "Add to basket"}
              </button>
            </article>
          ))}
        </section>

        <aside className="cart">
          <h2>Basket</h2>
          {lines.length === 0 && <p className="muted">Your basket is empty.</p>}
          <ul className="cart-lines">
            {lines.map(({ product, qty }) => (
              <li key={product.id} className="cart-line">
                {product.imageUrl && (
                  <img src={product.imageUrl} alt={product.name} className="cart-thumb" />
                )}
                <div className="cart-line-info">
                  <strong>{product.name}</strong>
                  <span className="muted">{formatPrice(product.price, product.currency)} each</span>
                </div>
                <div className="qty">
                  <button
                    type="button"
                    aria-label={`Decrease ${product.name}`}
                    onClick={() => setQty(product.id, qty - 1)}
                  >
                    −
                  </button>
                  <span>{qty}</span>
                  <button
                    type="button"
                    aria-label={`Increase ${product.name}`}
                    onClick={() => setQty(product.id, qty + 1)}
                  >
                    +
                  </button>
                </div>
                <span className="cart-line-total">
                  {formatPrice(product.price * qty, product.currency)}
                </span>
                <button
                  type="button"
                  className="remove"
                  aria-label={`Remove ${product.name}`}
                  onClick={() => remove(product.id)}
                >
                  ×
                </button>
              </li>
            ))}
          </ul>
          {lines.length > 0 && (
            <>
              <p className="cart-total">
                Total: <strong>{formatPrice(total, "eur")}</strong> ({count}{" "}
                {count === 1 ? "item" : "items"})
              </p>
              {PROCESSORS.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  className="pay"
                  disabled={busy}
                  onClick={() => pay(p.id)}
                >
                  Pay with {p.label}
                </button>
              ))}
            </>
          )}
        </aside>
      </div>
    </main>
  );
}
