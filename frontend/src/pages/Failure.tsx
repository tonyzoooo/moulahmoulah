import { Link, useParams } from "react-router-dom";

export default function Failure() {
  const { orderId } = useParams();

  return (
    <main>
      <h1>Payment not completed</h1>
      <div className="card">
        <p>
          Order <code>{orderId}</code> was cancelled or failed. No money was moved.
        </p>
      </div>
      <p>
        <Link to="/">Back to the shop</Link>
      </p>
    </main>
  );
}
