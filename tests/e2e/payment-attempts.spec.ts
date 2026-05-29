import { expect, Page, test } from "@playwright/test";
import { NeonServer, startNeonServer } from "./helpers/server";

const PORT_SUCCESS = 8080;
const PORT_FAILURE = 8081;
const PORT_TIMER_RACE = 8082;

test.describe("MVP-E success-flow journeys (E-E1, E-E2, E-E5, E-E6, E-E7)", () => {
  let server: NeonServer;

  test.beforeEach(async () => {
    server = await startNeonServer({
      port: PORT_SUCCESS,
      env: {
        PAYMENT_NEVER_FAIL: "1",
      },
    });
  });

  test.afterEach(async () => {
    if (server) {
      await server.stop();
    }
  });

  test("E-E1: completes happy path booking to CONFIRMED", async ({ page }) => {
    await page.goto(`${server.baseURL}/`);
    await startOrder(page, 0);
    await selectSeatAndProceed(page, "1B");

    await page.locator("#payment-code").fill("12345");
    await page.getByRole("button", { name: "Submit payment" }).click();

    await expect(page.locator("#order-status")).toHaveText("CONFIRMED", { timeout: 15000 });
    await expect(page.locator("#confirmation-message")).toContainText("are now booked");
  });

  test("E-E2: seat change refreshes timer close to full duration", async ({ page }) => {
    await page.goto(`${server.baseURL}/`);
    await startOrder(page, 0);
    await selectSeatAndProceed(page, "1B");
    const orderID = await page.evaluate(() => localStorage.getItem("neon_order_id"));
    const flightID = new URL(page.url()).searchParams.get("flight_id");
    expect(orderID).toBeTruthy();
    expect(flightID).toBeTruthy();

    await page.goto(
      `${server.baseURL}/seats?flight_id=${encodeURIComponent(flightID || "")}&order_id=${encodeURIComponent(orderID || "")}`,
    );
    await page.waitForTimeout(2500);
    const beforeChange = await readTimerSeconds(page, "#timer-display");
    await clickAnyAvailableSeat(page);
    await page.getByRole("button", { name: /Proceed to payment/i }).click();
    const refreshedTimer = await readTimerSeconds(page, "#timer-display");

    expect(refreshedTimer).toBeGreaterThanOrEqual(110);
    expect(refreshedTimer).toBeGreaterThanOrEqual(beforeChange);
  });

  test("E-E5: flight inventories are isolated", async ({ browser }) => {
    const holderCtx = await browser.newContext();
    const otherFlightCtx = await browser.newContext();
    const page = await holderCtx.newPage();
    const secondFlightPage = await otherFlightCtx.newPage();

    await page.goto(`${server.baseURL}/`);
    await startOrder(page, 0);
    const heldSeatID = await clickAnyAvailableSeat(page);
    await page.getByRole("button", { name: /Proceed to payment/i }).click();

    await secondFlightPage.goto(`${server.baseURL}/`);
    await startOrder(secondFlightPage, 1);
    await expect(secondFlightPage.getByLabel(`${heldSeatID} AVAILABLE`)).toBeEnabled();

    await holderCtx.close();
    await otherFlightCtx.close();
  });

  test("E-E6: second user sees held seats as unavailable", async ({ browser }) => {
    const holder = await browser.newContext();
    const observer = await browser.newContext();
    const holderPage = await holder.newPage();
    const observerPage = await observer.newPage();

    await holderPage.goto(`${server.baseURL}/`);
    await startOrder(holderPage, 0);
    const heldSeatID = await clickAnyAvailableSeat(holderPage);
    await holderPage.getByRole("button", { name: /Proceed to payment/i }).click();

    await observerPage.goto(`${server.baseURL}/`);
    await startOrder(observerPage, 0);
    await observerPage.getByRole("button", { name: /Refresh map/i }).click();
    await expect(observerPage.getByLabel(`${heldSeatID} HELD`)).toBeDisabled();

    await holder.close();
    await observer.close();
  });

  test("E-E7: blocks new booking during active order and allows after terminal", async ({ page }) => {
    await page.goto(`${server.baseURL}/`);
    await startOrder(page, 0);
    await page.goto(`${server.baseURL}/`);

    await page.locator(".select-btn").nth(1).click();
    await expect(page.locator("#error")).toContainText("already have an active booking");

    await page.getByRole("link", { name: /Continue booking/i }).click();
    await page.getByRole("button", { name: /Cancel order/i }).click();
    await page.goto(`${server.baseURL}/`);

    await page.locator(".select-btn").nth(1).click();
    await expect(page).toHaveURL(/\/seats\?/);
  });
});

