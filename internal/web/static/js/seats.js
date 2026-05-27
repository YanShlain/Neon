(function initSeatsPage() {
  const params = new URLSearchParams(window.location.search);
  const flightID = params.get("flight_id");
  let orderID = params.get("order_id") || getStoredOrderID();

  const flightIdEl = document.getElementById("flight-id");
  const departedBanner = document.getElementById("departed-banner");
  const orderPanel = document.getElementById("order-panel");
  const orderIdEl = document.getElementById("order-id");
  const orderStatusEl = document.getElementById("order-status");
  const timerDisplay = document.getElementById("timer-display");
  const selectionSummary = document.getElementById("selection-summary");
  const loadingEl = document.getElementById("loading");
  const errorEl = document.getElementById("error");
  const seatMapEl = document.getElementById("seat-map");
  const gridEl = document.getElementById("seat-grid");
  const refreshBtn = document.getElementById("refresh-btn");
  const cancelBtn = document.getElementById("cancel-btn");
  const payBtn = document.getElementById("pay-btn");

  let selectedSeats = new Set();
  let timerSeconds = 0;
  let timerHandle = null;
  let latestSeats = [];
  let syncInFlight = false;
  let orderStatus = "";

  if (!flightID) {
    loadingEl.classList.add("hidden");
    showError(errorEl, "Missing flight_id. Go back and select a flight.");
    return;
  }

  flightIdEl.textContent = flightID;
  refreshBtn.addEventListener("click", () => refreshAll());
  cancelBtn.addEventListener("click", () => cancelOrder());
  payBtn.addEventListener("click", () => goToPayment());

  bootstrap();

  async function bootstrap() {
    if (!orderID) {
      loadingEl.classList.add("hidden");
      showError(errorEl, "No active order. Go back and select a flight.");
      return;
    }
    setStoredOrderID(orderID);
    orderPanel.classList.remove("hidden");
    orderIdEl.textContent = orderID.slice(0, 8) + "…";
    await refreshAll();
  }

  async function refreshAll() {
    hideError(errorEl);
    await loadFlightMeta(flightID);
    await loadOrder();
    await loadSeatMap(flightID);
  }

  async function loadFlightMeta(id) {
    try {
      const data = await fetchJSON("/flights");
      const flight = (data.flights || []).find((f) => f.id === id);
      if (!flight) {
        return;
      }
      if (isDeparted(flight.departure_at)) {
        departedBanner.classList.remove("hidden");
      } else {
        departedBanner.classList.add("hidden");
      }
    } catch {
      // optional metadata
    }
  }

  async function loadOrder() {
    const order = await fetchJSON(`/orders/${encodeURIComponent(orderID)}`);
    orderStatus = order.status;
    orderStatusEl.textContent = order.status;
    selectedSeats = new Set(order.held_seat_ids || []);
    timerSeconds = order.timer_remaining_seconds || 0;
    startTimer();
    updateSelectionSummary();
    updatePayButton(order);

    if (isTerminalStatus(order.status)) {
      setStoredOrderID(null);
      if (order.status === "CANCELLED" || order.status === "EXPIRED" || order.status === "PAYMENT_FAILED") {
        showError(errorEl, terminalSeatsMessage(order.status));
      }
    }
  }

  function terminalSeatsMessage(status) {
    if (status === "PAYMENT_FAILED") {
      return "Payment failed. Start a new booking from the flight list.";
    }
    if (status === "EXPIRED") {
      return "Order expired. Start a new booking from the flight list.";
    }
    return `Order ${status.toLowerCase()}. Start a new booking from the flight list.`;
  }

  function updatePayButton(order) {
    const canPay =
      order.status === "SEATS_HELD" &&
      (order.held_seat_ids || []).length > 0 &&
      !syncInFlight;
    payBtn.disabled = !canPay;
  }

  async function goToPayment() {
    hideError(errorEl);
    payBtn.disabled = true;
    try {
      const order = await syncSeatsToServer();
      if (order.status !== "SEATS_HELD" || !(order.held_seat_ids || []).length) {
        showError(errorEl, "Hold at least one seat before proceeding to payment.");
        return;
      }
      window.location.href = `/payment?flight_id=${encodeURIComponent(flightID)}&order_id=${encodeURIComponent(orderID)}`;
    } catch (err) {
      showError(errorEl, err.message);
    } finally {
      updatePayButton({ status: orderStatus, held_seat_ids: [...selectedSeats] });
    }
  }

  async function loadSeatMap(id) {
    loadingEl.classList.remove("hidden");
    seatMapEl.classList.add("hidden");

    try {
      const query = orderID ? `?order_id=${encodeURIComponent(orderID)}` : "";
      const data = await fetchJSON(`/flights/${encodeURIComponent(id)}/seats${query}`);
      latestSeats = data.seats || [];
      loadingEl.classList.add("hidden");
      renderSeatGrid(gridEl, latestSeats, selectedSeats, toggleSeat, syncInFlight);
      seatMapEl.classList.remove("hidden");
    } catch (err) {
      loadingEl.classList.add("hidden");
      showError(errorEl, `Could not load seat map: ${err.message}`);
    }
  }

  function toggleSeat(seatID) {
    if (orderStatus === "AWAITING_PAYMENT") {
      return;
    }
    if (selectedSeats.has(seatID)) {
      selectedSeats.delete(seatID);
    } else {
      selectedSeats.add(seatID);
    }
    renderSeatGrid(gridEl, latestSeats, selectedSeats, toggleSeat, false);
    updateSelectionSummary();
    updatePayButton({ status: orderStatus, held_seat_ids: [...selectedSeats] });
  }

  function updateSelectionSummary() {
    if (selectedSeats.size === 0) {
      selectionSummary.textContent = "Click seats to hold them, then proceed to payment.";
      return;
    }
    selectionSummary.textContent = `Held: ${[...selectedSeats].sort().join(", ")}`;
  }

  async function syncSeatsToServer() {
    if (syncInFlight) {
      return { status: orderStatus, held_seat_ids: [...selectedSeats] };
    }
    syncInFlight = true;
    payBtn.disabled = true;
    try {
      const payloadSeatIds = [...selectedSeats].sort();
      const order = await patchJSON(`/orders/${encodeURIComponent(orderID)}/seats`, {
        seat_ids: payloadSeatIds,
      });
      orderStatus = order.status;
      selectedSeats = new Set(order.held_seat_ids || []);
      orderStatusEl.textContent = order.status;
      timerSeconds = order.timer_remaining_seconds || 0;
      startTimer();
      return order;
    } finally {
      syncInFlight = false;
      updatePayButton({ status: orderStatus, held_seat_ids: [...selectedSeats] });
    }
  }

  async function cancelOrder() {
    hideError(errorEl);
    cancelBtn.disabled = true;
    try {
      const order = await postJSON(`/orders/${encodeURIComponent(orderID)}/cancel`, {});
      orderStatus = order.status;
      orderStatusEl.textContent = order.status;
      timerSeconds = 0;
      startTimer();
      setStoredOrderID(null);
      selectedSeats.clear();
      await loadSeatMap(flightID);
      showError(errorEl, "Order cancelled. You can start a new booking from the flight list.");
      payBtn.disabled = true;
    } catch (err) {
      showError(errorEl, err.message);
    } finally {
      cancelBtn.disabled = false;
    }
  }

  function startTimer() {
    if (timerHandle) {
      clearInterval(timerHandle);
    }
    timerDisplay.textContent = formatTimer(timerSeconds);
    if (timerSeconds <= 0) {
      return;
    }
    timerHandle = setInterval(() => {
      timerSeconds -= 1;
      timerDisplay.textContent = formatTimer(timerSeconds);
      if (timerSeconds <= 0) {
        clearInterval(timerHandle);
      }
    }, 1000);
  }
})();

