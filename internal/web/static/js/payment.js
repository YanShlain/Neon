(function initPaymentPage() {
  const params = new URLSearchParams(window.location.search);
  const orderID = params.get("order_id") || getStoredOrderID();
  const flightID = params.get("flight_id");

  const orderIdEl = document.getElementById("order-id");
  const orderStatusEl = document.getElementById("order-status");
  const timerDisplay = document.getElementById("timer-display");
  const heldSeatsEl = document.getElementById("held-seats");
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
    orderStatusEl.textContent = order.status;
    heldSeatsEl.textContent = `Held seats: ${(order.held_seat_ids || []).join(", ") || "—"}`;
    timerSeconds = order.timer_remaining_seconds || 0;
    startTimer();

    if (order.status === "CONFIRMED") {
      showConfirmation(order);
      return;
    }
    if (order.status !== "SEATS_HELD" && order.status !== "AWAITING_PAYMENT") {
      paymentPanel.classList.add("hidden");
      showError(errorEl, `Order is ${order.status}. Cannot accept payment.`);
      submitBtn.disabled = true;
    }
  }

  async function submitPayment() {
    hideError(errorEl);
    hideFeedback();

    const code = paymentCode.value.trim();
    if (!/^\d{5}$/.test(code)) {
      showFeedback("Enter exactly 5 digits.", "error");
      return;
    }

    submitBtn.disabled = true;
    try {
      const order = await postJSON(`/orders/${encodeURIComponent(orderID)}/payment`, { code });
      orderStatusEl.textContent = order.status;
      timerSeconds = order.timer_remaining_seconds || 0;
      startTimer();

      if (order.status === "CONFIRMED") {
        setStoredOrderID(null);
        showConfirmation(order);
        return;
      }

      const lastEvent = (order.payment_events || []).slice(-1)[0];
      const message = lastEvent?.message || "Payment failed. Try again with the same code.";
      showFeedback(message, "error");
    } catch (err) {
      if (err.status === 400) {
        showFeedback(err.message, "error");
        await loadOrder();
      } else {
        showError(errorEl, err.message);
      }
    } finally {
      submitBtn.disabled = false;
    }
  }

  function showConfirmation(order) {
    paymentPanel.classList.add("hidden");
    confirmationPanel.classList.remove("hidden");
    confirmationMessage.textContent = `Seats ${(order.held_seat_ids || []).join(", ")} are now booked.`;
    orderStatusEl.textContent = order.status;
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
