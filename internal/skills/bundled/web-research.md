---
name: web-research
description: How Donna researches open-ended questions on the web — search broadly, fetch sources, cross-check claims, and cite where each fact came from
---

# Web research procedure

1. Restate the question and identify what specific facts are needed.
2. Check memory first (`memory_search`, `search_notes`) when the answer may be
   personal (preferences, past events, people). Don't search the web for things
   the user already told Donna.
3. Fetch 2–3 independent sources with `fetch_url` (or `browse_page` when the
   page needs JavaScript). Prefer primary sources over aggregators.
4. Cross-check any number, date, price, or name across at least two sources
   before presenting it. If sources disagree, say so explicitly.
5. Summarize with the answer first, then supporting detail, and name the source
   for each key fact.
6. Never present a fact you could not ground in a tool result.

# When to stop

Stop researching when additional sources stop changing the answer. Deliver the
summary with citations rather than chasing exhaustive coverage.
