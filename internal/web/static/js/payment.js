(function initPaymentPage() {
  const MAX_ATTEMPTS_PER_METHOD = 3;

  const params = new URLSearchParams(window.location.search);
  const orderID = params.get("order_id") || getStoredOrderID();
  const flightID = params.get("flight_id");

  const orderIdEl = document.getElementById("order-id");
  const orderStatusEl = document.getElementById("order-status");
  const timerDisplay = document.getElementById("timer-display");
  const heldSeatsEl = document.getElementById("held-seats");
  const methodsUsedEl = document.getElementById("methods-used");
  const methodsRemainingEl = document.getElementById("methods-remaining");
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
  const newMethodBtn = document.getElementById("new-method-btn");
  const errorEl = document.getElementById("error");

  let timerSeconds = 0;
  let timerHandle = null;

  if (!orderID || !flightID) {
    showError(errorEl, "Missing order or flight. Return to seat selection.");
    return;
  }

  orderIdEl.textContent = orderID.slice(0, 8) + "…";
  viewSeatsLink.href = `/seats?flight_id=${encodeURIComponent(flightID)}&order_id=${encodeURIComponent(orderID)}`;

  paymentForm.addEventListener("submit", (event) => {
    event.preventDefault();
    submitPayment();
  });

  newMethodBtn.addEventListener("click", () => {
    startNewPaymentMethod();
  });

  bootstrap();

  async function bootstrap() {
    hideError(errorEl);
    try {
      await loadOrder();
    } catch (err) {
      showError(errorEl, err.message);
    }
  }

  async function loadOrder() {
    const order = await fetchJSON(`/orders/${encodeURIComponent(orderID)}`);
    renderOrder(order);
  }

  function renderOrder(order) {
    const failures = order.payment_failures ?? 0;
    const methodsUsed = order.methods_used ?? 0;
    const methodsRemaining = order.methods_remaining ?? 0;
    const attemptsExhausted = failures >= MAX_ATTEMPTS_PER_METHOD;
    const canStartNewMethod =
      order.status === "SEATS_HELD" &&
      methodsUsed > 0 &&
      methodsRemaining > 0 &&
      attemptsExhausted;
    const canSubmit =
      order.status === "SEATS_HELD" && !attemptsExhausted;

    orderStatusEl.textContent = order.status;
    heldSeatsEl.textContent = `Held seats: ${(order.held_seat_ids || []).join(", ") || "—"}`;
    methodsUsedEl.textContent = String(methodsUsed);
    methodsRemainingEl.textContent = String(methodsRemaining);
    attemptsUsedEl.textContent = `${failures} / ${MAX_ATTEMPTS_PER_METHOD}`;
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
    submitBtn.disabled = !canSubmit;
    newMethodBtn.disabled = !canStartNewMethod;
    paymentCode.disabled = !canSubmit && !canStartNewMethod;

    if (attemptsExhausted && methodsRemaining > 0) {
      showFeedback(
        "Attempts exhausted for this code. Try a new payment method, then enter a different 5-digit code.",
        "info"
      );
    } else {
      hideFeedback();
    }
  }

  function setFormDisabled(disabled) {
    submitBtn.disabled = disabled;
    newMethodBtn.disabled = disabled;
    paymentCode.disabled = disabled;
  }

  async function submitPayment() {
    if (submitBtn.disabled) {
      return;
    }
    hideError(errorEl);

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

      const lastEvent = (order.payment_events || []).slice(-1)[0];
      const message = lastEvent?.message || "Payment failed. Try again with the same code.";
      showFeedback(message, "error");
    } catch (err) {
      if (err.status === 400 || err.status === 410) {
        showFeedback(err.message, "error");
      } else {
        showError(errorEl, err.message);
      }
      await loadOrder();
    }
  }

  async function startNewPaymentMethod() {
    if (newMethodBtn.disabled) {
      return;
    }
    hideError(errorEl);
    setFormDisabled(true);
    try {
      const order = await postJSON(`/orders/${encodeURIComponent(orderID)}/payment/new-method`, {});
      paymentCode.value = "";
      renderOrder(order);
      showFeedback("New payment method started. Enter a different 5-digit code.", "info");
    } catch (err) {
      showFeedback(err.message, "error");
      await loadOrder();
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
        return "Attempts exhausted on current method";
      case "method_change_required":
        return "Different code rejected";
      case "new_method_started":
        return "New payment method started";
      case "methods_exhausted":
        return "All payment methods exhausted";
      case "rejected_by_timer":
        return "Payment rejected (timer expired)";
      default:
        return type;
    }
  }

  function terminalMessage(status) {
    if (status === "PAYMENT_FAILED") {
      return "All payment methods failed. Your hold has been released.";
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
