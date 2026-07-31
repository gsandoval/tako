---
title: Static where static wins
date: 2026-07-25
---

A blog is 99% words that never change after publish. Rendering those
per-request buys nothing; generating them buys caching, speed, and a
build you can inspect. The 1% that IS dynamic — comments — doesn't
need a framework takeover, just one island per page.
