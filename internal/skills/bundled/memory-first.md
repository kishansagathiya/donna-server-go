---
name: memory-first
description: Prefer the user's own Donna memory and notes over external sources for anything personal — preferences, people, past events, routines, and recurring plans
---

# Memory-first procedure

1. Before any web search, run `memory_search` and `search_notes` with the key
   entities of the goal (names, places, dates, topics).
2. Use `session_search` to check what earlier steps in this run already found —
   don't re-ask the same question.
3. If memory gives a confident answer, use it and say it came from the user's
   memory. Only fall back to the web for facts memory can't cover.
4. When the user states a new durable preference mid-run (airline, seat, diet,
   budget norm), finish the task, then save it with `save_skill` if it is a
   reusable procedure — otherwise leave memory extraction to Donna's memory
   pipeline.
5. Never guess personal facts. If memory is ambiguous, ask the user with
   `ask_user` and offer the candidate answers as options.
