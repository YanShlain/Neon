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
      .map(
        (f) => `
        <button type="button" class="flight-card" data-flight-id="${escapeHTML(f.id)}">
          <h2 class="flight-card-id">Flight ${escapeHTML(f.id)}</h2>
          <p class="flight-card-meta">Departs ${formatDateTime(f.departure_at)}</p>
          <p class="flight-card-meta">${f.capacity} seats</p>
        </button>`
      )
      .join("");

    flightsEl.querySelectorAll(".flight-card").forEach((card) => {
      card.addEventListener("click", () => startBooking(card.dataset.flightId));
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
