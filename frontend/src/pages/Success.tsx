import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { formatPrice, getOrder, type Order, type OrderStatus } from "../api";

const TERMINAL: OrderStatus[] = ["CAPTURED", "DECLINED", "FAILED"];

export default function Success() {
  const { orderId } = useParams();
  const [order, setOrder] = useState<Order | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!orderId) return;
    let cancelled = false;

    const poll = async () => {
      try {
        const o = await getOrder(orderId);
        if (cancelled) return;
        setOrder(o);
        if (!TERMINAL.includes(o.status)) {
          setTimeout(poll, 1500);
        }
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      }
    };
    poll();

    return () => {
      cancelled = true;
    };
  }, [orderId]);

  return (
    <main>
      <h1>{order?.status === "CAPTURED" ? "Payment captured" : "Payment in progress"}</h1>
      {error && <p className="error">{error}</p>}
      {order && (
        <div className="card">
          <p>
            Order <code>{order.id}</code>
          </p>
          <p>
            {formatPrice(order.amount, order.currency)} via {order.processorId}
          </p>
          <p>
            Status: <strong>{order.status}</strong>
          </p>
        </div>
      )}
      <p>
        <Link to="/">Back to the shop</Link>
      </p>
    </main>
  );
}
