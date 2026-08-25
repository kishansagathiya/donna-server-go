---
name: booking-proposal
description: How Donna researches travel or purchases and asks for approval with a structured itinerary card — never invents prices, never stores card numbers, never books without Confirm
---

# Booking and purchase procedure

Donna cannot charge a card or complete a vendor checkout. Live flight-search
partners are optional; until one is configured, `search_flights` will tell you
to research with `fetch_url` / `browse_page`.

## Research

1. `memory_search` for airline, seat, home airport, budget, and traveler prefs.
2. Call `search_flights` if the goal is air travel. If it says unconfigured,
   research publicly with `fetch_url` then `browse_page` on JS-heavy sites.
3. Cross-check price, dates, and airline across sources. If they disagree, say so.
4. Never invent availability, confirmation numbers, or a completed booking.

## Approval card

When you have a concrete option the user should accept or reject, call
`request_approval` and stop. Set:

- `kind`: `book_flight`, `book_hotel`, `pay`, or a similarly specific slug
- `summary`: one sentence of what they would approve
- `details` object with as many of these as you have (omit unknowns):
  - `itinerary` (route + times, e.g. `SFO→LIS 10:15–07:40+1`)
  - `total` (numeric) and `currency` (e.g. `USD`)
  - `airline` / `vendor`
  - `dates`
  - `passengers` or `quantity`
  - `cabin` / `seat`
  - `source_url`
  - `notes` (constraints, not payment data)

Never put card numbers, CVV, passwords, or checkout tokens in `details`,
memory, or skills.

## After they approve

1. `propose_calendar_event` for the trip or appointment (title, when, location).
   They confirm that separately in Actions — do not assume Google write succeeded.
2. `write_memory_fact` for durable prefs you learned (home airport, airline,
   aisle/window, budget band). Skip one-off fares and anything payment-related.
3. `save_skill` only if the *procedure* is reusable (how this user books), not
   the specific PNR.

If they deny, ask what to change with `ask_user` (options when possible) and
research again. Do not book.
