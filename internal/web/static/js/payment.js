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
  const errorEl = document.getElementById("error");

  const CACHED_CODE_KEY = `neon_payment_method_code_${orderID}`;

  let timerSeconds = 0;
  let timerHandle = null;
  let latestOrder = null;
  // The 5-digit code locked to the current server-side payment method slot.
  // Persisted in sessionStorage so a page refresh retains context.
  let cachedMethodCode = sessionStorage.getItem(CACHED_CODE_KEY) || "";

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

  paymentCode.addEventListener("input", () => {
    if (latestOrder) {
      updateFormControls(latestOrder);
    }
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

  function setCachedMethodCode(code) {
    cachedMethodCode = code;
    if (code) {
      sessionStorage.setItem(CACHED_CODE_KEY, code);
    } else {
      sessionStorage.removeItem(CACHED_CODE_KEY);
    }
  }

  function getCurrentMethodCode(events) {
    if (!events?.length) {
      return "";
    }
    for (let i = events.length - 1; i >= 0; i--) {
      if (events[i].type === "new_method_started") {
        return "";
      }
      if (events[i].code) {
        return events[i].code;
      }
    }
    return "";
  }

  function updateFormControls(order) {
    const failures = order.payment_failures ?? 0;
    const methodsRemaining = order.methods_remaining ?? 0;
    const attemptsExhausted = failures >= MAX_ATTEMPTS_PER_METHOD;
    const typedCode = paymentCode.value.trim();
    const validInput = /^\d{5}$/.test(typedCode);

    // After 3 failures: Submit only if a different code is typed and methods remain.
    // Before 3 failures: Submit for any valid code; different code will get backend-rejected (U-D5).
    const canSubmit =
      order.status === "SEATS_HELD" &&
      validInput &&
      (!attemptsExhausted || (methodsRemaining > 0 && typedCode !== cachedMethodCode));

    submitBtn.disabled = !canSubmit;
    paymentCode.disabled = order.status !== "SEATS_HELD" || (attemptsExhausted && methodsRemaining === 0);
  }

  function renderOrder(order) {
    latestOrder = order;
    const failures = order.payment_failures ?? 0;
    const methodsUsed = order.methods_used ?? 0;
    const methodsRemaining = order.methods_remaining ?? 0;
    const attemptsExhausted = failures >= MAX_ATTEMPTS_PER_METHOD;

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
      setCachedMethodCode("");
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

    // Sync cached method code from payment events after each server response.
    const codeFromEvents = getCurrentMethodCode(order.payment_events || []);
    if (codeFromEvents) {
      setCachedMethodCode(codeFromEvents);
    } else if (!cachedMethodCode && failures > 0) {
      // Fallback: derive from the typed value on first failure
      setCachedMethodCode(paymentCode.value.trim());
    } else if (failures === 0) {
      setCachedMethodCode("");
    }

    updateFormControls(order);

    if (attemptsExhausted && methodsRemaining > 0) {
      showFeedback("Attempts exhausted for this code. Enter a different 5-digit code to continue.", "info");
    }
  }

  function feedbackForLastEvent(order) {
    const last = (order.payment_events || []).slice(-1)[0];
    if (!last) {
      return "";
    }
    if (last.type === "method_change_required") {
      return last.message || "Start a new payment method before using a different code.";
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
      let order = latestOrder;
      const failures = order?.payment_failures ?? 0;
      const attemptsExhausted = failures >= MAX_ATTEMPTS_PER_METHOD;

      // Auto-trigger new-method when attempts are exhausted and a different code was entered.
      if (attemptsExhausted && cachedMethodCode !== "" && code !== cachedMethodCode) {
        order = await postJSON(`/orders/${encodeURIComponent(orderID)}/payment/new-method`, {});
        renderOrder(order);
      }

      order = await postJSON(`/orders/${encodeURIComponent(orderID)}/payment`, { code });
      renderOrder(order);

      if (order.status === "CONFIRMED") {
        setStoredOrderID(null);
        setCachedMethodCode("");
        showConfirmation(order);
        return;
      }

      const exhaustedAfterPay = (order.payment_failures ?? 0) >= MAX_ATTEMPTS_PER_METHOD;
      const methodsRemaining = order.methods_remaining ?? 0;
      if (exhaustedAfterPay && methodsRemaining > 0) {
        showFeedback("Attempts exhausted for this code. Enter a different 5-digit code to continue.", "info");
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
