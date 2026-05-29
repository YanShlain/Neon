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
  viewSeatsLink.href = `/seats?flight_id=${encodeURIComponent(flightID)}&order_id=${encodeURIComponent(orderID)}`;

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
      await loadOrder();
      startLiveUpdates();
    } catch (err) {
      showError(errorEl, err.message);
    }
  }

  async function loadOrder() {
    const order = await fetchJSON(`/orders/${encodeURIComponent(orderID)}`);
    renderOrder(order);
  }

  function updateFormControls(order) {
    const failures = order.payment_failures ?? 0;
    const attemptsExhausted = failures >= MAX_PAYMENT_ATTEMPTS;
    const typedCode = paymentCode.value.trim();
    const validInput = /^\d{5}$/.test(typedCode);

    const canSubmit = order.status === "SEATS_HELD" && validInput && !attemptsExhausted;

    submitBtn.disabled = !canSubmit;
    paymentCode.disabled = order.status !== "SEATS_HELD" || attemptsExhausted;
  }

  function renderOrder(order) {
    latestOrder = order;
    const failures = order.payment_failures ?? 0;
    const attemptsExhausted = failures >= MAX_PAYMENT_ATTEMPTS;

    orderStatusEl.textContent = order.status;
    heldSeatsEl.textContent = `Held seats: ${(order.held_seat_ids || []).join(", ") || "—"}`;
    attemptsUsedEl.textContent = `${failures} / ${MAX_PAYMENT_ATTEMPTS}`;
    renderPaymentEvents(order.payment_events || []);
    timerSeconds = order.timer_remaining_seconds || 0;
    startTimer();

    if (order.status === "CONFIRMED") {
      showConfirmation(order);
      return;
    }

    if (order.status === "PAYMENT_FAILED" || order.status === "EXPIRED") {
      paymentPanel.classList.add("hidden");
      showError(errorEl, terminalMessage(order.status));
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

    if (attemptsExhausted) {
      showFeedback("Payment attempts exhausted. The order has failed.", "error");
    }
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

  function terminalMessage(status) {
    if (status === "PAYMENT_FAILED") {
      return "Payment failed after 3 attempts. Your hold has been released.";
    }
    if (status === "EXPIRED") {
      return "Your hold expired. Seats have been released.";
    }
    return `Order is ${status}.`;
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
    }, 2000);
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