function parseSeatID(seatID) {
  const match = /^(\d+)([A-Z]+)$/.exec(seatID);
  if (!match) {
    return null;
  }
  return { row: parseInt(match[1], 10), col: match[2] };
}

function renderSeatGrid(container, seats, selectedSeats, onToggle, syncing) {
  const parsed = seats.map((s) => ({ ...s, pos: parseSeatID(s.seat_id) })).filter((s) => s.pos);

  if (parsed.length === 0) {
    container.innerHTML = "<p>No seats to display.</p>";
    return;
  }

  const rows = [...new Set(parsed.map((s) => s.pos.row))].sort((a, b) => a - b);
  const cols = [...new Set(parsed.map((s) => s.pos.col))].sort();
  const seatByKey = new Map(parsed.map((s) => [`${s.pos.row}${s.pos.col}`, s]));

  const grid = document.createElement("div");
  grid.className = "seat-grid";
  grid.style.gridTemplateColumns = `2rem repeat(${cols.length}, 2rem)`;

  grid.appendChild(cell("corner", ""));

  cols.forEach((col) => {
    grid.appendChild(cell("col-label", col));
  });

  rows.forEach((row) => {
    grid.appendChild(cell("row-label", String(row)));
    cols.forEach((col) => {
      const seat = seatByKey.get(`${row}${col}`);
      if (seat) {
        grid.appendChild(seatCell(seat, selectedSeats, onToggle, syncing));
      } else {
        grid.appendChild(cell("corner", ""));
      }
    });
  });

  container.innerHTML = "";
  container.appendChild(grid);
}

function cell(className, text) {
  const el = document.createElement("div");
  el.className = className;
  el.textContent = text;
  return el;
}

function seatCell(seat, selectedSeats, onToggle, syncing) {
  const el = document.createElement("button");
  el.type = "button";
  const status = (seat.status || "AVAILABLE").toLowerCase();
  const isSelected = selectedSeats.has(seat.seat_id);
  const isMine = seat.is_mine || isSelected;

  el.className = "seat-cell";
  if (status === "booked" || (status === "held" && !isMine)) {
    el.classList.add(status);
    el.disabled = true;
  } else if (isMine || isSelected) {
    el.classList.add("mine");
    el.disabled = syncing;
    if (!syncing) {
      el.addEventListener("click", () => onToggle(seat.seat_id));
    }
  } else {
    el.classList.add("available");
    el.disabled = syncing;
    if (!syncing) {
      el.addEventListener("click", () => onToggle(seat.seat_id));
    }
  }

  el.title = `${seat.seat_id} — ${isSelected ? "selected" : seat.status}`;
  el.setAttribute("aria-label", `${seat.seat_id} ${seat.status}`);
  el.textContent = seat.seat_id.replace(/^\d+/, "");
  return el;
}
