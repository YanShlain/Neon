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
  let pollHandle = null;

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
  window.addEventListener("beforeunload", stopLiveUpdates);

  async function bootstrap() {
    if (!orderID) {
      loadingEl.classList.add("hidden");
      showError(errorEl, "No active order. Go back and select a flight.");
      return;
    }
    setStoredOrderID(orderID);
    orderPanel.classList.remove("hidden");
    orderIdEl.textContent = formatOrderDisplayID(orderID);
    await refreshAll();
    startLiveUpdates();
  }

  async function refreshAll() {
    hideError(errorEl);
    await loadFlightMeta(flightID);
    await loadOrder({ resetTimer: true });
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

  async function loadOrder(options = {}) {
    const order = await fetchJSON(`/orders/${encodeURIComponent(orderID)}`);
    const mapKeyBefore = seatMapKey();
    applyOrderState(order, options);
    return mapKeyBefore !== seatMapKey();
  }

  function seatMapKey() {
    return orderSeatsSignature(orderStatus, [...selectedSeats]);
  }

  function applyOrderState(order, { resetTimer = false } = {}) {
    const signatureBefore = seatMapKey();
    orderStatus = order.status;
    orderStatusEl.textContent = order.status;
    selectedSeats = new Set(order.held_seat_ids || []);
    const signatureAfter = seatMapKey();
    const showTimer = shouldShowHoldTimer(order);
    const serverTimer = effectiveTimerSeconds(order);
    const forceTimer =
      resetTimer || signatureBefore !== signatureAfter || isTerminalStatus(order.status);

    if (isTerminalStatus(order.status) || !showTimer) {
      timerSeconds = 0;
      stopTimer();
      updateTimerDisplay(timerDisplay, 0, false);
    } else {
      timerSeconds = reconcileTimerSeconds(timerSeconds, serverTimer, { force: forceTimer });
      if (forceTimer) {
        restartTimer(showTimer);
      } else {
        updateTimerDisplay(timerDisplay, timerSeconds, showTimer);
        startTimer(showTimer);
      }
    }

    updateSelectionSummary();
    updatePayButton(order);

    if (isTerminalStatus(order.status)) {
      setStoredOrderID(null);
      if (order.status === "CANCELLED" || order.status === "EXPIRED" || order.status === "PAYMENT_FAILED") {
        showError(errorEl, terminalSeatsMessage(order.status));
      }
    }
  }

  function startLiveUpdates() {
    stopLiveUpdates();
    startOrderPolling();
  }

  function startOrderPolling() {
    if (pollHandle) {
      return;
    }
    pollHandle = setInterval(async () => {
      try {
        const seatMapChanged = await loadOrder();
        if (seatMapChanged) {
          await loadSeatMap(flightID, { silent: true });
        }
      } catch {
        // best effort polling
      }
    }, ORDER_POLL_INTERVAL_MS);
  }

  function stopLiveUpdates() {
    if (pollHandle) {
      clearInterval(pollHandle);
      pollHandle = null;
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
    const payableStatus = order.status === "SEATS_HELD" || order.status === "CREATED";
    const canPay =
      payableStatus &&
      (order.held_seat_ids || []).length > 0 &&
      !syncInFlight;
    payBtn.disabled = !canPay;
  }

  async function goToPayment() {
    hideError(errorEl);
    payBtn.disabled = true;
    try {
      let order;
      if (orderStatus === "SEATS_HELD" && selectedSeats.size > 0) {
        order = await fetchJSON(`/orders/${encodeURIComponent(orderID)}`);
        if (!heldSeatsMatch(order.held_seat_ids, selectedSeats)) {
          order = await syncSeatsToServer();
        }
      } else {
        order = await syncSeatsToServer();
      }
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

  function heldSeatsMatch(serverSeatIDs, localSeats) {
    const server = [...(serverSeatIDs || [])].sort().join(",");
    const local = [...localSeats].sort().join(",");
    return server === local;
  }

  async function loadSeatMap(id, { silent = false } = {}) {
    const mapWasVisible = !seatMapEl.classList.contains("hidden");
    if (!silent) {
      loadingEl.classList.remove("hidden");
      seatMapEl.classList.add("hidden");
    }

    try {
      const query = orderID ? `?order_id=${encodeURIComponent(orderID)}` : "";
      const data = await fetchJSON(`/flights/${encodeURIComponent(id)}/seats${query}`);
      latestSeats = data.seats || [];
      if (!silent) {
        loadingEl.classList.add("hidden");
      }
      renderSeatGrid(gridEl, latestSeats, selectedSeats, toggleSeat, syncInFlight);
      seatMapEl.classList.remove("hidden");
      if (silent && !mapWasVisible) {
        loadingEl.classList.add("hidden");
      }
    } catch (err) {
      if (!silent) {
        loadingEl.classList.add("hidden");
      }
      showError(errorEl, `Could not load seat map: ${err.message}`);
    }
  }

  async function toggleSeat(seatID) {
    if (orderStatus === "AWAITING_PAYMENT") {
      return;
    }
    if (selectedSeats.has(seatID)) {
      selectedSeats.delete(seatID);
    } else {
      selectedSeats.add(seatID);
    }
    renderSeatGrid(gridEl, latestSeats, selectedSeats, toggleSeat, true);
    updateSelectionSummary();
    hideError(errorEl);
    try {
      await syncSeatsToServer();
      await loadSeatMap(flightID);
    } catch (err) {
      showError(errorEl, err.message);
    }
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
      const showTimer = shouldShowHoldTimer(order);
      timerSeconds = reconcileTimerSeconds(
        timerSeconds,
        effectiveTimerSeconds(order),
        { force: true }
      );
      restartTimer(showTimer);
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
      stopTimer();
      updateTimerDisplay(timerDisplay, 0, false);
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

  function stopTimer() {
    if (timerHandle) {
      clearInterval(timerHandle);
      timerHandle = null;
    }
  }

  function startTimer(showTimer = true) {
    updateTimerDisplay(timerDisplay, timerSeconds, showTimer);
    if (!showTimer || timerSeconds <= 0) {
      stopTimer();
      return;
    }
    if (timerHandle) {
      return;
    }
    timerHandle = setInterval(() => {
      timerSeconds -= 1;
      updateTimerDisplay(timerDisplay, timerSeconds, true);
      if (timerSeconds <= 0) {
        stopTimer();
        updateTimerDisplay(timerDisplay, 0, false);
      }
    }, 1000);
  }

  function restartTimer(showTimer = true) {
    stopTimer();
    startTimer(showTimer);
  }
})();

function splitColsAtAisle(cols) {
  const dIndex = cols.indexOf("D");
  if (dIndex <= 0) {
    return { leftCols: cols, rightCols: [], hasAisle: false };
  }
  return {
    leftCols: cols.slice(0, dIndex),
    rightCols: cols.slice(dIndex),
    hasAisle: true,
  };
}

function appendSeatOrPlaceholder(parent, seatByKey, row, col, selectedSeats, onToggle, syncing) {
  const seat = seatByKey.get(`${row}${col}`);
  if (seat) {
    parent.appendChild(seatCell(seat, selectedSeats, onToggle, syncing));
  } else {
    parent.appendChild(cell("corner", ""));
  }
}

function buildColLabelRow(leftCount, rightCount, hasAisle, withLavatory) {
  const row = document.createElement("div");
  row.className = "cabin-row cabin-row--col-labels";
  row.appendChild(cell("corner", ""));
  for (let i = 1; i <= leftCount; i += 1) {
    row.appendChild(cell("col-label", String(i)));
  }
  if (hasAisle) {
    row.appendChild(cell("seat-aisle", ""));
  }
  for (let i = 1; i <= rightCount; i += 1) {
    row.appendChild(cell("col-label", String(i)));
  }
  if (withLavatory) {
    const lav = cell("cabin-lavatory", "");
    lav.setAttribute("aria-hidden", "true");
    row.appendChild(lav);
  }
  return row;
}

function buildSeatRow(row, leftCols, rightCols, hasAisle, seatByKey, selectedSeats, onToggle, syncing, withLavatory) {
  const rowEl = document.createElement("div");
  rowEl.className = "cabin-row";
  rowEl.appendChild(cell("row-label", String(row)));
  leftCols.forEach((col) => {
    appendSeatOrPlaceholder(rowEl, seatByKey, row, col, selectedSeats, onToggle, syncing);
  });
  if (hasAisle) {
    rowEl.appendChild(cell("seat-aisle", ""));
  }
  rightCols.forEach((col) => {
    appendSeatOrPlaceholder(rowEl, seatByKey, row, col, selectedSeats, onToggle, syncing);
  });
  if (withLavatory) {
    const lav = cell("cabin-lavatory", "");
    lav.setAttribute("aria-hidden", "true");
    rowEl.appendChild(lav);
  }
  return rowEl;
}

function buildAisleArrows() {
  const wrap = document.createElement("div");
  wrap.className = "aisle-arrows";
  wrap.setAttribute("aria-hidden", "true");
  const up = document.createElement("span");
  up.className = "aisle-arrow aisle-arrow--up";
  const down = document.createElement("span");
  down.className = "aisle-arrow aisle-arrow--down";
  wrap.appendChild(up);
  wrap.appendChild(down);
  return wrap;
}

function buildHorizontalAisle(leftCount, rightCount, hasAisle, withLavatory) {
  const aisle = document.createElement("div");
  aisle.className = "cabin-row aisle-horizontal";
  aisle.appendChild(cell("corner", ""));
  for (let i = 0; i < leftCount; i += 1) {
    aisle.appendChild(cell("aisle-spacer", ""));
  }
  if (hasAisle) {
    const cross = cell("seat-aisle aisle-cross", "");
    cross.appendChild(buildAisleArrows());
    aisle.appendChild(cross);
  }
  for (let i = 0; i < rightCount; i += 1) {
    aisle.appendChild(cell("aisle-spacer", ""));
  }
  if (withLavatory) {
    const lav = cell("cabin-lavatory", "");
    lav.setAttribute("aria-hidden", "true");
    aisle.appendChild(lav);
  }
  return aisle;
}

function cabinGridTracks(leftCols, rightCols, hasAisle) {
  const rowLabelW = "1.75rem";
  const seatW = "2.25rem";
  const aisleW = "1.75rem";
  const lavatoryW = "1.5rem";
  return [
    rowLabelW,
    ...leftCols.map(() => seatW),
    ...(hasAisle ? [aisleW] : []),
    ...rightCols.map(() => seatW),
    lavatoryW,
  ].join(" ");
}

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
  const { leftCols, rightCols, hasAisle } = splitColsAtAisle(cols);
  const topRows = rows.filter((r) => r <= 5);
  const bottomRows = rows.filter((r) => r > 5);
  const withLavatory = true;
  const gridTracks = cabinGridTracks(leftCols, rightCols, hasAisle);

  container.className = "seat-grid";
  container.replaceChildren();

  const cabin = document.createElement("div");
  cabin.className = "seat-map-cabin";
  cabin.style.setProperty("--cabin-cols", gridTracks);

  cabin.appendChild(cell("cabin-front", ""));

  const topSection = document.createElement("div");
  topSection.className = "seat-map-section seat-map-section--top";
  topSection.appendChild(buildColLabelRow(leftCols.length, rightCols.length, hasAisle, withLavatory));
  topRows.forEach((row) => {
    topSection.appendChild(
      buildSeatRow(row, leftCols, rightCols, hasAisle, seatByKey, selectedSeats, onToggle, syncing, withLavatory)
    );
  });
  cabin.appendChild(topSection);

  if (topRows.length > 0 && bottomRows.length > 0) {
    cabin.appendChild(buildHorizontalAisle(leftCols.length, rightCols.length, hasAisle, withLavatory));
  }

  if (bottomRows.length > 0) {
    const bottomSection = document.createElement("div");
    bottomSection.className = "seat-map-section seat-map-section--bottom";
    bottomRows.forEach((row) => {
      bottomSection.appendChild(
        buildSeatRow(row, leftCols, rightCols, hasAisle, seatByKey, selectedSeats, onToggle, syncing, withLavatory)
      );
    });
    bottomSection.appendChild(buildColLabelRow(leftCols.length, rightCols.length, hasAisle, withLavatory));
    cabin.appendChild(bottomSection);
  }

  cabin.appendChild(cell("cabin-rear", ""));

  container.appendChild(cabin);
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
  return el;
}
