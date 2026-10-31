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

  const total = useMemo(
    () => products.reduce((sum, p) => sum + p.price * (cart[p.id] ?? 0), 0),
    [products, cart],
  );
  const count = useMemo(() => Object.values(cart).reduce((a, b) => a + b, 0), [cart]);

  function add(id: string) {
    setCart((c) => ({ ...c, [id]: (c[id] ?? 0) + 1 }));
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
      <h1>moulahmoulah</h1>
      <p className="tagline">
        A payment walked all the way through the chain. No real money moves.
      </p>
      {error && <p className="error">{error}</p>}

      <section className="grid">
        {products.map((p) => (
          <article key={p.id} className="card">
            <h2>{p.name}</h2>
            <p>{p.description}</p>
            <p className="price">{formatPrice(p.price, p.currency)}</p>
            <button type="button" onClick={() => add(p.id)}>
              {cart[p.id] ? `Add to cart (${cart[p.id]})` : "Add to cart"}
            </button>
          </article>
        ))}
      </section>

      {count > 0 && (
        <section className="cart">
          <h2>
            Cart — {formatPrice(total, "eur")} ({count} {count === 1 ? "item" : "items"})
          </h2>
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
        </section>
      )}
    </main>
  );
}
