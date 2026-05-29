(function initPaymentPage() {
  const MAX_ATTEMPTS_PER_METHOD = 3;
  const MAX_PAYMENT_METHODS = 3;

  const params = new URLSearchParams(window.location.search);
  const orderID = params.get("order_id") || getStoredOrderID();
  const flightID = params.get("flight_id");

  const orderIdEl = document.getElementById("order-id");
  const orderStatusEl = document.getElementById("order-status");
  const flightIdEl = document.getElementById("flight-id");
  const timerDisplay = document.getElementById("timer-display");
  const heldSeatsEl = document.getElementById("held-seats");
  const attemptsUsedEl = document.getElementById("attempts-used");
  const methodsUsedEl = document.getElementById("methods-used");
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
  let latestOrder = null;
  let lastSubmittedCode = "";
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

  if (newMethodBtn) {
    newMethodBtn.addEventListener("click", () => {
      startNewPaymentMethod();
    });
  }

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

  function methodsRemaining(order) {
    const remaining = order.methods_remaining;
    if (typeof remaining === "number") {
      return remaining;
    }
    const used = order.methods_used ?? 0;
    return Math.max(0, MAX_PAYMENT_METHODS - used);
  }

  function allMethodsExhausted(order) {
    return methodsRemaining(order) <= 0 && lastMethodExhausted(order);
  }

  function lastMethodExhausted(order) {
    const events = order.payment_events || [];
    const last = events[events.length - 1];
    return last && last.type === "attempts_exhausted" && methodsRemaining(order) <= 0;
  }

  function currentCodeAttempts(order) {
    const events = order.payment_events || [];
    let attempts = 0;
    for (const ev of events) {
      if (ev.type === "new_method_started") {
        attempts = 0;
        continue;
      }
      if (ev.type === "attempts_exhausted") {
        attempts = 0;
        continue;
      }
      if (ev.type === "validation_failed") {
        attempts += 1;
      }
    }
    return Math.min(attempts, MAX_ATTEMPTS_PER_METHOD);
  }

  function updateFormControls(order) {
    const typedCode = paymentCode.value.trim();
    const validInput = /^\d{5}$/.test(typedCode);
    const exhausted = allMethodsExhausted(order);
    const methodExhausted = currentMethodExhausted(order);
    const needsNewMethod =
      lastSubmittedCode &&
      typedCode &&
      typedCode !== lastSubmittedCode &&
      !methodSwitchAllowed(order) &&
      !currentMethodExhausted(order);

    const canSubmit =
      order.status === "SEATS_HELD" &&
      validInput &&
      !exhausted &&
      !needsNewMethod &&
      (!methodExhausted || (typedCode && typedCode !== lastSubmittedCode));

    submitBtn.disabled = !canSubmit;
    paymentCode.disabled = order.status !== "SEATS_HELD" || exhausted;

    if (newMethodBtn) {
      const canNewMethod =
        order.status === "SEATS_HELD" &&
        methodsRemaining(order) > 0 &&
        !methodExhausted &&
        lastSubmittedCode &&
        currentCodeAttempts(order) > 0 &&
        typedCode === lastSubmittedCode;
      newMethodBtn.disabled = !canNewMethod;
    }
  }

  function methodSwitchAllowed(order) {
    if (currentMethodExhausted(order)) {
      return true;
    }
    const events = order.payment_events || [];
    const last = events[events.length - 1];
    return last && last.type === "new_method_started";
  }

  function currentMethodExhausted(order) {
    const events = order.payment_events || [];
    const last = events[events.length - 1];
    return (
      last &&
      last.type === "attempts_exhausted" &&
      methodsRemaining(order) > 0
    );
  }

  function renderOrder(order) {
    latestOrder = order;
    const attemptsOnCode = currentCodeAttempts(order);
    const methodsUsed = order.methods_used ?? 0;
    const remaining = methodsRemaining(order);

    orderStatusEl.textContent = order.status;
    heldSeatsEl.textContent = `Held seats: ${(order.held_seat_ids || []).join(", ") || "—"}`;
    attemptsUsedEl.textContent = `${attemptsOnCode} / ${MAX_ATTEMPTS_PER_METHOD} on current code`;
    if (methodsUsedEl) {
      methodsUsedEl.textContent = `${methodsUsed} / ${MAX_PAYMENT_METHODS} (remaining: ${remaining})`;
    }
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

    if (currentMethodExhausted(order) && methodsRemaining(order) > 0) {
      lastSubmittedCode = "";
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
    if (newMethodBtn) {
      newMethodBtn.disabled = disabled;
    }
  }

  async function startNewPaymentMethod() {
    if (newMethodBtn?.disabled) {
      return;
    }
    hideError(errorEl);
    hideFeedback();
    setFormDisabled(true);
    try {
      const order = await postJSON(`/orders/${encodeURIComponent(orderID)}/payment/new-method`, {});
      lastSubmittedCode = "";
      renderOrder(order);
      showFeedback("New payment method ready. Enter a different 5-digit code.", "info");
    } catch (err) {
      showFeedback(err.message, "error");
      await loadOrder();
    } finally {
      if (latestOrder?.status === "SEATS_HELD") {
        updateFormControls(latestOrder);
      }
    }
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
      lastSubmittedCode = code;
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
        return "Payment method exhausted";
      case "new_method_started":
        return "New payment method";
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