test.describe("MVP-E failure journey (E-E3)", () => {
  let server: NeonServer;

  test.beforeEach(async () => {
    server = await startNeonServer({
      port: PORT_FAILURE,
      env: {
        PAYMENT_ALWAYS_FAIL: "1",
      },
    });
  });

  test.afterEach(async () => {
    if (server) {
      await server.stop();
    }
  });

  test("E-E3: full 3x3 method exhaustion reaches PAYMENT_FAILED", async ({ page }) => {
    await page.goto(`${server.baseURL}/`);
    await startOrder(page, 0);
    await selectSeatAndProceed(page, "1B");

    const codes = ["11111", "22222", "33333"];
    for (let i = 0; i < codes.length; i++) {
      if (i > 0) {
        await page.locator("#payment-code").fill(codes[i]);
        await expect(page.getByRole("button", { name: "Submit payment" })).toBeEnabled();
      }
      await exhaustPaymentCode(page, codes[i]);
    }

    await expect(page.locator("#order-status")).toHaveText("PAYMENT_FAILED", { timeout: 15000 });
    await expect(page.locator("#error")).toContainText(/All payment methods failed/i);
    await expect(page.evaluate(() => localStorage.getItem("neon_order_id"))).resolves.toBeNull();
    await expect(page.locator("#methods-used")).toContainText("3 / 3");
  });
});

test.describe("MVP-E timer race journey (E-E4)", () => {
  let server: NeonServer;

  test.beforeEach(async () => {
    server = await startNeonServer({
      port: PORT_TIMER_RACE,
      env: {
        HOLD_DURATION: "2s",
        PAYMENT_NEVER_FAIL: "1",
        PAYMENT_VALIDATION_DELAY: "5s",
      },
    });
  });

  test.afterEach(async () => {
    if (server) {
      await server.stop();
    }
  });

  test("E-E4: timer expiry rejects in-flight payment", async ({ page }) => {
    await page.goto(`${server.baseURL}/`);
    await startOrder(page, 0);
    await selectSeatAndProceed(page, "1B");

    await page.locator("#payment-code").fill("12345");
    await page.getByRole("button", { name: "Submit payment" }).click();
    await page.waitForTimeout(6500);
    await page.reload();

    await expect(page.locator("#order-status")).toHaveText("EXPIRED", { timeout: 20000 });
    await expect(page.locator("#error")).toContainText("hold expired");
    await expect(page.locator("#payment-events")).toContainText("Payment rejected (timer expired)");
  });
});

async function startOrder(page: Page, flightCardIndex: number): Promise<void> {
  await page.locator(".select-btn").nth(flightCardIndex).click();
  await expect(page).toHaveURL(/\/seats\?/);
}

async function selectSeatAndProceed(page: Page, seatLabel: string): Promise<void> {
  const preferredSeat = page.getByLabel(`${seatLabel} AVAILABLE`);
  if (await preferredSeat.count()) {
    await preferredSeat.click();
  } else {
    await clickAnyAvailableSeat(page);
  }
  await page.getByRole("button", { name: /Proceed to payment/i }).click();
  await expect(page).toHaveURL(/\/payment\?/);
}

async function clickAnyAvailableSeat(page: Page): Promise<string> {
  const seat = page.locator("button[aria-label$=' AVAILABLE']").first();
  const label = ((await seat.getAttribute("aria-label")) || "").trim();
  await seat.click();
  return label.split(" ")[0];
}

async function exhaustPaymentCode(page: Page, code: string): Promise<void> {
  const codeInput = page.locator("#payment-code");
  const submit = page.getByRole("button", { name: "Submit payment" });

  for (let attempt = 0; attempt < 3; attempt++) {
    await codeInput.fill(code);
    await expect(submit).toBeEnabled({ timeout: 10000 });
    await submit.click();
    if (attempt < 2) {
      await expect(page.locator("#payment-feedback")).toContainText(/failed/i, { timeout: 10000 });
    }
  }

  await expect(page.locator("#attempts-used")).toHaveText("0 / 3 on current code");
  await expect(submit).toBeDisabled();
  await expect(page.locator("#payment-feedback")).toContainText(/exhausted/i);
}

async function readTimerSeconds(page: Page, selector: string): Promise<number> {
  const value = await page.locator(selector).textContent();
  const match = /^(\d+):(\d+)$/.exec((value || "").trim());
  if (!match) {
    return 0;
  }
  return Number(match[1]) * 60 + Number(match[2]);
}
