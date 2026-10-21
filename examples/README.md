# Examples

The examples are Tako's acceptance suite, written in both **Go** and **cfire** (`.cf`) and graduated in order:

| Stage | Go Example | cfire Example | Features Exercised |
| :--- | :--- | :--- | :--- |
| **1 · hello** | [`examples/hello`](hello/hello.go) | [`examples/cfire-hello`](cfire-hello/hello.cf) | `API`, path/query/JSON request binding, typed errors, `tako dev` hot-reload |
| **2 · shortlink** | [`examples/shortlink`](shortlink/app.go) | [`examples/cfire-shortlink`](cfire-shortlink/shortlink.cf) | `Entity`, transactions (`Tx`), `Queue` + `Worker`, `Cache`, `Cron` |
| **3 · gallery** | [`examples/gallery`](gallery/app.go) | [`examples/cfire-gallery`](cfire-gallery/gallery.cf) | `Bucket`, signed URLs, `Topic` + `Subscribe`, `Lock`, `Secret`, `Counter` |
| **4 · journal** | [`examples/journal`](journal/app.go) | [`examples/cfire-journal`](cfire-journal/journal.cf) | `Site`, `Page`, `Auth` (Google), `Role` / `Policy`, `StatusPage`, HTMX fragments |
| **5 · shop** | [`examples/shop`](shop/app.go) | [`examples/cfire-shop`](cfire-shop/shop.cf) | Multi-service RPC, `tako gen client ts`, `Job` + `Runner`, `Singleton` |
| **6 · blog** | [`examples/blog`](blog/app.go) | [`examples/cfire-blog`](cfire-blog/blog.cf) | Hugo static site build (`SiteConfig.Build`), dynamic HTMX comment islands |
| **7 · siren** | [`examples/siren`](siren/app.go) | [`examples/cfire-siren`](cfire-siren/siren.cf) | `Counter`, `Histogram`, `Alert` conditions, failure switches, notification channels |
