import { Route, Routes } from "react-router-dom";
import Catalog from "./pages/Catalog";
import Failure from "./pages/Failure";
import Success from "./pages/Success";

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Catalog />} />
      <Route path="/success/:orderId" element={<Success />} />
      <Route path="/failure/:orderId" element={<Failure />} />
    </Routes>
  );
}
