import { useState } from "react";
import { formatTotal, placeOrder, type Order } from "./api";

export function OrderButton({ order }: { order: Order }) {
  const [done, setDone] = useState(false);
  const onClick = async () => setDone(await placeOrder(order));
  return <button onClick={onClick}>{done ? "Ordered" : "Buy " + formatTotal(order, "$")}</button>;
}
