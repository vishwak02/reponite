export interface Order {
  id: string;
  total: number;
}

export async function placeOrder(o: Order): Promise<boolean> {
  const r = await fetch("/api/orders", { method: "POST", body: JSON.stringify(o) });
  return r.ok;
}

export function formatTotal(o: Order, currency: string): string {
  return currency + (o.total / 100).toFixed(2);
}
