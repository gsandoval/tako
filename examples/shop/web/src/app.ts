// shop's frontend: the whole backend conversation goes through the
// GENERATED client (web/src/api.ts) — typed methods, typed errors,
// session cookie riding along. Regenerate the client with
//   tako gen client ts --out web/src/api.ts
import { createClient, ApiError, Product, Order } from "./api";

const api = createClient();

const $ = (id: string) => document.getElementById(id)!;

function status(msg: string) {
  $("status").textContent = msg;
}

function money(cents: number): string {
  return "$" + (cents / 100).toFixed(2);
}

async function refreshOrders() {
  let orders: Order[];
  try {
    orders = await api.listOrders();
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return; // signed out: the hint text stands
    throw e;
  }
  const el = $("orders");
  el.textContent = orders.length ? "" : "no orders yet.";
  for (const o of orders) {
    const row = document.createElement("div");
    row.textContent = `${o.qty} × ${o.productId} — ${money(o.totalCents)} (${new Date(o.created).toLocaleString()})`;
    el.append(row);
  }
}

async function buy(p: Product) {
  try {
    const order = await api.placeOrder({ productId: p.id, qty: 1 });
    status(`ordered ${p.name} — ${money(order.totalCents)}`);
    await refreshOrders();
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      location.assign("/api/_session/signin?next=/");
      return;
    }
    status(e instanceof ApiError ? e.message : String(e));
  }
}

// renderSession makes the session VISIBLE: who is signed in, and a
// working sign-out — state the page reflects instead of assuming.
async function renderSession() {
  const who = await api.me();
  const signin = $("signin") as HTMLAnchorElement;
  const signout = $("signout") as HTMLButtonElement;
  if (who.signedIn) {
    signin.style.display = "none";
    $("who").textContent = `signed in as ${who.name} ·`;
    signout.style.display = "inline";
    // A fresh database is an EMPTY shop (deleting .tako/data is the
    // local reset story): any signed-in user stocks the shelf — v0
    // has no admin concept, and this form is addProduct through the
    // generated client.
    const stock = $("stock") as HTMLFormElement;
    stock.style.display = "block";
    stock.onsubmit = async (e) => {
      e.preventDefault();
      const name = ($("p-name") as HTMLInputElement).value;
      const priceCents = parseInt(($("p-price") as HTMLInputElement).value, 10);
      try {
        await api.addProduct({ name, priceCents });
        stock.reset();
        await renderProducts();
      } catch (err) {
        status(err instanceof ApiError ? err.message : String(err));
      }
    };
    signout.onclick = async () => {
      await fetch("/api/_session", { method: "DELETE", credentials: "same-origin" });
      location.reload();
    };
  }
}

async function renderProducts() {
  const products = await api.listProducts();
  const el = $("products");
  el.textContent = products.length ? "" : "the shelf is empty — sign in and stock it.";
  for (const p of products) {
    const row = document.createElement("div");
    row.className = "product";
    const label = document.createElement("span");
    label.textContent = `${p.name} — ${money(p.priceCents)}`;
    const btn = document.createElement("button");
    btn.textContent = "buy";
    btn.onclick = () => void buy(p);
    row.append(label, btn);
    el.append(row);
  }
}

async function main() {
  await renderSession().catch(() => {});
  await renderProducts();
  await refreshOrders();
}

void main().catch((e) => status(String(e)));
