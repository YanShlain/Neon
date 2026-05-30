(function initPaymentPage() {
  const MAX_PAYMENT_ATTEMPTS = 3;

  const params = new URLSearchParams(window.location.search);
  const orderID = params.get("order_id") || getStoredOrderID();
  const flightID = params.get("flight_id");

  const orderIdEl = document.getElementById("order-id");
  const orderStatusEl = document.getElementById("order-status");
  const flightIdEl = document.getElementById("flight-id");
  const timerDisplay = document.getElementById("timer-display");
  const heldSeatsEl = document.getElementById("held-seats");
  const attemptsUsedEl = document.getElementById("attempts-used");
  const paymentEventsEl = document.getElementById("payment-events");
  const paymentPanel = document.getElementById("payment-panel");
  const confirmationPanel = document.getElementById("confirmation-panel");
  const confirmationMessage = document.getElementById("confirmation-message");
  const viewSeatsLink = document.getElementById("view-seats-link");
  const bannerSeatsLink = document.getElementById("banner-seats-link");
  const paymentForm = document.getElementById("payment-form");
  const paymentCode = document.getElementById("payment-code");
  const paymentFeedback = document.getElementById("payment-feedback");
  const submitBtn = document.getElementById("submit-btn");
  const errorEl = document.getElementById("error");

  let timerSeconds = 0;
  let timerHandle = null;
  let latestOrder = null;
  let orderStream = null;
  let pollHandle = null;

  if (!orderID || !flightID) {
    showError(errorEl, "Missing order or flight. Return to seat selection.");
    return;
  }

  orderIdEl.textContent = formatOrderDisplayID(orderID);
  if (flightIdEl) {
    flightIdEl.textContent = flightID;
  }
  const seatsPageURL = `/seats?flight_id=${encodeURIComponent(flightID)}&order_id=${encodeURIComponent(orderID)}`;
  viewSeatsLink.href = seatsPageURL;
  if (bannerSeatsLink) {
    bannerSeatsLink.href = seatsPageURL;
  }

  paymentForm.addEventListener("submit", (event) => {
    event.preventDefault();
    submitPayment();
  });

  paymentCode.addEventListener("input", () => {
    if (latestOrder) {
      updateFormControls(latestOrder);
    }
  });

  bootstrap();
  window.addEventListener("beforeunload", stopLiveUpdates);

  async function bootstrap() {
    hideError(errorEl);
    try {
      const carriedTimer = takeCarriedHoldTimer();
      await loadOrder({ carriedTimer });
      startLiveUpdates();
    } catch (err) {
      showError(errorEl, err.message);
    }
  }

  async function loadOrder(options = {}) {
    const order = await fetchJSON(`/orders/${encodeURIComponent(orderID)}`);
    renderOrder(order, options);
  }

  function failuresCount(order) {
    return order.payment_failures ?? 0;
  }

  function allAttemptsExhausted(order) {
    return failuresCount(order) >= MAX_PAYMENT_ATTEMPTS;
  }

  function updateFormControls(order) {
    const typedCode = paymentCode.value.trim();
    const validInput = /^\d{5}$/.test(typedCode);
    const exhausted = allAttemptsExhausted(order);

    const canSubmit =
      order.status === "SEATS_HELD" &&
      validInput &&
      !exhausted;

    submitBtn.disabled = !canSubmit;
    paymentCode.disabled = order.status !== "SEATS_HELD" || exhausted;
  }

  function renderOrder(order, { resetTimer = false, carriedTimer = 0 } = {}) {
    const prev = latestOrder;
    latestOrder = order;
    const seatsBefore = prev ? heldSeatsSignature(prev.held_seat_ids) : "";
    const seatsAfter = heldSeatsSignature(order.held_seat_ids);
    const seatsChanged = seatsBefore !== seatsAfter;
    const resumeFromSeatsPage = carriedTimer > 0 && !prev;
    const forceTimer =
      !resumeFromSeatsPage &&
      (resetTimer || !prev || seatsChanged);

    const attemptsUsed = failuresCount(order);

    orderStatusEl.textContent = order.status;
    heldSeatsEl.textContent = `Held seats: ${(order.held_seat_ids || []).join(", ") || "—"}`;
    if (attemptsUsedEl) {
      attemptsUsedEl.textContent = `${attemptsUsed} / ${MAX_PAYMENT_ATTEMPTS} attempts used`;
    }
    renderPaymentEvents(order.payment_events || []);

    if (isTerminalStatus(order.status) || !shouldShowHoldTimer(order)) {
      timerSeconds = 0;
      stopTimer();
      updateTimerDisplay(timerDisplay, 0, false);
    } else {
      const showTimer = shouldShowHoldTimer(order);
      const serverTimer = effectiveTimerSeconds(order);
      const localBase = resumeFromSeatsPage ? carriedTimer : timerSeconds;
      timerSeconds = reconcileTimerSeconds(localBase, serverTimer, {
        force: forceTimer && !resumeFromSeatsPage,
      });
      if (forceTimer) {
        restartTimer(showTimer);
      } else {
        updateTimerDisplay(timerDisplay, timerSeconds, showTimer);
        startTimer(showTimer);
      }
    }

    if (order.status === "CONFIRMED") {
      showConfirmation(order);
      return;
    }

    if (order.status === "PAYMENT_FAILED" || order.status === "EXPIRED") {
      paymentPanel.classList.add("hidden");
      showError(errorEl, terminalMessage(order));
      setStoredOrderID(null);
      setFormDisabled(true);
      return;
    }

    if (order.status === "AWAITING_PAYMENT") {
      paymentPanel.classList.remove("hidden");
      confirmationPanel.classList.add("hidden");
      setFormDisabled(true);
      return;
    }

    if (order.status !== "SEATS_HELD") {
      paymentPanel.classList.add("hidden");
      showError(errorEl, `Order is ${order.status}. Cannot accept payment.`);
      setFormDisabled(true);
      return;
    }

    paymentPanel.classList.remove("hidden");
    confirmationPanel.classList.add("hidden");
    updateFormControls(order);
  }

  function feedbackForLastEvent(order) {
    const last = (order.payment_events || []).slice(-1)[0];
    if (!last) {
      return "";
    }
    return last.message || "";
  }

  function setFormDisabled(disabled) {
    submitBtn.disabled = disabled;
    paymentCode.disabled = disabled;
  }

  async function submitPayment() {
    if (submitBtn.disabled) {
      return;
    }
    hideError(errorEl);
    hideFeedback();

    const code = paymentCode.value.trim();
    if (!/^\d{5}$/.test(code)) {
      showFeedback("Enter exactly 5 digits.", "error");
      return;
    }

    setFormDisabled(true);
    try {
      const order = await postJSON(`/orders/${encodeURIComponent(orderID)}/payment`, { code });
      renderOrder(order);

      if (order.status === "CONFIRMED") {
        setStoredOrderID(null);
        showConfirmation(order);
        return;
      }

      const message = feedbackForLastEvent(order) || "Payment failed. Try again.";
      showFeedback(message, "error");
    } catch (err) {
      await loadOrder();
      const eventMessage = feedbackForLastEvent(latestOrder);
      if (eventMessage) {
        showFeedback(eventMessage, "error");
      } else if (err.status === 400 || err.status === 410) {
        showFeedback(err.message, "error");
      } else {
        showError(errorEl, err.message);
      }
    } finally {
      if (latestOrder?.status === "SEATS_HELD") {
        updateFormControls(latestOrder);
      }
    }
  }

  function renderPaymentEvents(events) {
    if (!events.length) {
      paymentEventsEl.innerHTML = "<li class=\"payment-event-empty\">No payment attempts yet.</li>";
      return;
    }
    paymentEventsEl.innerHTML = events
      .map((ev) => {
        const label = formatEventType(ev.type);
        const code = ev.code ? ` (${escapeHTML(ev.code)})` : "";
        const detail = ev.message ? `: ${escapeHTML(ev.message)}` : "";
        return `<li class="payment-event payment-event-${escapeHTML(ev.type)}">${escapeHTML(label)}${code}${detail}</li>`;
      })
      .join("");
  }

  function formatEventType(type) {
    switch (type) {
      case "validation_success":
        return "Payment succeeded";
      case "validation_failed":
        return "Payment failed";
      case "format_invalid":
        return "Invalid code format";
      case "attempts_exhausted":
        return "All payment attempts exhausted";
      case "rejected_by_timer":
        return "Payment rejected (timer expired)";
      default:
        return type;
    }
  }

  function terminalMessage(order) {
    if (order.status === "PAYMENT_FAILED") {
      return (
        order.last_error ||
        "The maximum payment retries is reached the booking process is cancelled."
      );
    }
    if (order.status === "EXPIRED") {
      return "Your hold expired. Seats have been released.";
    }
    return `Order is ${order.status}.`;
  }

  function showConfirmation(order) {
    paymentPanel.classList.add("hidden");
    confirmationPanel.classList.remove("hidden");
    confirmationMessage.textContent = `Seats ${(order.held_seat_ids || []).join(", ")} are now booked.`;
    orderStatusEl.textContent = order.status;
    setFormDisabled(true);
  }

  function showFeedback(message, kind) {
    paymentFeedback.textContent = message;
    paymentFeedback.className = `payment-feedback ${kind}`;
    paymentFeedback.classList.remove("hidden");
  }

  function hideFeedback() {
    paymentFeedback.classList.add("hidden");
    paymentFeedback.textContent = "";
  }

  function startLiveUpdates() {
    stopLiveUpdates();
    try {
      orderStream = new EventSource(`/api/v1/orders/${encodeURIComponent(orderID)}/stream`);
      orderStream.addEventListener("status", (event) => {
        try {
          const order = JSON.parse(event.data);
          renderOrder(order);
        } catch {
          // ignore malformed stream payload
        }
      });
      orderStream.onerror = () => {
        stopLiveUpdates();
        startOrderPolling();
      };
    } catch {
      startOrderPolling();
    }
  }

  function startOrderPolling() {
    if (pollHandle) {
      return;
    }
    pollHandle = setInterval(async () => {
      try {
        await loadOrder();
      } catch {
        // best effort polling
      }
    }, ORDER_POLL_INTERVAL_MS);
  }

  function stopLiveUpdates() {
    if (orderStream) {
      orderStream.close();
      orderStream = null;
    }
    if (pollHandle) {
      clearInterval(pollHandle);
      pollHandle = null;
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
