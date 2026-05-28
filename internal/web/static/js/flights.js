// Fixed display metadata keyed by backend flight ID.
// Times, airports and prices are demo data only; the backend flight ID is used for booking.
const FLIGHT_DISPLAY = {
  "NA4821": { from: "TLV", to: "PRG", dep: "7:10 AM",  arr: "10:15 AM", dur: "4h 05m", price: 361 },
  "NA1954": { from: "JFK", to: "MIA", dep: "9:25 AM",  arr: "12:05 PM", dur: "2h 40m", price: 289 },
  "NA7308": { from: "LAX", to: "SEA", dep: "11:00 AM", arr: "2:15 PM",  dur: "3h 15m", price: 344 },
  "NA2647": { from: "AMS", to: "CDG", dep: "1:40 PM",  arr: "3:35 PM",  dur: "1h 55m", price: 241 },
  "NA9182": { from: "MAD", to: "FCO", dep: "3:20 PM",  arr: "5:40 PM",  dur: "2h 20m", price: 312 },
  "NA5539": { from: "DXB", to: "DEL", dep: "5:55 PM",  arr: "9:25 PM",  dur: "3h 30m", price: 427 },
  "NA8420": { from: "SFO", to: "ORD", dep: "7:05 PM",  arr: "11:15 PM", dur: "4h 10m", price: 398 },
  "NA3176": { from: "SIN", to: "BKK", dep: "8:30 PM",  arr: "11:15 PM", dur: "2h 45m", price: 266 },
  "NA6015": { from: "LHR", to: "DUB", dep: "10:00 AM", arr: "11:10 AM", dur: "1h 10m", price: 187 },
  "NA0743": { from: "CDG", to: "BRU", dep: "12:30 PM", arr: "1:45 PM",  dur: "1h 15m", price: 143 },
};

(async function initFlightsPage() {
  const loadingEl = document.getElementById("loading");
  const errorEl = document.getElementById("error");
  const flightsEl = document.getElementById("flights");
  const activeOrderEl = document.getElementById("active-order-banner");

  try {
    const activeOrderID = getStoredOrderID();
    if (activeOrderID) {
      try {
        const order = await fetchJSON(`/orders/${encodeURIComponent(activeOrderID)}`);
        if (!isTerminalStatus(order.status)) {
          activeOrderEl.innerHTML = `
            Active booking in progress (Flight ${escapeHTML(order.flight_id)}, ${escapeHTML(order.status)}).
            <a href="/seats?flight_id=${encodeURIComponent(order.flight_id)}&order_id=${encodeURIComponent(order.order_id)}">Continue booking</a>
          `;
          activeOrderEl.classList.remove("hidden");
        } else {
          setStoredOrderID(null);
        }
      } catch {
        setStoredOrderID(null);
      }
    }

    const data = await fetchJSON("/flights");
    loadingEl.classList.add("hidden");

    if (!data.flights || data.flights.length === 0) {
      showError(errorEl, "No flights available.");
      return;
    }

    flightsEl.innerHTML = data.flights
      .map((f) => {
        const meta = FLIGHT_DISPLAY[f.id];
        if (meta) {
          return `
            <article class="flight-card" data-flight-id="${escapeHTML(f.id)}"
              aria-label="NEON Airlines flight ${escapeHTML(meta.from)} to ${escapeHTML(meta.to)}, $${meta.price}">
              <div class="flight-card-brand">
                <div class="flight-airline">NEON Airlines</div>
                <div class="flight-id-label">Flight ${escapeHTML(f.id)}</div>
              </div>
              <div class="flight-route">
                <div class="flight-leg flight-leg--dep">
                  <div class="flight-time">${escapeHTML(meta.dep)}</div>
                  <div class="flight-airport">${escapeHTML(meta.from)}</div>
                </div>
                <div class="flight-route-mid">
                  <div class="flight-duration">${escapeHTML(meta.dur)}</div>
                  <div class="flight-direct">Direct ✈</div>
                </div>
                <div class="flight-leg flight-leg--arr">
                  <div class="flight-time">${escapeHTML(meta.arr)}</div>
                  <div class="flight-airport">${escapeHTML(meta.to)}</div>
                </div>
              </div>
              <div class="flight-price-col">
                <div class="flight-price">$${meta.price}</div>
                <button type="button" class="select-btn">Select →</button>
              </div>
            </article>`;
        }
        // Fallback for any flight not in the display map
        return `
          <article class="flight-card" data-flight-id="${escapeHTML(f.id)}"
            aria-label="Flight ${escapeHTML(f.id)}">
            <div class="flight-card-brand">
              <div class="flight-airline">NEON Airlines</div>
              <div class="flight-id-label">Flight ${escapeHTML(f.id)}</div>
            </div>
            <div class="flight-route">
              <div class="flight-leg flight-leg--dep">
                <div class="flight-time">${formatDateTime(f.departure_at)}</div>
              </div>
              <div class="flight-route-mid"></div>
              <div class="flight-leg flight-leg--arr"></div>
            </div>
            <div class="flight-price-col">
              <button type="button" class="select-btn">Select →</button>
            </div>
          </article>`;
      })
      .join("");

    flightsEl.querySelectorAll(".flight-card").forEach((card) => {
      card.addEventListener("click", (e) => {
        if (e.target.closest(".select-btn") || e.target === card) {
          startBooking(card.dataset.flightId);
        }
      });
      card.querySelector(".select-btn").addEventListener("click", (e) => {
        e.stopPropagation();
        startBooking(card.dataset.flightId);
      });
    });

    flightsEl.classList.remove("hidden");
  } catch (err) {
    loadingEl.classList.add("hidden");
    showError(errorEl, `Could not load flights: ${err.message}`);
  }

  async function startBooking(flightID) {
    hideError(errorEl);
    const existing = getStoredOrderID();
    if (existing) {
      try {
        const order = await fetchJSON(`/orders/${encodeURIComponent(existing)}`);
        if (!isTerminalStatus(order.status)) {
          showError(
            errorEl,
            `You already have an active booking on flight ${order.flight_id}. Continue or cancel it first.`
          );
          return;
        }
        setStoredOrderID(null);
      } catch {
        setStoredOrderID(null);
      }
    }

    try {
      const order = await postJSON("/orders", { flight_id: flightID });
      setStoredOrderID(order.order_id);
      window.location.href = `/seats?flight_id=${encodeURIComponent(flightID)}&order_id=${encodeURIComponent(order.order_id)}`;
    } catch (err) {
      showError(errorEl, `Could not start booking: ${err.message}`);
    }
  }
})();
